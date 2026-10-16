package app

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

func inventoryResult(t *testing.T, h *harness, id string) inventoryResponse {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		res := h.request("GET", "inventory?accountId="+id, nil, true)
		if res.Code != 200 {
			t.Fatal(res.Code, res.Body.String())
		}
		var v inventoryResponse
		if e := json.Unmarshal(res.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		if v.Phase == "ready" {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatal("inventory did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestInventoryRequiresSessionAndDoesNotLeakReadErrors(t *testing.T) {
	h := setup(t)
	defer h.a.stopInventories()
	id := h.add(t)
	if r := h.request("GET", "inventory?accountId="+id, nil, false); r.Code != 401 {
		t.Fatal(r.Code)
	}
	h.fake.errorRead = errors.New("fixture-secret-access-token")
	v := inventoryResult(t, h, id)
	b, _ := json.Marshal(v)
	if v.Complete || len(v.Failures) != 1 || strings.Contains(string(b), "fixture-secret") || v.Stats.Total != 0 {
		t.Fatal(string(b))
	}
}
func TestInventoryAsyncSingleFlightSummaryThrottleAndCloudStats(t *testing.T) {
	h := setup(t)
	defer h.a.stopInventories()
	id := h.add(t)
	h.fake.items = []cloud.Instance{{ID: "same", Name: "same", Region: "one", State: "running"}, {ID: "same", Name: "same", Region: "two", State: "offline"}, {ID: "third", Region: "two", State: "provisioning"}}
	gate := make(chan struct{})
	var reads atomic.Int32
	h.fake.beforeRead = func() { reads.Add(1); <-gate }
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := h.request("GET", "inventory?accountId="+id, nil, true)
			if res.Code != 200 {
				t.Error(res.Code)
			}
		}()
	}
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatal("duplicate scans", reads.Load())
	}
	close(gate)
	v := inventoryResult(t, h, id)
	if !v.Complete || v.Stats.Total != 3 || v.Stats.Running != 1 || v.Stats.Stopped != 1 || v.Stats.Other != 1 || len(v.Items) != 3 {
		t.Fatal(v)
	}
	res := h.request("GET", "inventory?summary=1&accountId="+id, nil, true)
	var summary inventoryResponse
	json.Unmarshal(res.Body.Bytes(), &summary)
	if len(summary.Items) != 0 || summary.Stats.Total != 3 {
		t.Fatal(summary)
	}
	if r := h.request("GET", "inventory?refresh=1&accountId="+id, nil, true); r.Code != 429 {
		t.Fatal("refresh not throttled", r.Code)
	}
	if r := h.request("GET", "inventory?retry=unknown&accountId="+id, nil, true); r.Code != 400 {
		t.Fatal("unvalidated retry", r.Code)
	}
}
func TestInventoryRevisionRetirementAndTerminalJobInvalidateCache(t *testing.T) {
	h := setup(t)
	defer h.a.stopInventories()
	id := h.add(t)
	first := inventoryResult(t, h, id)
	h.fake.items = []cloud.Instance{{ID: "new", Name: "new", Region: "r", State: "running"}}
	_, e := h.a.S.DB.Exec("UPDATE accounts SET revision=revision+1 WHERE id=?", id)
	if e != nil {
		t.Fatal(e)
	}
	second := inventoryResult(t, h, id)
	if second.AccountRevision == first.AccountRevision || second.Stats.Total != 1 {
		t.Fatal(second)
	}
	h.fake.items = []cloud.Instance{}
	_, e = h.a.S.DB.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,created,updated) VALUES('terminal-cache-test',?,'create','r','','new',x'00','succeeded','',1,1)", id)
	if e != nil {
		t.Fatal(e)
	}
	if v := inventoryResult(t, h, id); v.Stats.Total != 0 {
		t.Fatal("finished cloud operation left stale cache", v)
	}
	_, e = h.a.S.DB.Exec("UPDATE accounts SET retired=1 WHERE id=?", id)
	if e != nil {
		t.Fatal(e)
	}
	if r := h.request("GET", "inventory?accountId="+id, nil, true); r.Code != 400 {
		t.Fatal("retired data returned", r.Code)
	}
}
func TestInventoryGlobalConcurrencyAndBoundedCache(t *testing.T) {
	h := setup(t)
	defer h.a.stopInventories()
	var active, maxActive atomic.Int32
	ids := []string{}
	for n := 0; n < inventoryCacheLimit; n++ {
		ids = append(ids, h.add(t))
	}
	extra := h.add(t)
	gate := make(chan struct{})
	h.fake.beforeRead = func() {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old; old = maxActive.Load() {
			if maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		<-gate
	}
	for _, id := range ids {
		if r := h.request("GET", "inventory?accountId="+id, nil, true); r.Code != 200 {
			t.Fatal(r.Code)
		}
	}
	if r := h.request("GET", "inventory?accountId="+extra, nil, true); r.Code != 429 {
		t.Fatal("unbounded queued scans", r.Code)
	}
	if maxActive.Load() > 2 {
		t.Fatal("unbounded scan concurrency", maxActive.Load())
	}
	close(gate)
	for _, id := range ids {
		inventoryResult(t, h, id)
	}
	if v := inventoryResult(t, h, extra); !v.Complete {
		t.Fatal(v)
	}
	h.a.inventoryMu.Lock()
	size := len(h.a.inventories)
	h.a.inventoryMu.Unlock()
	if size > inventoryCacheLimit {
		t.Fatal(size)
	}
}
