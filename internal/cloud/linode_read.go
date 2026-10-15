package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

func linodeID(s string) error {
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 || strconv.Itoa(n) != s {
		return errors.New("Linode资源ID无效")
	}
	return nil
}
func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (v linInstance) instance() Instance {
	ips := append([]string{}, v.IPv4...)
	if v.IPv6 != "" {
		ips = append(ips, v.IPv6)
	}
	return Instance{ID: fmt.Sprint(v.ID), Name: v.Label, Region: v.Region, State: v.Status, Plan: v.Type, Image: v.Image, IPs: PublicIPs(ips), CPU: v.Specs.Vcpus, Memory: v.Specs.Memory, Disk: v.Specs.Disk / 1024, Architecture: "x86_64", DiskKind: "套餐磁盘"}
}
func (l *Linode) state(ctx context.Context, id string) (LinodeState, error) {
	s := LinodeState{}
	if e := linodeID(id); e != nil {
		return s, e
	}
	path := "/linode/instances/" + id
	var v struct {
		linInstance
		InterfaceGeneration string            `json:"interface_generation"`
		PlacementGroup      *struct{ ID int } `json:"placement_group"`
		Locks               []string          `json:"locks"`
		LKEClusterID        *int              `json:"lke_cluster_id"`
	}
	if e := l.call(ctx, "GET", path, nil, &v); e != nil {
		return s, e
	}
	if fmt.Sprint(v.ID) != id || v.Region == "" || v.Label == "" || v.Type == "" {
		return s, &Error{Code: "实例身份响应不完整"}
	}
	s.Instance = v.linInstance.instance()
	s.Generation = v.InterfaceGeneration
	s.Locks = v.Locks
	s.PlacementGroup = v.PlacementGroup != nil && v.PlacementGroup.ID > 0
	s.Managed = v.LKEClusterID != nil && *v.LKEClusterID > 0
	if s.Generation == "" {
		return s, &Error{Code: "网络接口代际未返回，不能安全判断防火墙绑定；请到控制台核实"}
	}
	if e := l.pages(ctx, path+"/disks", &s.Disks); e != nil {
		return s, e
	}
	if e := l.pages(ctx, path+"/configs", &s.Configs); e != nil {
		return s, e
	}
	if e := l.pages(ctx, path+"/volumes", &s.Volumes); e != nil {
		return s, e
	}
	s.Interfaces = []LinodeInterface{}
	if s.Generation == "linode" {
		if e := l.call(ctx, "GET", path+"/interfaces", nil, &struct {
			Interfaces *[]LinodeInterface `json:"interfaces"`
		}{&s.Interfaces}); e != nil {
			return s, e
		}
	}
	if s.Generation != "legacy_config" && s.Generation != "linode" {
		return s, &Error{Code: "网络接口类型尚不支持，未发送操作"}
	}
	// Monitor state, IPs and timestamps are not persisted in the identity digest.
	s.Snapshot = fingerprint(struct {
		ID, Name, Region, Plan, Image, Generation string
		Disks                                     []LinodeDisk
		Configs                                   []LinodeConfig
		Interfaces                                []LinodeInterface
		Locks                                     []string
		PlacementGroup, Managed                   bool
		Volumes                                   any
	}{id, v.Label, v.Region, v.Type, v.Image, s.Generation, s.Disks, s.Configs, s.Interfaces, s.Locks, s.PlacementGroup, s.Managed, s.Volumes})
	s.QueriedAt = time.Now().Unix()
	return s, nil
}
func (l *Linode) firewall(ctx context.Context, id string) (LinodeFirewall, error) {
	f := LinodeFirewall{}
	if e := linodeID(id); e != nil {
		return f, e
	}
	// Preserve the distinction between explicit null and an omitted direction.
	// A missing rules field must not be projected to an apparently empty list.
	wire := struct {
		*LinodeFirewall
		Rules json.RawMessage `json:"rules"`
	}{LinodeFirewall: &f}
	if e := l.call(ctx, "GET", "/networking/firewalls/"+id, nil, &wire); e != nil {
		return f, e
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(wire.Rules, &fields) != nil || len(fields["inbound"]) == 0 || len(fields["outbound"]) == 0 {
		return f, &Error{Code: "防火墙双向规则字段缺失，无法确认规则范围"}
	}
	if e := decode(wire.Rules, &f.Rules); e != nil {
		return f, e
	}
	if fmt.Sprint(f.ID) != id || f.Label == "" {
		return f, &Error{Code: "防火墙身份响应不完整"}
	}
	if e := l.pages(ctx, "/networking/firewalls/"+id+"/devices", &f.Devices); e != nil {
		return f, e
	}
	f.Snapshot = fingerprint(struct {
		ID            int
		Label, Status string
		Rules         FirewallRules
		Devices       []FirewallDevice
	}{f.ID, f.Label, f.Status, f.Rules, f.Devices})
	return f, nil
}
func (l *Linode) firewallList(ctx context.Context) (LinodeFirewallView, error) {
	out := LinodeFirewallView{QueriedAt: time.Now().Unix()}
	if e := l.pages(ctx, "/networking/firewalls", &out.Items); e != nil {
		return out, e
	}
	if len(out.Items) > 200 {
		return out, &Error{Code: "防火墙超过200个，请在云控制台管理"}
	}
	// Device sets are essential to explain shared-rule blast radius.
	for i := range out.Items {
		f, e := l.firewall(ctx, fmt.Sprint(out.Items[i].ID))
		if e != nil {
			return out, e
		}
		out.Items[i] = f
	}
	out.Snapshot = fingerprint(out.Items)
	return out, nil
}

// Presentation copies only. Keep the raw representation used for fingerprints,
// confirmation and cloud-write verification unchanged (nil and [] differ there).
func firewallDisplay(f LinodeFirewall) (LinodeFirewall, error) {
	bad := func() (LinodeFirewall, error) {
		return LinodeFirewall{}, &Error{Code: "防火墙规则或绑定响应不完整，无法确认影响范围"}
	}
	if f.ID < 1 || f.Label == "" || (f.Status != "enabled" && f.Status != "disabled") || f.Snapshot == "" {
		return bad()
	}
	if f.Devices == nil {
		f.Devices = []FirewallDevice{}
	}
	if f.Rules.Inbound == nil {
		f.Rules.Inbound = []FirewallRule{}
	}
	if f.Rules.Outbound == nil {
		f.Rules.Outbound = []FirewallRule{}
	}
	if ValidateFirewallRules(f.Rules) != nil {
		return bad()
	}
	seen := map[int]bool{}
	for _, d := range f.Devices {
		if d.ID < 1 || seen[d.ID] || d.Entity.ID < 1 || !contains([]string{"linode", "linode_interface", "nodebalancer"}, d.Entity.Type) {
			return bad()
		}
		seen[d.ID] = true
		if d.Entity.Type == "linode_interface" && (d.Entity.ParentEntity == nil || d.Entity.ParentEntity.ID < 1 || d.Entity.ParentEntity.Type != "linode") {
			return bad()
		}
	}
	return f, nil
}
func (l *Linode) LinodeRead(ctx context.Context, view, id, period string, page int) (any, error) {
	if page < 1 || page > 100 {
		return nil, errors.New("分页范围无效")
	}
	switch view {
	case "instance":
		return l.state(ctx, id)
	case "firewall":
		f, e := l.firewall(ctx, id)
		if e != nil {
			return nil, e
		}
		return firewallDisplay(f)
	case "firewalls":
		v, e := l.firewallList(ctx)
		if e != nil {
			return nil, e
		}
		if v.Items == nil {
			v.Items = []LinodeFirewall{}
		}
		for i := range v.Items {
			v.Items[i], e = firewallDisplay(v.Items[i])
			if e != nil {
				return nil, e
			}
		}
		return v, nil
	case "instance-firewalls":
		if e := linodeID(id); e != nil {
			return nil, e
		}
		var v []LinodeFirewall

		iid := 0
		if period != "" {
			var e error
			iid, e = strconv.Atoi(period)
			if e != nil || iid < 1 {
				return nil, errors.New("网络接口ID无效")
			}
			s, e := l.state(ctx, id)
			if e != nil {
				return nil, e
			}
			found := false
			for _, i := range s.Interfaces {
				if i.ID == iid && (i.Public != nil || i.VPC != nil) {
					found = true
				}
			}
			if !found {
				return nil, errors.New("接口不属于该实例或类型不支持防火墙")
			}
		}
		var err error
		v, err = l.attachedFirewalls(ctx, id, iid)
		if err != nil {
			return nil, err
		}
		snapshot := fingerprint(v)
		if v == nil {
			v = []LinodeFirewall{}
		}
		for _, f := range v {
			if f.ID < 1 || f.Label == "" || !contains([]string{"enabled", "disabled"}, f.Status) {
				return nil, &Error{Code: "防火墙绑定身份响应不完整"}
			}
		}
		return map[string]any{"items": v, "snapshot": snapshot}, nil
	case "permissions":
		return l.permissions(ctx), nil
	case "transfer":
		return l.transfer(ctx, id, period)
	case "stats":
		return l.stats(ctx, id, period)
	case "billing", "invoices", "invoice":
		return l.billing(ctx, view, id, page)
	}
	return nil, errors.New("不支持的Linode查询")
}
