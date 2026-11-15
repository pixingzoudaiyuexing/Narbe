package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func observedEgress(context.Context) (Egress, error) {
	return Egress{IP: "8.8.8.8", Country: "US", ObservedAt: time.Now().UTC(), Source: EgressSource}, nil
}

func TestAgentEnrollHeartbeatResultsPersistAndRejectReplay(t *testing.T) {
	enrolls, polls, results, checks := 0, 0, 0, 0
	task := Task{TaskID: "task-1", LeaseID: "lease-1", Target: Target{IP: "8.8.8.8", Port: 443, Protocol: "tcp"}, TimeoutMS: 1000}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/probe/v1/enroll" && r.Header.Get("Authorization") != "Bearer "+testToken {
			t.Error("missing stable identity token")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/probe/v1/enroll":
			enrolls++
			var q EnrollRequest
			if json.NewDecoder(r.Body).Decode(&q) != nil || q.RegistrationToken != testToken || q.Version != "test" || r.Header.Get("Authorization") != "" {
				t.Fatal("bad registration exchange")
			}
			json.NewEncoder(w).Encode(Identity{ProbeID: "probe-1", Token: testToken})
		case "/probe/v1/poll":
			polls++
			var q PollRequest
			if json.NewDecoder(r.Body).Decode(&q) != nil || q.EgressCountry != "US" || q.EgressIP != "8.8.8.8" || q.EgressObservedAt.IsZero() {
				t.Fatal("actual egress missing")
			}
			json.NewEncoder(w).Encode(PollResponse{Task: &task})
		case "/probe/v1/results":
			results++
			var q ResultRequest
			if json.NewDecoder(r.Body).Decode(&q) != nil || q.TaskID != task.TaskID || q.LeaseID != task.LeaseID || q.Target != task.Target || !q.Result.Success || q.Egress.Country != "US" {
				t.Fatal("result correlation failed")
			}
			w.WriteHeader(204)
		default:
			t.Error("unknown endpoint")
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	check := func(ctx context.Context, target Target, timeout time.Duration) Result {
		checks++
		if target != task.Target || timeout != time.Second {
			t.Fatal("target not exact")
		}
		return Result{Success: true, Classification: "success", StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}
	}
	cfg := Config{ControlURL: s.URL, RegistrationToken: testToken, DataDir: t.TempDir(), AllowedPorts: []int{443}, Version: "test"}
	a, err := newAgent(cfg, s.Client(), observedEgress, check)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !Healthy(cfg.DataDir) || enrolls != 1 || results != 1 || checks != 1 {
		t.Fatalf("unexpected cycle %d/%d/%d", enrolls, results, checks)
	}
	info, err := os.Stat(filepath.Join(cfg.DataDir, "identity.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("identity not private")
	}
	cfg.RegistrationToken = "invalid-after-enrollment"
	a, err = newAgent(cfg, s.Client(), observedEgress, check)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Step(context.Background()); err == nil || !strings.Contains(err.Error(), "replay") || checks != 1 || enrolls != 1 {
		t.Fatalf("lease replay accepted checks=%d err=%v", checks, err)
	}
}

func TestAgentDurableResultRetryDoesNotRepeatCheck(t *testing.T) {
	checks, results, polls := 0, 0, 0
	task := Task{TaskID: "task-2", LeaseID: "lease-2", Target: Target{IP: "8.8.8.8", Port: 80, Protocol: "http"}, TimeoutMS: 1000}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/probe/v1/enroll":
			json.NewEncoder(w).Encode(Identity{ProbeID: "probe-2", Token: testToken})
		case "/probe/v1/poll":
			polls++
			if polls == 1 {
				json.NewEncoder(w).Encode(PollResponse{Task: &task})
			} else {
				json.NewEncoder(w).Encode(PollResponse{})
			}
		case "/probe/v1/results":
			results++
			if results == 1 {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(204)
			}
		}
	}))
	defer s.Close()
	checker := func(context.Context, Target, time.Duration) Result {
		checks++
		return Result{Classification: "timeout", Error: "连接超时"}
	}
	cfg := Config{ControlURL: s.URL, RegistrationToken: testToken, DataDir: t.TempDir(), AllowedPorts: []int{80}}
	a, err := newAgent(cfg, s.Client(), observedEgress, checker)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Step(context.Background()); err == nil || checks != 1 {
		t.Fatal("result upload failure not retained")
	}
	if _, err = readPrivate(filepath.Join(cfg.DataDir, "pending.json")); err != nil {
		t.Fatal("pending result not durable", err)
	}
	a, err = newAgent(cfg, s.Client(), observedEgress, checker)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Step(context.Background()); err != nil || checks != 1 || results != 2 || polls != 2 {
		t.Fatalf("result retry replayed target %d %d %d %v", checks, results, polls, err)
	}
}

func TestAgentRejectsUnsafeTargetAndEgressChanges(t *testing.T) {
	for _, mode := range []string{"private", "port", "egress-change", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			checks, observes := 0, 0
			target := Target{IP: "8.8.8.8", Port: 443, Protocol: "tcp"}
			timeout := 1000
			if mode == "private" {
				target.IP = "169.254.169.254"
			}
			if mode == "port" {
				target.Port = 25
			}
			if mode == "timeout" {
				timeout = 600000
			}
			var got ResultRequest
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/probe/v1/enroll":
					json.NewEncoder(w).Encode(Identity{ProbeID: "probe", Token: testToken})
				case "/probe/v1/poll":
					json.NewEncoder(w).Encode(PollResponse{Task: &Task{TaskID: "task", LeaseID: "lease", Target: target, TimeoutMS: timeout}})
				case "/probe/v1/results":
					json.NewDecoder(r.Body).Decode(&got)
					w.WriteHeader(204)
				}
			}))
			defer s.Close()
			observe := func(ctx context.Context) (Egress, error) {
				observes++
				e, err := observedEgress(ctx)
				if mode == "egress-change" && observes >= 3 {
					e.IP = "1.1.1.1"
				}
				return e, err
			}
			checker := func(context.Context, Target, time.Duration) Result {
				checks++
				return Result{Success: true, Classification: "success"}
			}
			a, err := newAgent(Config{ControlURL: s.URL, RegistrationToken: testToken, DataDir: t.TempDir(), AllowedPorts: []int{443}}, s.Client(), observe, checker)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got.Result.Success || mode != "egress-change" && checks != 0 || mode == "egress-change" && got.Result.Classification != "egress_unverified" {
				t.Fatalf("unsafe check %+v calls=%d", got, checks)
			}
		})
	}
}

func TestAgentExpiredResultDroppedWithoutReexecution(t *testing.T) {
	results, polls, checks := 0, 0, 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/probe/v1/results":
			results++
			w.WriteHeader(http.StatusGone)
		case "/probe/v1/poll":
			polls++
			json.NewEncoder(w).Encode(PollResponse{})
		default:
			t.Error("unexpected reenrollment")
			w.WriteHeader(500)
		}
	}))
	defer s.Close()
	cfg := Config{ControlURL: s.URL, DataDir: t.TempDir(), AllowedPorts: []int{443}}
	a, err := newAgent(cfg, s.Client(), observedEgress, func(context.Context, Target, time.Duration) Result { checks++; return Result{} })
	if err != nil {
		t.Fatal(err)
	}
	a.saved = savedIdentity{ControlURL: s.URL, Identity: Identity{ProbeID: "probe", Token: testToken}, Seen: []string{"task:lease"}}
	if err = a.saveIdentity(); err != nil {
		t.Fatal(err)
	}
	pending := ResultRequest{TaskID: "task", LeaseID: "lease", Target: Target{IP: "8.8.8.8", Port: 443, Protocol: "tls"}, Result: Result{Classification: "timeout"}}
	b, _ := json.Marshal(pending)
	if err = privateWrite(filepath.Join(cfg.DataDir, "pending.json"), b); err != nil {
		t.Fatal(err)
	}
	a, err = newAgent(cfg, s.Client(), observedEgress, func(context.Context, Target, time.Duration) Result { checks++; return Result{} })
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if results != 1 || polls != 1 || checks != 0 || a.pending != nil || len(a.saved.Seen) != 1 {
		t.Fatalf("expired result reexecuted or blocking %d/%d/%d", results, polls, checks)
	}
	if _, err = os.Stat(filepath.Join(cfg.DataDir, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("expired pending result retained")
	}
}

func TestAgentHTTPSAndPrivateIdentitySafety(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com?token=secret", "https://example.com#x"} {
		if _, err := NewAgent(Config{ControlURL: u, DataDir: t.TempDir(), AllowedPorts: []int{80}}); err == nil {
			t.Fatalf("unsafe control URL accepted %s", u)
		}
	}
	d := t.TempDir()
	if err := os.Symlink("/etc/passwd", filepath.Join(d, "identity.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAgent(Config{ControlURL: "https://example.com", DataDir: d, AllowedPorts: []int{80}}); err == nil {
		t.Fatal("symlink identity read")
	}
	if Healthy(t.TempDir()) {
		t.Fatal("absent heartbeat healthy")
	}
}
