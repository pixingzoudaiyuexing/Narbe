package app

import (
	"context"
	"encoding/json"
	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
	"strings"
	"testing"
	"time"
)

func ipHarness(t *testing.T) (*harness, *screeningFake, mutation) {
	t.Helper()
	h := setup(t)
	aid := h.add(t)
	f := &screeningFake{fakeProvider: h.fake, a: h.a, method: "do-reserved-allocate", ips: []string{"8.8.8.8", "8.8.4.4"}}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
	h.fake.mu.Lock()
	h.fake.items = []cloud.Instance{{ID: "existing-7", Name: "retained-server", Region: "r", State: "running", IPs: []string{"8.8.8.8"}}}
	h.fake.mu.Unlock()
	h.a.connectivity.tick = time.Millisecond
	return h, f, mutation{ID: store.ID(), AccountID: aid, Action: "ip-change", Region: "r", Target: "existing-7", Name: "retained-server", IPChange: &cloud.IPChange{Method: "do-reserved-allocate", Snapshot: "fixture-ip", RiskAccepted: true}}
}
func TestManualIPChangeUsesNormalConfirmationAndKeepsExistingVM(t *testing.T) {
	h, f, b := ipHarness(t)
	b = h.confirm(t, b)
	if r := h.request("POST", "jobs", b, true); r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	if !h.a.runOne(context.Background()) {
		t.Fatal("not queued")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	driveScreenings(ctx, h.a)
	v, _, e := h.a.screenLoad(b.ID)
	if e != nil || v.Kind != "manual-ip" || v.State != "accepted" || v.IP != "8.8.4.4" || f.creates != 0 || f.replacements != 1 || v.ResourceID != b.Target {
		t.Fatalf("%+v %v creates%d changes%d", v, e, f.creates, f.replacements)
	}
	if r := h.request("POST", "jobs", b, true); r.Code != 200 {
		t.Fatal("idempotent submit", r.Code, r.Body.String())
	}
	if h.a.runOne(context.Background()) || h.a.runScreeningOne(context.Background()) {
		t.Fatal("completed operation replayed")
	}
}
func TestManualIPChangeStaleIdentityAndSnapshotRejectBeforeQueue(t *testing.T) {
	for _, scenario := range []string{"snapshot", "target", "name", "risk", "method", "autoDelete"} {
		t.Run(scenario, func(t *testing.T) {
			h, f, b := ipHarness(t)
			switch scenario {
			case "snapshot":
				b.IPChange.Snapshot = "foreign"
			case "target":
				b.Target = "not-owned"
			case "name":
				b.Name = "different"
			case "risk":
				b.IPChange.RiskAccepted = false
			case "method":
				b.IPChange.Method = "delete"
			case "autoDelete":
				c := testScreenConfig()
				c.ProbeID = seedScreenProbe(t, h)
				b.Screening = &c
			}
			r := screenRequest(h, "POST", "confirm", b)
			if r.Code == 200 || f.creates != 0 || f.replacements != 0 {
				t.Fatalf("unsafe %s %d %s", scenario, r.Code, r.Body.String())
			}
		})
	}
}
func TestExistingIPReadOnlyCheckHTTPAndExactRequestId(t *testing.T) {
	h, f, b := ipHarness(t)
	h.a.connectivity.observe = func(context.Context) (probe.Egress, error) { return freshEgress("1.1.1.1", "US"), nil }
	h.a.connectivity.check = func(context.Context, probe.Target, time.Duration) probe.Result { return fixtureProbeResult(true) }
	c := testScreenConfig()
	c.AutoReplace = false
	c.ProbeID = seedScreenProbe(t, h)
	body := map[string]any{"id": store.ID(), "accountId": b.AccountID, "region": "r", "target": b.Target, "ip": "8.8.8.8", "config": c}
	for n := 0; n < 2; n++ {
		r := screenRequest(h, "POST", "ip/check", body)
		if r.Code != 202 {
			t.Fatal("check submit", r.Code, r.Body.String())
		}
	}
	id := body["id"].(string)
	v := runScreen(t, h, id, func(int) bool { return true })
	if v.State != "accepted" || v.Kind != "check" || f.creates != 0 || f.replacements != 0 {
		t.Fatalf("%+v cloudwrites%d/%d", v, f.creates, f.replacements)
	}
	r := screenRequest(h, "GET", "ip?accountId="+b.AccountID+"&region=r&target="+b.Target, nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"lastCheck"`) {
		t.Fatal("IP info/recent result", r.Code, r.Body.String())
	}
	for _, ip := range []string{"1.1.1.1", "127.0.0.1", "169.254.169.254", "10.0.0.1"} {
		body["id"] = store.ID()
		body["ip"] = ip
		if r := screenRequest(h, "POST", "ip/check", body); r.Code == 202 {
			t.Fatal("arbitrary scan", ip)
		}
	}
	body["id"] = id
	body["ip"] = "8.8.8.8"
	c.Port = 80
	body["config"] = c
	if r := screenRequest(h, "POST", "ip/check", body); r.Code != 409 {
		t.Fatal("request reuse", r.Code, r.Body.String())
	}
}
func TestProbeDeleteRevokesAndHidesWithoutDeletingHistory(t *testing.T) {
	h, _, _ := ipHarness(t)
	id := seedScreenProbe(t, h)
	if r := screenRequest(h, "POST", "probes/"+id+"/disable", nil); r.Code != 200 {
		t.Fatal(r.Code)
	}
	items, _ := h.a.probeViews()
	if len(items) != 1 || items[0].Enabled {
		t.Fatal("disable should retain visible identity", items)
	}
	if r := screenRequest(h, "DELETE", "probes/"+id, nil); r.Code != 200 {
		t.Fatal(r.Code)
	}
	items, _ = h.a.probeViews()
	if len(items) != 0 {
		t.Fatal("deleted probe remains visible")
	}
	var token string
	var deleted int
	if e := h.a.S.DB.QueryRow("SELECT token_hash,deleted FROM connectivity_probes WHERE id=?", id).Scan(&token, &deleted); e != nil || token != "" || deleted != 1 {
		t.Fatal("history/revocation", token, deleted, e)
	}
}
func TestSchedulerReleasesCloudClaimsWhileManyProbesWait(t *testing.T) {
	h, f, b := newScreenHarness(t)
	// Seed more waiting workflows than both cloud dispatch slots; a third create
	// must still run while all earlier probe leases have no result.
	for n := 0; n < 3; n++ {
		m := intent(b.AccountID)
		m.Screening = b.Screening
		m = h.confirm(t, m)
		if r := h.request("POST", "jobs", m, true); r.Code != 202 {
			t.Fatal(r.Body.String())
		}
		h.a.runOne(context.Background())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.a.runControlOne(context.Background())
		h.a.runScreeningOne(context.Background())
		var count int
		h.a.S.DB.QueryRow("SELECT count(*) FROM probe_tasks WHERE state='queued'").Scan(&count)
		if count == 4 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	var busy, queued int
	h.a.S.DB.QueryRow("SELECT sum(busy) FROM screenings").Scan(&busy)
	h.a.S.DB.QueryRow("SELECT count(*) FROM probe_tasks WHERE state='queued'").Scan(&queued)
	if f.creates != 4 || busy != 0 || queued != 4 {
		t.Fatalf("starvation creates%d busy%d queued%d", f.creates, busy, queued)
	}
	var payload []byte
	h.a.S.DB.QueryRow("SELECT data FROM screenings WHERE id=?", b.ID).Scan(&payload)
	var view screeningView
	json.Unmarshal(payload, &view)
	if view.Phase != "domestic-await" || view.NextRun == 0 {
		t.Fatal("waiting was not persisted", view.Phase, view.NextRun)
	}
}

func TestAutoLinodeOnlyExplicitQuotaAllowsOwnedRecreate(t *testing.T) {
	for _, scenario := range []string{"quota", "permission", "lost"} {
		t.Run(scenario, func(t *testing.T) {
			h, f, b := newScreenHarness(t)
			f.method = "linode-allocate"
			v, p, _ := h.a.screenLoad(b.ID)
			_ = v
			p.Provider = "linode"
			raw, _ := json.Marshal(p)
			sealed, _ := h.a.S.Seal(raw, "screening:"+b.ID)
			h.a.S.DB.Exec("UPDATE screenings SET payload=? WHERE id=?", sealed, b.ID)
			switch scenario {
			case "quota":
				f.replaceErr = cloud.ErrIPQuota
			case "permission":
				f.replaceErr = &cloud.Error{Code: "403 Permission denied"}
			case "lost":
				f.replaceErr = &cloud.Error{Code: "response lost", Uncertain: true}
			}
			result := runScreen(t, h, b.ID, func(n int) bool { return n > 2 })
			if scenario == "quota" {
				if f.creates != 2 || result.State != "accepted" || len(result.Rounds[0].ReplacementHistory) != 1 {
					t.Fatalf("quota fallback missing creates%d %+v", f.creates, result)
				}
			} else if f.creates != 1 || result.State != "unknown" {
				t.Fatalf("nonquota failure deleted/recreated: %s creates%d state%s", scenario, f.creates, result.State)
			}
		})
	}
}

func TestManualWorkflowIsAtomicAndNeverVisibleAsCreate(t *testing.T) {
	h, f, b := ipHarness(t)
	c := testScreenConfig()
	c.AutoReplace = false
	c.ProbeID = seedScreenProbe(t, h)
	b.Screening = &c
	h.a.connectivity.observe = func(context.Context) (probe.Egress, error) { return freshEgress("1.1.1.1", "US"), nil }
	h.a.connectivity.check = func(context.Context, probe.Target, time.Duration) probe.Result { return fixtureProbeResult(true) }
	b = h.confirm(t, b)
	if r := h.request("POST", "jobs", b, true); r.Code != 202 {
		t.Fatal(r.Body.String())
	}
	// The database observes the row exactly as it becomes claimable. This catches
	// the former two-transaction initialization even without timing luck.
	_, e := h.a.S.DB.Exec(`CREATE TRIGGER manual_initial_kind BEFORE INSERT ON screenings WHEN EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id AND action='ip-change') AND (json_extract(NEW.data,'$.kind')!='manual-ip' OR json_extract(NEW.data,'$.phase')!='manual-begin' OR NEW.state!='replacing') BEGIN SELECT RAISE(ABORT,'manual workflow exposed as create'); END`)
	if e != nil {
		t.Fatal(e)
	}
	h.a.runOne(context.Background())
	v, _, e := h.a.screenLoad(b.ID)
	if e != nil || v.Kind != "manual-ip" || v.Phase != "manual-begin" || len(v.Rounds) != 1 {
		t.Fatal("non-atomic manual setup", v, e)
	}
	v = runScreen(t, h, b.ID, func(int) bool { return true })
	if v.State != "accepted" || f.creates != 0 || f.replacements != 1 {
		t.Fatal("manual operation changed workflow scope", v.State, f.creates, f.replacements)
	}
}

func TestAzureCleanupPlansIncludeEveryOwnedPreviousAddress(t *testing.T) {
	base := cloud.Operation{ResourceID: "vm", Region: "r", Owned: []string{"pip-0"}}
	first := cloud.Operation{ResourceID: "vm", Region: "r", IP: &cloud.IPOperation{TargetID: "vm", Method: "azure-public-ip", OldResourceID: "pip-0", NewResourceID: "pip-1", OldIP: "8.8.8.8", NewIP: "8.8.4.4", RequestID: "first-token", Checkpoint: map[string]string{}, After: &cloud.IPInfo{InstanceID: "vm", Region: "r", PrimaryIPv4: "8.8.4.4"}}}
	second := cloud.Operation{ResourceID: "vm", Region: "r", IP: &cloud.IPOperation{TargetID: "vm", Method: "azure-public-ip", OldResourceID: "pip-1", NewResourceID: "pip-2", OldIP: "8.8.4.4", NewIP: "9.9.9.9", RequestID: "second-token", Checkpoint: map[string]string{}, After: &cloud.IPInfo{InstanceID: "vm", Region: "r", PrimaryIPv4: "9.9.9.9"}}}
	v := screeningView{Kind: "screening", Rounds: []screeningRound{{CreateToken: "create-token", CreateOperation: base, Operation: first}, {CreateToken: "create-token", CreateOperation: base, Operation: second}}}
	plans := screeningCleanupPlans(v, second)
	if len(plans) != 2 || plans[0].IP.OldResourceID != "pip-0" || plans[1].IP.OldResourceID != "pip-1" {
		t.Fatal("older billed PIP omitted", plans)
	}
	if plans[0].IP.Checkpoint["cleanup-owner-token"] != "create-token" || plans[1].IP.Checkpoint["cleanup-owner-token"] != "first-token" {
		t.Fatal("wrong ownership tokens")
	}
	for _, p := range plans {
		if p.IP.NewResourceID != "pip-2" || p.IP.NewIP != "9.9.9.9" {
			t.Fatal("cleanup must preserve latest binding")
		}
	}
	plans[0].IP.Checkpoint["mutation"] = "test"
	if second.IP.Checkpoint["mutation"] != "" {
		t.Fatal("cleanup plan aliases active operation")
	}
	v.Kind = "manual-ip"
	if len(screeningCleanupPlans(v, second)) != 0 {
		t.Fatal("manual old addresses must be retained")
	}
}

func TestFirstActualIPWriteRejectsStaleEvidenceAndAllWritesHonorStop(t *testing.T) {
	h, _, b := newScreenHarness(t)
	v, p, _ := h.a.screenLoad(b.ID)
	now := time.Now().UTC()
	d := fixtureProbeResult(false)
	c := fixtureProbeResult(true)
	evidence := screeningEvidence{Target: p.Mutation.Screening.target("8.8.8.8"), Domestic: d, Control: c, DomesticEgress: freshEgress("223.5.5.5", "CN"), ControlEgress: freshEgress("1.1.1.1", "US")}
	v.Phase = "ip-advance"
	v.State = "replacing"
	v.IP = "8.8.8.8"
	v.Rounds = []screeningRound{{ResourceID: "owned", Operation: cloud.Operation{ResourceID: "owned", IP: &cloud.IPOperation{Writes: []cloud.ScreeningWrite{{Action: "stop-instance", State: "dispatching"}}}}, Evidence: []screeningEvidence{evidence, evidence}}}
	v.Rounds[0].Evidence[1].Control.CompletedAt = now.Add(-2 * time.Minute)
	if e := h.a.screenBeforeWrite(context.Background(), &v, p); e == nil {
		t.Fatal("first actual write accepted stale detection")
	}
	if len(v.Rounds[0].Writes) != 0 {
		t.Fatal("expired evidence reached dispatch")
	}
	v.Rounds[0].Evidence[1] = evidence
	if e := h.a.screenBeforeWrite(context.Background(), &v, p); e != nil {
		t.Fatal("fresh exact evidence rejected", e)
	}
	v.Rounds[0].Operation.IP.Writes = append(v.Rounds[0].Operation.IP.Writes, cloud.ScreeningWrite{Action: "start-instance", State: "dispatching"})
	v.Rounds[0].Evidence = nil
	if e := h.a.screenBeforeWrite(context.Background(), &v, p); e != nil {
		t.Fatal("continuing already dispatched operation needs no new detection", e)
	}
	h.a.S.DB.Exec("UPDATE screenings SET stop=1 WHERE id=?", b.ID)
	if e := h.a.screenBeforeWrite(context.Background(), &v, p); e == nil {
		t.Fatal("stopped workflow allowed another write")
	}
}
