package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

// A real HTTP/API/SQLite browser seam. No Playwright route fulfills API data,
// and the provider never connects to Linode or executes a cloud write.
type firewallBrowserFake struct {
	*advancedFake
	fixtureMu sync.Mutex
	scenario  string
}

func (f *firewallBrowserFake) LinodeRead(ctx context.Context, view, id, period string, page int) (any, error) {
	f.fixtureMu.Lock()
	scenario := f.scenario
	f.fixtureMu.Unlock()
	rules := map[string]any{"inbound": nil, "outbound": nil, "inbound_policy": "DROP", "outbound_policy": "ACCEPT"}
	fw := map[string]any{"id": 7, "label": "empty-fw", "status": "enabled", "rules": rules, "devices": nil, "snapshot": "fw-snapshot"}
	switch scenario {
	case "inbound-null":
		rules["outbound"] = []any{}
	case "outbound-null":
		rules["inbound"] = []any{}
	case "arrays":
		fw["devices"] = []any{}
		rules["inbound"] = []any{}
		rules["outbound"] = []any{}
	case "multiple":
		fw["devices"] = []any{map[string]any{"id": 51, "entity": map[string]any{"id": 123, "type": "linode", "label": "legacy-server"}}, map[string]any{"id": 52, "entity": map[string]any{"id": 8, "type": "linode_interface", "label": "public-interface", "parent_entity": map[string]any{"id": 456, "type": "linode"}}}}
	case "bad-policy":
		rules["inbound_policy"] = "UNKNOWN"
	case "missing-inbound":
		delete(rules, "inbound")
	case "missing-outbound":
		delete(rules, "outbound")
	case "missing-policy":
		delete(rules, "outbound_policy")
	case "bad-devices":
		fw["devices"] = map[string]any{}
	case "bad-id":
		fw["id"] = nil
	case "bad-status":
		fw["status"] = "unknown"
	case "bad-device":
		fw["devices"] = []any{map[string]any{"id": 51, "entity": nil}}
	case "bad-rule":
		rules["inbound"] = []any{nil}
	case "bad-rules":
		rules["inbound"] = map[string]any{}
	case "bad-items":
		return map[string]any{"items": map[string]any{}, "snapshot": "list-snapshot"}, nil
	}
	switch view {
	case "firewalls":
		var items any = []any{fw}
		if scenario == "empty" {
			items = nil
		}
		return map[string]any{"items": items, "snapshot": "list-snapshot"}, nil
	case "firewall":
		raw, _ := json.Marshal(fw)
		var out cloud.LinodeFirewall
		err := json.Unmarshal(raw, &out)
		return out, err
	case "instance":
		generation := "legacy_config"
		interfaces := []cloud.LinodeInterface{}
		if scenario == "interfaces" {
			generation = "linode"
			interfaces = append(interfaces, cloud.LinodeInterface{ID: 8, Public: map[string]any{"ipv4": true}})
		}
		return cloud.LinodeState{Instance: cloud.Instance{ID: "123", Name: "source", Region: "r", State: "offline", Plan: "p", Image: "im", IPs: []string{}}, Snapshot: "source-snapshot", Generation: generation, Disks: []cloud.LinodeDisk{}, Configs: []cloud.LinodeConfig{}, Interfaces: interfaces}, nil
	case "instance-firewalls":
		return map[string]any{"items": nil, "snapshot": "binding-snapshot"}, nil
	}
	return nil, fmt.Errorf("unexpected test view %s", view)
}
func (f *firewallBrowserFake) ValidateChange(_ context.Context, id, action string, c cloud.LinodeChange) error {
	if !c.Acknowledge {
		return fmt.Errorf("ack required")
	}
	if action == "firewall-create" || action == "firewall-rules" {
		if c.Rules == nil {
			return fmt.Errorf("missing rules")
		}
		if err := cloud.ValidateFirewallRules(*c.Rules); err != nil {
			return err
		}
	}
	if action == "firewall-bind" {
		if c.Snapshot != "source-snapshot" || c.BindingSnapshot != "binding-snapshot" {
			return fmt.Errorf("binding identity changed")
		}
	} else if action != "firewall-create" && c.Snapshot != "fw-snapshot" {
		return fmt.Errorf("firewall snapshot changed")
	}
	return nil
}
func TestBrowserFirewallNullCollections(t *testing.T) {
	if os.Getenv("NARBE_BROWSER_TEST") != "1" {
		t.Skip("set NARBE_BROWSER_TEST=1")
	}
	h, advanced, aid := advancedSetup(t)
	f := &firewallBrowserFake{advancedFake: advanced, scenario: "null"}
	f.items = []cloud.Instance{{ID: "123", Name: "source", Region: "r", State: "offline", Plan: "p", Image: "im", Actions: []string{"start", "delete"}}}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
	handler := h.a.Handler()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__firewall-fixture" {
			f.fixtureMu.Lock()
			f.scenario = r.URL.Query().Get("case")
			f.fixtureMu.Unlock()
			w.WriteHeader(204)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	h.a.Origin = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(root, "scripts/browser-firewall.mjs"), server.URL, aid)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NARBE_TEST_PASSWORD="+testPassword)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("browser invoked a provider write")
	}
}
