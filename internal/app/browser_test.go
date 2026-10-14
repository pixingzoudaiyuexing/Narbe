package app

import (
	"context"
	"encoding/json"
	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBrowser(t *testing.T) {
	if os.Getenv("NARBE_BROWSER_TEST") != "1" {
		t.Skip("set NARBE_BROWSER_TEST=1 after npm ci and installing Chromium")
	}
	h := setup(t)
	// Historical Azure deletion records stay readable after account retirement.
	aid := "browser-retired-azure"
	if _, e := h.a.S.DB.Exec("INSERT INTO accounts(id,name,provider,credentials,created,retired) VALUES(?,'历史Azure账号','azure',x'00',?,1)", aid, store.Now()-1000); e != nil {
		t.Fatal(e)
	}
	ledger := &cloud.ResourceLedger{Status: "linked", SourceJobID: "original-create", SourceState: "succeeded"}
	for _, kind := range []string{"disk", "nic", "nsg", "public-ip"} {
		ledger.Resources = append(ledger.Resources, cloud.RelatedResource{ID: "/subscriptions/test/resourceGroups/rg/" + kind + "/browser-resource", Kind: kind, State: "unverified"})
	}
	for _, fixture := range []struct {
		id, name, state, progress string
		op                        cloud.Operation
	}{
		{"azure-delete-ledger", "azure-ledger-server", "succeeded", "虚拟机已删除；关联资源及费用未核实", cloud.Operation{ResourceID: "/subscriptions/test/resourceGroups/rg/vm/known", Region: "r", Expected: "deleted", Ledger: ledger}},
		{"azure-delete-unknown", "azure-unlinked-server", "succeeded", "虚拟机已删除；关联资源及费用未核实", cloud.Operation{ResourceID: "/subscriptions/test/resourceGroups/rg/vm/unknown", Region: "r", Expected: "deleted", Ledger: &cloud.ResourceLedger{Status: "unknown"}}},
		{"cancelled-after-restart", "forgotten-server", "cancelled", "应用重启，未派发任务已取消；请重新检查并确认操作", cloud.Operation{}},
	} {
		var result []byte
		if fixture.op.ResourceID != "" {
			result, _ = json.Marshal(fixture.op)
		}
		action := "delete"
		if fixture.state == "cancelled" {
			action = "create"
		}
		if _, e := h.a.S.DB.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,result,created,updated) VALUES(?,?,?,'r','',?,x'00',?,?,?, ?,?)", fixture.id, aid, action, fixture.name, fixture.state, fixture.progress, result, store.Now()-900, store.Now()-900); e != nil {
			t.Fatal(e)
		}
	}
	server := httptest.NewUnstartedServer(h.a.Handler())
	h.a.Origin = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { h.a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	repo, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("node", filepath.Join(repo, "scripts/browser-test.mjs"), server.URL)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "NARBE_TEST_PASSWORD="+testPassword)
	out, e := cmd.CombinedOutput()
	t.Log(string(out))
	if e != nil {
		t.Fatal(e)
	}
}
