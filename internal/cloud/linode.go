package cloud

import (
	"context"
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
		all = append(all, res.Data...)
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
			c.Regions = append(c.Regions, Choice{ID: r.ID, Name: r.Label, Extra: map[string]string{"siteType": r.Site_type}})
		}
	}
	if region == "" {
		return c, nil
	}
	if _, ok := Find(c.Regions, region); !ok {
		return c, &Error{Code: "地区不可用"}
	}
	var plans []struct {
		ID, Label           string
		Vcpus, Memory, Disk int
		Price               struct{ Monthly float64 }
		Class               string
	}
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
			c.Plans = append(c.Plans, Choice{ID: p.ID, Name: p.Label, Architecture: "x86_64", CPU: p.Vcpus, Memory: p.Memory, Disk: p.Disk / 1024, Price: p.Price.Monthly})
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
		c.Images = append(c.Images, Choice{ID: im.ID, Name: im.Label, Architecture: arch, Family: Family(im.Label), Disk: (im.Size + 1023) / 1024})
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
		out = append(out, Instance{ID: fmt.Sprint(v.ID), Name: v.Label, Region: v.Region, State: v.Status, IPs: ips, Plan: v.Type, Image: v.Image, Actions: Actions(v.Status)})
	}
	return out, nil
}
func (l *Linode) Create(ctx context.Context, s Spec, token string) (Operation, error) {
	if e := ValidateLinodePassword(s.Password); e != nil {
		return Operation{}, e
	}
	body := map[string]any{"label": s.Name, "region": s.Region, "type": s.Plan, "image": s.Image, "booted": true, "backups_enabled": s.Backups, "tags": []string{"narbe-" + token}}
	if s.Password != "" {
		body["root_pass"] = s.Password
	}
	if s.SSHPublicKey != "" {
		body["authorized_keys"] = []string{s.SSHPublicKey}
	}
	if s.UserData != "" {
		return Operation{}, &Error{Code: "此适配器不接受初始化脚本，请使用 SSH 配置"}
	}
	var res linInstance
	e := l.call(ctx, "POST", "/linode/instances", body, &res)
	if e != nil {
		return Operation{}, e
	}
	if res.ID == 0 {
		return Operation{}, &Error{Code: "未返回实例标识", Uncertain: true}
	}
	return Operation{ResourceID: fmt.Sprint(res.ID), Region: s.Region, Expected: "running"}, nil
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
