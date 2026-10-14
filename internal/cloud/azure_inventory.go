package cloud

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type azureSKU struct {
	Name, ResourceType string
	Locations          []string
	Capabilities       []struct{ Name, Value string }
	Restrictions       []jsonRaw
}

func azureSKUChoice(s azureSKU) Choice {
	p := Choice{ID: s.Name, Name: s.Name, Extra: map[string]string{}}
	for _, c := range s.Capabilities {
		switch c.Name {
		case "vCPUs":
			p.CPU = mustInt(c.Value)
		case "MemoryGB":
			p.Memory = int(mustFloat(c.Value) * 1024)
		case "CpuArchitectureType":
			p.Architecture = Arch(c.Value)
		case "HyperVGenerations":
			p.Extra["hyperV"] = c.Value
		}
	}
	return p
}
func mustFloat(s string) float64 { n, _ := strconv.ParseFloat(s, 64); return n }

func (a *Azure) vmInGroup(id string) bool {
	prefix := a.group() + "/providers/Microsoft.Compute/virtualMachines/"
	if !strings.HasPrefix(strings.ToLower(id), strings.ToLower(prefix)) {
		return false
	}
	leaf, e := url.PathUnescape(id[len(prefix):])
	return e == nil && leaf != "" && leaf != "." && leaf != ".." && !strings.ContainsAny(leaf, "/\\?#")
}
func (a *Azure) scanInventory(ctx context.Context, retry string, previous Inventory, report func(Inventory)) Inventory {
	v := EmptyInventory("当前接入资源组 · 全部地区（非整个订阅）")
	c, ce := a.Catalog(ctx, "", "")
	if ce == nil {
		v.Regions = c.Regions
		LocalizeRegions("azure", v.Regions)
	} else {
		v.Warnings = append(v.Warnings, "地区目录未确认，保留原始地区代码")
	}
	var raw []azVM
	if e := a.list(ctx, a.group()+"/providers/Microsoft.Compute/virtualMachines?api-version=2024-07-01", &raw); e != nil {
		v.Failures = append(v.Failures, RegionFailure{Region: "*", Message: ReadError(e)})
		v.Phase = "ready"
		v.summarize("azure")
		report(v)
		return v
	}
	grouped := map[string][]azVM{}
	for _, vm := range raw {
		if !a.vmInGroup(vm.ID) || vm.Location == "" {
			v.Failures = append(v.Failures, RegionFailure{Region: "*", Message: "返回的虚拟机身份或资源组范围无法确认"})
			continue
		}
		grouped[vm.Location] = append(grouped[vm.Location], vm)
	}
	if retry != "" && retry != "*" {
		if previous.StartedAt > 0 {
			v.StartedAt = previous.StartedAt
		}
		v.Warnings = append(v.Warnings, "仅重试失败地区；其他地区沿用本次扫描先前的成功结果")
		v.Items = append([]Instance{}, previous.Items...)
		v.Failures = append(v.Failures, previous.Failures...)
		v.Succeeded = append([]string{}, previous.Succeeded...)
		for region := range grouped {
			if region != retry {
				delete(grouped, region)
			}
		}
		if _, ok := grouped[retry]; !ok {
			grouped[retry] = nil
		}
	}
	type result struct {
		region  string
		items   []Instance
		err     error
		warning bool
	}
	tasks := make(chan string, len(grouped))
	results := make(chan result)
	for region := range grouped {
		tasks <- region
	}
	close(tasks)
	for n := 0; n < 3; n++ {
		go func() {
			for region := range tasks {
				sub, cancel := context.WithTimeout(ctx, 20*time.Second)
				items := []Instance{}
				var err error
				for _, vm := range grouped[region] {
					if len(items) >= InventoryLimit {
						err = &Error{Code: "资源数量超限"}
						break
					}
					var i Instance
					i, err = a.hydrateVM(sub, vm)
					if err != nil {
						break
					}
					items = append(items, i)
				}
				warn := false
				if err == nil && len(items) > 0 {
					var skus []azureSKU
					e := a.list(sub, a.prefix()+"/providers/Microsoft.Compute/skus?api-version=2021-07-01&$filter="+url.QueryEscape("location eq '"+region+"'"), &skus)
					if e != nil {
						warn = true
					} else {
						types := map[string]Choice{}
						for _, s := range skus {
							if s.ResourceType == "virtualMachines" && contains(s.Locations, region) {
								types[s.Name] = azureSKUChoice(s)
							}
						}
						for j := range items {
							if p, ok := types[items[j].Plan]; ok {
								items[j].CPU = p.CPU
								items[j].Memory = p.Memory
								items[j].Architecture = p.Architecture
							} else {
								warn = true
							}
						}
					}
				}
				cancel()
				results <- result{region, items, err, warn}
			}
		}()
	}
	report(v)
	for range grouped {
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
			v.Warnings = append(v.Warnings, RegionLabel("azure", r.region, "")+"：部分配置未确认，实例数量不受影响")
		}
		v.summarize("azure")
		report(v)
	}
	v.Phase = "ready"
	v.UpdatedAt = time.Now().Unix()
	v.summarize("azure")
	report(v)
	return v
}
