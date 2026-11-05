package app

import (
	"context"
	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type taskUXBrowserProvider struct{ *beta5BrowserProvider }

func (p *taskUXBrowserProvider) LinodeRead(ctx context.Context, view, id, period string, page int) (any, error) {
	if view == "instance" {
		yes := true
		return cloud.LinodeState{Instance: cloud.Instance{ID: "123", Name: "source", Region: "r", State: "running", Plan: "p", Disk: 50, IPs: []string{}}, HasUserData: &yes, Snapshot: "source-snapshot", Generation: "legacy_config", Disks: []cloud.LinodeDisk{{ID: 42, Label: "System", Filesystem: "ext4", Status: "ready", Size: 25000}}, Configs: []cloud.LinodeConfig{}, Interfaces: []cloud.LinodeInterface{}}, nil
	}
	return p.beta5BrowserProvider.LinodeRead(ctx, view, id, period, page)
}
func TestBrowserTaskUXFollowup(t *testing.T) {
	if os.Getenv("NARBE_BROWSER_TEST") != "1" {
		t.Skip("set NARBE_BROWSER_TEST=1")
	}
	h, f, aid := advancedSetup(t)
	p := &taskUXBrowserProvider{&beta5BrowserProvider{f}}
	p.items = []cloud.Instance{{ID: "123", Name: "source", Region: "r", State: "running", Plan: "p", Image: "im", Actions: []string{}, IPs: []string{}}}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return p, nil }
	_, err := h.a.S.DB.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,result,created,updated) VALUES(?,?,?,?,?,?,?,'unknown',?,?,?,?)", "task-ux-unknown-fixture", aid, "password-reset", "r", "123", "source", []byte{}, "密码重置请求结果未知；请只读核实事件", `{"resourceId":"123","region":"r","linode":{"action":"password-reset","stage":"正在重置密码"}}`, store.Now(), store.Now())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(h.a.Handler())
	h.a.Origin = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("node", filepath.Join(root, "scripts/browser-task-ux.mjs"), server.URL)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NARBE_TEST_PASSWORD="+testPassword)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("fixture must not dispatch real cloud writes", f.calls)
	}
}
