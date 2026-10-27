package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

type advancedFake struct {
	*fakeProvider
	readErr      error
	changeErr    error
	blocked      bool
	beforeChange func()
	polls        int
}

func (f *advancedFake) LinodeRead(_ context.Context, view, id, period string, page int) (any, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	if view == "firewall" {
		return cloud.LinodeFirewall{ID: 7, Label: "shared-fw", Snapshot: "fw-snapshot", Devices: []cloud.FirewallDevice{}}, nil
	}
	if view == "instance" {
		return cloud.LinodeState{Instance: cloud.Instance{ID: "123", Name: "source", Region: "us-east", State: "offline"}, Snapshot: "source-snapshot", Generation: "legacy_config"}, nil
	}
	return map[string]any{"safe": "read"}, nil
}
func (f *advancedFake) ValidateChange(_ context.Context, id, action string, c cloud.LinodeChange) error {
	if !c.Acknowledge {
		return errors.New("需要确认影响")
	}
	if f.blocked || c.Snapshot != "source-snapshot" {
		return errors.New("配置已变化")
	}
	return nil
}
func (f *advancedFake) ValidateFirewallSelection(context.Context, cloud.FirewallSelection) error {
	return nil
}
func (f *advancedFake) Change(ctx context.Context, id, action string, c cloud.LinodeChange, token string) (cloud.Operation, error) {
	if f.beforeChange != nil {
		f.beforeChange()
	}
	f.calls++
	op := cloud.Operation{ResourceID: id, Region: "us-east", Linode: &cloud.LinodeOperation{Action: action}}
	if action == "clone" {
		op.ResourceID = "456"
		op.Owned = []string{"linode:456"}
	}
	return op, f.changeErr
}
func (f *advancedFake) Poll(context.Context, cloud.Operation) (bool, error) {
	f.polls++
	return true, nil
}
func advancedSetup(t *testing.T) (*harness, *advancedFake, string) {
	t.Helper()
	h := setup(t)
	aid := h.add(t)
	if _, e := h.a.S.DB.Exec("UPDATE accounts SET provider='linode' WHERE id=?", aid); e != nil {
		t.Fatal(e)
	}
	f := &advancedFake{fakeProvider: h.fake}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
	return h, f, aid
}
func advancedIntent(aid, action string) map[string]any {
	b := map[string]any{"id": "advanced-intent-fixture-001", "accountId": aid, "action": action, "region": "us-east", "target": "123", "name": "source", "spec": map[string]any{}, "advanced": map[string]any{"snapshot": "source-snapshot", "acknowledge": true, "password": "fixtureStrong-Unused-294!"}, "confirmation": "", "confirmName": "source"}
	if action == "rebuild" {
		b["advanced"].(map[string]any)["metadataAcknowledged"] = true
	}
	return b
}
func confirmAdvanced(t *testing.T, h *harness, b map[string]any) {
	t.Helper()
	res := h.request("POST", "confirm", b, true)
	if res.Code != 200 {
		t.Fatalf("confirm %d %s", res.Code, res.Body.String())
	}
	var c map[string]any
	json.Unmarshal(res.Body.Bytes(), &c)
	b["confirmation"] = c["confirmation"]
	b["accountRevision"] = c["accountRevision"]
}
func TestLinodeAdvancedAuthenticationAndAccountIsolation(t *testing.T) {
	h, f, aid := advancedSetup(t)
	for _, path := range []string{"linode?accountId=" + aid + "&view=billing", "linode?accountId=" + aid + "&view=stats&target=123"} {
		if r := h.request("GET", path, nil, false); r.Code != 401 {
			t.Fatal(r.Code)
		}
	}
	b := advancedIntent(aid, "rebuild")
	if r := h.request("POST", "jobs", b, true); r.Code != 409 {
		t.Fatal(r.Code)
	}
	b["region"] = "eu-west"
	if r := h.request("POST", "confirm", b, true); r.Code == 200 {
		t.Fatal("cross-region accepted")
	}
	b["region"] = "us-east"
	b["target"] = "456"
	if r := h.request("POST", "confirm", b, true); r.Code == 200 {
		t.Fatal("cross-target accepted")
	}
	b["target"] = "123"
	b["confirmName"] = "wrong"
	if r := h.request("POST", "confirm", b, true); r.Code != 200 {
		t.Fatal("typed name must not be required", r.Code, r.Body.String())
	}
	b["confirmName"] = "source"
	b["advanced"].(map[string]any)["acknowledge"] = false
	if r := h.request("POST", "confirm", b, true); r.Code == 200 {
		t.Fatal("missing destructive ack")
	}
	if f.calls != 0 {
		t.Fatal("validation wrote cloud")
	}
}
func TestLinodeAdvancedIntentEncryptedIdempotentAndRevalidated(t *testing.T) {
	h, f, aid := advancedSetup(t)
	b := advancedIntent(aid, "rebuild")
	confirmAdvanced(t, h, b)
	if r := h.request("POST", "jobs", b, true); r.Code != 202 {
		t.Fatal(r.Body.String())
	}
	if r := h.request("POST", "jobs", b, true); r.Code != 200 {
		t.Fatal("lost reply duplicate rejected")
	}
	var sealed []byte
	h.a.S.DB.QueryRow("SELECT payload FROM jobs WHERE id=?", b["id"]).Scan(&sealed)
	if strings.Contains(string(sealed), "fixtureStrong") {
		t.Fatal("root password plaintext in DB")
	}
	f.blocked = true
	h.a.runOne(context.Background())
	if f.calls != 0 {
		t.Fatal("changed live state wrote cloud")
	}
	jobs := h.request("GET", "jobs", nil, true)
	if strings.Contains(jobs.Body.String(), "fixtureStrong") || !strings.Contains(jobs.Body.String(), `"state":"failed"`) {
		t.Fatal(jobs.Body.String())
	}
}
func TestLinodeAdvancedWorkerUnknownNotRepeated(t *testing.T) {
	for _, action := range []string{"rebuild", "resize", "password-reset", "clone", "migrate", "firewall-bind"} {
		t.Run(action, func(t *testing.T) {
			h, f, aid := advancedSetup(t)
			f.changeErr = &cloud.Error{Code: "response lost", Uncertain: true}
			b := advancedIntent(aid, action)
			confirmAdvanced(t, h, b)
			if r := h.request("POST", "jobs", b, true); r.Code != 202 {
				t.Fatal(r.Body.String())
			}
			h.a.runOne(context.Background())
			h.a.runOne(context.Background())
			if f.calls != 1 {
				t.Fatal("uncertain write repeated", f.calls)
			}
			r := h.request("GET", "jobs", nil, true)
			if !strings.Contains(r.Body.String(), `"state":"unknown"`) {
				t.Fatal(r.Body.String())
			}
			if f.polls != 0 {
				t.Fatal("write rejection should not auto poll/retry")
			}
		})
	}
}
func TestLinodeAdvancedQueuedExpiryCancelAndCredentialChange(t *testing.T) {
	for _, mode := range []string{"expired", "cancelled", "revision"} {
		t.Run(mode, func(t *testing.T) {
			h, f, aid := advancedSetup(t)
			b := advancedIntent(aid, "clone")
			confirmAdvanced(t, h, b)
			if r := h.request("POST", "jobs", b, true); r.Code != 202 {
				t.Fatal(r.Body.String())
			}
			switch mode {
			case "expired":
				h.a.S.DB.Exec("UPDATE jobs SET created=? WHERE id=?", store.Now()-301, b["id"])
			case "cancelled":
				h.request("POST", "jobs/"+b["id"].(string)+"/cancel", map[string]any{}, true)
			case "revision":
				h.a.S.DB.Exec("UPDATE accounts SET revision=revision+1 WHERE id=?", aid)
			}
			h.a.runOne(context.Background())
			if f.calls != 0 {
				t.Fatal("stale task reached cloud")
			}
		})
	}
}
func TestLinodeAdvancedRestartCancelsQueuedRetainsUnknown(t *testing.T) {
	h, _, aid := advancedSetup(t)
	b := advancedIntent(aid, "clone")
	confirmAdvanced(t, h, b)
	h.request("POST", "jobs", b, true)
	h.a.S.Close()
	reopened, e := store.Open(h.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	var state string
	reopened.DB.QueryRow("SELECT state FROM jobs WHERE id=?", b["id"]).Scan(&state)
	if state != "cancelled" {
		t.Fatal(state)
	}
	if raw, e := os.ReadFile(filepath.Join(h.dir, "narbe.db")); e != nil || strings.Contains(string(raw), "fixtureStrong") {
		t.Fatal("private root password leaked")
	}
}
func TestLinodeAdvancedReadBoundAndRevisionFence(t *testing.T) {
	h, f, aid := advancedSetup(t)
	f.readErr = &cloud.Error{Status: 403, Code: "permission denied"}
	if r := h.request("GET", "linode?accountId="+aid+"&view=billing", nil, true); r.Code != 400 || strings.Contains(r.Body.String(), "token") {
		t.Fatal(r.Body.String())
	}
	f.readErr = nil
	h.a.advancedSlots <- struct{}{}
	h.a.advancedSlots <- struct{}{}
	if r := h.request("GET", "linode?accountId="+aid+"&view=billing", nil, true); r.Code != 429 {
		t.Fatal(r.Code)
	}
	<-h.a.advancedSlots
	<-h.a.advancedSlots
	if r := h.request("GET", "linode?accountId="+aid+"&view=billing&page=invalid", nil, true); r.Code != 400 {
		t.Fatal(r.Code)
	}
}
