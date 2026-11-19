package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

type screeningFake struct {
	*fakeProvider
	a                               *App
	mu                              sync.Mutex
	creates, replacements, inspects int
	method                          string
	ips                             []string
	createErr, replaceErr           error
	beforeCreate                    func()
	beforeReplace                   func()
	fingerprintChange               bool
}

func (f *screeningFake) Create(ctx context.Context, s cloud.Spec, token string) (cloud.Operation, error) {
	if f.beforeCreate != nil {
		f.beforeCreate()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	// An actual cloud adapter may not write until a durable creation intent exists.
	var state string
	if f.a.S.DB.QueryRow("SELECT state FROM screenings WHERE json_extract(data,'$.rounds[#-1].createToken')=?", token).Scan(&state) != nil || state != "creating" {
		return cloud.Operation{}, errors.New("missing durable creation intent")
	}
	if f.createErr != nil {
		return cloud.Operation{}, f.createErr
	}
	f.fakeProvider.mu.Lock()
	f.fakeProvider.items = append(f.fakeProvider.items, cloud.Instance{ID: token, Name: s.Name, Region: s.Region, State: "running", IPs: []string{"8.8.8.8"}})
	f.fakeProvider.mu.Unlock()
	return cloud.Operation{ResourceID: token, Region: s.Region, Expected: "running"}, nil
}
func (f *screeningFake) InspectScreening(ctx context.Context, s cloud.Spec, token string, op cloud.Operation) (cloud.ScreeningResource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspects++
	ip := "8.8.8.8"
	if len(f.ips) > 0 {
		n := f.creates - 1
		if f.method != "delete-recreate" {
			n = f.replacements
		}
		if n >= len(f.ips) {
			n = len(f.ips) - 1
		}
		ip = f.ips[n]
	}
	fingerprint := "unchanged"
	if f.fingerprintChange && f.inspects > 1 {
		fingerprint = "external-change"
	}
	return cloud.ScreeningResource{Instance: cloud.Instance{ID: op.ResourceID, Region: s.Region, State: "running", IPs: []string{ip}}, Fingerprint: fingerprint, Method: f.method, Resources: []cloud.RelatedResource{{ID: op.ResourceID, Kind: "instance", State: "exists"}}}, nil
}
func (f *screeningFake) ReplaceScreeningIP(ctx context.Context, s cloud.Spec, token string, op cloud.Operation, fp string) (cloud.Operation, error) {
	if f.beforeReplace != nil {
		f.beforeReplace()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replacements++
	if f.replaceErr != nil {
		return op, f.replaceErr
	}
	if f.method == "delete-recreate" {
		op.Expected = "deleted"
	}
	return op, nil
}

// Exercise the real incremental adapter contract without sleeping inside a provider.
func (f *screeningFake) IPInfo(ctx context.Context, i cloud.Instance) (cloud.IPInfo, error) {
	r, e := f.InspectScreening(ctx, cloud.Spec{Region: i.Region}, "", cloud.Operation{ResourceID: i.ID})
	if e != nil {
		return cloud.IPInfo{}, e
	}
	return cloud.IPInfo{InstanceID: i.ID, Region: i.Region, State: "running", PrimaryIPv4: r.Instance.IPs[0], Addresses: []cloud.IPAddress{{Address: r.Instance.IPs[0], Primary: true}}, Snapshot: "fixture-ip", Methods: []cloud.IPMethod{{ID: f.method, Available: f.method != "delete-recreate"}}}, nil
}
func (f *screeningFake) BeginIPChange(ctx context.Context, i cloud.Instance, c cloud.IPChange, token string) (cloud.Operation, error) {
	info, e := f.IPInfo(ctx, i)
	if e != nil {
		return cloud.Operation{}, e
	}
	return cloud.NewIPOperation(i, c, info, token)
}
func (f *screeningFake) AdvanceIPChange(ctx context.Context, op cloud.Operation) (cloud.Operation, bool, error) {
	if f.beforeReplace != nil {
		f.beforeReplace()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replacements++
	if f.replaceErr != nil {
		if errors.Is(f.replaceErr, cloud.ErrIPQuota) {
			op.IP.Writes = []cloud.ScreeningWrite{{Action: "allocate-ip", State: "dispatching"}}
		}
		return op, false, f.replaceErr
	}
	ip := "8.8.8.8"
	if len(f.ips) > 0 {
		n := f.replacements
		if n >= len(f.ips) {
			n = len(f.ips) - 1
		}
		ip = f.ips[n]
	}
	op.IP.NewIP = ip
	info := op.IP.Before
	info.PrimaryIPv4 = ip
	info.Addresses = []cloud.IPAddress{{Address: ip, Primary: true}}
	op.IP.After = &info
	op.IP.Step = "completed"
	return op, true, nil
}
func (f *screeningFake) ReconcileIPChange(ctx context.Context, op cloud.Operation) (cloud.Operation, bool, error) {
	return op, false, nil
}
func (f *screeningFake) Action(ctx context.Context, i cloud.Instance, action, id string) (cloud.Operation, error) {
	if action != "delete" {
		return f.fakeProvider.Action(ctx, i, action, id)
	}
	if f.beforeReplace != nil {
		f.beforeReplace()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replacements++
	if f.replaceErr != nil && !errors.Is(f.replaceErr, cloud.ErrIPQuota) {
		return cloud.Operation{}, f.replaceErr
	}
	return cloud.Operation{ResourceID: i.ID, Region: i.Region, Expected: "deleted"}, nil
}
func driveScreenings(ctx context.Context, a *App) bool {
	claimed := false
	for ctx.Err() == nil {
		if a.runControlOne(ctx) {
			claimed = true
		}
		if a.runScreeningOne(ctx) {
			claimed = true
		}
		var n int
		_ = a.S.DB.QueryRow("SELECT count(*) FROM screenings WHERE state IN ('queued','creating','waiting-ready','probing','replacing') AND stop=0").Scan(&n)
		if n == 0 {
			return claimed
		}
		select {
		case <-ctx.Done():
			return claimed
		case <-time.After(time.Millisecond):
		}
	}
	return claimed
}
func freshEgress(ip, country string) probe.Egress {
	return probe.Egress{IP: ip, Country: country, ObservedAt: time.Now().UTC(), Source: probe.EgressSource}
}
func testScreenConfig() screeningConfig {
	return screeningConfig{Protocol: "tcp", Port: 443, TimeoutSeconds: 1, IntervalSeconds: 2, Successes: 2, Failures: 2, ReadySeconds: 30, MaxDurationSeconds: 60, MaxAttempts: 3, AutoReplace: true, BudgetUSD: 3, CostPerAttemptUSD: 1, RiskAccepted: true, ControlProbeID: "manager"}
}
func seedScreenProbe(t *testing.T, h *harness) string {
	t.Helper()
	id := store.ID()
	eg, _ := json.Marshal(freshEgress("223.5.5.5", "CN"))
	_, e := h.a.S.DB.Exec("INSERT INTO connectivity_probes(id,name,role,expected_ip,last_seen,version,egress,token_hash) VALUES(?,?,'domestic','223.5.5.5',?,'fixture',?,?)", id, "国内隔离替身", store.Now(), eg, store.Hash("fixture-probe-bearer-token-123456789"))
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func newScreenHarness(t *testing.T) (*harness, *screeningFake, mutation) {
	t.Helper()
	h := setup(t)
	aid := h.add(t)
	f := &screeningFake{fakeProvider: h.fake, a: h.a, method: "delete-recreate"}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
	h.a.connectivity.tick = time.Millisecond
	h.a.connectivity.observe = func(context.Context) (probe.Egress, error) { return freshEgress("1.1.1.1", "US"), nil }
	h.a.connectivity.lookup = func(_ context.Context, ip string) (probe.Egress, error) { return freshEgress(ip, "CN"), nil }
	h.a.connectivity.check = func(context.Context, probe.Target, time.Duration) probe.Result { return fixtureProbeResult(true) }
	b := intent(aid)
	c := testScreenConfig()
	c.ProbeID = seedScreenProbe(t, h)
	b.Screening = &c
	b = h.confirm(t, b)
	r := h.request("POST", "jobs", b, true)
	if r.Code != 202 {
		t.Fatalf("submit: %d %s", r.Code, r.Body.String())
	}
	if !h.a.runOne(context.Background()) {
		t.Fatal("job not claimed")
	}
	return h, f, b
}
func fixtureProbeResult(success bool) probe.Result {
	c := "timeout"
	if success {
		c = "success"
	}
	now := time.Now().UTC()
	return probe.Result{Success: success, Classification: c, StartedAt: now, CompletedAt: now, LatencyMS: 1}
}
func screenRequest(h *harness, method, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, h.a.Origin+"/api/"+path, strings.NewReader(string(raw)))
	r.Header.Set("Origin", h.a.Origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", h.csrf)
	r.AddCookie(h.cookie)
	w := httptest.NewRecorder()
	h.a.Handler().ServeHTTP(w, r)
	return w
}
func emulateCN(ctx context.Context, a *App, success func(int) bool) <-chan error {
	return emulateCNResults(ctx, a, func(n int) probe.Result { return fixtureProbeResult(success(n)) })
}
func emulateCNResults(ctx context.Context, a *App, resultFor func(int) probe.Result) <-chan error {
	done := make(chan error, 1)
	go func() {
		n := 0
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			var id string
			var data []byte
			e := a.S.DB.QueryRow("UPDATE probe_tasks SET state='leased' WHERE id=(SELECT id FROM probe_tasks WHERE state='queued' ORDER BY created,id LIMIT 1) AND state='queued' RETURNING id,target").Scan(&id, &data)
			if e == nil {
				n++
				result := resultFor(n)
				raw, _ := json.Marshal(result)
				eg, _ := json.Marshal(freshEgress("223.5.5.5", "CN"))
				_, e = a.S.DB.Exec("UPDATE probe_tasks SET state='done',result=?,egress=? WHERE id=?", raw, eg, id)
				if e != nil {
					done <- e
					return
				}
			}
			t := time.NewTimer(time.Millisecond)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
	return done
}
func runScreen(t *testing.T, h *harness, id string, success func(int) bool) screeningView {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := emulateCN(ctx, h.a, success)
	if !driveScreenings(ctx, h.a) {
		t.Fatal("screening not claimed")
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	v, _, e := h.a.screenLoad(id)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestScreeningCNAndControlSuccessAcceptsOwnedInstance(t *testing.T) {
	h, f, b := newScreenHarness(t)
	v := runScreen(t, h, b.ID, func(int) bool { return true })
	if v.State != "accepted" || f.creates != 1 || f.replacements != 0 || len(v.Rounds[0].Evidence) != 2 {
		t.Fatalf("%+v writes %d/%d", v, f.creates, f.replacements)
	}
	r := h.request("GET", "screenings", nil, true)
	if r.Code != 200 || strings.Contains(r.Body.String(), "fixture-cloud-secret") || strings.Contains(r.Body.String(), "credentials") {
		t.Fatal("public data leaked/failed", r.Body.String())
	}
}
func TestScreeningSameIPRecordedAndAttemptsBounded(t *testing.T) {
	h, f, b := newScreenHarness(t)
	v := runScreen(t, h, b.ID, func(int) bool { return false })
	if v.State != "exhausted" || f.creates != 3 || f.replacements != 2 || len(v.Rounds) != 3 || !v.Rounds[1].RepeatedIP || v.EstimatedUSD != 3 {
		t.Fatalf("state=%s create=%d replace=%d rounds=%+v", v.State, f.creates, f.replacements, v.Rounds)
	}
}
func TestScreeningNativeIPReplacementPreservesVM(t *testing.T) {
	for _, method := range []string{"aws-stop-start", "azure-public-ip"} {
		t.Run(method, func(t *testing.T) {
			h, f, b := newScreenHarness(t)
			f.method = method
			_, payload, _ := h.a.screenLoad(b.ID)
			payload.Provider = "aws"
			if method == "azure-public-ip" {
				payload.Provider = "azure"
			}
			raw, _ := json.Marshal(payload)
			sealed, _ := h.a.S.Seal(raw, "screening:"+b.ID)
			h.a.S.DB.Exec("UPDATE screenings SET payload=? WHERE id=?", sealed, b.ID)
			f.ips = []string{"8.8.8.8", "8.8.4.4"}
			v := runScreen(t, h, b.ID, func(n int) bool { return n > 2 })
			if v.State != "accepted" || f.creates != 1 || f.replacements != 1 || v.Rounds[0].ResourceID != v.Rounds[1].ResourceID || v.IP != "8.8.4.4" {
				t.Fatalf("%+v creates=%d repl=%d", v, f.creates, f.replacements)
			}
		})
	}
}
func TestScreeningBudgetStopsBeforeDestruction(t *testing.T) {
	h, f, b := newScreenHarness(t)
	v, p, _ := h.a.screenLoad(b.ID)
	p.Mutation.Screening.BudgetUSD = 1
	raw, _ := json.Marshal(p)
	sealed, _ := h.a.S.Seal(raw, "screening:"+b.ID)
	_, _ = h.a.S.DB.Exec("UPDATE screenings SET payload=? WHERE id=?", sealed, b.ID)
	v = runScreen(t, h, b.ID, func(int) bool { return false })
	if v.State != "exhausted" || f.creates != 1 || f.replacements != 0 {
		t.Fatalf("budget: %s %d %d", v.State, f.creates, f.replacements)
	}
}
func TestScreeningUncertainCreateOrDeleteNeverStartsAnother(t *testing.T) {
	for _, phase := range []string{"create", "delete"} {
		t.Run(phase, func(t *testing.T) {
			h, f, b := newScreenHarness(t)
			if phase == "create" {
				f.createErr = &cloud.Error{Code: "response lost", Uncertain: true}
			} else {
				f.replaceErr = &cloud.Error{Code: "response lost", Uncertain: true}
			}
			v := runScreen(t, h, b.ID, func(int) bool { return false })
			if v.State != "unknown" || f.creates != 1 {
				t.Fatalf("%s %+v create%d", phase, v, f.creates)
			}
			if h.a.runScreeningOne(context.Background()) {
				t.Fatal("unknown replayed")
			}
		})
	}
}
func TestScreeningStopAndExternalChangeNeverDelete(t *testing.T) {
	for _, scenario := range []string{"stop", "changed", "egress", "offline"} {
		t.Run(scenario, func(t *testing.T) {
			h, f, b := newScreenHarness(t)
			switch scenario {
			case "stop":
				r := screenRequest(h, "POST", "screenings/"+b.ID+"/stop", nil)
				if r.Code != 200 {
					t.Fatal(r.Body.String())
				}
				if h.a.runScreeningOne(context.Background()) {
					t.Fatal("stopped claimed")
				}
				if f.creates != 0 {
					t.Fatal("stopped created")
				}
				return
			case "changed":
				f.fingerprintChange = true
			case "egress":
				h.a.connectivity.observe = func(context.Context) (probe.Egress, error) { return freshEgress("223.5.5.5", "CN"), nil }
			case "offline":
				_, _ = h.a.S.DB.Exec("UPDATE connectivity_probes SET last_seen=0")
			}
			v := runScreen(t, h, b.ID, func(int) bool { return false })
			if v.State != "paused" || f.replacements != 0 {
				t.Fatalf("%s %s replacements%d", scenario, v.State, f.replacements)
			}
		})
	}
}
func TestScreeningCloudWriteAndSecretsSurviveRestartWithoutReplay(t *testing.T) {
	h, f, b := newScreenHarness(t)
	f.createErr = &cloud.Error{Code: "response lost", Uncertain: true}
	_ = runScreen(t, h, b.ID, func(int) bool { return false })
	var encrypted []byte
	_ = h.a.S.DB.QueryRow("SELECT payload FROM screenings WHERE id=?", b.ID).Scan(&encrypted)
	if strings.Contains(string(encrypted), "fixture-cloud-secret") {
		t.Fatal("unencrypted snapshot")
	}
	if e := h.a.S.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(h.dir)
	if e != nil {
		t.Fatal(e)
	}
	h.a.S = s
	t.Cleanup(func() { s.Close() })
	if h.a.runScreeningOne(context.Background()) {
		t.Fatal("replayed")
	}
	r := screenRequest(h, "POST", "jobs/"+b.ID+"/reconcile", nil)
	if r.Code != 409 {
		t.Fatal("ordinary reconcile bypass", r.Code, r.Body.String())
	}
}
func TestScreeningNormalWorkersContinueDuringProbeWait(t *testing.T) {
	h, f, b := newScreenHarness(t)
	started := make(chan struct{})
	f.beforeCreate = func() { close(started) }
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { driveScreenings(ctx, h.a); close(finished) }()
	<-started
	f.fakeProvider.mu.Lock()
	f.fakeProvider.items = append(f.fakeProvider.items, cloud.Instance{ID: "other-server", Name: "other-server", Region: "r", State: "running", Actions: []string{"reboot"}})
	f.fakeProvider.mu.Unlock()
	other := mutation{ID: store.ID(), AccountID: b.AccountID, Action: "reboot", Region: "r", Target: "other-server", Name: "other-server"}
	other = h.confirm(t, other)
	if r := h.request("POST", "jobs", other, true); r.Code != 202 {
		t.Fatal(r.Body.String())
	}
	if !h.a.runOne(context.Background()) {
		t.Fatal("normal task blocked")
	}
	var state string
	_ = h.a.S.DB.QueryRow("SELECT state FROM jobs WHERE id=?", other.ID).Scan(&state)
	if state != "succeeded" {
		t.Fatal("ordinary task", state)
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("workflow did not stop")
	}
}
func TestScreeningConfigRejectsAmbiguousAndDangerousTargets(t *testing.T) {
	h, _, b := newScreenHarness(t)
	for _, change := range []func(*screeningConfig){func(c *screeningConfig) { c.Port = 0 }, func(c *screeningConfig) { c.MaxAttempts = 0 }, func(c *screeningConfig) { c.BudgetUSD = 0 }, func(c *screeningConfig) { c.RiskAccepted = false }, func(c *screeningConfig) { c.Successes = 1 }, func(c *screeningConfig) { c.Challenge = "password\n" }, func(c *screeningConfig) { c.ControlProbeID = c.ProbeID }} {
		c := *b.Screening
		change(&c)
		if e := h.a.validateScreening(c); e == nil {
			t.Fatal("unsafe config accepted", fmt.Sprint(c))
		}
	}
}

func TestScreeningNonNetworkEvidenceCannotAuthorizeDeletion(t *testing.T) {
	for _, classification := range []string{"invalid_target", "egress_unverified", "canceled", "tls_error", "challenge_mismatch", "redirect_rejected", "http_status", "response_too_large"} {
		t.Run(classification, func(t *testing.T) {
			h, f, b := newScreenHarness(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			done := emulateCNResults(ctx, h.a, func(int) probe.Result { r := fixtureProbeResult(false); r.Classification = classification; return r })
			driveScreenings(ctx, h.a)
			cancel()
			<-done
			v, _, _ := h.a.screenLoad(b.ID)
			if f.replacements != 0 || v.State != "paused" {
				t.Fatalf("insufficient %s evidence authorized %d replacements, state %s", classification, f.replacements, v.State)
			}
		})
	}
}
func TestScreeningStopCannotOverwriteConcurrentCloudIntent(t *testing.T) {
	h, _, b := newScreenHarness(t)
	v, _, _ := h.a.screenLoad(b.ID)
	v.State = "creating"
	v.Rounds = []screeningRound{{ResourceID: "exact-created-instance", Operation: cloud.Operation{ResourceID: "exact-created-instance"}, State: "create-dispatching"}}
	raw, _ := json.Marshal(v)
	// Deterministic interleaving: another worker persists its intent just after
	// stop's earlier snapshot, before stop's UPDATE. A stale full-row save loses it.
	_, e := h.a.S.DB.Exec("CREATE TRIGGER screen_stop_interleave BEFORE UPDATE OF stop ON screenings WHEN NEW.stop=1 BEGIN UPDATE screenings SET busy=1,state='creating',data='" + strings.ReplaceAll(string(raw), "'", "''") + "' WHERE id=NEW.id; END")
	if e != nil {
		t.Fatal(e)
	}
	r := screenRequest(h, "POST", "screenings/"+b.ID+"/stop", nil)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	latest, _, e := h.a.screenLoad(b.ID)
	if e != nil || len(latest.Rounds) != 1 || latest.Rounds[0].ResourceID != "exact-created-instance" {
		t.Fatal("stop overwrote live cloud intent", latest, e)
	}
}
func TestScreeningReplacementReservesNextAttemptBeforeCloudWrite(t *testing.T) {
	h, f, b := newScreenHarness(t)
	var reserved float64
	f.beforeReplace = func() { v, _, _ := h.a.screenLoad(b.ID); reserved = v.EstimatedUSD }
	_ = runScreen(t, h, b.ID, func(n int) bool { return n > 2 })
	if reserved < 2 {
		t.Fatalf("replacement issued before next-round fee reservation: %v", reserved)
	}
}

func TestScreeningReadinessSlowAndNoListenerNeverDeletes(t *testing.T) {
	for _, ready := range []bool{true, false} {
		t.Run(fmt.Sprint(ready), func(t *testing.T) {
			h, f, b := newScreenHarness(t)
			n := 0
			h.a.connectivity.check = func(context.Context, probe.Target, time.Duration) probe.Result {
				n++
				return fixtureProbeResult(ready && n > 2)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := emulateCN(ctx, h.a, func(int) bool { return true })
			driveScreenings(ctx, h.a)
			cancel()
			<-done
			v, _, _ := h.a.screenLoad(b.ID)
			if f.replacements != 0 || f.creates != 1 {
				t.Fatal("unready service authorized destructive action")
			}
			if ready && v.State != "accepted" {
				t.Fatal(v.State, v.Stage)
			}
			if !ready && v.State != "waiting-ready" && v.State != "paused" {
				t.Fatal(v.State, v.Stage)
			}
		})
	}
}
func TestScreeningRestartActiveAndQueuedOnlyReadOnly(t *testing.T) {
	for _, state := range []string{"queued", "creating", "waiting-ready", "probing", "replacing"} {
		t.Run(state, func(t *testing.T) {
			h, f, b := newScreenHarness(t)
			v, _, _ := h.a.screenLoad(b.ID)
			v.State = state
			v.Stage = "cloud-write-sent"
			v.Rounds = []screeningRound{{Attempt: 1, ResourceID: "known-resource", Operation: cloud.Operation{ResourceID: "known-resource"}, Writes: []cloud.ScreeningWrite{{Action: "create-instance", State: "dispatching"}}}}
			if h.a.screenSave(&v) != nil {
				t.Fatal("save")
			}
			_, _ = h.a.S.DB.Exec("UPDATE screenings SET busy=1 WHERE id=?", b.ID)
			if e := h.a.S.Close(); e != nil {
				t.Fatal(e)
			}
			s, e := store.Open(h.dir)
			if e != nil {
				t.Fatal(e)
			}
			h.a.S = s
			t.Cleanup(func() { s.Close() })
			v, _, e = h.a.screenLoad(b.ID)
			if e != nil || v.State != "paused" || len(v.Rounds) != 1 || len(v.Rounds[0].Writes) != 1 {
				t.Fatal("lost recovery evidence", v, e)
			}
			if h.a.runScreeningOne(context.Background()) || f.creates != 0 {
				t.Fatal("restart replayed write")
			}
		})
	}
}
func TestScreeningEvidenceFreshnessAndExactTarget(t *testing.T) {
	c := testScreenConfig()
	now := time.Now().UTC()
	result := fixtureProbeResult(false)
	control := fixtureProbeResult(true)
	eg := freshEgress("223.5.5.5", "CN")
	ce := freshEgress("1.1.1.1", "US")
	e := screeningEvidence{Target: c.target("8.8.8.8"), Domestic: result, Control: control, DomesticEgress: eg, ControlEgress: ce}
	v := screeningView{IP: "8.8.8.8", Rounds: []screeningRound{{Evidence: []screeningEvidence{e, e}}}}
	if err := validReplacementEvidence(v, &c, now); err != nil {
		t.Fatal(err)
	}
	cases := []func(*screeningEvidence){func(e *screeningEvidence) { e.Target.IP = "1.1.1.1" }, func(e *screeningEvidence) { e.Target.Port = 22 }, func(e *screeningEvidence) { e.Domestic.CompletedAt = now.Add(-2 * time.Minute) }, func(e *screeningEvidence) { e.Control.StartedAt = now.Add(-time.Minute) }, func(e *screeningEvidence) { e.Control.CompletedAt = now.Add(-2 * time.Minute) }, func(e *screeningEvidence) { e.Control.Success = false }, func(e *screeningEvidence) { e.Domestic.Classification = "egress_unverified" }, func(e *screeningEvidence) { e.DomesticEgress.Country = "US" }, func(e *screeningEvidence) { e.ControlEgress.Country = "CN" }}
	for n, change := range cases {
		x := v
		x.Rounds = []screeningRound{{Evidence: []screeningEvidence{e, e}}}
		change(&x.Rounds[0].Evidence[1])
		if validReplacementEvidence(x, &c, now) == nil {
			t.Fatalf("unsafe evidence case%d authorized", n)
		}
	}
}
func TestScreeningStopDuringCreateKeepsResourceAndNoNextWrite(t *testing.T) {
	h, f, b := newScreenHarness(t)
	f.beforeCreate = func() { _, _ = h.a.S.DB.Exec("UPDATE screenings SET stop=1 WHERE id=?", b.ID) }
	v := runScreen(t, h, b.ID, func(int) bool { return false })
	if f.creates != 1 || f.replacements != 0 || v.State != "stopped" || v.ResourceID == "" || len(v.Rounds[0].Writes) == 0 {
		t.Fatalf("stop failed %+v %d/%d", v, f.creates, f.replacements)
	}
}
func TestScreeningConcurrentWorkflowsSameAccountNoBusinessLock(t *testing.T) {
	h, f, b := newScreenHarness(t)
	second := intent(b.AccountID)
	second.Screening = b.Screening
	second = h.confirm(t, second)
	if r := h.request("POST", "jobs", second, true); r.Code != 202 {
		t.Fatal(r.Body.String())
	}
	h.a.runOne(context.Background())
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	f.beforeCreate = func() { arrived <- struct{}{}; <-release }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	for n := 0; n < 2; n++ {
		go func() { defer wg.Done(); driveScreenings(ctx, h.a) }()
	}
	for n := 0; n < 2; n++ {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			close(release)
			cancel()
			wg.Wait()
			t.Fatal("workflows serialized/blocked on same account")
		}
	}
	close(release)
	cancel()
	wg.Wait()
	if f.creates != 2 {
		t.Fatal(f.creates)
	}
}

func TestScreeningFullWorkflowUsesActualHTTPSProbeProtocol(t *testing.T) {
	h, f, b := newScreenHarness(t)
	server := httptest.NewTLSServer(h.a.Handler())
	defer server.Close()
	h.a.Origin = server.URL
	peer := &cnProbeHTTP{h: h, s: server}
	token := "fixture-probe-bearer-token-123456789"
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan struct{})
	var checks int
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			eg := freshEgress("223.5.5.5", "CN")
			status, raw := peer.req(t, "POST", "/probe/v1/poll", probe.PollRequest{Version: "fixture", EgressIP: eg.IP, EgressCountry: eg.Country, EgressObservedAt: eg.ObservedAt, EgressSource: eg.Source}, token, false)
			if status != 200 {
				t.Error("poll", status, string(raw))
				return
			}
			var response probe.PollResponse
			if json.Unmarshal(raw, &response) != nil {
				t.Error("poll response")
				return
			}
			if response.Task != nil {
				checks++
				task := response.Task
				result := fixtureProbeResult(checks > 2)
				status, raw = peer.req(t, "POST", "/probe/v1/results", probe.ResultRequest{TaskID: task.TaskID, LeaseID: task.LeaseID, Target: task.Target, Result: result, Egress: freshEgress("223.5.5.5", "CN")}, token, false)
				if status != 204 {
					t.Error("result", status, string(raw))
					return
				}
			}
			timer := time.NewTimer(time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	if !driveScreenings(ctx, h.a) {
		t.Fatal("not claimed")
	}
	cancel()
	<-done
	v, _, e := h.a.screenLoad(b.ID)
	if e != nil || v.State != "accepted" || f.creates != 2 || f.replacements != 1 || checks != 4 {
		t.Fatalf("full HTTP workflow %+v err%v writes%d/%d checks%d", v, e, f.creates, f.replacements, checks)
	}
}

func TestScreeningPartialCreationLedgerRetainsEveryPossiblyBillableResource(t *testing.T) {
	op := cloud.Operation{ResourceID: "vm-owned", Owned: []string{"pip-old", "nic-owned", "nsg-owned", "disk-owned"}}
	ledger := mergeScreenResources(nil, op)
	next := op
	next.Owned = append(next.Owned, "pip-new")
	next.Ledger = &cloud.ResourceLedger{Resources: []cloud.RelatedResource{{ID: "pip-old", Kind: "public-ip", State: "deleted-confirmed"}, {ID: "pip-new", Kind: "public-ip", State: "unattached-billable"}}}
	ledger = mergeScreenResources(ledger, next)
	if len(ledger) != 6 {
		t.Fatal("lost associated IDs", ledger)
	}
	ledger = mergeRelated(ledger, []cloud.RelatedResource{{ID: "vm-owned", Kind: "instance", State: "running"}})
	found := false
	for _, r := range ledger {
		if r.ID == "pip-old" {
			found = r.State == "deleted-confirmed"
		}
	}
	if !found {
		t.Fatal("later reads overwrote old deletion history", ledger)
	}
}

func TestScreeningReadOnlyReconcileHonorsCredentialRetirementAndRevision(t *testing.T) {
	for _, change := range []string{"retired=1", "revision=revision+1"} {
		t.Run(change, func(t *testing.T) {
			h, f, b := newScreenHarness(t)
			v := runScreen(t, h, b.ID, func(int) bool { return true })
			if v.State != "accepted" {
				t.Fatal(v.State)
			}
			before := f.inspects
			if _, e := h.a.S.DB.Exec("UPDATE accounts SET "+change+" WHERE id=?", b.AccountID); e != nil {
				t.Fatal(e)
			}
			r := screenRequest(h, "POST", "screenings/"+b.ID+"/reconcile", nil)
			if r.Code != 409 || f.inspects != before {
				t.Fatalf("retired/revised credential reused for cloud read: http%d reads before%d after%d", r.Code, before, f.inspects)
			}
		})
	}
}

func TestScreeningExplicitCloudRejectionShowsActualSafeReason(t *testing.T) {
	h, f, b := newScreenHarness(t)
	f.createErr = &cloud.Error{Status: 429, Code: "provider quota exceeded"}
	v := runScreen(t, h, b.ID, func(int) bool { return false })
	if v.State != "failed" || !strings.Contains(v.Reason, "HTTP 429") || !strings.Contains(v.Reason, "provider quota exceeded") || f.replacements != 0 {
		t.Fatalf("cloud rejection hidden or retried: %+v", v)
	}
}
