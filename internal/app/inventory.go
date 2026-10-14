package app

import (
	"context"
	"net/http"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

const inventoryCacheLimit = 8

type inventoryEntry struct {
	revision            int
	snapshot            cloud.Inventory
	touched             time.Time
	started             time.Time
	cancel              context.CancelFunc
	latestJob           int64
	completedAt         time.Time
	fullScanCompletedAt time.Time
}
type inventoryResponse struct {
	cloud.Inventory
	AccountID       string `json:"accountId"`
	AccountRevision int    `json:"accountRevision"`
	NextRefreshAt   int64  `json:"nextRefreshAt"`
}

func copyInventory(v cloud.Inventory) cloud.Inventory {
	v.Items = append([]cloud.Instance{}, v.Items...)
	v.Regions = append([]cloud.Choice{}, v.Regions...)
	v.Failures = append([]cloud.RegionFailure{}, v.Failures...)
	v.Succeeded = append([]string{}, v.Succeeded...)
	v.Warnings = append([]string{}, v.Warnings...)
	return v
}
func (a *App) stopInventories() {
	a.inventoryMu.Lock()
	defer a.inventoryMu.Unlock()
	for _, e := range a.inventories {
		e.cancel()
	}
}
func (a *App) inventory(w http.ResponseWriter, r *http.Request) {
	// Always re-check retirement and credential revision before returning cached data.
	acc, p, err := a.provider(r.URL.Query().Get("accountId"))
	if err != nil {
		fail(w, 400, "云账号不存在或凭据无法读取")
		return
	}
	now := time.Now()
	var latestJob int64
	if err := a.S.DB.QueryRow("SELECT COALESCE(MAX(rowid),0) FROM jobs WHERE account_id=? AND state IN ('succeeded','failed','unknown','resolved')", acc.ID).Scan(&latestJob); err != nil {
		fail(w, 500, "无法核实资源缓存，请稍后重试")
		return
	}
	retry := r.URL.Query().Get("retry")
	force := r.URL.Query().Get("refresh") == "1" || retry != ""
	a.inventoryMu.Lock()
	var liveRevision int
	if e := a.S.DB.QueryRow("SELECT revision FROM accounts WHERE id=? AND retired=0", acc.ID).Scan(&liveRevision); e != nil || liveRevision != acc.Revision {
		a.inventoryMu.Unlock()
		fail(w, 409, "账号凭据已变化，请重新加载总览")
		return
	}
	current := a.inventories[acc.ID]
	if current != nil && current.latestJob != latestJob {
		current.cancel()
		delete(a.inventories, acc.ID)
		current = nil
	}
	if current != nil && current.revision != acc.Revision {
		current.cancel()
		delete(a.inventories, acc.ID)
		current = nil
	}
	if retry != "" {
		found := false
		if current != nil {
			for _, f := range current.snapshot.Failures {
				if f.Region == retry {
					found = true
				}
			}
		}
		if !found {
			a.inventoryMu.Unlock()
			fail(w, 400, "只能重试本次查询失败的地区，请先刷新总览")
			return
		}
		// Expired partial snapshots must not extend old successful regions forever.
		if !current.fullScanCompletedAt.IsZero() && now.Sub(current.fullScanCompletedAt) > 60*time.Second {
			retry = "*"
		}
	}
	start := current == nil || current.snapshot.Phase == "ready" && (force || !current.completedAt.IsZero() && now.Sub(current.completedAt) > 60*time.Second)
	if start && current != nil && now.Sub(current.started) < 15*time.Second {
		a.inventoryMu.Unlock()
		w.Header().Set("Retry-After", "15")
		fail(w, 429, "资源刷新至少间隔15秒，请稍后重试")
		return
	}
	if start {
		if current == nil && len(a.inventories) >= inventoryCacheLimit {
			oldest := ""
			var oldestTime time.Time
			for id, e := range a.inventories {
				if e.snapshot.Phase == "ready" && (oldest == "" || e.touched.Before(oldestTime)) {
					oldest = id
					oldestTime = e.touched
				}
			}
			if oldest == "" {
				a.inventoryMu.Unlock()
				w.Header().Set("Retry-After", "3")
				fail(w, 429, "其他账号正在查询，请稍后重试")
				return
			}
			a.inventories[oldest].cancel()
			delete(a.inventories, oldest)
		}
		previous := cloud.Inventory{}
		if current != nil {
			previous = copyInventory(current.snapshot)
			current.cancel()
		}
		scope := "当前账号 · 全部地区"
		if acc.Provider == "azure" {
			scope = "当前接入资源组 · 全部地区（非整个订阅）"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		entry := &inventoryEntry{revision: acc.Revision, snapshot: cloud.EmptyInventory(scope), touched: now, started: now, cancel: cancel}
		entry.latestJob = latestJob
		if current != nil && retry != "" && retry != "*" {
			entry.fullScanCompletedAt = current.fullScanCompletedAt
		}
		entry.snapshot.Phase = "queued"
		a.inventories[acc.ID] = entry
		current = entry
		go func() {
			defer cancel()
			report := func(v cloud.Inventory) {
				a.inventoryMu.Lock()
				defer a.inventoryMu.Unlock()
				if a.inventories[acc.ID] == entry {
					if v.Phase == "ready" && entry.completedAt.IsZero() {
						entry.completedAt = time.Now()
						if entry.fullScanCompletedAt.IsZero() {
							entry.fullScanCompletedAt = entry.completedAt
						}
					}
					if v.Phase == "ready" && v.UpdatedAt == 0 {
						v.UpdatedAt = entry.completedAt.Unix()
					}
					entry.snapshot = copyInventory(v)
				}
			}
			select {
			case a.inventorySlots <- struct{}{}:
				defer func() { <-a.inventorySlots }()
				cloud.ScanInventory(ctx, p, acc.Provider, retry, previous, report)
			case <-ctx.Done():
				v := cloud.EmptyInventory(scope)
				v.Phase = "ready"
				v.Failures = append(v.Failures, cloud.RegionFailure{Region: "*", Message: "等待查询超时，请重试"})
				report(v)
			}
		}()
	}
	current.touched = now
	result := inventoryResponse{Inventory: copyInventory(current.snapshot), AccountID: acc.ID, AccountRevision: acc.Revision, NextRefreshAt: current.started.Add(15 * time.Second).Unix()}
	if r.URL.Query().Get("summary") == "1" {
		result.Items = []cloud.Instance{}
	}
	a.inventoryMu.Unlock()
	reply(w, 200, result)
}
