package cloud

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type DigitalOcean struct{ rest }
type doRegion struct {
	Slug, Name string
	Available  bool
	Sizes      []string
}
type doSize struct {
	Slug                string
	Vcpus, Memory, Disk int
	Price_monthly       float64
	Price_hourly        float64
	Available           bool
	Regions             []string
}
type doImage struct {
	ID                                             int
	Name, Slug, Distribution, Status, Architecture string
	Regions                                        []string
	Public                                         bool
	Min_disk_size                                  int
}
type doDroplet struct {
	ID                        int
	Name, Status, RegionDummy string
	Region                    doRegion
	Size                      doSize
	Image                     doImage
	Networks                  struct {
		V4 []struct{ Ip_address, Type string }
		V6 []struct{ Ip_address, Type string }
	}
}

func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}
func (d *DigitalOcean) pages(ctx context.Context, path, field string, out any) error {
	var all []jsonRaw
	for page := 1; page <= 100; page++ {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		var data map[string]jsonRaw
		if e := d.call(ctx, "GET", fmt.Sprintf("%s%sper_page=200&page=%d", path, sep, page), nil, &data); e != nil {
			return e
		}
		var batch []jsonRaw
		if e := decode(data[field], &batch); e != nil {
			return e
		}
		if batch == nil {
			return &Error{Code: "DigitalOcean 列表响应缺少数据，不能确认资源数量"}
		}
		all = append(all, batch...)
		if field == "droplets" && len(all) > InventoryLimit {
			return &Error{Code: "DigitalOcean 实例数量超限，不能确认总数"}
		}
		if len(batch) < 200 {
			return decode(marshal(all), out)
		}
	}
	return &Error{Code: "分页超过安全上限，请缩小查询范围"}
}
func (d *DigitalOcean) Catalog(ctx context.Context, region, plan string) (Catalog, error) {
	c := Catalog{}
	var regions []doRegion
	if e := d.pages(ctx, "/regions", "regions", &regions); e != nil {
		return c, e
	}
	for _, r := range regions {
		if r.Available {
			c.Regions = append(c.Regions, Choice{ID: r.Slug, Name: r.Name})
		}
	}
	if region == "" {
		return c, nil
	}
	ridx := -1
	for i := range regions {
		if regions[i].Slug == region && regions[i].Available {
			ridx = i
		}
	}
	if ridx < 0 {
		return c, &Error{Code: "地区不可用"}
	}
	var sizes []doSize
	if e := d.pages(ctx, "/sizes", "sizes", &sizes); e != nil {
		return c, e
	}
	for _, s := range sizes {
		if s.Available && contains(s.Regions, region) && contains(regions[ridx].Sizes, s.Slug) {
			c.Plans = append(c.Plans, Choice{ID: s.Slug, Name: s.Slug, CPU: s.Vcpus, Memory: s.Memory, Disk: s.Disk, DiskKind: "套餐磁盘", Price: s.Price_monthly, Pricing: advertisedPrice(s.Price_monthly, s.Price_hourly, "DigitalOcean sizes API"), Architecture: "x86_64"})
		}
	}
	var images []doImage
	if e := d.pages(ctx, "/images?type=distribution", "images", &images); e != nil {
		return c, e
	}
	for _, im := range images {
		if im.Status == "available" && contains(im.Regions, region) {
			c.Images = append(c.Images, Choice{ID: strconv.Itoa(im.ID), Name: im.Name, Family: Family(im.Distribution + im.Name), Architecture: Arch(im.Architecture), Disk: im.Min_disk_size})
		}
	}
	var keys []struct {
		ID                int
		Name, Fingerprint string
	}
	if e := d.pages(ctx, "/account/keys", "ssh_keys", &keys); e != nil {
		return c, e
	}
	for _, k := range keys {
		c.Keys = append(c.Keys, Choice{ID: strconv.Itoa(k.ID), Name: k.Name})
	}
	return c, nil
}
func (d *DigitalOcean) Instances(ctx context.Context, region string) ([]Instance, error) {
	var droplets []doDroplet
	if e := d.pages(ctx, "/droplets", "droplets", &droplets); e != nil {
		return nil, e
	}
	out := []Instance{}
	for _, v := range droplets {
		if region != "" && v.Region.Slug != region {
			continue
		}
		i := Instance{ID: strconv.Itoa(v.ID), Name: v.Name, Region: v.Region.Slug, State: v.Status, Plan: v.Size.Slug, Image: v.Image.Name, Actions: Actions(v.Status), IPs: []string{}, CPU: v.Size.Vcpus, Memory: v.Size.Memory, Disk: v.Size.Disk, DiskKind: "套餐磁盘", Pricing: advertisedPrice(v.Size.Price_monthly, v.Size.Price_hourly, "DigitalOcean Droplet size API")}
		for _, n := range v.Networks.V4 {
			if n.Type == "public" {
				i.IPs = append(i.IPs, n.Ip_address)
			}
		}
		for _, n := range v.Networks.V6 {
			if n.Type == "public" {
				i.IPs = append(i.IPs, n.Ip_address)
			}
		}
		out = append(out, i)
	}
	return out, nil
}
func (d *DigitalOcean) Create(ctx context.Context, s Spec, token string) (Operation, error) {
	body := map[string]any{"name": s.Name, "region": s.Region, "size": s.Plan, "image": mustInt(s.Image), "ipv6": s.IPv6, "backups": s.Backups, "tags": []string{"narbe-" + token}, "user_data": s.UserData}
	if s.Key != "" {
		body["ssh_keys"] = []int{mustInt(s.Key)}
	}
	var res struct{ Droplet doDroplet }
	e := d.call(ctx, "POST", "/droplets", body, &res)
	if e != nil {
		return Operation{}, e
	}
	if res.Droplet.ID == 0 {
		return Operation{}, &Error{Code: "未返回实例标识", Uncertain: true}
	}
	return Operation{ResourceID: strconv.Itoa(res.Droplet.ID), Region: s.Region, Expected: "active"}, nil
}
func (d *DigitalOcean) Action(ctx context.Context, i Instance, action, token string) (Operation, error) {
	op := Operation{ResourceID: i.ID, Region: i.Region}
	if action == "delete" {
		op.Expected = "deleted"
		return op, d.call(ctx, "DELETE", "/droplets/"+Escape(i.ID), nil, nil)
	}
	a := map[string]string{"start": "power_on", "stop": "shutdown", "reboot": "reboot"}[action]
	if a == "" {
		return op, &Error{Code: "不支持的操作"}
	}
	var res struct{ Action struct{ ID int } }
	e := d.call(ctx, "POST", "/droplets/"+Escape(i.ID)+"/actions", map[string]string{"type": a}, &res)
	if e == nil && res.Action.ID <= 0 {
		return op, &Error{Code: "未返回有效操作标识", Uncertain: true}
	}
	op.ActionID = strconv.Itoa(res.Action.ID)
	op.Expected = "active"
	if action == "stop" {
		op.Expected = "off"
	}
	return op, e
}
func (d *DigitalOcean) Poll(ctx context.Context, op Operation) (bool, error) {
	if op.ActionID != "" && op.ActionID != "0" {
		var res struct{ Action struct{ Status string } }
		if e := d.call(ctx, "GET", "/actions/"+url.PathEscape(op.ActionID), nil, &res); e != nil {
			return false, e
		}
		if res.Action.Status == "errored" {
			return false, &Error{Code: "云端操作失败"}
		}
		if res.Action.Status != "completed" {
			return false, nil
		}
	}
	var res struct{ Droplet doDroplet }
	e := d.call(ctx, "GET", "/droplets/"+Escape(op.ResourceID), nil, &res)
	if e != nil {
		if ce, ok := e.(*Error); ok && ce.Status == 404 && op.Expected == "deleted" {
			return true, nil
		}
		return false, e
	}
	return res.Droplet.Status == op.Expected, nil
}
func mustInt(s string) int { i, _ := strconv.Atoi(s); return i }
