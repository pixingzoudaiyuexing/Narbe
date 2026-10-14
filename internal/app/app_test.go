package app

import (
	"context"
	"encoding/json"

	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

const testPassword = "fixture-admin-password-2026"

type fakeProvider struct {
	mu                   sync.Mutex
	calls                int
	createErr, errorRead error
	items                []cloud.Instance
	beforeRead           func()
	beforeCatalog        func()
}

func (f *fakeProvider) Catalog(context.Context, string, string) (cloud.Catalog, error) {
	if f.beforeCatalog != nil {
		f.beforeCatalog()
	}
	return cloud.Catalog{Regions: []cloud.Choice{{ID: "r", Name: "测试地区"}}, Plans: []cloud.Choice{{ID: "p", Name: "标准规格", Architecture: "x86_64", CPU: 2, Memory: 2048, Disk: 25}}, Images: []cloud.Choice{{ID: "im", Name: "Ubuntu 26.04 LTS", Architecture: "x86_64", Family: "ubuntu", Disk: 10}}, Keys: []cloud.Choice{{ID: "key", Name: "测试 SSH 密钥"}}}, nil
}
func (f *fakeProvider) Instances(context.Context, string) ([]cloud.Instance, error) {
	if f.beforeRead != nil {
		f.beforeRead()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cloud.Instance{}, f.items...), f.errorRead
}
func (f *fakeProvider) Create(_ context.Context, s cloud.Spec, token string) (cloud.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.createErr != nil {
		return cloud.Operation{}, f.createErr
	}
	f.items = append(f.items, cloud.Instance{ID: token, Name: s.Name, Region: s.Region, State: "active", Plan: s.Plan, Image: s.Image, IPs: []string{"192.0.2.10"}, Actions: []string{"stop", "reboot", "delete"}})
	return cloud.Operation{ResourceID: token, Region: s.Region, Expected: "active"}, nil
}
func (f *fakeProvider) Action(_ context.Context, i cloud.Instance, action, token string) (cloud.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if action == "delete" {
		f.items = []cloud.Instance{}
	}
	return cloud.Operation{ResourceID: i.ID, Region: i.Region, Expected: "active"}, f.createErr
}
func (f *fakeProvider) Poll(context.Context, cloud.Operation) (bool, error) { return true, nil }

type harness struct {
	a      *App
	cookie *http.Cookie
	csrf   string
	fake   *fakeProvider
	dir    string
}

func setup(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	s, e := store.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	pass := filepath.Join(dir, "bootstrap")
	os.WriteFile(pass, []byte(testPassword), 0600)
	if e = s.InitPassword(pass); e != nil {
		t.Fatal(e)
	}
	f := &fakeProvider{items: []cloud.Instance{}}
	a := New(s, "http://narbe.test")
	a.Lifecycle = nil
	a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
	h := &harness{a: a, fake: f, dir: dir}
	r := h.request("POST", "login", map[string]string{"password": testPassword}, false)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	h.cookie = r.Result().Cookies()[0]
	var session map[string]string
	json.Unmarshal(r.Body.Bytes(), &session)
	h.csrf = session["csrf"]
	return h
}
func (h *harness) request(method, path string, body any, auth bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "http://narbe.test/api/"+path, strings.NewReader(string(raw)))
	req.Header.Set("Origin", "http://narbe.test")
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.AddCookie(h.cookie)
		req.Header.Set("X-CSRF-Token", h.csrf)
	}
	res := httptest.NewRecorder()
	h.a.Handler().ServeHTTP(res, req)
	return res
}
func (h *harness) add(t *testing.T) string {
	t.Helper()
	r := h.request("POST", "accounts", map[string]any{"name": "个人云账号", "provider": "digitalocean", "credentials": map[string]string{"token": "fixture-cloud-secret"}}, true)
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var a account
	json.Unmarshal(r.Body.Bytes(), &a)
	return a.ID
}
func intent(aid string) mutation {
	return mutation{ID: store.ID(), AccountID: aid, Action: "create", Region: "r", Name: "dev-01", ConfirmName: "dev-01", Spec: cloud.Spec{Name: "dev-01", Region: "r", Plan: "p", Image: "im", Architecture: "x86_64", Key: "key"}}
}
func (h *harness) confirm(t *testing.T, b mutation) mutation {
	t.Helper()
	r := h.request("POST", "confirm", b, true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var out struct {
		Confirmation string `json:"confirmation"`
		Revision     int    `json:"accountRevision"`
	}
	json.Unmarshal(r.Body.Bytes(), &out)
	b.Confirmation = out.Confirmation
	b.AccountRevision = out.Revision
	return b
}
func TestAuthenticationOriginCSRFAndSecretRedaction(t *testing.T) {
	h := setup(t)
	if r := h.request("GET", "accounts", nil, false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	req := httptest.NewRequest("POST", "http://narbe.test/api/accounts", strings.NewReader("{}"))
	req.AddCookie(h.cookie)
	req.Header.Set("Origin", "http://evil.test")
	req.Header.Set("X-CSRF-Token", h.csrf)
	r := httptest.NewRecorder()
	h.a.Handler().ServeHTTP(r, req)
	if r.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	req.Header.Set("Origin", "http://narbe.test")
	req.Header.Del("X-CSRF-Token")
	r = httptest.NewRecorder()
	h.a.Handler().ServeHTTP(r, req)
	if r.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	id := h.add(t)
	r = h.request("GET", "accounts", nil, true)
	if strings.Contains(r.Body.String(), "fixture-cloud-secret") || strings.Contains(r.Body.String(), "credentials") {
		t.Fatal("secret in API")
	}
	var raw []byte
	h.a.S.DB.QueryRow("SELECT credentials FROM accounts WHERE id=?", id).Scan(&raw)
	if strings.Contains(string(raw), "fixture-cloud-secret") {
		t.Fatal("plaintext credentials")
	}
	if h.cookie.SameSite != http.SameSiteStrictMode || !h.cookie.HttpOnly {
		t.Fatal("cookie missing security flags")
	}
}
func TestPublicCatalogDoesNotProveCredentialValidity(t *testing.T) {
	h := setup(t)
	h.fake.errorRead = &cloud.Error{Status: 401, Code: "invalid"}
	r := h.request("POST", "accounts", map[string]any{"name": "invalid", "provider": "linode", "credentials": map[string]string{"token": "not-a-real-secret"}}, true)
	if r.Code != 400 {
		t.Fatal("public catalog accepted invalid token", r.Code)
	}
}
func TestConfirmationIsBoundSingleUseExpiringAndIdempotent(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := h.confirm(t, intent(aid))
	changed := b
	changed.Spec.Plan = "other"
	if r := h.request("POST", "jobs", changed, true); r.Code != 409 {
		t.Fatal("tampered confirmation accepted", r.Code)
	}
	if r := h.request("POST", "jobs", b, true); r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := h.request("POST", "jobs", b, true); r.Code != 200 {
		t.Fatal("duplicate not idempotent", r.Code)
	}
	changed = b
	changed.Spec.Name = "other"
	if r := h.request("POST", "jobs", changed, true); r.Code != 409 {
		t.Fatal("id reused with changed payload")
	}
	var count int
	h.a.S.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate jobs", count)
	}
	if !h.a.runOne(context.Background()) {
		t.Fatal("worker idle")
	}
	if h.fake.calls != 1 {
		t.Fatal("duplicate provider dispatch")
	}
	jobs, _ := h.a.jobs()
	if jobs[0].State != "succeeded" {
		t.Fatal(jobs[0])
	}
	b = h.confirm(t, intent(aid))
	h.a.S.DB.Exec("UPDATE confirmations SET expires=0")
	if r := h.request("POST", "jobs", b, true); r.Code != 409 {
		t.Fatal("expired confirmation accepted")
	}
}
func TestConcurrentAccountMutationsHaveOneWinner(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	a := h.confirm(t, intent(aid))
	b := h.confirm(t, intent(aid))
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, m := range []mutation{a, b} {
		wg.Add(1)
		go func(m mutation) { defer wg.Done(); codes <- h.request("POST", "jobs", m, true).Code }(m)
	}
	wg.Wait()
	close(codes)
	wins := 0
	for code := range codes {
		if code == 202 {
			wins++
		} else if code != 409 {
			t.Fatal("unexpected", code)
		}
	}
	if wins != 1 {
		t.Fatal("concurrent dispatch winners", wins)
	}
}
func TestUnknownNeverReplaysAndBlocksCredentialRemoval(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	h.fake.createErr = &cloud.Error{Code: "connection lost", Uncertain: true}
	b := h.confirm(t, intent(aid))
	h.request("POST", "jobs", b, true)
	h.a.runOne(context.Background())
	jobs, _ := h.a.jobs()
	if jobs[0].State != "unknown" {
		t.Fatal(jobs[0])
	}
	if h.a.runOne(context.Background()) {
		t.Fatal("unknown task replayed")
	}
	if h.fake.calls != 1 {
		t.Fatal(h.fake.calls)
	}
	if r := h.request("DELETE", "accounts/"+aid, map[string]string{"confirmName": "个人云账号"}, true); r.Code != 409 {
		t.Fatal("busy credentials removed")
	}
	h.a.S.Close()
	s, e := store.Open(h.dir)
	if e != nil {
		t.Fatal(e)
	}
	h.a.S = s
	t.Cleanup(func() { s.Close() })
	if h.a.runOne(context.Background()) {
		t.Fatal("restart replayed unknown")
	}
	if h.fake.calls != 1 {
		t.Fatal("restart dispatched")
	}
	if r := h.request("POST", "jobs/"+b.ID+"/resolve", map[string]any{"password": testPassword, "confirmName": b.Name, "checked": true}, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := h.request("DELETE", "accounts/"+aid, map[string]string{"confirmName": "个人云账号"}, true); r.Code != 200 {
		t.Fatal(r.Code)
	}
	jobs, _ = h.a.jobs()
	if len(jobs) != 1 || jobs[0].State != "resolved" {
		t.Fatal("history lost")
	}
}
func TestQueuedCancellationAndLiveIdentityCheck(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := h.confirm(t, intent(aid))
	h.request("POST", "jobs", b, true)
	r := h.request("POST", "jobs/"+b.ID+"/cancel", map[string]any{}, true)
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	if h.a.runOne(context.Background()) || h.fake.calls != 0 {
		t.Fatal("cancelled task dispatched")
	}
	h.fake.items = []cloud.Instance{{ID: "i", Name: "before", Region: "r", Actions: []string{"delete"}}}
	m := mutation{ID: store.ID(), AccountID: aid, Action: "delete", Region: "r", Target: "i", Name: "before", ConfirmName: "before"}
	m = h.confirm(t, m)
	h.request("POST", "jobs", m, true)
	h.fake.items[0].Name = "changed"
	h.a.runOne(context.Background())
	jobs, _ := h.a.jobs()
	for _, j := range jobs {
		if j.ID == m.ID && j.State != "failed" {
			t.Fatal(j)
		}
	}
	if h.fake.calls != 0 {
		t.Fatal("changed identity deleted")
	}
}
func TestPasswordRotationRevokesSessions(t *testing.T) {
	h := setup(t)
	r := h.request("POST", "password", map[string]string{"current": testPassword, "password": "new-fixture-password"}, true)
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	if r = h.request("GET", "session", nil, true); r.Code != 401 {
		t.Fatal("old session survived")
	}
}

func TestConfirmationCannotSwitchCloudCredentials(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := h.confirm(t, intent(aid))
	r := h.request("PUT", "accounts/"+aid, map[string]any{"name": "renamed", "credentials": map[string]string{"token": "new-fixture-account-token"}}, true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = h.request("POST", "jobs", b, true)
	if r.Code != 409 {
		t.Fatal("stale confirmation used new cloud account", r.Code, r.Body.String())
	}
	if h.fake.calls != 0 {
		t.Fatal("unexpected cloud write")
	}
}

func TestCredentialUpdateCannotResurrectRetiredAccount(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	started, release := make(chan struct{}), make(chan struct{})
	h.fake.beforeRead = func() { close(started); <-release }
	done := make(chan int, 1)
	go func() {
		done <- h.request("PUT", "accounts/"+aid, map[string]any{"name": "new-name", "credentials": map[string]string{"token": "new-fixture-token"}}, true).Code
	}()
	<-started
	r := h.request("DELETE", "accounts/"+aid, map[string]string{"confirmName": "个人云账号"}, true)
	if r.Code != 200 {
		close(release)
		t.Fatal(r.Code, r.Body.String())
	}
	close(release)
	if code := <-done; code != 409 {
		t.Fatal("credential update resurrected retired account", code)
	}
	var retired, size int
	h.a.S.DB.QueryRow("SELECT retired,length(credentials) FROM accounts WHERE id=?", aid).Scan(&retired, &size)
	if retired != 1 || size != 0 {
		t.Fatal("retired account retained credentials", retired, size)
	}
}
