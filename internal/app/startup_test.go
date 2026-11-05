package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
	"strings"
	"testing"
)

type scriptProvider struct {
	*fakeProvider
	changed         bool
	permission      bool
	beforeStackRead func()
	seen            cloud.Spec
}

func (p *scriptProvider) StackScripts(context.Context, string, string, int) (cloud.StackScriptPage, error) {
	return cloud.StackScriptPage{Items: []cloud.StackScript{{ID: 7, Label: "trusted"}}, Page: 1, Pages: 1}, nil
}
func (p *scriptProvider) StackScript(context.Context, int) (cloud.StackScript, error) {
	if p.beforeStackRead != nil {
		p.beforeStackRead()
	}
	if p.permission {
		return cloud.StackScript{}, &cloud.Error{Status: 403, Code: "permission denied"}
	}
	return cloud.StackScript{ID: 7, Label: "trusted", Snapshot: "script-snapshot"}, nil
}
func (p *scriptProvider) ValidateStackScript(ctx context.Context, s cloud.Spec) error {
	if _, e := p.StackScript(ctx, s.StackScriptID); e != nil {
		return e
	}
	if p.changed || s.StackScriptID != 7 || s.StackScriptSnapshot != "script-snapshot" {
		return errors.New("StackScript已变化")
	}
	return nil
}
func (p *scriptProvider) Create(ctx context.Context, s cloud.Spec, id string) (cloud.Operation, error) {
	p.seen = s
	return p.fakeProvider.Create(ctx, s, id)
}
func startupSetup(t *testing.T) (*harness, *scriptProvider, string) {
	h := setup(t)
	aid := h.add(t)
	p := &scriptProvider{fakeProvider: h.fake}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return p, nil }
	return h, p, aid
}
func TestStartupEncryptedIntentNoTaskDisclosureAndCreationNotExecution(t *testing.T) {
	h, p, aid := startupSetup(t)
	b := intent(aid)
	secret := "fixture-script-private-中文-87882"
	b.Spec.ScriptMode = "cloud-config"
	b.Spec.ScriptCompatibilityAck = true
	b.Spec.UserData = "#cloud-config\nruncmd:\n  - echo '" + secret + "'\n"
	b = h.confirm(t, b)
	if r := h.request("POST", "jobs", b, true); r.Code != 202 {
		t.Fatal(r.Body.String())
	}
	if r := h.request("POST", "jobs", b, true); r.Code != 200 {
		t.Fatal("duplicate lost response created new task")
	}
	h.a.runOne(context.Background())
	h.a.runOne(context.Background())
	if p.calls != 1 || p.seen.UserData != b.Spec.UserData {
		t.Fatal("bytes changed/replayed")
	}
	var payload []byte
	h.a.S.DB.QueryRow("SELECT payload FROM jobs WHERE id=?", b.ID).Scan(&payload)
	if strings.Contains(string(payload), secret) {
		t.Fatal("plaintext persisted")
	}
	r := h.request("GET", "jobs", nil, true)
	if strings.Contains(r.Body.String(), secret) || !strings.Contains(r.Body.String(), cloud.StartupNotice) || strings.Contains(r.Body.String(), "脚本执行成功") {
		t.Fatal(r.Body.String())
	}
	var jobs []job
	json.Unmarshal(r.Body.Bytes(), &jobs)
	if jobs[0].Result.Startup.SHA256 == "" || jobs[0].Result.Startup.Execution != "submitted-unverified" {
		t.Fatal("missing safe fingerprint")
	}
}
func TestStackScriptChangedOrRevokedRejectsOldConfirmationAndWorker(t *testing.T) {
	for _, stage := range []string{"submit-changed", "submit-revoked", "worker-changed"} {
		t.Run(stage, func(t *testing.T) {
			h, p, aid := startupSetup(t)
			h.a.S.DB.Exec("UPDATE accounts SET provider='linode' WHERE id=?", aid)
			b := intent(aid)
			b.Spec.Key = ""
			b.Spec.Password = "Fixture-Strong-Password-83!"
			b.Spec.ScriptMode = "stackscript"
			b.Spec.StackScriptID = 7
			b.Spec.StackScriptName = "trusted"
			b.Spec.StackScriptSnapshot = "script-snapshot"
			b.Spec.StackScriptData = map[string]string{"sensitive": "fixture-parameter-secret"}
			b = h.confirm(t, b)
			if stage == "submit-changed" {
				p.changed = true
			}
			if stage == "submit-revoked" {
				p.permission = true
			}
			res := h.request("POST", "jobs", b, true)
			if stage != "worker-changed" {
				if res.Code == 202 {
					t.Fatal("stale script confirmation accepted")
				}
			} else {
				if res.Code != 202 {
					t.Fatal(res.Body.String())
				}
				p.changed = true
				h.a.runOne(context.Background())
			}
			if p.calls != 0 {
				t.Fatal("stale script wrote cloud")
			}
		})
	}
}
func TestStartupLargeEscapedUTF8JSONAndUnknownFields(t *testing.T) {
	h, _, aid := startupSetup(t)
	b := intent(aid)
	b.Spec.ScriptMode = "shell"
	b.Spec.ScriptCompatibilityAck = true
	b.Spec.UserData = "#!/bin/bash\n" + strings.Repeat("<", 65536-len("#!/bin/bash\n"))
	r := h.request("POST", "confirm", b, true)
	if r.Code != 200 {
		t.Fatal("valid DO64KiB with JSON escaping rejected", r.Code, r.Body.String())
	}
	b.Spec.UserData += "x"
	if h.request("POST", "confirm", b, true).Code == 200 {
		t.Fatal("overflow accepted")
	}
	raw := map[string]any{"spec": map[string]any{"scriptMode": "shell", "metadata": map[string]string{"user_data": "evil"}}}
	if h.request("POST", "confirm", raw, true).Code == 200 {
		t.Fatal("unknown fields accepted")
	}
}
func TestStackScriptReadAuthenticationAndRevisionFence(t *testing.T) {
	h, p, aid := startupSetup(t)
	h.a.S.DB.Exec("UPDATE accounts SET provider='linode' WHERE id=?", aid)
	if h.request("GET", "stackscripts?accountId="+aid+"&id=7", nil, false).Code != 401 {
		t.Fatal("anonymous scripts access")
	}
	p.beforeStackRead = func() { h.a.S.DB.Exec("UPDATE accounts SET revision=revision+1 WHERE id=?", aid) }
	if r := h.request("GET", "stackscripts?accountId="+aid+"&id=7", nil, true); r.Code != 409 {
		t.Fatal(r.Body.String())
	}
	_ = store.Now()
}

func TestStackScriptConcurrentDuplicateSubmissionAndRevisionChange(t *testing.T) {
	h, p, aid := startupSetup(t)
	h.a.S.DB.Exec("UPDATE accounts SET provider='linode' WHERE id=?", aid)
	b := intent(aid)
	b.Spec.Key = ""
	b.Spec.Password = "Fixture-Strong-Password-84!"
	b.Spec.ScriptMode = "stackscript"
	b.Spec.StackScriptID = 7
	b.Spec.StackScriptName = "trusted"
	b.Spec.StackScriptSnapshot = "script-snapshot"
	b = h.confirm(t, b)
	codes := make(chan int, 12)
	for i := 0; i < 12; i++ {
		go func() { codes <- h.request("POST", "jobs", b, true).Code }()
	}
	for i := 0; i < 12; i++ {
		code := <-codes
		if code != 200 && code != 202 {
			t.Fatal("duplicate rejected", code)
		}
	}
	h.a.runOne(context.Background())
	if p.calls != 1 {
		t.Fatal("duplicate cloud create", p.calls)
	}
	h2, p2, a2 := startupSetup(t)
	h2.a.S.DB.Exec("UPDATE accounts SET provider='linode' WHERE id=?", a2)
	other := b
	other.ID = store.ID()
	other.AccountID = a2
	other.Confirmation = ""
	other.AccountRevision = 0
	other = h2.confirm(t, other)
	h2.a.S.DB.Exec("UPDATE accounts SET revision=revision+1 WHERE id=?", a2)
	if h2.request("POST", "jobs", other, true).Code == 202 || p2.calls != 0 {
		t.Fatal("old account revision accepted")
	}
}
func TestStackScriptSameIDCannotCrossAccountPermissions(t *testing.T) {
	h, p, aid := startupSetup(t)
	other := h.add(t)
	h.a.S.DB.Exec("UPDATE accounts SET provider='linode' WHERE id IN (?,?)", aid, other)
	denied := &scriptProvider{fakeProvider: h.fake, permission: true}
	cipher, e := h.a.S.Seal([]byte(`{"token":"other-account"}`), "account:"+other)
	if e != nil {
		t.Fatal(e)
	}
	h.a.S.DB.Exec("UPDATE accounts SET credentials=? WHERE id=?", cipher, other)
	h.a.Factory = func(_ string, c cloud.Credentials) (cloud.Provider, error) {
		if c["token"] == "other-account" {
			return denied, nil
		}
		return p, nil
	}
	b := intent(aid)
	b.Spec.Key = ""
	b.Spec.Password = "Fixture-Strong-Password-84!"
	b.Spec.ScriptMode = "stackscript"
	b.Spec.StackScriptID = 7
	b.Spec.StackScriptName = "trusted"
	b.Spec.StackScriptSnapshot = "script-snapshot"
	b = h.confirm(t, b)
	b.AccountID = other
	b.ID = store.ID()
	b.Confirmation = ""
	b.AccountRevision = 0
	if r := h.request("POST", "confirm", b, true); r.Code == 200 {
		t.Fatal("private script borrowed across accounts")
	}
	if p.calls != 0 {
		t.Fatal("cloud write in validation")
	}
}

func TestStartupUncertainCreateIsNotExecutionAndNotReplayed(t *testing.T) {
	h, p, aid := startupSetup(t)
	p.fakeProvider.createErr = &cloud.Error{Code: "response lost", Uncertain: true}
	b := intent(aid)
	b.Spec.ScriptMode = "shell"
	b.Spec.ScriptCompatibilityAck = true
	b.Spec.UserData = "#!/bin/bash\necho fixture-uncertain-private\n"
	b = h.confirm(t, b)
	if h.request("POST", "jobs", b, true).Code != 202 {
		t.Fatal("submit")
	}
	h.a.runOne(context.Background())
	h.a.runOne(context.Background())
	if p.calls != 1 {
		t.Fatal("write replayed")
	}
	r := h.request("GET", "jobs", nil, true)
	if strings.Contains(r.Body.String(), "fixture-uncertain-private") || !strings.Contains(r.Body.String(), `"execution":"submission-unknown"`) || !strings.Contains(r.Body.String(), `"state":"unknown"`) {
		t.Fatal(r.Body.String())
	}
}
