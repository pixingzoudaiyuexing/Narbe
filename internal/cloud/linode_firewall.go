package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

func OpenFirewallRules() FirewallRules {
	return FirewallRules{Inbound: []FirewallRule{}, Outbound: []FirewallRule{}, InboundPolicy: "ACCEPT", OutboundPolicy: "ACCEPT"}
}
func ValidateFirewallRules(r FirewallRules) error {
	if (r.InboundPolicy != "ACCEPT" && r.InboundPolicy != "DROP") || (r.OutboundPolicy != "ACCEPT" && r.OutboundPolicy != "DROP") {
		return errors.New("防火墙默认策略必须为ACCEPT或DROP")
	}
	if r.Inbound == nil || r.Outbound == nil || len(r.Inbound)+len(r.Outbound) > 25 {
		return errors.New("防火墙需要完整双向规则列表，合计最多25条")
	}
	for _, v := range append(append([]FirewallRule{}, r.Inbound...), r.Outbound...) {
		if v.Action != "ACCEPT" && v.Action != "DROP" {
			return errors.New("规则操作必须为允许或拒绝")
		}
		if !contains([]string{"TCP", "UDP", "ICMP", "IPENCAP"}, v.Protocol) {
			return errors.New("协议仅支持TCP、UDP、ICMP或IPENCAP")
		}
		if v.Label != "" && (len(v.Label) < 3 || len(v.Label) > 32) {
			return errors.New("规则名称过长")
		}
		if v.Protocol != "TCP" && v.Protocol != "UDP" && v.Ports != "" {
			return errors.New("ICMP/IPENCAP规则不能指定端口")
		}
		pieces := 0
		if v.Ports != "" {
			for index, part := range strings.Split(v.Ports, ",") {
				if index > 0 {
					part = strings.TrimPrefix(part, " ")
				}
				bounds := strings.Split(part, "-")
				if len(bounds) > 2 {
					return errors.New("端口格式无效")
				}
				last := 0
				for _, s := range bounds {
					n, e := strconv.Atoi(s)
					if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != s || n <= last {
						return errors.New("端口应为1–65535，无前导零，范围末端大于起点")
					}
					last = n
					pieces++
				}
			}
		}
		if pieces > 15 {
			return errors.New("每条规则端口最多15个片段，范围占2个")
		}
		if len(v.Addresses.IPv4)+len(v.Addresses.IPv6) < 1 || len(v.Addresses.IPv4)+len(v.Addresses.IPv6) > 255 {
			return errors.New("规则需要1–255个IPv4或IPv6 CIDR")
		}
		for _, list := range []struct {
			values []string
			v4     bool
		}{{v.Addresses.IPv4, true}, {v.Addresses.IPv6, false}} {
			for _, ip := range list.values {
				p, e := netip.ParsePrefix(ip)
				if e != nil || p.Addr().Is4() != list.v4 || p.Addr().Is4In6() || p.Addr().Zone() != "" {
					return errors.New("IPv4/IPv6地址类型或CIDR格式不符")
				}
			}
		}
	}
	return nil
}
func (l *Linode) ValidateFirewallSelection(ctx context.Context, s FirewallSelection) error {
	switch s.Mode {
	case "all":
		if s.ID != 0 || s.Rules != nil {
			return errors.New("全部放行不能附带已有ID或限制规则")
		}
	case "existing":
		if s.ID < 1 || s.Rules != nil || s.AllowUnverified {
			return errors.New("已有防火墙参数无效")
		}
		f, e := l.firewall(ctx, fmt.Sprint(s.ID))
		if e != nil {
			return e
		}
		if s.Snapshot != "" && s.Snapshot != f.Snapshot {
			return errors.New("已有防火墙规则或影响范围已变化，请重新确认")
		}
		if f.Status != "enabled" {
			return errors.New("所选防火墙未启用，请明确启用后再绑定")
		}
		return nil
	case "custom":
		if s.ID != 0 || s.Rules == nil || s.AllowUnverified {
			return errors.New("自定义防火墙参数无效")
		}
		if e := ValidateFirewallRules(*s.Rules); e != nil {
			return e
		}
	default:
		return errors.New("请选择全部放行、已有防火墙或自定义规则")
	}
	// Permission probe is read-only; account default settings are never changed.
	var list struct {
		Data  []jsonRaw
		Pages int
	}
	if e := l.call(ctx, "GET", "/networking/firewalls?page_size=25", nil, &list); e != nil {
		if s.Mode == "all" && s.AllowUnverified {
			return nil
		}
		return errors.New("无法读取防火墙，不能确认全部放行。请补充读取权限，或明确选择创建后人工核实")
	}
	if list.Data == nil || list.Pages < 1 {
		return &Error{Code: "防火墙读取响应无效"}
	}
	return nil
}
func (l *Linode) createFirewall(ctx context.Context, label string, rules FirewallRules, op *Operation) (int, error) {
	if e := ValidateFirewallRules(rules); e != nil {
		return 0, e
	}
	var f LinodeFirewall
	if e := l.call(ctx, "POST", "/networking/firewalls", map[string]any{"label": label, "rules": rules}, &f); e != nil {
		return 0, e
	}
	if f.ID < 1 {
		return 0, &Error{Code: "防火墙创建未返回ID", Uncertain: true}
	}
	op.Owned = append(op.Owned, "firewall:"+fmt.Sprint(f.ID))
	if e := recordOperation(ctx, *op); e != nil {
		return f.ID, e
	}
	return f.ID, nil
}
func (l *Linode) selectionID(ctx context.Context, s *FirewallSelection, token string, op *Operation) (int, error) {
	if s == nil || s.Mode == "all" {
		return -1, nil
	}
	if s.Mode == "existing" {
		if e := l.ValidateFirewallSelection(ctx, *s); e != nil {
			return 0, e
		}
		return s.ID, nil
	}
	return l.createFirewall(ctx, "narbe-"+token[:min(20, len(token))], *s.Rules, op)
}
func (l *Linode) attachedFirewalls(ctx context.Context, id string, iid int) ([]LinodeFirewall, error) {
	path := "/linode/instances/" + id + "/firewalls"
	if iid > 0 {
		path = "/linode/instances/" + id + "/interfaces/" + fmt.Sprint(iid) + "/firewalls"
	}
	var f []LinodeFirewall
	if e := l.pages(ctx, path, &f); e != nil {
		return nil, e
	}
	return f, nil
}
func (l *Linode) verifySelection(ctx context.Context, id string, iid int, s *FirewallSelection) (bool, error) {
	attached, e := l.attachedFirewalls(ctx, id, iid)
	if e != nil {
		return false, e
	}
	if s == nil || s.Mode == "all" {
		return len(attached) == 0, nil
	}
	if len(attached) != 1 || attached[0].ID != s.ID {
		return false, nil
	}
	f, e := l.firewall(ctx, fmt.Sprint(s.ID))
	if e != nil {
		return false, e
	}
	if f.Status != "enabled" {
		return false, nil
	}
	if s.Rules != nil && fingerprint(f.Rules) != fingerprint(*s.Rules) {
		return false, nil
	}
	return true, nil
}
func (l *Linode) bindFirewall(ctx context.Context, s LinodeState, iid, newID int, op *Operation) (err error) {
	wrote := false
	defer func() {
		if err != nil && wrote && !Uncertain(err) {
			err = &Error{Code: "部分网络绑定已修改，后续失败，请按账本核实", Uncertain: true}
		}
	}()
	checkpoint := func(step string) error {
		wrote = true
		op.Linode.Steps = append(op.Linode.Steps, step)
		return recordOperation(ctx, *op)
	}
	old, e := l.attachedFirewalls(ctx, s.Instance.ID, iid)
	if e != nil {
		return e
	}
	if s.Generation == "legacy_config" {
		ids := []int{}
		if newID > 0 {
			ids = append(ids, newID)
		}
		if e = l.call(ctx, "PUT", "/linode/instances/"+s.Instance.ID+"/firewalls", map[string]any{"firewall_ids": ids}, nil); e != nil {
			return e
		}
		return checkpoint("传统实例绑定替换已受理")
	}
	// New interfaces are separate devices. Never replace another interface/instance.
	for _, f := range old {
		full, e := l.firewall(ctx, fmt.Sprint(f.ID))
		if e != nil {
			return e
		}
		found := false
		for _, d := range full.Devices {
			if d.Entity.Type == "linode_interface" && d.Entity.ID == iid {
				if d.Entity.ParentEntity == nil || fmt.Sprint(d.Entity.ParentEntity.ID) != s.Instance.ID {
					return errors.New("接口防火墙归属无法核实，未修改该绑定")
				}
				found = true
				if e = l.call(ctx, "DELETE", fmt.Sprintf("/networking/firewalls/%d/devices/%d", f.ID, d.ID), nil, nil); e != nil {
					return e
				}
				if e = checkpoint(fmt.Sprintf("已解绑firewall:%d device:%d interface:%d", f.ID, d.ID, iid)); e != nil {
					return e
				}
			}
		}
		if !found {
			return errors.New("接口绑定已变化，请重新核实")
		}
	}
	if newID > 0 {
		var d FirewallDevice
		if e = l.call(ctx, "POST", fmt.Sprintf("/networking/firewalls/%d/devices", newID), map[string]any{"id": iid, "type": "linode_interface"}, &d); e != nil {
			return e
		}
		if d.Entity.Type != "linode_interface" || d.Entity.ID != iid || d.Entity.ParentEntity == nil || fmt.Sprint(d.Entity.ParentEntity.ID) != s.Instance.ID {
			return &Error{Code: "防火墙接口归属响应无法确认", Uncertain: true}
		}
		if e = checkpoint(fmt.Sprintf("已绑定firewall:%d interface:%d", newID, iid)); e != nil {
			return e
		}
	}
	return nil
}

func (l *Linode) verifyInstanceSelection(ctx context.Context, id string, s *FirewallSelection) (bool, error) {
	v, e := l.state(ctx, id)
	if e != nil {
		return false, e
	}
	if v.Generation == "legacy_config" {
		return l.verifySelection(ctx, id, 0, s)
	}
	count := 0
	for _, i := range v.Interfaces {
		if i.Public != nil || i.VPC != nil {
			count++
			ok, e := l.verifySelection(ctx, id, i.ID, s)
			if e != nil || !ok {
				return false, e
			}
		}
	}
	if count == 0 {
		return false, &Error{Code: "没有可确认的公网或VPC接口"}
	}
	return true, nil
}
