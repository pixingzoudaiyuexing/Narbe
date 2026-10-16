package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

func networkBody(rev int) map[string]any {
	return map[string]any{"revision": rev, "mode": "proxy", "protocol": "https", "host": "proxy.example.com", "port": 8443, "username": "user", "password": "secret-proxy-fixture", "passwordAction": "replace"}
}
func TestNetworkAuthRevisionEncryptedPersistenceAndPasswordLifecycle(t *testing.T) {
	h := setup(t)
	if h.request("GET", "network", nil, false).Code != 401 || h.request("PUT", "network", networkBody(0), false).Code != 401 {
		t.Fatal("unauthenticated network API")
	}
	if h.request("POST", "network/test", map[string]any{"kind": "direct"}, false).Code != 401 {
		t.Fatal("unauthenticated probe")
	}
	if h.request("GET", "network", nil, true).Code != 200 {
		t.Fatal("default not readable")
	}
	r := h.request("PUT", "network", networkBody(0), true)
	if r.Code != 200 || strings.Contains(r.Body.String(), "secret-proxy") {
		t.Fatal(r.Code, r.Body.String())
	}
	var stored string
	h.a.S.DB.QueryRow("SELECT value FROM settings WHERE key=?", networkSetting).Scan(&stored)
	if strings.Contains(stored, "secret-proxy") || strings.Contains(stored, "proxy.example") {
		t.Fatal("plaintext proxy persisted")
	}
	// A fresh application instance actually decrypts the persisted configuration.
	reloaded := New(h.a.S, "http://narbe.test")
	s, e := reloaded.networkSnapshot()
	if e != nil || s.Config.Password != "secret-proxy-fixture" || s.Revision != 1 {
		t.Fatal("persistence", e)
	}
	if h.request("PUT", "network", networkBody(0), true).Code != 409 {
		t.Fatal("stale revision accepted")
	}
	b := networkBody(1)
	b["password"] = ""
	b["passwordAction"] = "keep"
	if h.request("PUT", "network", b, true).Code != 200 {
		t.Fatal("keep password rejected")
	}
	s, _ = h.a.networkSnapshot()
	if s.Config.Password != "secret-proxy-fixture" {
		t.Fatal("keep lost password")
	}
	b["revision"] = 2
	b["host"] = "other.example.com"
	if h.request("PUT", "network", b, true).Code == 200 {
		t.Fatal("password reused across endpoints")
	}
	b["host"] = "proxy.example.com"
	b["passwordAction"] = "clear"
	if h.request("PUT", "network", b, true).Code != 200 {
		t.Fatal("clear failed")
	}
	s, _ = h.a.networkSnapshot()
	if s.Config.Password != "" {
		t.Fatal("not cleared")
	}
	// Corrupt settings cannot silently become direct after restart.
	h.a.S.DB.Exec("UPDATE settings SET value='broken' WHERE key=?", networkSetting)
	if _, e = New(h.a.S, "").networkSnapshot(); e == nil {
		t.Fatal("corrupt proxy fell back to direct")
	}
}
func TestNetworkChangesCancelQueuedAndInvalidateConfirmations(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	old := h.confirm(t, intent(aid))
	b := h.confirm(t, intent(aid))
	if h.request("POST", "jobs", b, true).Code != 202 {
		t.Fatal("enqueue failed")
	}
	if h.request("PUT", "network", networkBody(0), true).Code != 200 {
		t.Fatal("save failed")
	}
	var immediate string
	if err := h.a.S.DB.QueryRow("SELECT state FROM jobs WHERE id=?", b.ID).Scan(&immediate); err != nil || immediate != "cancelled" {
		t.Fatal("configuration save must atomically cancel queued work", immediate, err)
	}
	if h.request("POST", "jobs", old, true).Code != 409 {
		t.Fatal("stale confirmation used new egress")
	}
	h.a.runOne(context.Background())
	if h.fake.calls != 0 {
		t.Fatal("cancelled queued job wrote")
	}
	var state string
	h.a.S.DB.QueryRow("SELECT state FROM jobs WHERE id=?", b.ID).Scan(&state)
	if state != "cancelled" {
		t.Fatal(state)
	}
	// Lost-response retry resolves to original cancelled job, never a second write.
	if h.request("POST", "jobs", b, true).Code != 200 {
		t.Fatal("stable duplicate lost")
	}
}

func TestNetworkChangeDiscardsOldInventoryAndLateResult(t *testing.T) {
	h := setup(t)
	defer h.a.stopInventories()
	aid := h.add(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.fake.beforeRead = func() { once.Do(func() { close(started) }); <-release }
	if h.request("GET", "inventory?accountId="+aid, nil, true).Code != 200 {
		t.Fatal("inventory admission")
	}
	<-started
	if h.request("PUT", "network", networkBody(0), true).Code != 200 {
		t.Fatal("network change")
	}
	close(release)
	// A cancelled old scan can finish, but cannot repopulate the new cache.
	h.a.inventoryMu.Lock()
	_, exists := h.a.inventories[aid]
	h.a.inventoryMu.Unlock()
	if exists {
		t.Fatal("old network inventory survived")
	}
	v := inventoryResult(t, h, aid)
	if !v.Complete {
		t.Fatal("new network inventory did not complete")
	}
}
func TestNetworkRunningPinsClientAndNewFactoriesUseNewClient(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	h.a.Factory = nil
	old, e := h.a.networkSnapshot()
	if e != nil {
		t.Fatal(e)
	}
	_, p, e := h.a.providerContext(context.WithValue(context.Background(), networkContext{}, old), aid)
	if e != nil {
		t.Fatal(e)
	}
	if h.request("PUT", "network", networkBody(0), true).Code != 200 {
		t.Fatal("save failed")
	}
	_, next, e := h.a.provider(aid)
	if e != nil || p == next {
		t.Fatal("new provider missing")
	}
	again, e := h.a.factory(context.WithValue(context.Background(), networkContext{}, old), "digitalocean", cloud.Credentials{"token": "fixture"})
	if e != nil || again == nil {
		t.Fatal(e)
	}
	if old.Config.Mode != "direct" {
		t.Fatal("inflight snapshot mutated")
	}
	current, _ := h.a.networkSnapshot()
	if old.client == current.client || current.Config.Mode != "proxy" {
		t.Fatal("connection pool reused")
	}
}
func TestNetworkConcurrentTwelveUnknownIntentsNeverReplay(t *testing.T) {
	h := setup(t)
	h.fake.createErr = &cloud.Error{Code: "proxy connection lost", Uncertain: true}
	bs := []mutation{}
	for i := 0; i < 12; i++ {
		aid := h.add(t)
		b := h.confirm(t, intent(aid))
		if h.request("POST", "jobs", b, true).Code != 202 {
			t.Fatal("enqueue")
		}
		bs = append(bs, b)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.a.runOne(context.Background()) }()
	}
	wg.Wait()
	for i := 0; i < 24; i++ {
		if !h.a.runOne(context.Background()) {
			break
		}
	}
	for _, b := range bs {
		if h.request("POST", "jobs", b, true).Code != 200 {
			t.Fatal("duplicate rejected")
		}
	}
	h.a.runOne(context.Background())
	h.fake.mu.Lock()
	calls := h.fake.calls
	h.fake.mu.Unlock()
	if calls != 12 {
		t.Fatal("write replay", calls)
	}
	var unknown int
	h.a.S.DB.QueryRow("SELECT count(*) FROM jobs WHERE state='unknown'").Scan(&unknown)
	if unknown != 12 {
		t.Fatal(unknown)
	}
	if h.request("PUT", "network", networkBody(0), true).Code != 200 {
		t.Fatal("save")
	}
	h.a.runOne(context.Background())
	if h.fake.calls != 12 {
		t.Fatal("setting change replayed unknown")
	}
}

func TestNetworkRunningUsesOriginalRevisionWhileConfigurationChanges(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := h.confirm(t, intent(aid))
	if h.request("POST", "jobs", b, true).Code != 202 {
		t.Fatal("queue")
	}
	old, _ := h.a.networkSnapshot()
	once := sync.Once{}
	h.fake.beforeCatalog = func() {
		once.Do(func() {
			if r := h.request("PUT", "network", networkBody(0), true); r.Code != 200 {
				t.Fatal(r.Code, r.Body.String())
			}
		})
	}
	h.a.runOne(context.Background())
	if h.fake.calls != 1 {
		t.Fatal("running context was silently rebound/cancelled", h.fake.calls)
	}
	if old.Config.Mode != "direct" || old.Revision != 0 {
		t.Fatal("immutable config changed")
	}
	var state string
	h.a.S.DB.QueryRow("SELECT state FROM jobs WHERE id=?", b.ID).Scan(&state)
	if state != "succeeded" {
		t.Fatal("pinned running task failed", state)
	}
}
func TestNetworkDiagnosticsClassificationRateLimitsAndAuthentication(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	for _, fixture := range []struct {
		e    error
		want string
	}{{&cloud.Error{Status: 401, Code: "denied"}, "云API认证失败"}, {&cloud.Error{Status: 403, Code: "denied"}, "云账号权限不足"}, {&cloud.Error{Code: "代理认证失败"}, "代理认证失败"}, {&cloud.Error{Code: "TLS握手失败"}, "TLS握手失败"}, {nil, "授权查询通过"}} {
		h.fake.errorRead = fixture.e
		h.a.network.Lock()
		h.a.network.diagnosticAt = time.Time{}
		h.a.network.Unlock()
		r := h.request("POST", "network/test", map[string]any{"kind": "cloud", "accountId": aid}, true)
		if r.Code != 200 || !strings.Contains(r.Body.String(), fixture.want) {
			t.Fatal(r.Code, r.Body.String())
		}
		if h.request("POST", "network/test", map[string]any{"kind": "cloud", "accountId": aid}, true).Code != 429 {
			t.Fatal("diagnostic not limited")
		}
	}
	req := httptest.NewRequest("PUT", "http://narbe.test/api/network", strings.NewReader(`{}`))
	req.AddCookie(h.cookie)
	req.Header.Set("Origin", "http://narbe.test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.a.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("network CSRF bypass", w.Code)
	}
}
