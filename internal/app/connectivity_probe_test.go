package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

// All protocol requests cross a real local TLS HTTP server and App.Handler.
// Only the public geolocation lookup is replaced; no cloud adapter is called.
type cnProbeHTTP struct {
	h *harness
	s *httptest.Server
}

func newCNProbeHTTP(t *testing.T) *cnProbeHTTP {
	t.Helper()
	h := setup(t)
	h.a.connectivity.lookup = func(_ context.Context, ip string) (probe.Egress, error) {
		country := "CN"
		if ip == "8.8.8.8" {
			country = "US"
		}
		return probe.Egress{IP: ip, Country: country, ObservedAt: time.Now().UTC(), Source: probe.EgressSource}, nil
	}
	s := httptest.NewTLSServer(h.a.Handler())
	h.a.Origin = s.URL
	t.Cleanup(s.Close)
	t.Cleanup(func() {
		h.fake.mu.Lock()
		defer h.fake.mu.Unlock()
		if h.fake.calls != 0 {
			t.Errorf("probe protocol caused %d cloud writes", h.fake.calls)
		}
	})
	return &cnProbeHTTP{h, s}
}

func (p *cnProbeHTTP) req(t *testing.T, method, path string, body any, token string, admin bool) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest(method, p.s.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", p.s.URL)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if admin {
		r.AddCookie(p.h.cookie)
		r.Header.Set("X-CSRF-Token", p.h.csrf)
	}
	response, err := p.s.Client().Do(r)
	if err != nil {
		t.Error(err)
		return 0, nil
	}
	defer response.Body.Close()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.Error(err)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Error("sensitive protocol response missing no-store")
	}
	return response.StatusCode, b
}

func (p *cnProbeHTTP) register(t *testing.T, role, ip string) (string, string) {
	t.Helper()
	status, b := p.req(t, "POST", "/api/probes", map[string]string{"name": "受控探针", "role": role, "expectedIP": ip}, "", true)
	if status != 201 {
		t.Fatalf("register %d %s", status, b)
	}
	var created struct{ ID, RegistrationToken string }
	if json.Unmarshal(b, &created) != nil || created.ID == "" || len(created.RegistrationToken) < 32 {
		t.Fatalf("bad registration %s", b)
	}
	return created.ID, created.RegistrationToken
}

func (p *cnProbeHTTP) enroll(t *testing.T, registration string) probe.Identity {
	t.Helper()
	status, b := p.req(t, "POST", "/probe/v1/enroll", probe.EnrollRequest{RegistrationToken: registration, Version: "controlled-test"}, "", false)
	var id probe.Identity
	if status != 200 || json.Unmarshal(b, &id) != nil || id.ProbeID == "" || len(id.Token) < 32 {
		t.Fatalf("enroll %d %s", status, b)
	}
	return id
}

func cnProbeEgress(ip, country string) probe.Egress {
	return probe.Egress{IP: ip, Country: country, ObservedAt: time.Now().UTC(), Source: probe.EgressSource}
}

func cnPoll(e probe.Egress) probe.PollRequest {
	return probe.PollRequest{Version: "controlled-test", EgressIP: e.IP, EgressCountry: e.Country, EgressObservedAt: e.ObservedAt, EgressSource: e.Source, EgressError: e.Error}
}

func (p *cnProbeHTTP) fixture(t *testing.T, probeID, state string, stop bool) (string, probe.Task) {
	t.Helper()
	status, b := p.req(t, "POST", "/api/accounts", map[string]any{"name": "加密云凭据替身", "provider": "digitalocean", "credentials": map[string]string{"token": "cn-protocol-CLOUD-SECRET"}}, "", true)
	var acc account
	if status != 201 || json.Unmarshal(b, &acc) != nil {
		t.Fatalf("accountfixture %d %s", status, b)
	}
	id := store.ID()
	private, err := json.Marshal(screeningPayload{Credentials: cloud.Credentials{"token": "cn-protocol-CLOUD-SECRET"}, Provider: "digitalocean"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := p.h.a.S.Seal(private, "screening:"+id)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := json.Marshal(screeningView{ID: id, JobID: id, State: state, Stage: "检测替身", Provider: "digitalocean", Region: "r", Rounds: []screeningRound{}, Resources: []cloud.RelatedResource{}})
	_, err = p.h.a.S.DB.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,created,updated) VALUES(?,?,'create','r','','fixture',?,'screening','fixture',?,?)", id, acc.ID, sealed, store.Now(), store.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.h.a.S.DB.Exec("INSERT INTO screenings(id,job_id,state,stage,payload,data,stop,created,updated) VALUES(?,?,?,'fixture',?,?,?,?,?)", id, id, state, sealed, view, stop, store.Now(), store.Now())
	if err != nil {
		t.Fatal(err)
	}
	task := probe.Task{TaskID: store.ID(), LeaseID: store.ID(), Target: probe.Target{IP: "8.8.4.4", Port: 443, Protocol: "tcp"}, TimeoutMS: 1000}
	target, _ := json.Marshal(task.Target)
	_, err = p.h.a.S.DB.Exec("INSERT INTO probe_tasks(id,probe_id,screening_id,lease,target,timeout_ms,state,created,expires) VALUES(?,?,?,?,?,?,'queued',?,?)", task.TaskID, probeID, id, task.LeaseID, target, task.TimeoutMS, store.Now()-1, store.Now()+120)
	if err != nil {
		t.Fatal(err)
	}
	return id, task
}

func (p *cnProbeHTTP) lease(t *testing.T, id probe.Identity, e probe.Egress) probe.Task {
	t.Helper()
	status, b := p.req(t, "POST", "/probe/v1/poll", cnPoll(e), id.Token, false)
	var out probe.PollResponse
	if status != 200 || json.Unmarshal(b, &out) != nil || out.Task == nil {
		t.Fatalf("lease %d %s", status, b)
	}
	return *out.Task
}

func cnProbeReport(task probe.Task, e probe.Egress) probe.ResultRequest {
	now := time.Now().UTC()
	return probe.ResultRequest{TaskID: task.TaskID, LeaseID: task.LeaseID, Target: task.Target, Egress: e, Result: probe.Result{Success: true, Classification: "success", StartedAt: now.Add(-time.Millisecond), CompletedAt: now, LatencyMS: 1}}
}

func TestConnectivityProbeEnrollmentOneUseExpiryAndHTTPS(t *testing.T) {
	p := newCNProbeHTTP(t)
	_, registration := p.register(t, "domestic", "223.5.5.5")
	id := p.enroll(t, registration)
	if status, _ := p.req(t, "POST", "/probe/v1/enroll", probe.EnrollRequest{RegistrationToken: registration, Version: "test"}, "", false); status != 401 {
		t.Fatalf("reused enrollment %d", status)
	}
	_, expired := p.register(t, "domestic", "223.6.6.6")
	if _, err := p.h.a.S.DB.Exec("UPDATE probe_enrollments SET expires=? WHERE token_hash=?", store.Now()-1, store.Hash(expired)); err != nil {
		t.Fatal(err)
	}
	if status, _ := p.req(t, "POST", "/probe/v1/enroll", probe.EnrollRequest{RegistrationToken: expired, Version: "test"}, "", false); status != 401 {
		t.Fatalf("expired enrollment %d", status)
	}
	for _, token := range []string{"", strings.Repeat("x", 64), registration} {
		if status, _ := p.req(t, "POST", "/probe/v1/poll", cnPoll(cnProbeEgress("223.5.5.5", "CN")), token, false); status != 401 {
			t.Fatalf("invalididentity %q: %d", token, status)
		}
	}
	if status, _ := p.req(t, "GET", "/probe/v1/poll", nil, id.Token, false); status != 403 {
		t.Fatalf("GETagentrequest %d", status)
	}
	p.h.a.Origin = "http://narbe.test"
	if status, _ := p.req(t, "POST", "/probe/v1/poll", cnPoll(cnProbeEgress("223.5.5.5", "CN")), id.Token, false); status != 403 {
		t.Fatalf("HTTPcontrolorigin %d", status)
	}
}

func TestConnectivityProbeAdminAuthorizationAndDisable(t *testing.T) {
	p := newCNProbeHTTP(t)
	if status, _ := p.req(t, "GET", "/api/probes", nil, "", false); status != 401 {
		t.Fatalf("anonymousadminGET %d", status)
	}
	r, err := http.NewRequest("POST", p.s.URL+"/api/probes", strings.NewReader(`{"name":"csrf","role":"domestic","expectedIP":"223.5.5.5"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", p.s.URL)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(p.h.cookie)
	resp, err := p.s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("missingCSRF %d", resp.StatusCode)
	}
	id, registration := p.register(t, "domestic", "223.5.5.5")
	stable := p.enroll(t, registration)
	p.fixture(t, id, "probing", false)
	leased := p.lease(t, stable, cnProbeEgress("223.5.5.5", "CN"))
	if status, _ := p.req(t, "POST", "/api/probes/"+id+"/disable", nil, "", true); status != 200 {
		t.Fatalf("disable %d", status)
	}
	var state string
	if err := p.h.a.S.DB.QueryRow("SELECT state FROM probe_tasks WHERE id=?", leased.TaskID).Scan(&state); err != nil || state != "expired" {
		t.Fatalf("disableleftlivelease %s %v", state, err)
	}
	if status, _ := p.req(t, "POST", "/probe/v1/poll", cnPoll(cnProbeEgress("223.5.5.5", "CN")), stable.Token, false); status != 401 {
		t.Fatalf("disabledidentity %d", status)
	}
	_, registration = p.register(t, "domestic", "223.6.6.6")
	var disabledID string
	if err := p.h.a.S.DB.QueryRow("SELECT probe_id FROM probe_enrollments WHERE token_hash=?", store.Hash(registration)).Scan(&disabledID); err != nil {
		t.Fatal(err)
	}
	if status, _ := p.req(t, "DELETE", "/api/probes/"+disabledID, nil, "", true); status != 200 {
		t.Fatal(status)
	}
	if status, _ := p.req(t, "POST", "/probe/v1/enroll", probe.EnrollRequest{RegistrationToken: registration, Version: "test"}, "", false); status != 401 {
		t.Fatalf("disabledenrollment %d", status)
	}
}

func TestConnectivityProbeDomesticEgressFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, ip, country string
		age               time.Duration
		source            string
		good              bool
	}{
		{"verified", "223.5.5.5", "CN", 0, probe.EgressSource, true},
		{"overseas", "223.5.5.5", "US", 0, probe.EgressSource, false},
		{"expected-ip-mismatch", "223.6.6.6", "CN", 0, probe.EgressSource, false},
		{"unknown-country", "223.5.5.5", "ZZ", 0, probe.EgressSource, false},
		{"private", "127.0.0.1", "CN", 0, probe.EgressSource, false},
		{"stale", "223.5.5.5", "CN", -2 * time.Minute, probe.EgressSource, false},
		{"future", "223.5.5.5", "CN", time.Minute, probe.EgressSource, false},
		{"wrong-source", "223.5.5.5", "CN", 0, "http://unverified.invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newCNProbeHTTP(t)
			pid, registration := p.register(t, "domestic", "223.5.5.5")
			id := p.enroll(t, registration)
			_, want := p.fixture(t, pid, "probing", false)
			e := cnProbeEgress(tc.ip, tc.country)
			e.ObservedAt = e.ObservedAt.Add(tc.age)
			e.Source = tc.source
			status, b := p.req(t, "POST", "/probe/v1/poll", cnPoll(e), id.Token, false)
			var out probe.PollResponse
			if status != 200 || json.Unmarshal(b, &out) != nil || (out.Task != nil) != tc.good {
				t.Fatalf("egressguard %d %s", status, b)
			}
			if out.Task != nil && *out.Task != want {
				t.Fatal("taskidentitychanged")
			}
			status, b = p.req(t, "GET", "/api/probes", nil, "", true)
			var views struct {
				Items []probeView `json:"items"`
			}
			if status != 200 || json.Unmarshal(b, &views) != nil || len(views.Items) != 1 {
				t.Fatal(string(b))
			}
			if (views.Items[0].Status == "online") != tc.good {
				t.Fatalf("unverifiedegressshownonline %s", b)
			}
		})
	}
}

func TestConnectivityProbeControlEgressCountryGuard(t *testing.T) {
	for _, tc := range []struct {
		country string
		good    bool
	}{{"US", true}, {"CN", false}, {"ZZ", false}, {"XX", false}} {
		t.Run(tc.country, func(t *testing.T) {
			p := newCNProbeHTTP(t)
			pid, registration := p.register(t, "control", "8.8.8.8")
			id := p.enroll(t, registration)
			p.fixture(t, pid, "probing", false)
			status, b := p.req(t, "POST", "/probe/v1/poll", cnPoll(cnProbeEgress("8.8.8.8", tc.country)), id.Token, false)
			var out probe.PollResponse
			if status != 200 || json.Unmarshal(b, &out) != nil || (out.Task != nil) != tc.good {
				t.Fatalf("controlcountryguard %d %s", status, b)
			}
		})
	}
}

func TestConnectivityProbeConcurrentEnrollmentConsumesOnce(t *testing.T) {
	p := newCNProbeHTTP(t)
	_, registration := p.register(t, "domestic", "223.5.5.5")
	var wg sync.WaitGroup
	statuses := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := p.req(t, "POST", "/probe/v1/enroll", probe.EnrollRequest{RegistrationToken: registration, Version: "test"}, "", false)
			statuses <- status
		}()
	}
	wg.Wait()
	close(statuses)
	success := 0
	for status := range statuses {
		if status == 200 {
			success++
		} else if status != 401 {
			t.Errorf("concurrentenrollmentunexpectedstatus%d", status)
		}
	}
	if success != 1 {
		t.Fatalf("single-use token registered %d identities", success)
	}
}

func TestConnectivityProbeIndependentLookupEvidence(t *testing.T) {
	for _, mode := range []string{"failure", "ip-mismatch", "country-mismatch", "source-mismatch", "error-evidence", "stale-evidence", "future-evidence", "missing-time"} {
		t.Run(mode, func(t *testing.T) {
			p := newCNProbeHTTP(t)
			pid, registration := p.register(t, "domestic", "223.5.5.5")
			id := p.enroll(t, registration)
			p.fixture(t, pid, "probing", false)
			p.h.a.connectivity.lookup = func(context.Context, string) (probe.Egress, error) {
				e := cnProbeEgress("223.5.5.5", "CN")
				switch mode {
				case "failure":
					return e, errors.New("fixturelookupfail")
				case "ip-mismatch":
					e.IP = "223.6.6.6"
				case "country-mismatch":
					e.Country = "US"
				case "source-mismatch":
					e.Source = "http://wrong.invalid"
				case "error-evidence":
					e.Error = "unverified"
				case "stale-evidence":
					e.ObservedAt = time.Now().Add(-2 * time.Minute)
				case "future-evidence":
					e.ObservedAt = time.Now().Add(2 * time.Minute)
				case "missing-time":
					e.ObservedAt = time.Time{}
				}
				return e, nil
			}
			status, b := p.req(t, "POST", "/probe/v1/poll", cnPoll(cnProbeEgress("223.5.5.5", "CN")), id.Token, false)
			var response probe.PollResponse
			if status != 200 || json.Unmarshal(b, &response) != nil || response.Task != nil {
				t.Fatalf("invalidindependentgeoevidenceissuedtask %d %s", status, b)
			}
		})
	}
}

func TestConnectivityProbeLeaseCASAndStoppedWorkflow(t *testing.T) {
	p := newCNProbeHTTP(t)
	pid, registration := p.register(t, "domestic", "223.5.5.5")
	id := p.enroll(t, registration)
	_, want := p.fixture(t, pid, "probing", false)
	var wg sync.WaitGroup
	var mu sync.Mutex
	claims := 0
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, b := p.req(t, "POST", "/probe/v1/poll", cnPoll(cnProbeEgress("223.5.5.5", "CN")), id.Token, false)
			var out probe.PollResponse
			if status != 200 || json.Unmarshal(b, &out) != nil {
				t.Errorf("concurrentpoll %d %s", status, b)
				return
			}
			if out.Task != nil {
				mu.Lock()
				claims++
				mu.Unlock()
				if *out.Task != want {
					t.Error("wrongleasedtask")
				}
			}
		}()
	}
	wg.Wait()
	if claims != 1 {
		t.Fatalf("CASclaimed %d tasks", claims)
	}
	if _, err := p.h.a.S.DB.Exec("UPDATE probe_tasks SET state='done' WHERE id=?", want.TaskID); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"probing", "paused", "stopped"} {
		p.fixture(t, pid, state, true)
	}
	status, b := p.req(t, "POST", "/probe/v1/poll", cnPoll(cnProbeEgress("223.5.5.5", "CN")), id.Token, false)
	var out probe.PollResponse
	if status != 200 || json.Unmarshal(b, &out) != nil || out.Task != nil {
		t.Fatalf("stoppedtaskissued %d %s", status, b)
	}
}

func TestConnectivityProbeResultACKLossIdentityAndBounds(t *testing.T) {
	p := newCNProbeHTTP(t)
	pid, registration := p.register(t, "domestic", "223.5.5.5")
	id := p.enroll(t, registration)
	p.fixture(t, pid, "probing", false)
	e := cnProbeEgress("223.5.5.5", "CN")
	task := p.lease(t, id, e)
	report := cnProbeReport(task, e)
	_, otherRegistration := p.register(t, "control", "8.8.8.8")
	other := p.enroll(t, otherRegistration)
	if status, b := p.req(t, "POST", "/probe/v1/results", report, other.Token, false); status != 409 {
		t.Fatalf("otherprobeacceptedlease %d %s", status, b)
	}
	for _, mutate := range []struct {
		name   string
		change func(*probe.ResultRequest)
		status int
	}{
		{"target", func(q *probe.ResultRequest) { q.Target.IP = "1.1.1.1" }, 409},
		{"lease", func(q *probe.ResultRequest) { q.LeaseID = store.ID() }, 409},
		{"task", func(q *probe.ResultRequest) { q.TaskID = store.ID() }, 409},
		{"old-start", func(q *probe.ResultRequest) {
			q.Result.StartedAt = time.Now().Add(-time.Hour)
			q.Result.CompletedAt = q.Result.StartedAt.Add(time.Millisecond)
		}, 409},
		{"future", func(q *probe.ResultRequest) {
			q.Result.StartedAt = time.Now().Add(time.Minute)
			q.Result.CompletedAt = q.Result.StartedAt.Add(time.Millisecond)
		}, 409},
		{"latency", func(q *probe.ResultRequest) { q.Result.LatencyMS = 30001 }, 400},
		{"reversed-time", func(q *probe.ResultRequest) { q.Result.CompletedAt = q.Result.StartedAt.Add(-time.Second) }, 400},
		{"unknown-class", func(q *probe.ResultRequest) { q.Result.Classification = "arbitrary-raw-error" }, 400},
		{"oversize-error", func(q *probe.ResultRequest) { q.Result.Error = strings.Repeat("x", 257) }, 400},
		{"invalid-status", func(q *probe.ResultRequest) { q.Result.StatusCode = -7 }, 400},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			q := report
			mutate.change(&q)
			if status, b := p.req(t, "POST", "/probe/v1/results", q, id.Token, false); status != mutate.status {
				t.Fatalf("resultguard expected%d got%d %s", mutate.status, status, b)
			}
		})
	}
	if status, b := p.req(t, "POST", "/probe/v1/results", report, id.Token, false); status != 204 {
		t.Fatalf("firstresult %d %s", status, b)
	}
	// ACK is lost: retry the exact serialized report and prove no double-count.
	if status, b := p.req(t, "POST", "/probe/v1/results", report, id.Token, false); status != 204 {
		t.Fatalf("exactretry %d %s", status, b)
	}
	var checks, successes int
	if err := p.h.a.S.DB.QueryRow("SELECT checks,successes FROM connectivity_probes WHERE id=?", pid).Scan(&checks, &successes); err != nil || checks != 1 || successes != 1 {
		t.Fatalf("duplicatecounted %d/%d %v", checks, successes, err)
	}
	report.Result.LatencyMS++
	if status, _ := p.req(t, "POST", "/probe/v1/results", report, id.Token, false); status != 409 {
		t.Fatalf("alteredreplay %d", status)
	}
}

func TestConnectivityProbeExpiredResultAndSecretRedaction(t *testing.T) {
	p := newCNProbeHTTP(t)
	pid, registration := p.register(t, "domestic", "223.5.5.5")
	id := p.enroll(t, registration)
	screen, _ := p.fixture(t, pid, "probing", false)
	e := cnProbeEgress("223.5.5.5", "CN")
	task := p.lease(t, id, e)
	report := cnProbeReport(task, e)
	if _, err := p.h.a.S.DB.Exec("UPDATE probe_tasks SET expires=? WHERE id=?", store.Now()-1, task.TaskID); err != nil {
		t.Fatal(err)
	}
	if status, b := p.req(t, "POST", "/probe/v1/results", report, id.Token, false); status != 410 {
		t.Fatalf("expiredresult %d %s", status, b)
	}
	for _, path := range []string{"/api/probes", "/api/screenings", "/api/screenings/" + screen} {
		status, b := p.req(t, "GET", path, nil, "", true)
		if status != 200 {
			t.Fatalf("publicGET %s %d %s", path, status, b)
		}
		for _, secret := range []string{"cn-protocol-CLOUD-SECRET", registration, id.Token, "token_hash", "registrationToken", "credentials", "payload"} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("secret %q leaked via%s %s", secret, path, b)
			}
		}
	}
}

func TestConnectivityProbeConcurrentExactResultIdempotency(t *testing.T) {
	p := newCNProbeHTTP(t)
	pid, registration := p.register(t, "domestic", "223.5.5.5")
	id := p.enroll(t, registration)
	p.fixture(t, pid, "probing", false)
	e := cnProbeEgress("223.5.5.5", "CN")
	task := p.lease(t, id, e)
	report := cnProbeReport(task, e)
	// All requests first see the leased row, then converge on the DB CAS.
	// This reproduces ACK races without timing-dependent sleeps.
	const concurrent = 8
	barrier := make(chan struct{})
	var gateMu sync.Mutex
	arrived := 0
	p.h.a.connectivity.mu.Lock()
	p.h.a.connectivity.geoCache = map[string]probe.Egress{}
	p.h.a.connectivity.mu.Unlock()
	p.h.a.connectivity.lookup = func(ctx context.Context, ip string) (probe.Egress, error) {
		gateMu.Lock()
		arrived++
		if arrived == concurrent {
			close(barrier)
		}
		gateMu.Unlock()
		select {
		case <-ctx.Done():
			return probe.Egress{}, ctx.Err()
		case <-barrier:
		}
		return cnProbeEgress(ip, "CN"), nil
	}
	statuses := make(chan int, concurrent)
	var wg sync.WaitGroup
	for range concurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _ := p.req(t, "POST", "/probe/v1/results", report, id.Token, false)
			statuses <- status
		}()
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 204 {
			t.Errorf("concurrent exact ACK must be idempotent, got%d", status)
		}
	}
	var checks, successes int
	if err := p.h.a.S.DB.QueryRow("SELECT checks,successes FROM connectivity_probes WHERE id=?", pid).Scan(&checks, &successes); err != nil || checks != 1 || successes != 1 {
		t.Fatalf("duplicateCAS doublecount %d/%d %v", checks, successes, err)
	}
}

func TestConnectivityProbeRecentRateUsesLast100Checks(t *testing.T) {
	h := setup(t)
	probeID := seedScreenProbe(t, h)
	aid := h.add(t)
	jobID := store.ID()
	_, e := h.a.S.DB.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,created,updated) VALUES(?,?,'create','r','','fixture',?,'succeeded','fixture',?,?)", jobID, aid, []byte("fixture"), store.Now(), store.Now())
	if e != nil {
		t.Fatal(e)
	}
	_, e = h.a.S.DB.Exec("INSERT INTO screenings(id,job_id,state,stage,payload,data,created,updated) VALUES(?,?,'accepted','fixture',?,'{}',?,?)", jobID, jobID, []byte("fixture"), store.Now(), store.Now())
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 110; n++ {
		result := fixtureProbeResult(n < 10 || n >= 60)
		raw, _ := json.Marshal(result)
		eg, _ := json.Marshal(freshEgress("223.5.5.5", "CN"))
		_, e = h.a.S.DB.Exec("INSERT INTO probe_tasks(id,probe_id,screening_id,lease,target,timeout_ms,state,created,expires,result,egress) VALUES(?,?,?,'fixture','{}',1000,'done',?,?,?,?)", fmt.Sprintf("recent-%03d", n), probeID, jobID, store.Now()+int64(n), store.Now()+300, raw, eg)
		if e != nil {
			t.Fatal(e)
		}
	}
	rows, e := h.a.probeViews()
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(rows[0])
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	if v["recentChecks"] != float64(100) || v["recentSuccesses"] != float64(50) {
		t.Fatalf("recent rate must exclude 10 older successes: %s", raw)
	}
}
