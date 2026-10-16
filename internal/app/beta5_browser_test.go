package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

type beta5BrowserProvider struct{ *advancedFake }

func (f *beta5BrowserProvider) Catalog(ctx context.Context, region, plan string) (cloud.Catalog, error) {
	c, e := f.fakeProvider.Catalog(ctx, region, plan)
	c.Regions[0].Extra = map[string]string{"metadata": "true", "siteType": "core"}
	c.Images[0].StartupSupport = "cloud-init"
	return c, e
}
func (f *beta5BrowserProvider) LinodeRead(ctx context.Context, v, id, p string, n int) (any, error) {
	if v == "instance" {
		yes := true
		return cloud.LinodeState{Instance: cloud.Instance{ID: "123", Name: "source", Region: "r", State: "offline", Plan: "p", Disk: 50, IPs: []string{}}, HasUserData: &yes, Snapshot: "source-snapshot", Generation: "legacy_config", Disks: []cloud.LinodeDisk{}, Configs: []cloud.LinodeConfig{}, Interfaces: []cloud.LinodeInterface{}}, nil
	}
	if v == "firewalls" {
		return map[string]any{"items": []any{}, "snapshot": "empty"}, nil
	}
	return f.advancedFake.LinodeRead(ctx, v, id, p, n)
}
func (f *beta5BrowserProvider) StackScripts(context.Context, string, string, int) (cloud.StackScriptPage, error) {
	s, _ := f.StackScript(context.Background(), 7)
	return cloud.StackScriptPage{Items: []cloud.StackScript{s}, Page: 1, Pages: 1, Results: 1}, nil
}
func (f *beta5BrowserProvider) StackScript(context.Context, int) (cloud.StackScript, error) {
	return cloud.StackScript{ID: 7, Label: "trusted-script", Images: []string{"im"}, Snapshot: "snapshot-stack-fixture", Fields: []cloud.StackField{{Name: "SECRET", Label: "脚本参数"}}, Public: true}, nil
}
func (f *beta5BrowserProvider) ValidateStackScript(context.Context, cloud.Spec) error { return nil }
func TestBrowserBeta5NetworkAndRebuild(t *testing.T) {
	if os.Getenv("NARBE_BROWSER_TEST") != "1" {
		t.Skip("set NARBE_BROWSER_TEST=1")
	}
	h, f, _ := advancedSetup(t)
	p := &beta5BrowserProvider{f}
	p.items = []cloud.Instance{{ID: "123", Name: "source", Region: "r", State: "offline", Plan: "p", Image: "im", Actions: []string{"start", "delete"}, IPs: []string{}}}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return p, nil }
	proxyHost := ""
	proxyPort := 0
	if os.Getenv("NARBE_LIVE_NETWORK_TEST") == "1" {
		proxyHost, proxyPort = liveReadProxy(t)
		t.Setenv("NARBE_PROXY_ALLOW_PRIVATE", net.JoinHostPort(proxyHost, strconv.Itoa(proxyPort)))
	}
	handler := h.a.Handler()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only network diagnostics are deterministic Go HTTP fixtures; config saves,
		// sessions, revisions, confirmations, encrypted jobs use production handlers.
		if r.URL.Path == "/api/network/test" && os.Getenv("NARBE_LIVE_NETWORK_TEST") != "1" {
			reply(w, 200, map[string]any{"kind": "fixture", "mode": "proxy", "status": "success", "ip": "203.0.113.55", "checkedAt": time.Now().Unix(), "latencyMs": 12, "message": "测试替身出口"})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	h.a.Origin = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("node", filepath.Join(root, "scripts/browser-beta5.mjs"), server.URL)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NARBE_TEST_PASSWORD="+testPassword, "NARBE_LIVE_PROXY_HOST="+proxyHost, "NARBE_LIVE_PROXY_PORT="+strconv.Itoa(proxyPort))
	out, e := cmd.CombinedOutput()
	t.Log(string(out))
	if e != nil {
		t.Fatal(e)
	}
	if f.calls != 0 {
		t.Fatal(fmt.Sprint("real cloud writes", f.calls))
	}
}
