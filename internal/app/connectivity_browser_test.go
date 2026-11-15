package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This serves connectivity protocol responses from Go over real HTTP. The
// browser runs the embedded application, its real login/session, and the real
// account/catalog reads. It does not mock fetch or execute any cloud writes.
func TestBrowserConnectivity(t *testing.T) {
	if os.Getenv("NARBE_BROWSER_TEST") != "1" {
		t.Skip("set NARBE_BROWSER_TEST=1")
	}
	h := setup(t)
	aid := h.add(t)
	var mu sync.Mutex
	stopped, disabled := false, false
	inventoryReleased := false
	catalogReads := 0
	screeningReadFailures := 1
	created, submissions, reconciles := 0, 0, 0
	now := time.Now().Unix()
	fixtures := h.a.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/api/inventory":
			// Keep the first inventory queued until the create dialog asks for
			// its own catalog. An empty cached regions slice is not a catalog.
			reply(w, 200, map[string]any{"accountId": aid, "accountRevision": 1, "phase": "queued", "complete": false, "items": []any{}, "regions": []any{}, "stats": map[string]any{"total": 0}})
		case r.URL.Path == "/api/probes" && r.Method == "GET":
			reply(w, 200, map[string]any{"items": []any{
				map[string]any{"id": "cn-probe", "name": "国内直连测试替身", "role": "domestic", "expectedIP": "198.51.100.44", "enabled": !disabled, "status": "online", "lastSeen": now, "egress": map[string]any{"ip": "198.51.100.44", "country": "CN"}, "checks": 12, "successes": 9, "version": "cn-candidate-fixture"},
				map[string]any{"id": "offline-probe", "name": "出口待核实替身", "role": "domestic", "expectedIP": "198.51.100.45", "enabled": true, "status": "offline", "lastSeen": now - 3600, "egress": map[string]any{"ip": "198.51.100.45", "country": "US"}},
				map[string]any{"id": "control-probe", "name": "海外对照测试替身", "role": "control", "expectedIP": "203.0.113.45", "enabled": true, "status": "online", "lastSeen": now, "egress": map[string]any{"ip": "203.0.113.45", "country": "US"}},
			}})
		case r.URL.Path == "/api/probes" && r.Method == "POST":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["expectedIP"] != "198.51.100.46" {
				t.Error("probe registration did not transmit expected exit identity")
			}
			created++
			reply(w, 201, map[string]any{"id": "once-probe", "registrationToken": "fixture-once-only-registration-token"})
		case r.URL.Path == "/api/probes/cn-probe/disable":
			disabled = true
			reply(w, 200, map[string]any{"ok": true})
		case r.URL.Path == "/api/screenings" && r.Method == "GET":
			if screeningReadFailures > 0 {
				screeningReadFailures--
				reply(w, 503, map[string]any{"error": "检测记录暂时不可用（HTTP测试替身）"})
				return
			}
			state, stage := "probing", "海外可达，等待国内连续检测"
			if stopped {
				state, stage = "stopped", "已停止；实例仍存在"
			}
			reply(w, 200, map[string]any{"items": []any{
				map[string]any{"id": "screening-active", "jobId": "create-job", "provider": "linode", "region": "ap-south", "state": state, "stage": stage, "attempt": 2, "maxAttempts": 5, "resourceId": "instance-exact-123", "ip": "192.0.2.41", "updated": now, "estimatedUSD": 2, "budgetUSD": 5, "method": "recreate", "rounds": []any{map[string]any{"attempt": 1, "resourceId": "instance-old-122", "ip": "192.0.2.41", "method": "recreate", "state": "replacing", "estimatedUSD": 1, "repeatedIP": true, "domestic": map[string]any{"success": false, "classification": "timeout", "latencyMs": 5000}, "control": map[string]any{"success": true, "latencyMs": 19}}}, "resources": []any{map[string]any{"id": "instance-exact-123", "kind": "instance", "state": "present"}}},
				map[string]any{"id": "screening-unknown", "provider": "azure", "region": "eastasia", "state": "unknown", "stage": "删除请求已提交，响应丢失", "reason": "删除结果未知；不会创建下一台收费实例", "resourceId": "/subscriptions/fixture/resourceGroups/long-resource-group-name/providers/Microsoft.Compute/virtualMachines/owned-candidate", "updated": now, "rounds": []any{map[string]any{"attempt": 1, "resourceId": "deleted-old-vm", "ip": "192.0.2.40", "state": "unknown", "writes": []any{map[string]any{"action": "create-instance", "resourceId": "deleted-old-vm", "state": "dispatching", "at": now}}, "operation": map[string]any{"resourceId": "deleted-old-vm", "password": "never-render-ledger-secret", "owned": []string{"azure-old-pip", "azure-new-pip", "azure-partial-owned-disk"}, "ledger": map[string]any{"resources": []any{map[string]any{"id": "azure-old-pip", "kind": "public-ip", "state": "deleted-confirmed"}, map[string]any{"id": "azure-new-pip", "kind": "public-ip", "state": "unverified"}}}, "screening": map[string]any{"writes": []any{map[string]any{"action": "create-public-ip", "resourceId": "azure-new-pip", "state": "dispatching", "at": now}, map[string]any{"action": "delete-public-ip", "resourceId": "azure-old-pip", "state": "unknown", "at": now}}}}}}, "resources": []any{map[string]any{"id": "/subscriptions/fixture/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/owned-ip", "kind": "public-ip", "state": "unverified"}}},
				map[string]any{"id": "screening-no-target", "provider": "aws", "region": "us-east-1", "state": "paused", "stage": "尚无有效检测目标", "reason": "海外对照端口也未监听，不能判断国内可用性，不自动删机", "updated": now, "rounds": []any{}, "resources": []any{}},
			}})
		case r.URL.Path == "/api/screenings/screening-active/stop":
			stopped = true
			reply(w, 200, map[string]any{"ok": true})
		case strings.HasPrefix(r.URL.Path, "/api/screenings/") && strings.HasSuffix(r.URL.Path, "/reconcile"):
			reconciles++
			reply(w, 200, map[string]any{"state": "unknown", "reason": "只读核实尚无充分证据"})
		case r.URL.Path == "/api/confirm" || r.URL.Path == "/api/jobs":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid browser mutation")
			}
			if r.URL.Path == "/api/confirm" {
				reply(w, 200, map[string]any{"confirmation": "http-fixture-only", "accountRevision": 1, "networkRevision": 0})
			} else {
				submissions++
				_, enabled := body["screening"]
				if (submissions == 1 && enabled) || (submissions >= 2 && !enabled) {
					t.Error("disabled screening changed ordinary create, or enabled settings lost")
				}
				reply(w, 202, map[string]any{"id": "http-fixture-job"})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	app := h.a.Handler()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/catalog" {
			mu.Lock()
			inventoryReleased = true
			catalogReads++
			mu.Unlock()
		}
		if r.URL.Path == "/api/inventory" {
			mu.Lock()
			released := inventoryReleased
			mu.Unlock()
			if !released {
				fixtures.ServeHTTP(w, r)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/probes") || strings.HasPrefix(r.URL.Path, "/api/screenings") || (r.Method == "POST" && (r.URL.Path == "/api/confirm" || r.URL.Path == "/api/jobs")) {
			fixtures.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	}))
	h.a.Origin = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	repo, _ := filepath.Abs("../..")
	cmd := exec.Command("node", filepath.Join(repo, "scripts/browser-connectivity.mjs"), server.URL)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "NARBE_TEST_PASSWORD="+testPassword)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if created != 1 || submissions != 5 || reconciles != 1 || !stopped || !disabled {
		t.Fatalf("missing browser acceptance: registration=%d create=%d reconcile=%d stopped=%t disabled=%t", created, submissions, reconciles, stopped, disabled)
	}
	if catalogReads < 1 {
		t.Fatal("create must fetch a catalog while inventory has no regions")
	}
	if h.fake.calls != 0 {
		t.Fatal("unexpected cloud writes", h.fake.calls)
	}
}
