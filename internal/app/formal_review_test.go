package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

func TestReviewLoginManySourcesDoNotBlockAdministrator(t *testing.T) {
	h := setup(t)
	// Invalid JSON exercises the real limiter cheaply without 4,100 Argon2 computations.
	for i := 0; i < 4100; i++ {
		req := httptest.NewRequest("POST", "http://narbe.test/api/login", strings.NewReader("{"))
		req.RemoteAddr = fmt.Sprintf("198.18.%d.%d:1234", i/256, i%256)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://narbe.test")
		res := httptest.NewRecorder()
		h.a.Handler().ServeHTTP(res, req)
		if res.Code != 401 {
			t.Fatalf("source %d incorrectly denied by other sources: %d", i, res.Code)
		}
	}
	req := httptest.NewRequest("POST", "http://narbe.test/api/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
	req.RemoteAddr = "192.0.2.200:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://narbe.test")
	res := httptest.NewRecorder()
	h.a.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("legitimate administrator blocked by source capacity: %d %s", res.Code, res.Body.String())
	}
	h.a.mu.Lock()
	size := len(h.a.attempts)
	h.a.mu.Unlock()
	if size > 4096 {
		t.Fatalf("limiter records unbounded: %d", size)
	}
}
func TestReviewQueuedTasksExpireBeforeCloudDispatch(t *testing.T) {
	for _, action := range []string{"create", "delete"} {
		t.Run(action, func(t *testing.T) {
			h := setup(t)
			aid := h.add(t)
			m := intent(aid)
			if action == "delete" {
				h.fake.items = []cloud.Instance{{ID: "i", Name: m.Name, Region: "r", Actions: []string{"delete"}}}
				m.Action = "delete"
				m.Target = "i"
				m.Spec = cloud.Spec{}
			}
			m = h.confirm(t, m)
			if r := h.request("POST", "jobs", m, true); r.Code != 202 {
				t.Fatal(r.Code)
			}
			if _, e := h.a.S.DB.Exec("UPDATE jobs SET created=? WHERE id=?", store.Now()-301, m.ID); e != nil {
				t.Fatal(e)
			}
			h.a.runOne(context.Background())
			var state, progress string
			h.a.S.DB.QueryRow("SELECT state,progress FROM jobs WHERE id=?", m.ID).Scan(&state, &progress)
			if state != "cancelled" || h.fake.calls != 0 {
				t.Fatalf("expired %s dispatched: state=%s calls=%d progress=%s", action, state, h.fake.calls, progress)
			}
		})
	}
}
func TestReviewRestartCancelsQueuedWithoutWritingCloud(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	m := h.confirm(t, intent(aid))
	h.request("POST", "jobs", m, true)
	h.a.S.Close()
	s, e := store.Open(h.dir)
	if e != nil {
		t.Fatal(e)
	}
	h.a.S = s
	t.Cleanup(func() { s.Close() })
	h.a.runOne(context.Background())
	var state string
	s.DB.QueryRow("SELECT state FROM jobs WHERE id=?", m.ID).Scan(&state)
	if state != "cancelled" || h.fake.calls != 0 {
		t.Fatalf("recovered queue dispatched: state=%s calls=%d", state, h.fake.calls)
	}
}
func TestReviewLinodeWeakPasswordRejectedBeforeQueue(t *testing.T) {
	h := setup(t)
	r := h.request("POST", "accounts", map[string]any{"name": "Linode测试", "provider": "linode", "credentials": map[string]string{"token": "fixture-token"}}, true)
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var aid string
	h.a.S.DB.QueryRow("SELECT id FROM accounts WHERE provider='linode'").Scan(&aid)
	m := intent(aid)
	m.Spec.Key = ""
	m.Spec.Password = strings.Repeat("a", 24)
	r = h.request("POST", "confirm", m, true)
	if r.Code != 400 {
		t.Fatalf("obviously weak root password entered confirmation flow: %d %s", r.Code, r.Body.String())
	}
	if h.fake.calls != 0 {
		t.Fatal("cloud write from password validation")
	}
}

type ledgerAzureFake struct {
	*fakeProvider
	scope       string
	trusted     bool
	beforeWrite func()
}

func (f *ledgerAzureFake) Plan(s cloud.Spec, id string) cloud.Operation {
	base := "/subscriptions/" + f.scope + "/resourceGroups/rg/providers/"
	return cloud.Operation{ResourceID: base + "Microsoft.Compute/virtualMachines/" + id, Region: s.Region, Expected: "running", Owned: []string{base + "Microsoft.Network/publicIPAddresses/" + id, base + "Microsoft.Network/networkInterfaces/" + id, base + "Microsoft.Network/networkSecurityGroups/" + id, base + "Microsoft.Compute/disks/" + id}}
}
func (f *ledgerAzureFake) MatchesCreation(_ context.Context, s cloud.Spec, id string, op cloud.Operation, target string) bool {
	expected := f.Plan(s, id)
	if !f.trusted || !strings.EqualFold(op.ResourceID, expected.ResourceID) || !strings.EqualFold(target, expected.ResourceID) || len(op.Owned) != len(expected.Owned) {
		return false
	}
	for n := range expected.Owned {
		if !strings.EqualFold(op.Owned[n], expected.Owned[n]) {
			return false
		}
	}
	return true
}
func seedReviewAzureCreate(t *testing.T, h *harness, aid string, f *ledgerAzureFake) (mutation, cloud.Operation) {
	t.Helper()
	m := intent(aid)
	m.AccountRevision = 1
	op := f.Plan(m.Spec, m.ID)
	raw, _ := json.Marshal(m)
	sealed, e := h.a.S.Seal(raw, "job:"+m.ID)
	if e != nil {
		t.Fatal(e)
	}
	result, _ := json.Marshal(op)
	_, e = h.a.S.DB.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,result,created,updated) VALUES(?,?,?,?,?,?,?,'succeeded','创建完成',?,?,?)", m.ID, aid, m.Action, m.Region, m.Target, m.Name, sealed, result, store.Now()-600, store.Now()-600)
	if e != nil {
		t.Fatal(e)
	}
	return m, op
}
func TestReviewAzureDeleteRetainsCreationLedger(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	h.a.S.DB.Exec("UPDATE accounts SET provider='azure' WHERE id=?", aid)
	f := &ledgerAzureFake{fakeProvider: h.fake, scope: "sub", trusted: true}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
	source, op := seedReviewAzureCreate(t, h, aid, f)
	h.fake.items = []cloud.Instance{{ID: op.ResourceID, Name: "vm", Region: "r", Actions: []string{"delete"}}}
	m := mutation{ID: store.ID(), AccountID: aid, Action: "delete", Target: op.ResourceID, Region: "r", Name: "vm", ConfirmName: "vm"}
	m = h.confirm(t, m)
	if r := h.request("POST", "jobs", m, true); r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	h.a.runOne(context.Background())
	r := h.request("GET", "jobs", nil, true)
	var jobs []map[string]any
	json.Unmarshal(r.Body.Bytes(), &jobs)
	found := false
	for _, j := range jobs {
		if j["id"] != m.ID {
			continue
		}
		found = true
		result := j["result"].(map[string]any)
		ledger, ok := result["ledger"].(map[string]any)
		if !ok {
			t.Fatal("Azure delete loses original creation resource ledger", result)
		}
		if ledger["sourceJobId"] != source.ID || ledger["status"] != "linked" {
			t.Fatal(ledger)
		}
		resources := ledger["resources"].([]any)
		if len(resources) != 4 {
			t.Fatal(resources)
		}
		for n, record := range resources {
			resource := record.(map[string]any)
			if resource["id"] != op.Owned[n] || resource["state"] != "unverified" {
				t.Fatal(resource)
			}
		}
	}
	if !found {
		t.Fatal("delete task absent from API")
	}
	if strings.Contains(r.Body.String(), "fixture-cloud-secret") {
		t.Fatal("credential in delete ledger")
	}
}

func (f *ledgerAzureFake) Action(ctx context.Context, i cloud.Instance, action, token string) (cloud.Operation, error) {
	if f.beforeWrite != nil {
		f.beforeWrite()
	}
	return f.fakeProvider.Action(ctx, i, action, token)
}
func TestReviewLimiterKeepsSourceLimitWhenCapacityIsFull(t *testing.T) {
	h := setup(t)
	now := time.Now()
	for n := 0; n < 8; n++ {
		if !h.a.allowLogin("attacker", now) {
			t.Fatal("source limited too early")
		}
	}
	for n := 0; n < 5000; n++ {
		if !h.a.allowLogin(fmt.Sprint("rotating-", n), now) {
			t.Fatal("new source denied by capacity")
		}
	}
	if h.a.allowLogin("attacker", now) {
		t.Fatal("source limit evicted by first-attempt traffic")
	}
	if !h.a.allowLogin("administrator", now) {
		t.Fatal("legitimate source denied")
	}
	h.a.mu.Lock()
	size := len(h.a.attempts)
	h.a.mu.Unlock()
	if size > 4096 {
		t.Fatal(size)
	}
	if !h.a.allowLogin("attacker", now.Add(11*time.Minute)) {
		t.Fatal("expired limit never clears")
	}
}
func TestReviewConcurrentLoginSourcesStayBoundedAndArgonSlotsRemainLimited(t *testing.T) {
	h := setup(t)
	var wg sync.WaitGroup
	for n := 0; n < 32; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				h.a.allowLogin(fmt.Sprintf("%d-%d", n, i), time.Now())
			}
		}(n)
	}
	wg.Wait()
	h.a.mu.Lock()
	size := len(h.a.attempts)
	h.a.mu.Unlock()
	if size > 4096 {
		t.Fatal(size)
	}
	h.a.loginSlots <- struct{}{}
	h.a.loginSlots <- struct{}{}
	r := h.request("POST", "login", map[string]string{"password": testPassword}, false)
	<-h.a.loginSlots
	<-h.a.loginSlots
	if r.Code != 429 || !strings.Contains(r.Body.String(), "繁忙") {
		t.Fatal("Argon2 parallel admission no longer bounded", r.Code, r.Body.String())
	}
}
func TestReviewValidationExpiryCancelsBeforeFirstWrite(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	m := h.confirm(t, intent(aid))
	h.request("POST", "jobs", m, true)
	h.fake.beforeCatalog = func() {
		if _, e := h.a.S.DB.Exec("UPDATE jobs SET created=? WHERE id=?", store.Now()-301, m.ID); e != nil {
			t.Error(e)
		}
	}
	h.a.runOne(context.Background())
	var state string
	h.a.S.DB.QueryRow("SELECT state FROM jobs WHERE id=?", m.ID).Scan(&state)
	if state != "cancelled" || h.fake.calls != 0 {
		t.Fatalf("preflight completed after expiry but dispatched: %s %d", state, h.fake.calls)
	}
}
func TestReviewCancelledIntentReplayRequiresFreshConfirmation(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	old := h.confirm(t, intent(aid))
	h.request("POST", "jobs", old, true)
	h.a.S.Close()
	s, e := store.Open(h.dir)
	if e != nil {
		t.Fatal(e)
	}
	h.a.S = s
	t.Cleanup(func() { s.Close() })
	if r := h.request("POST", "jobs", old, true); r.Code != 200 {
		t.Fatal("lost-response idempotency changed", r.Code)
	}
	h.a.runOne(context.Background())
	if h.fake.calls != 0 {
		t.Fatal("cancelled intent replayed")
	}
	fresh := old
	fresh.ID = store.ID()
	if r := h.request("POST", "jobs", fresh, true); r.Code != 409 {
		t.Fatal("old confirmation reused for new operation")
	}
	fresh.Confirmation = ""
	fresh = h.confirm(t, fresh)
	if r := h.request("POST", "jobs", fresh, true); r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	h.a.runOne(context.Background())
	if h.fake.calls != 1 {
		t.Fatal("fresh confirmed operation not exactly once", h.fake.calls)
	}
}
func TestReviewConcurrentClaimAndCancellationNeverWriteAfterSuccessfulCancel(t *testing.T) {
	for iteration := 0; iteration < 12; iteration++ {
		h := setup(t)
		aid := h.add(t)
		m := h.confirm(t, intent(aid))
		h.request("POST", "jobs", m, true)
		start := make(chan struct{})
		var wg sync.WaitGroup
		codes := make(chan int, 1)
		for n := 0; n < 2; n++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; h.a.runOne(context.Background()) }()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes <- h.request("POST", "jobs/"+m.ID+"/cancel", map[string]any{}, true).Code
		}()
		close(start)
		wg.Wait()
		code := <-codes
		if h.fake.calls > 1 {
			t.Fatal("two workers sent duplicate writes")
		}
		if code == 200 && h.fake.calls != 0 {
			t.Fatal("write after successful cancellation")
		}
		if code != 200 && code != 409 {
			t.Fatal(code)
		}
	}
}
func TestReviewAzureDeleteUnknownLedgerBoundaries(t *testing.T) {
	for _, variant := range []string{"missing", "account", "revision", "subscription", "resource-group", "resource-id", "owned", "payload", "duplicate", "tag"} {
		t.Run(variant, func(t *testing.T) {
			h := setup(t)
			aid := h.add(t)
			h.a.S.DB.Exec("UPDATE accounts SET provider='azure' WHERE id=?", aid)
			f := &ledgerAzureFake{fakeProvider: h.fake, scope: "sub", trusted: true}
			h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
			source, op := seedReviewAzureCreate(t, h, aid, f)
			switch variant {
			case "missing":
				h.a.S.DB.Exec("DELETE FROM jobs WHERE id=?", source.ID)
			case "account":
				other := h.add(t)
				h.a.S.DB.Exec("UPDATE jobs SET account_id=? WHERE id=?", other, source.ID)
			case "revision":
				h.a.S.DB.Exec("UPDATE accounts SET revision=2 WHERE id=?", aid)
			case "subscription":
				f.scope = "other-sub"
			case "resource-group":
				op.Owned[0] = strings.Replace(op.Owned[0], "/resourceGroups/rg/", "/resourceGroups/other/", 1)
			case "resource-id":
				op.ResourceID += "-different"
			case "owned":
				op.Owned[1] = "https://evil.test/not-a-resource"
			case "payload":
				h.a.S.DB.Exec("UPDATE jobs SET payload=x'00' WHERE id=?", source.ID)
			case "duplicate":
				copyID := store.ID()
				raw, _ := json.Marshal(source)
				sealed, _ := h.a.S.Seal(raw, "job:"+copyID)
				result, _ := json.Marshal(op)
				h.a.S.DB.Exec("INSERT INTO jobs SELECT ?,account_id,action,region,target,name,?,state,progress,?,error,created,updated FROM jobs WHERE id=?", copyID, sealed, result, source.ID)
			case "tag":
				f.trusted = false
			}
			if variant == "owned" || variant == "resource-group" || variant == "resource-id" {
				result, _ := json.Marshal(op)
				h.a.S.DB.Exec("UPDATE jobs SET result=? WHERE id=?", result, source.ID)
			}
			target := f.Plan(source.Spec, source.ID).ResourceID
			if variant == "subscription" {
				target = op.ResourceID
			}
			h.fake.items = []cloud.Instance{{ID: target, Name: "vm", Region: "r", Actions: []string{"delete"}}}
			m := h.confirm(t, mutation{ID: store.ID(), AccountID: aid, Action: "delete", Target: target, Region: "r", Name: "vm", ConfirmName: "vm"})
			h.request("POST", "jobs", m, true)
			h.a.runOne(context.Background())
			j := reviewJob(t, h, m.ID)
			if j.Result == nil || j.Result.Ledger == nil || j.Result.Ledger.Status != "unknown" || len(j.Result.Ledger.Resources) != 0 {
				t.Fatal("untrusted resource IDs exposed", j.Result)
			}
			if h.fake.calls != 1 {
				t.Fatal("unknown ledger changed VM-only deletion behavior", h.fake.calls)
			}
		})
	}
}
func TestReviewAzureDeleteLedgerIsDurableBeforeWriteAndBeyondRecentPage(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	h.a.S.DB.Exec("UPDATE accounts SET provider='azure' WHERE id=?", aid)
	f := &ledgerAzureFake{fakeProvider: h.fake, scope: "sub", trusted: true}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
	source, op := seedReviewAzureCreate(t, h, aid, f)
	for n := 0; n < 205; n++ {
		_, e := h.a.S.DB.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,created,updated) VALUES(?,?,'start','r','other','other',x'00','succeeded','',?,?)", store.ID(), aid, store.Now()-100, store.Now()-100)
		if e != nil {
			t.Fatal(e)
		}
	}
	h.fake.items = []cloud.Instance{{ID: op.ResourceID, Name: "vm", Region: "r", Actions: []string{"delete"}}}
	m := h.confirm(t, mutation{ID: store.ID(), AccountID: aid, Action: "delete", Target: op.ResourceID, Region: "r", Name: "vm", ConfirmName: "vm"})
	h.request("POST", "jobs", m, true)
	f.beforeWrite = func() {
		var result []byte
		h.a.S.DB.QueryRow("SELECT result FROM jobs WHERE id=?", m.ID).Scan(&result)
		var saved cloud.Operation
		json.Unmarshal(result, &saved)
		if saved.Ledger == nil || saved.Ledger.SourceJobID != source.ID {
			t.Error("ledger was not persisted before DELETE")
		}
	}
	h.fake.createErr = &cloud.Error{Status: 403, Code: "fixture denied"}
	h.a.runOne(context.Background())
	j := reviewJob(t, h, m.ID)
	if j.State != "failed" || j.Result == nil || j.Result.Ledger == nil || j.Result.Ledger.Status != "linked" {
		t.Fatal("definite delete denial or durable ledger changed", j)
	}
}

func TestReviewLinodePasswordAndSSHConfirmationModes(t *testing.T) {
	h := setup(t)
	r := h.request("POST", "accounts", map[string]any{"name": "Linode测试", "provider": "linode", "credentials": map[string]string{"token": "fixture-token"}}, true)
	if r.Code != 201 {
		t.Fatal(r.Code)
	}
	var aid string
	h.a.S.DB.QueryRow("SELECT id FROM accounts WHERE provider='linode'").Scan(&aid)
	public, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	key, e := ssh.NewPublicKey(public)
	if e != nil {
		t.Fatal(e)
	}
	pub := string(ssh.MarshalAuthorizedKey(key))
	for _, test := range []struct {
		name, password, public string
		want                   int
	}{
		{"short", strings.Repeat("A", 11), "", 400}, {"long", strings.Repeat("A", 129), "", 400}, {"single", strings.Repeat("a", 24), "", 400}, {"sequence", "123456789012", "", 400}, {"repeated", "Password123!Password123!", "", 400}, {"weak-with-ssh", "Password123!Password123!", pub, 400},
		{"leet", "P@ssw0rd123!", "", 400}, {"decorated-repeat", "password1password1!", "", 400}, {"random", "K8!vQ2$mR9#zT4@p", "", 200}, {"phrase", "cobalt orchard river lantern nebula meadow", "", 200}, {"ssh-only", "", pub, 200}, {"neither", "", "", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := intent(aid)
			m.Spec.Key = ""
			m.Spec.Password = test.password
			m.Spec.SSHPublicKey = test.public
			r := h.request("POST", "confirm", m, true)
			if r.Code != test.want {
				t.Fatal(r.Code, r.Body.String())
			}
			if test.password != "" && strings.Contains(r.Body.String(), test.password) {
				t.Fatal("password leaked in validation error")
			}
		})
	}
	var jobs int
	h.a.S.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs)
	if jobs != 0 || h.fake.calls != 0 {
		t.Fatal("validation queued or wrote resources")
	}
}

func TestReviewFutureQueueTimestampNeverExtendsAuthorization(t *testing.T) {
	for _, action := range []string{"create", "delete"} {
		t.Run(action, func(t *testing.T) {
			h := setup(t)
			aid := h.add(t)
			m := intent(aid)
			if action == "delete" {
				h.fake.items = []cloud.Instance{{ID: "i", Name: m.Name, Region: "r", Actions: []string{"delete"}}}
				m.Action = "delete"
				m.Target = "i"
				m.Spec = cloud.Spec{}
			}
			m = h.confirm(t, m)
			if r := h.request("POST", "jobs", m, true); r.Code != 202 {
				t.Fatal(r.Code)
			}
			h.a.S.DB.Exec("UPDATE jobs SET created=? WHERE id=?", store.Now()+600, m.ID)
			h.a.runOne(context.Background())
			var state string
			h.a.S.DB.QueryRow("SELECT state FROM jobs WHERE id=?", m.ID).Scan(&state)
			if state != "cancelled" || h.fake.calls != 0 {
				t.Fatal("future timestamp extended old authorization", state, h.fake.calls)
			}
		})
	}
}

func reviewJob(t *testing.T, h *harness, id string) job {
	t.Helper()
	jobs, e := h.a.jobs()
	if e != nil {
		t.Fatal(e)
	}
	for _, j := range jobs {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("expected task %s is absent", id)
	return job{}
}

func (f *ledgerAzureFake) Catalog(ctx context.Context, region, plan string) (cloud.Catalog, error) {
	c, e := f.fakeProvider.Catalog(ctx, region, plan)
	c.Images[0].Family = "windows"
	c.Networks = []cloud.Choice{{ID: "subnet", Name: "existing subnet"}}
	return c, e
}
func (f *ledgerAzureFake) Create(_ context.Context, s cloud.Spec, id string) (cloud.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	op := f.Plan(s, id)
	f.items = append(f.items, cloud.Instance{ID: op.ResourceID, Name: s.Name, Region: s.Region, State: "running", Actions: []string{"delete"}})
	return op, f.createErr
}
func TestReviewAzureWorkerCreateThenDeleteAssociatesAndRecoversLedger(t *testing.T) {
	h := setup(t)
	aid := h.add(t)
	h.a.S.DB.Exec("UPDATE accounts SET provider='azure' WHERE id=?", aid)
	f := &ledgerAzureFake{fakeProvider: h.fake, scope: "sub", trusted: true}
	h.a.Factory = func(string, cloud.Credentials) (cloud.Provider, error) { return f, nil }
	create := intent(aid)
	create.Spec.Key = ""
	create.Spec.Network = "subnet"
	create.Spec.Username = "narbe"
	create.Spec.Password = "K8!vQ2$mR9#zT4@p"
	create.Spec.ManagementCIDR = "192.0.2.1/32"
	create = h.confirm(t, create)
	if r := h.request("POST", "jobs", create, true); r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	h.a.runOne(context.Background())
	original := reviewJob(t, h, create.ID)
	if original.State != "succeeded" || original.Result == nil || len(original.Result.Owned) != 4 {
		t.Fatal("creation ledger missing", original)
	}
	remove := h.confirm(t, mutation{ID: store.ID(), AccountID: aid, Action: "delete", Target: original.Result.ResourceID, Region: "r", Name: create.Name, ConfirmName: create.Name})
	if r := h.request("POST", "jobs", remove, true); r.Code != 202 {
		t.Fatal(r.Code)
	}
	h.fake.createErr = &cloud.Error{Code: "fixture response lost", Uncertain: true}
	h.a.runOne(context.Background())
	deleted := reviewJob(t, h, remove.ID)
	if deleted.State != "unknown" || deleted.Result.Ledger == nil || deleted.Result.Ledger.SourceJobID != create.ID {
		t.Fatal("delete lost creation ledger", deleted)
	}
	h.a.S.Close()
	s, e := store.Open(h.dir)
	if e != nil {
		t.Fatal(e)
	}
	h.a.S = s
	t.Cleanup(func() { s.Close() })
	h.a.runOne(context.Background())
	deleted = reviewJob(t, h, remove.ID)
	if deleted.State != "unknown" || len(deleted.Result.Ledger.Resources) != 4 || h.fake.calls != 2 {
		t.Fatal("restart replayed DELETE or lost linked resources", deleted, h.fake.calls)
	}
}
