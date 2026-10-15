package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

type Linode struct{ rest }
type linInstance struct {
	ID                                 int
	Label, Region, Status, Type, Image string
	IPv4                               []string
	IPv6                               string
	Specs                              struct{ Vcpus, Memory, Disk int }
}
type linType struct {
	ID, Label           string
	Vcpus, Memory, Disk int
	Class               string
	Price               struct{ Monthly, Hourly float64 }
	Region_prices       []struct {
		ID              string
		Monthly, Hourly float64
	}
}

func (p linType) choice(region string) Choice {
	monthly, hourly := p.Price.Monthly, p.Price.Hourly
	for _, r := range p.Region_prices {
		if r.ID == region {
			monthly, hourly = r.Monthly, r.Hourly
			break
		}
	}
	return Choice{ID: p.ID, Name: p.Label, Architecture: "x86_64", CPU: p.Vcpus, Memory: p.Memory, Disk: p.Disk / 1024, DiskKind: "套餐磁盘", Price: monthly, Pricing: advertisedPrice(monthly, hourly, "Linode types API（含地区价格）")}
}

func (l *Linode) pages(ctx context.Context, path string, out any) error {
	var all []jsonRaw
	for page := 1; page <= 100; page++ {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		var res struct {
			Data  []jsonRaw
			Pages int
		}
		if e := l.call(ctx, "GET", fmt.Sprintf("%s%spage_size=500&page=%d", path, sep, page), nil, &res); e != nil {
			return e
		}
		if res.Data == nil || res.Pages < 1 {
			return &Error{Code: "Linode 分页响应无效，不能确认资源数量"}
		}
		all = append(all, res.Data...)
		if len(all) > 5000 {
			return &Error{Code: "Linode查询超过安全上限，请缩小查询范围"}
		}
		if path == "/linode/instances" && len(all) > InventoryLimit {
			return &Error{Code: "Linode 实例数量超限，不能确认总数"}
		}
		if page >= res.Pages {
			return decode(marshal(all), out)
		}
	}
	return &Error{Code: "分页超过安全上限"}
}
func (l *Linode) Catalog(ctx context.Context, region, plan string) (Catalog, error) {
	c := Catalog{}
	var regions []struct {
		ID, Label, Status, Site_type string
		Capabilities                 []string
	}
	if e := l.pages(ctx, "/regions", &regions); e != nil {
		return c, e
	}
	for _, r := range regions {
		if r.Status == "ok" && contains(r.Capabilities, "Linodes") {
			c.Regions = append(c.Regions, Choice{ID: r.ID, Name: r.Label, Extra: map[string]string{"siteType": r.Site_type, "linodeInterfaces": fmt.Sprint(contains(r.Capabilities, "Linode Interfaces")), "metadata": fmt.Sprint(contains(r.Capabilities, "Metadata"))}})
		}
	}
	if region == "" {
		return c, nil
	}
	if _, ok := Find(c.Regions, region); !ok {
		return c, &Error{Code: "地区不可用"}
	}
	var plans []linType
	if e := l.pages(ctx, "/linode/types", &plans); e != nil {
		return c, e
	}
	var available []struct {
		Region, Plan string
		Available    bool
	}
	if e := l.call(ctx, "GET", "/regions/"+Escape(region)+"/availability", nil, &available); e != nil {
		return c, e
	}
	for _, p := range plans {
		ok := false
		for _, a := range available {
			if a.Plan == p.ID && a.Available {
				ok = true
			}
		}
		if ok {
			c.Plans = append(c.Plans, p.choice(region))
		}
	}
	var images []struct {
		ID, Label, Status, Architecture string
		Deprecated, Is_public           bool
		Eol                             string
		Expiry                          string
		Size                            int
		Capabilities                    []string
	}
	if e := l.pages(ctx, "/images", &images); e != nil {
		return c, e
	}
	r, _ := Find(c.Regions, region)
	for _, im := range images {
		if im.Status != "available" || im.Deprecated || (expired(im.Eol) || expired(im.Expiry)) {
			continue
		}
		if r.Extra["siteType"] == "distributed" && !contains(im.Capabilities, "distributed-sites") {
			continue
		}
		arch := Arch(im.Architecture)
		if arch == "" {
			arch = "x86_64"
		}
		c.Images = append(c.Images, Choice{ID: im.ID, Name: im.Label, Architecture: arch, Family: Family(im.Label), Disk: (im.Size + 1023) / 1024, StartupSupport: func() string {
			if contains(im.Capabilities, "cloud-init") {
				return "cloud-init"
			}
			return "unsupported"
		}()})
	}
	var keys []struct {
		ID             int
		Label, Ssh_key string
	}
	if e := l.pages(ctx, "/profile/sshkeys", &keys); e != nil {
		return c, e
	}
	for _, k := range keys {
		c.Keys = append(c.Keys, Choice{ID: fmt.Sprint(k.ID), Name: k.Label, Extra: map[string]string{"publicKey": k.Ssh_key}})
	}
	return c, nil
}
func (l *Linode) Instances(ctx context.Context, region string) ([]Instance, error) {
	var data []linInstance
	if e := l.pages(ctx, "/linode/instances", &data); e != nil {
		return nil, e
	}
	out := []Instance{}
	for _, v := range data {
		if region != "" && region != v.Region {
			continue
		}
		ips := append([]string{}, v.IPv4...)
		if v.IPv6 != "" {
			ips = append(ips, v.IPv6)
		}
		out = append(out, Instance{ID: fmt.Sprint(v.ID), Name: v.Label, Region: v.Region, State: v.Status, IPs: PublicIPs(ips), Plan: v.Type, Image: v.Image, Actions: Actions(v.Status), CPU: v.Specs.Vcpus, Memory: v.Specs.Memory, Disk: v.Specs.Disk / 1024, DiskKind: "套餐磁盘"})
	}
	return out, nil
}
func (l *Linode) Create(ctx context.Context, s Spec, token string) (Operation, error) {
	if e := ValidateLinodePassword(s.Password); e != nil {
		return Operation{}, e
	}
	body := map[string]any{"label": s.Name, "region": s.Region, "type": s.Plan, "image": s.Image, "booted": true, "interface_generation": "legacy_config", "firewall_id": -1, "backups_enabled": s.Backups, "tags": []string{"narbe-" + token}}
	if s.Password != "" {
		body["root_pass"] = s.Password
	}
	if s.SSHPublicKey != "" {
		body["authorized_keys"] = []string{s.SSHPublicKey}
	}
	if e := ValidateStartupText("linode", s); e != nil {
		return Operation{}, e
	}
	if s.UserData != "" || s.ScriptMode == "stackscript" {
		c, e := l.Catalog(ctx, s.Region, s.Plan)
		if e != nil {
			return Operation{}, e
		}
		if e = ValidateStartup("linode", s, c); e != nil {
			return Operation{}, e
		}
		if s.ScriptMode == "stackscript" {
			if e = l.ValidateStackScript(ctx, s); e != nil {
				return Operation{}, e
			}
			body["stackscript_id"] = s.StackScriptID
			params := s.StackScriptData
			if params == nil {
				params = map[string]string{}
			}
			body["stackscript_data"] = params
		} else {
			body["metadata"] = map[string]string{"user_data": base64.StdEncoding.EncodeToString([]byte(s.UserData))}
		}
	}
	op := Operation{Region: s.Region, Expected: "running", Linode: &LinodeOperation{Action: "create", Firewall: s.Firewall}}
	if s.Firewall != nil && s.Firewall.Mode != "all" {
		if e := l.ValidateFirewallSelection(ctx, *s.Firewall); e != nil {
			return op, e
		}
		fid, e := l.selectionID(ctx, s.Firewall, token, &op)
		if e != nil {
			return op, e
		}
		body["firewall_id"] = fid
		f, e := l.firewall(ctx, fmt.Sprint(fid))
		if e != nil {
			if len(op.Owned) > 0 && !Uncertain(e) {
				e = &Error{Code: "防火墙已创建但回读未完成，请核实关联资源", Uncertain: true}
			}
			return op, e
		}
		op.Linode.Firewall = &FirewallSelection{Mode: "existing", ID: fid, Rules: &f.Rules}
	}
	var res linInstance
	e := l.call(ctx, "POST", "/linode/instances", body, &res)
	if e != nil {
		if len(op.Owned) > 0 && !Uncertain(e) {
			e = &Error{Code: "防火墙已创建但实例创建未完成，请核实账本关联资源", Uncertain: true}
		}
		return op, e
	}
	if res.ID == 0 {
		return op, &Error{Code: "未返回实例标识", Uncertain: true}
	}
	op.ResourceID = fmt.Sprint(res.ID)
	op.Owned = append(op.Owned, "linode:"+op.ResourceID)
	if e := recordOperation(ctx, op); e != nil {
		return op, e
	}
	return op, nil
}
func (l *Linode) Action(ctx context.Context, i Instance, action, token string) (Operation, error) {
	op := Operation{ResourceID: i.ID, Region: i.Region, Expected: "running"}
	if action == "delete" {
		op.Expected = "deleted"
		return op, l.call(ctx, "DELETE", "/linode/instances/"+Escape(i.ID), nil, nil)
	}
	a := map[string]string{"start": "boot", "stop": "shutdown", "reboot": "reboot"}[action]
	if a == "" {
		return op, &Error{Code: "不支持的操作"}
	}
	if action == "stop" {
		op.Expected = "offline"
	}
	return op, l.call(ctx, "POST", "/linode/instances/"+Escape(i.ID)+"/"+a, map[string]any{}, nil)
}
func (l *Linode) Poll(ctx context.Context, op Operation) (bool, error) {
	if op.Linode != nil {
		return l.pollAdvanced(ctx, op)
	}
	var res linInstance
	e := l.call(ctx, "GET", "/linode/instances/"+Escape(op.ResourceID), nil, &res)
	if e != nil {
		if ce, ok := e.(*Error); ok && ce.Status == 404 && op.Expected == "deleted" {
			return true, nil
		}
		return false, e
	}
	return res.Status == op.Expected, nil
}

func expired(s string) bool {
	if s == "" {
		return false
	}
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		t, e = time.ParseInLocation("2006-01-02T15:04:05", s, time.UTC)
	}
	return e != nil || t.Before(time.Now())
}
