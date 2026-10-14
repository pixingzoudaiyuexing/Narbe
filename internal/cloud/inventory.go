package cloud

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const InventoryLimit = 5000

type RegionFailure struct {
	Region     string `json:"region"`
	Message    string `json:"message"`
	RegionName string `json:"regionName,omitempty"`
}
type InventoryStats struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Stopped int `json:"stopped"`
	Other   int `json:"other"`
}
type Inventory struct {
	Items     []Instance      `json:"items"`
	Regions   []Choice        `json:"regions"`
	Failures  []RegionFailure `json:"failures"`
	Succeeded []string        `json:"succeeded"`
	Warnings  []string        `json:"warnings"`
	Phase     string          `json:"phase"`
	Scope     string          `json:"scope"`
	Complete  bool            `json:"complete"`
	StartedAt int64           `json:"startedAt"`
	UpdatedAt int64           `json:"updatedAt"`
	Stats     InventoryStats  `json:"stats"`
}

func StateGroup(state string) string {
	switch state {
	case "running", "active", "on":
		return "running"
	case "stopped", "off", "offline", "deallocated":
		return "stopped"
	default:
		return "other"
	}
}
func ReadError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "查询超时或已取消，请重试"
	}
	var ce *Error
	if errors.As(err, &ce) {
		switch {
		case ce.Status == 401 || ce.Status == 403:
			return "云账号权限不足或凭据已失效，请检查云控制台"
		case ce.Status == 429 || ce.Code == "RequestLimitExceeded" || ce.Code == "Throttling":
			return "云厂商限制查询频率，请稍后重试"
		case ce.Status >= 500:
			return "云厂商服务暂不可用，请稍后重试"
		}
	}
	return "云端查询未完成，请检查权限、网络或稍后重试"
}
func EmptyInventory(scope string) Inventory {
	return Inventory{Items: []Instance{}, Regions: []Choice{}, Failures: []RegionFailure{}, Succeeded: []string{}, Warnings: []string{}, Phase: "loading", Scope: scope, StartedAt: time.Now().Unix()}
}
func (v *Inventory) summarize(provider string) {
	v.Stats = InventoryStats{}
	names := map[string]string{}
	for _, r := range v.Regions {
		names[r.ID] = r.Name
	}
	seen := map[[2]string]bool{}
	items := make([]Instance, 0, len(v.Items))
	for _, i := range v.Items {
		key := [2]string{i.Region, i.ID}
		if i.ID == "" || i.Region == "" || seen[key] {
			v.Failures = append(v.Failures, RegionFailure{Region: "*", Message: "资源身份缺失或重复，数量暂未确认"})
			continue
		}
		seen[key] = true
		if len(items) >= InventoryLimit {
			v.Failures = append(v.Failures, RegionFailure{Region: "*", Message: "资源数量超过本次查询上限，数量暂未确认"})
			break
		}
		if name := names[i.Region]; name != "" {
			i.RegionName = name
		} else {
			i.RegionName = RegionLabel(provider, i.Region, "")
		}
		// Provider APIs may include RFC1918 addresses; only public addresses belong in IPs.
		i.IPs = PublicIPs(i.IPs)
		items = append(items, i)
		switch StateGroup(i.State) {
		case "running":
			v.Stats.Running++
		case "stopped":
			v.Stats.Stopped++
		default:
			v.Stats.Other++
		}
	}
	v.Items = items
	v.Stats.Total = len(items)
	// Retry history and multiple malformed rows may refer to the same scope.
	// Count failed scopes, not accumulated error events, retaining the newest reason.
	failures := []RegionFailure{}
	positions := map[string]int{}
	for _, f := range v.Failures {
		if f.Region != "*" {
			if name := names[f.Region]; name != "" {
				f.RegionName = name
			} else {
				f.RegionName = RegionLabel(provider, f.Region, "")
			}
		}
		if pos, exists := positions[f.Region]; exists {
			failures[pos] = f
		} else {
			positions[f.Region] = len(failures)
			failures = append(failures, f)
		}
	}
	v.Failures = failures
	sort.Slice(v.Items, func(i, j int) bool {
		a, b := v.Items[i], v.Items[j]
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		return a.ID < b.ID
	})
	v.Complete = v.Phase == "ready" && len(v.Failures) == 0
}

// ScanInventory is read-only. Write validation continues using fresh provider
// Catalog/Instances calls and never consumes this display cache.
func ScanInventory(ctx context.Context, p Provider, provider, retry string, previous Inventory, report func(Inventory)) Inventory {
	if a, ok := p.(*AWS); ok {
		return a.scanInventory(ctx, retry, previous, report)
	}
	if a, ok := p.(*Azure); ok {
		return a.scanInventory(ctx, retry, previous, report)
	}
	scope := "当前账号 · 全部地区"
	if provider == "azure" {
		scope = "当前接入资源组 · 全部地区（非整个订阅）"
	}
	v := EmptyInventory(scope)
	c, ce := p.Catalog(ctx, "", "")
	if ce == nil {
		v.Regions = c.Regions
		LocalizeRegions(provider, v.Regions)
	} else {
		v.Warnings = append(v.Warnings, "地区目录暂不可用，未确认的位置保留代码")
	}
	report(v)
	items, e := p.Instances(ctx, "")
	if e != nil {
		v.Failures = append(v.Failures, RegionFailure{Region: "*", Message: ReadError(e)})
	} else {
		v.Items = items
		v.Succeeded = []string{"*"}
		v.summarize(provider)
		report(v)
		if l, ok := p.(*Linode); ok && len(items) > 0 {
			meta, cancel := context.WithTimeout(ctx, 8*time.Second)
			var types []linType
			e := l.pages(meta, "/linode/types", &types)
			cancel()
			if e != nil {
				v.Warnings = append(v.Warnings, "部分规格或价格未确认，实例数量不受影响")
			} else {
				plans := map[string]linType{}
				for _, plan := range types {
					plans[plan.ID] = plan
				}
				for j := range v.Items {
					if plan, found := plans[v.Items[j].Plan]; found {
						choice := plan.choice(v.Items[j].Region)
						v.Items[j].Pricing = choice.Pricing
						if v.Items[j].CPU == 0 {
							v.Items[j].CPU = choice.CPU
						}
						if v.Items[j].Memory == 0 {
							v.Items[j].Memory = choice.Memory
						}
					}
				}
			}
		}
	}
	v.Phase = "ready"
	v.UpdatedAt = time.Now().Unix()
	v.summarize(provider)
	report(v)
	return v
}

func retryRead(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		var ce *Error
		if !errors.As(err, &ce) || !(ce.Status == 429 || ce.Status >= 500 || ce.Code == "RequestLimitExceeded" || strings.HasPrefix(ce.Code, "Throttl")) {
			return err
		}
		delay := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			delay.Stop()
			return ctx.Err()
		case <-delay.C:
		}
	}
	return err
}

func (a *AWS) scanInventory(ctx context.Context, retry string, previous Inventory, report func(Inventory)) Inventory {
	v := EmptyInventory("当前账号 · 已启用且可访问的 EC2 地区")
	c, e := a.Catalog(ctx, "", "")
	if e != nil {
		v.Failures = append(v.Failures, RegionFailure{Region: "*", Message: ReadError(e)})
		v.Phase = "ready"
		v.summarize("aws")
		report(v)
		return v
	}
	v.Regions = c.Regions
	LocalizeRegions("aws", v.Regions)
	work := []Choice{}
	if retry != "" && retry != "*" {
		if previous.StartedAt > 0 {
			v.StartedAt = previous.StartedAt
		}
		v.Warnings = append(v.Warnings, "仅重试失败地区；其他地区沿用本次扫描先前的成功结果")
		v.Items = append([]Instance{}, previous.Items...)
		v.Succeeded = append([]string{}, previous.Succeeded...)
		v.Failures = append([]RegionFailure{}, previous.Failures...)
		if r, ok := Find(v.Regions, retry); ok {
			work = append(work, r)
		} else {
			v.Failures = append(v.Failures, RegionFailure{Region: retry, Message: "地区已停用或无法确认，请刷新全部地区"})
		}
	} else {
		work = append(work, v.Regions...)
	}
	if len(work) > 100 {
		work = work[:100]
		v.Failures = append(v.Failures, RegionFailure{Region: "*", Message: "地区数量超过安全上限，总数未确认"})
	}
	type result struct {
		region  string
		items   []Instance
		err     error
		warning bool
	}
	tasks := make(chan Choice, len(work))
	// Backpressure keeps at most one completed result per worker in memory.
	// A buffered channel sized by all regions could retain every large response.
	results := make(chan result)
	for _, r := range work {
		tasks <- r
	}
	close(tasks)
	for worker := 0; worker < 3; worker++ {
		go func() {
			for r := range tasks {
				sub, cancel := context.WithTimeout(ctx, 15*time.Second)
				var items []Instance
				err := retryRead(sub, func() error { var e error; items, e = a.Instances(sub, r.ID); return e })
				warn := false
				if err == nil && len(items) > 0 {
					warn = a.enrichInstances(sub, r.ID, items) != nil
				}
				cancel()
				results <- result{r.ID, items, err, warn}
			}
		}()
	}
	report(v)
	for range work {
		r := <-results
		failures := v.Failures[:0]
		for _, f := range v.Failures {
			if f.Region != r.region {
				failures = append(failures, f)
			}
		}
		v.Failures = failures
		if r.err != nil {
			v.Failures = append(v.Failures, RegionFailure{Region: r.region, Message: ReadError(r.err)})
		} else {
			kept := v.Items[:0]
			for _, i := range v.Items {
				if i.Region != r.region {
					kept = append(kept, i)
				}
			}
			v.Items = append(kept, r.items...)
			if !contains(v.Succeeded, r.region) {
				v.Succeeded = append(v.Succeeded, r.region)
			}
		}
		if r.warning {
			v.Warnings = append(v.Warnings, RegionLabel("aws", r.region, "")+"：部分配置未确认，实例数量不受影响")
		}
		v.summarize("aws")
		report(v)
	}
	v.Phase = "ready"
	v.UpdatedAt = time.Now().Unix()
	v.summarize("aws")
	report(v)
	return v
}
