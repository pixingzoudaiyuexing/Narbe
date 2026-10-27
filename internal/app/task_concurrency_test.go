package app

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFollowupNoTypedNameStillBindsIdentityAndParameters(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := intent(aid)
	b.ConfirmName = ""
	r := h.request("POST", "confirm", b, true)
	if r.Code != 200 {
		t.Fatalf("ordinary intent must not require typing name: %d %s", r.Code, r.Body.String())
	}
	var c map[string]any
	json.Unmarshal(r.Body.Bytes(), &c)
	b.Confirmation = c["confirmation"].(string)
	b.AccountRevision = int(c["accountRevision"].(float64))
	changed := b
	changed.Spec.Name = "other"
	if h.request("POST", "jobs", changed, true).Code != 409 {
		t.Fatal("configuration tampering accepted")
	}
	if h.request("POST", "jobs", b, true).Code != 202 {
		t.Fatal("enqueue")
	}
	if h.request("POST", "jobs", b, true).Code != 200 {
		t.Fatal("stable request id")
	}
}

func TestFollowupSameAccountUnknownDoesNotBlockNewRequests(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := h.confirm(t, intent(aid))
	if h.request("POST", "jobs", b, true).Code != 202 {
		t.Fatal("first enqueue")
	}
	h.fake.createErr = &cloud.Error{Code: "connection lost", Uncertain: true}
	h.a.runOne(context.Background())
	second := h.confirm(t, intent(aid))
	if r := h.request("POST", "jobs", second, true); r.Code != 202 {
		t.Fatalf("unknown account lock: %d %s", r.Code, r.Body.String())
	}
	if h.request("POST", "jobs", b, true).Code != 200 {
		t.Fatal("unknown duplicate")
	}
	if h.request("PUT", "accounts/"+aid, map[string]any{"name": "updated"}, true).Code != 200 {
		t.Fatal("unknown blocked account edit")
	}
	h.fake.createErr = nil
	h.a.runOne(context.Background())
	var nextState string
	h.a.S.DB.QueryRow("SELECT state FROM jobs WHERE id=?", second.ID).Scan(&nextState)
	if nextState != "succeeded" || h.fake.calls != 2 {
		t.Fatal("new work did not execute independently of unknown", nextState, h.fake.calls)
	}
	var n int
	h.a.S.DB.QueryRow("SELECT count(*) FROM jobs WHERE state='unknown'").Scan(&n)
	if n != 1 {
		t.Fatal("unknown history lost")
	}
}

type parallelProvider struct {
	*fakeProvider
	blocked chan struct{}
	entered chan string
	peak    atomic.Int32
	active  atomic.Int32
	mu2     sync.Mutex
	writes  map[string]int
}

func (p *parallelProvider) Create(ctx context.Context, s cloud.Spec, id string) (cloud.Operation, error) {
	n := p.active.Add(1)
	defer p.active.Add(-1)
	for {
		old := p.peak.Load()
		if old >= n || p.peak.CompareAndSwap(old, n) {
			break
		}
	}
	p.mu2.Lock()
	p.writes[id]++
	p.mu2.Unlock()
	p.entered <- id
	if strings.HasPrefix(s.Name, "slow") {
		select {
		case <-p.blocked:
		case <-ctx.Done():
			return cloud.Operation{ResourceID: id}, &cloud.Error{Code: "connection lost", Uncertain: true}
		}
	}
	return cloud.Operation{ResourceID: id, Region: s.Region, Expected: "active"}, nil
}
func TestFollowupWorkerRunIsBoundedAndSlowTaskDoesNotBlockOthers(t *testing.T) {
	h := setup(t)
	p := &parallelProvider{fakeProvider: h.fake, blocked: make(chan struct{}), entered: make(chan string, 16), writes: map[string]int{}}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return p, nil }
	// Separate accounts isolate the existing serial executor defect before removing the account gate.
	for i := 0; i < 8; i++ {
		aid := h.add(t)
		b := intent(aid)
		b.Name = "fast"
		if i == 0 {
			b.Name = "slow"
		}
		b.Spec.Name = b.Name
		b.ConfirmName = b.Name
		b = h.confirm(t, b)
		if h.request("POST", "jobs", b, true).Code != 202 {
			t.Fatal("enqueue")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.a.Run(ctx); close(done) }()
	defer func() { cancel(); close(p.blocked); <-done }()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("worker never started")
	}
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("serial worker blocks all other jobs")
	}
	deadline := time.After(2 * time.Second)
	for {
		var n int
		h.a.S.DB.QueryRow("SELECT count(*) FROM jobs WHERE state='succeeded'").Scan(&n)
		if n == 7 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("fast jobs did not finish independently")
		case <-time.After(time.Millisecond):
		}
	}
	if p.peak.Load() > 4 {
		t.Fatal("unbounded workers", p.peak.Load())
	}
	cancel()
}

func TestFollowupSameServerStateIsLeftToCloudAPI(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	h.fake.items = []cloud.Instance{{ID: "same", Name: "same", Region: "r", State: "rebooting", Actions: []string{}}}
	for _, action := range []string{"start", "stop", "reboot", "delete"} {
		b := mutation{ID: store.ID(), AccountID: aid, Target: "same", Name: "same", Region: "r", Action: action}
		if r := h.request("POST", "confirm", b, true); r.Code != 200 {
			t.Fatalf("local status block for %s: %d %s", action, r.Code, r.Body.String())
		}
	}
	bad := mutation{ID: store.ID(), AccountID: aid, Target: "same", Name: "spoof", Region: "r", Action: "delete"}
	if h.request("POST", "confirm", bad, true).Code == 200 {
		t.Fatal("identity bypass")
	}
}

type credentialPinProvider struct {
	*fakeProvider
	token    string
	rotate   *atomic.Bool
	onRotate func()
	seen     chan string
}

func (p *credentialPinProvider) Catalog(ctx context.Context, r, plan string) (cloud.Catalog, error) {
	if p.rotate.CompareAndSwap(true, false) {
		p.onRotate()
	}
	return p.fakeProvider.Catalog(ctx, r, plan)
}
func (p *credentialPinProvider) Create(ctx context.Context, s cloud.Spec, id string) (cloud.Operation, error) {
	p.seen <- "write:" + p.token
	return cloud.Operation{ResourceID: id, Region: s.Region, Expected: "active"}, nil
}
func (p *credentialPinProvider) Poll(ctx context.Context, op cloud.Operation) (bool, error) {
	p.seen <- "poll:" + p.token
	return true, nil
}
func TestFollowupWorkerPinsCredentialsAcrossConcurrentAccountChanges(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	var rotate atomic.Bool
	seen := make(chan string, 8)
	replace := func() {
		raw, _ := json.Marshal(cloud.Credentials{"token": "new-token-fixture"})
		sealed, _ := h.a.S.Seal(raw, "account:"+aid)
		if _, err := h.a.S.DB.Exec("UPDATE accounts SET credentials=?,revision=revision+1 WHERE id=?", sealed, aid); err != nil {
			t.Error(err)
		}
	}
	h.a.Factory = func(_ string, c cloud.Credentials) (cloud.Provider, error) {
		return &credentialPinProvider{fakeProvider: h.fake, token: c["token"], rotate: &rotate, onRotate: replace, seen: seen}, nil
	}
	b := h.confirm(t, intent(aid))
	if h.request("POST", "jobs", b, true).Code != 202 {
		t.Fatal("enqueue")
	}
	rotate.Store(true)
	h.a.runOne(context.Background())
	if a, b := <-seen, <-seen; a != "write:fixture-cloud-secret" || b != "poll:fixture-cloud-secret" {
		t.Fatal("mixed credentials", a, b)
	}
	b = h.confirm(t, intent(aid))
	if b.AccountRevision != 2 {
		t.Fatal(b.AccountRevision)
	}
	h.request("POST", "jobs", b, true)
	h.a.runOne(context.Background())
	if a, b := <-seen, <-seen; a != "write:new-token-fixture" || b != "poll:new-token-fixture" {
		t.Fatal("new credentials ignored", a, b)
	}
}

func TestFollowupUnknownReconcileCannotUseChangedCloudIdentity(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := h.confirm(t, intent(aid))
	h.request("POST", "jobs", b, true)
	h.a.runOne(context.Background())
	h.a.S.DB.Exec("UPDATE jobs SET state='unknown' WHERE id=?", b.ID)
	if r := h.request("POST", "jobs/"+b.ID+"/reconcile", nil, true); r.Code != 200 {
		t.Fatal("readonly reliable result", r.Code, r.Body.String())
	}
	h.a.S.DB.Exec("UPDATE jobs SET state='unknown' WHERE id=?", b.ID)
	h.a.S.DB.Exec("UPDATE accounts SET revision=revision+1 WHERE id=?", aid)
	if r := h.request("POST", "jobs/"+b.ID+"/reconcile", nil, true); r.Code != 409 {
		t.Fatal("new identity incorrectly completed old task", r.Code)
	}
	if h.fake.calls != 1 {
		t.Fatal("reconcile wrote", h.fake.calls)
	}
}

func TestFollowupSlowConfirmationDoesNotSerializeOtherServers(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	slow := intent(aid)
	fast := intent(aid)
	entered, release := make(chan struct{}), make(chan struct{})
	var n atomic.Int32
	h.fake.beforeCatalog = func() {
		if n.Add(1) == 1 {
			close(entered)
			<-release
		}
	}
	done := make(chan int, 1)
	go func() { done <- h.request("POST", "confirm", slow, true).Code }()
	<-entered
	other := make(chan int, 1)
	go func() { other <- h.request("POST", "confirm", fast, true).Code }()
	select {
	case code := <-other:
		if code != 200 {
			t.Error(code)
		}
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("cloud read held global network lock")
	}
	close(release)
	if code := <-done; code != 200 {
		t.Fatal(code)
	}
}

func TestFollowupUnknownDoesNotBlockAdminRemovalAndHistoryPersists(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := h.confirm(t, intent(aid))
	h.request("POST", "jobs", b, true)
	h.a.S.DB.Exec("UPDATE jobs SET state='unknown' WHERE id=?", b.ID)
	if h.request("DELETE", "accounts/"+aid, map[string]any{"confirmName": "个人云账号"}, true).Code != 400 {
		t.Fatal("remove without administrator password")
	}
	if h.request("DELETE", "accounts/"+aid, map[string]any{"password": testPassword}, true).Code != 200 {
		t.Fatal("unknown account lock retained")
	}
	var n int
	h.a.S.DB.QueryRow("SELECT count(*) FROM jobs WHERE id=? AND state='unknown'", b.ID).Scan(&n)
	if n != 1 {
		t.Fatal("deleted task history")
	}
}

func TestFollowupSameAccountTwelveJobsOverlapWithinBound(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	p := &parallelProvider{fakeProvider: h.fake, blocked: make(chan struct{}), entered: make(chan string, 16), writes: map[string]int{}}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return p, nil }
	for i := 0; i < 12; i++ {
		b := intent(aid)
		b.Name = "slow"
		b.Spec.Name = "slow"
		b = h.confirm(t, b)
		if h.request("POST", "jobs", b, true).Code != 202 {
			t.Fatal("same-account enqueue")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.a.Run(ctx); close(done) }()
	released := false
	defer func() {
		cancel()
		if !released {
			close(p.blocked)
		}
		<-done
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-p.entered:
		case <-time.After(time.Second):
			t.Fatal("same account was serialized")
		}
	}
	select {
	case <-p.entered:
		t.Fatal("fifth task exceeded bounded execution")
	case <-time.After(80 * time.Millisecond):
	}
	close(p.blocked)
	released = true
	deadline := time.After(2 * time.Second)
	for {
		var n int
		h.a.S.DB.QueryRow("SELECT count(*) FROM jobs WHERE state='succeeded'").Scan(&n)
		if n == 12 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("tasks did not finish")
		case <-time.After(time.Millisecond):
		}
	}
	p.mu2.Lock()
	defer p.mu2.Unlock()
	for _, n := range p.writes {
		if n != 1 {
			t.Fatal("duplicate cloud write")
		}
	}
	if len(p.writes) != 12 || p.peak.Load() != 4 {
		t.Fatal("incorrect concurrency", len(p.writes), p.peak.Load())
	}
}

func TestFollowupSameServerCallsCloudAndDisplaysAPIRejection(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	h.fake.items = []cloud.Instance{{ID: "same", Name: "same", Region: "r", State: "rebooting", Actions: []string{}}}
	for i, action := range []string{"stop", "start"} {
		b := mutation{ID: store.ID(), AccountID: aid, Target: "same", Name: "same", Region: "r", Action: action}
		b = h.confirm(t, b)
		if r := h.request("POST", "jobs", b, true); r.Code != 202 {
			t.Fatal("enqueue", r.Code)
		}
		if i == 1 {
			h.fake.createErr = &cloud.Error{Status: 409, Code: "cloud refuses action while server is migrating"}
		}
		h.a.runOne(context.Background())
		var state, msg string
		h.a.S.DB.QueryRow("SELECT state,error FROM jobs WHERE id=?", b.ID).Scan(&state, &msg)
		if i == 0 && state != "succeeded" {
			t.Fatal(state)
		}
		if i == 1 && (state != "failed" || !strings.Contains(msg, "HTTP 409") || !strings.Contains(msg, "cloud refuses")) {
			t.Fatal(state, msg)
		}
	}
	if h.fake.calls != 2 {
		t.Fatal("local action lock", h.fake.calls)
	}
}

func TestFollowupRecoveryKeepsLastStepAndNeverReplays(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	b := h.confirm(t, intent(aid))
	h.request("POST", "jobs", b, true)
	raw := `{"resourceId":"same","linode":{"action":"password-reset","stage":"正在重置密码","passwordResetSent":true}}`
	if _, err := h.a.S.DB.Exec("UPDATE jobs SET state='running',progress='正在重置密码',result=? WHERE id=?", raw, b.ID); err != nil {
		t.Fatal(err)
	}
	var before []byte
	h.a.S.DB.QueryRow("SELECT payload FROM jobs WHERE id=?", b.ID).Scan(&before)
	keyBefore, err := os.ReadFile(filepath.Join(h.dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	h.a.S.Close()
	s, err := store.Open(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	h.a.S = s
	t.Cleanup(func() { s.Close() })
	var state, progress, result string
	var after []byte
	h.a.S.DB.QueryRow("SELECT state,progress,result,payload FROM jobs WHERE id=?", b.ID).Scan(&state, &progress, &result, &after)
	keyAfter, _ := os.ReadFile(filepath.Join(h.dir, "master.key"))
	if state != "unknown" || !strings.Contains(progress, "正在重置密码") || result != raw || !bytes.Equal(before, after) || !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("recovery lost step or encrypted state", state, progress, result)
	}
	if h.a.runOne(context.Background()) || h.fake.calls != 0 {
		t.Fatal("restart repeated original cloud write")
	}
}
