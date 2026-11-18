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

func TestBrowserIPManagement(t *testing.T) {
	if os.Getenv("NARBE_BROWSER_TEST") != "1" {
		t.Skip("set NARBE_BROWSER_TEST=1")
	}
	h := setup(t)
	providers := []string{"digitalocean", "linode", "aws", "azure"}
	ids := make(map[string]string)
	credentials := map[string]map[string]string{
		"digitalocean": {"token": "fixture-secret"},
		"linode":       {"token": "fixture-secret"},
		"aws":          {"accessKeyId": "fixture-key", "secretAccessKey": "fixture-secret"},
		"azure":        {"tenantId": "fixture-tenant", "clientId": "fixture-client", "clientSecret": "fixture-secret", "subscriptionId": "fixture-sub", "resourceGroup": "fixture-rg"},
	}
	for _, provider := range providers {
		r := h.request("POST", "accounts", map[string]any{"name": provider + "-fixture", "provider": provider, "credentials": credentials[provider]}, true)
		if r.Code != 201 {
			t.Fatalf("add %s: %d %s", provider, r.Code, r.Body.String())
		}
		var a account
		if err := json.Unmarshal(r.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		ids[provider] = a.ID
	}
	providerOf := func(id string) string {
		for provider, aid := range ids {
			if aid == id {
				return provider
			}
		}
		return ""
	}
	var mu sync.Mutex
	var confirms, jobs, checks, deletes int
	var lastJob map[string]any
	var lastCheck map[string]any
	now := time.Now().Unix()
	fixtures := h.a.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		aid := r.URL.Query().Get("accountId")
		provider := providerOf(aid)
		switch {
		case r.URL.Path == "/api/inventory" && r.Method == "GET":
			if provider == "" {
				reply(w, 404, map[string]any{"error": "fixture account missing"})
				return
			}
			reply(w, 200, map[string]any{"accountId": aid, "accountRevision": 1, "phase": "ready", "complete": true, "scope": "测试地区", "regions": []any{map[string]any{"id": "r", "name": "测试地区"}}, "stats": map[string]any{"total": 1, "running": 1, "stopped": 0, "other": 0}, "items": []any{map[string]any{"id": "instance-" + provider, "name": provider + "-server", "region": "r", "state": "running", "plan": "p", "image": "im", "ips": []string{"198.51.100.10"}}}})
		case r.URL.Path == "/api/ip" && r.Method == "GET":
			if provider == "" || r.URL.Query().Get("region") != "r" || r.URL.Query().Get("target") != "instance-"+provider {
				reply(w, 400, map[string]any{"error": "wrong instance identity"})
				return
			}
			method := map[string]any{"id": provider + "-method", "name": "原机更换方式", "available": true, "requiresStop": provider == "aws", "extraCharge": provider != "aws", "description": "保留原服务器和磁盘；回读实际新地址"}
			if provider == "digitalocean" {
				method = map[string]any{"id": "do-primary-unavailable", "name": "原始公网 IP 原机随机更换", "available": false, "reason": "Droplet 不支持原机更换原始公网 IPv4", "description": "不删除现有 Droplet"}
			}
			addresses := []any{map[string]any{"address": "198.51.100.10", "kind": "primary", "primary": true}}
			service := []string{"198.51.100.10"}
			available := []any{}
			if provider == "digitalocean" {
				addresses = append(addresses, map[string]any{"address": "198.51.100.20", "kind": "reserved", "primary": false, "resourceId": "reserved-id"})
				service = []string{"198.51.100.20"}
			}
			if provider == "linode" {
				available = append(available, map[string]any{"address": "198.51.100.30", "kind": "secondary", "resourceId": "allocation-id", "interfaceId": "interface-id", "instanceId": "source-instance", "region": "r"})
			}
			reply(w, 200, map[string]any{"accountRevision": 1, "networkRevision": 0, "provider": provider, "instance": map[string]any{"id": "instance-" + provider, "name": provider + "-server", "region": "r"}, "data": map[string]any{"instanceId": "instance-" + provider, "region": "r", "state": "running", "primaryIPv4": "198.51.100.10", "addresses": addresses, "availableIPs": available, "serviceIPv4": service, "methods": []any{method}, "snapshot": provider + "-snapshot", "warnings": []string{}}, "lastCheck": map[string]any{"kind": "check", "state": "accepted", "stage": "国内可用", "updated": now, "rounds": []any{map[string]any{"domestic": map[string]any{"success": true, "latencyMs": 32, "at": now}, "control": map[string]any{"success": true}}}}})
		case r.URL.Path == "/api/probes" && r.Method == "GET":
			reply(w, 200, map[string]any{"items": []any{map[string]any{"id": "cn-probe", "name": "国内探针", "role": "domestic", "enabled": true, "status": "online", "egress": map[string]any{"ip": "203.0.113.30", "country": "CN"}}, map[string]any{"id": "control-probe", "name": "海外探针", "role": "control", "enabled": true, "status": "online"}}})
		case r.URL.Path == "/api/probes/cn-probe" && r.Method == "DELETE":
			deletes++
			reply(w, 200, map[string]any{"ok": true})
		case r.URL.Path == "/api/ip/check" && r.Method == "POST":
			if json.NewDecoder(r.Body).Decode(&lastCheck) != nil {
				t.Error("invalid manual check body")
			}
			checks++
			reply(w, 202, map[string]any{"id": "manual-check-id"})
		case r.URL.Path == "/api/confirm" && r.Method == "POST":
			confirms++
			reply(w, 200, map[string]any{"confirmation": "fixture-confirm", "accountRevision": 1, "networkRevision": 0})
		case r.URL.Path == "/api/jobs" && r.Method == "POST":
			if json.NewDecoder(r.Body).Decode(&lastJob) != nil {
				t.Error("invalid job body")
			}
			jobs++
			reply(w, 202, map[string]any{"id": "manual-ip-job"})
		case r.URL.Path == "/api/jobs" && r.Method == "GET":
			reply(w, 200, []any{map[string]any{"id": "manual-ip-job", "accountId": ids["linode"], "action": "ip-change", "region": "r", "target": "instance-linode", "name": "linode-server", "state": "succeeded", "created": now, "result": map[string]any{"ip": map[string]any{"oldIP": "198.51.100.10", "newIP": "198.51.100.10", "primaryChanged": false, "addressRepeated": true, "after": map[string]any{"primaryIPv4": "198.51.100.10", "serviceIPv4": []string{"198.51.100.30"}}, "notice": "旧 IP 保留"}}}})
		case r.URL.Path == "/api/screenings" && r.Method == "GET":
			reply(w, 200, map[string]any{"items": []any{map[string]any{"id": "manual-check-id", "kind": "check", "provider": "linode", "region": "r", "state": "accepted", "stage": "国内可用", "updated": now, "rounds": []any{}}, map[string]any{"id": "post-check-id", "kind": "manual-ip", "provider": "linode", "region": "r", "state": "accepted", "stage": "新 IP 检测通过", "updated": now, "rounds": []any{}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	app := h.a.Handler()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/inventory" || r.URL.Path == "/api/ip" || r.URL.Path == "/api/ip/check" || r.URL.Path == "/api/probes" || r.URL.Path == "/api/screenings" || r.URL.Path == "/api/confirm" || r.URL.Path == "/api/jobs" || strings.HasPrefix(r.URL.Path, "/api/probes/") {
			fixtures.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	}))
	h.a.Origin = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(repo, "scripts/browser-ip-management.mjs"), server.URL)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "NARBE_TEST_PASSWORD="+testPassword)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if confirms != 1 || jobs != 1 || checks != 1 || deletes != 1 {
		t.Fatalf("browser operations: confirm=%d jobs=%d checks=%d delete=%d", confirms, jobs, checks, deletes)
	}
	change, ok := lastJob["ipChange"].(map[string]any)
	if !ok || change["method"] != "linode-method" || change["snapshot"] != "linode-snapshot" || change["address"] != "198.51.100.30" || change["allocationId"] != "allocation-id" || change["interfaceId"] != "interface-id" || change["sourceInstanceId"] != "source-instance" || change["riskAccepted"] != true || lastJob["action"] != "ip-change" || lastJob["target"] != "instance-linode" || lastJob["name"] != "linode-server" {
		t.Fatalf("wrong IP change selection: %#v", lastJob)
	}
	check, ok := lastCheck["config"].(map[string]any)
	if !ok || check["protocol"] != "http" || check["serverName"] != "health.example.com" || check["path"] != "/ready" || check["autoReplace"] != false || lastCheck["ip"] != "198.51.100.10" {
		t.Fatalf("wrong manual check config: %#v", lastCheck)
	}
	if h.fake.calls != 0 {
		t.Fatal("unexpected cloud writes", h.fake.calls)
	}
}
