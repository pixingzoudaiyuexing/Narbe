package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type doReservedIP struct {
	IP     string `json:"ip"`
	Region struct {
		Slug string `json:"slug"`
	} `json:"region"`
	Locked  *bool           `json:"locked"`
	Droplet json.RawMessage `json:"droplet"`
}

func (d *DigitalOcean) doDropletIP(ctx context.Context, id string) (doDroplet, string, error) {
	v := doDroplet{}
	if err := linodeID(id); err != nil {
		return v, "", err
	}
	var response struct {
		Droplet doDroplet `json:"droplet"`
	}
	if err := d.call(ctx, "GET", "/droplets/"+id, nil, &response); err != nil {
		return v, "", err
	}
	v = response.Droplet
	if strconv.Itoa(v.ID) != id || v.Region.Slug == "" || (v.Status != "active" && v.Status != "off" && v.Status != "new") {
		return v, "", &Error{Code: "Droplet 身份、地区或状态响应不完整"}
	}
	primary := ""
	for _, n := range v.Networks.V4 {
		if n.Type != "public" {
			continue
		}
		if !screeningIPv4(n.Ip_address) || primary != "" {
			return v, "", &Error{Code: "Droplet 原始公网 IPv4 不唯一或无效"}
		}
		primary = n.Ip_address
	}
	if primary == "" {
		return v, "", &Error{Code: "Droplet 尚无原始公网 IPv4"}
	}
	return v, primary, nil
}

func doReservedOwner(item doReservedIP) (string, error) {
	if !screeningIPv4(item.IP) || item.Region.Slug == "" || len(item.Droplet) == 0 {
		return "", &Error{Code: "Reserved IP 地址、地区或绑定字段缺失；请核对读取权限"}
	}
	if strings.TrimSpace(string(item.Droplet)) == "null" {
		return "", nil
	}
	var owner struct {
		ID     int `json:"id"`
		Region struct {
			Slug string `json:"slug"`
		} `json:"region"`
	}
	if json.Unmarshal(item.Droplet, &owner) != nil || owner.ID <= 0 || owner.Region.Slug != item.Region.Slug {
		return "", &Error{Code: "Reserved IP 的 Droplet 绑定身份或地区无法确认"}
	}
	return strconv.Itoa(owner.ID), nil
}

func (d *DigitalOcean) doReservedList(ctx context.Context) ([]doReservedIP, error) {
	items := []doReservedIP{}
	seen := map[string]bool{}
	for page := 1; page <= 10; page++ {
		var response struct {
			Items []doReservedIP `json:"reserved_ips"`
		}
		if err := d.call(ctx, "GET", fmt.Sprintf("/reserved_ips?per_page=200&page=%d", page), nil, &response); err != nil {
			return nil, err
		}
		if response.Items == nil || len(response.Items) > 200 {
			return nil, &Error{Code: "Reserved IP 列表不完整或数量异常；请检查 reserved_ip:read 权限"}
		}
		for _, item := range response.Items {
			if seen[item.IP] {
				return nil, &Error{Code: "Reserved IP 列表含重复地址"}
			}
			if _, err := doReservedOwner(item); err != nil {
				return nil, err
			}
			seen[item.IP] = true
			items = append(items, item)
		}
		if len(response.Items) < 200 {
			return items, nil
		}
	}
	return nil, &Error{Code: "Reserved IP 列表分页超过核实上限"}
}

func (d *DigitalOcean) IPInfo(ctx context.Context, i Instance) (IPInfo, error) {
	info := IPInfo{InstanceID: i.ID, Region: i.Region, Addresses: []IPAddress{}, AvailableIPs: []IPAddress{}, ServiceIPv4: []string{}, Methods: []IPMethod{}, Warnings: []string{}}
	v, primary, err := d.doDropletIP(ctx, i.ID)
	if err != nil {
		return info, err
	}
	if v.Region.Slug != i.Region {
		return info, &Error{Code: "Droplet 地区与所选实例不一致"}
	}
	info.State = v.Status
	info.PrimaryIPv4 = primary
	info.Addresses = append(info.Addresses, IPAddress{Address: primary, Kind: "primary", InstanceID: i.ID, Region: i.Region, Primary: true})
	items, err := d.doReservedList(ctx)
	if err != nil {
		return info, err
	}
	targetReserved := ""
	for _, item := range items {
		if item.Region.Slug != i.Region {
			continue
		}
		owner, _ := doReservedOwner(item)
		if owner == i.ID {
			if targetReserved != "" {
				return info, &Error{Code: "Droplet 绑定了多个 Reserved IP，无法安全管理"}
			}
			targetReserved = item.IP
			info.Addresses = append(info.Addresses, IPAddress{Address: item.IP, Kind: "reserved", ResourceID: item.IP, InstanceID: i.ID, Region: i.Region})
		} else if item.Locked != nil && !*item.Locked {
			info.AvailableIPs = append(info.AvailableIPs, IPAddress{Address: item.IP, Kind: "reserved", ResourceID: item.IP, InstanceID: owner, Region: i.Region})
		}
	}
	if targetReserved != "" {
		info.ServiceIPv4 = []string{targetReserved}
	} else {
		info.ServiceIPv4 = []string{primary}
	}
	available := targetReserved == "" && (v.Status == "active" || v.Status == "off")
	allocate := IPMethod{ID: "do-reserved-allocate", Name: "创建 Reserved IP", Available: available, ExtraCharge: true, Description: "创建并绑定额外服务 IPv4。Droplet 原始公网 IPv4 保持不变；Reserved IP 可能计费。"}
	assign := IPMethod{ID: "do-reserved-assign", Name: "分配已有 Reserved IP", Available: available && len(info.AvailableIPs) > 0, ExtraCharge: true, Description: "将本账号同地区 Reserved IP 绑定到此 Droplet；原始公网 IPv4 不变。若来自其他 Droplet，原实例服务地址将被迁走。"}
	if targetReserved != "" {
		allocate.Reason = "此 Droplet 已绑定 Reserved IP；每台只能绑定一个，请先单独处理原地址"
		assign.Reason = allocate.Reason
	} else if !available {
		allocate.Reason = "Droplet 状态尚不稳定"
		assign.Reason = allocate.Reason
	} else if len(info.AvailableIPs) == 0 {
		assign.Reason = "本账号同地区没有可分配的 Reserved IP"
	}
	info.Methods = append(info.Methods, allocate, assign)
	info.Warnings = append(info.Warnings, "Reserved IP 不会替换 Droplet 原始公网 IPv4；实际服务地址需由 DNS、入口或客户端切换。", "Reserved IP 配额和创建权限需由云端验证；若新地址保留，可能产生额外费用。")
	info.Snapshot = fingerprint(struct {
		ID                                int
		Region, Status, Primary, Reserved string
	}{v.ID, v.Region.Slug, v.Status, primary, targetReserved})
	return info, nil
}

func (d *DigitalOcean) BeginIPChange(ctx context.Context, i Instance, change IPChange, token string) (Operation, error) {
	info, err := d.IPInfo(ctx, i)
	if err != nil {
		return Operation{}, err
	}
	op, err := NewIPOperation(i, change, info, token)
	if err != nil {
		return op, err
	}
	if change.Method == "do-reserved-assign" {
		found := false
		for _, item := range info.AvailableIPs {
			if item.Address == change.Address && item.InstanceID == change.SourceInstanceID && item.Region == i.Region {
				found = true
				break
			}
		}
		if !found {
			return op, &Error{Code: "Reserved IP、来源 Droplet 或地区已变化"}
		}
		if change.SourceInstanceID != "" {
			source, _, e := d.doDropletIP(ctx, change.SourceInstanceID)
			if e != nil {
				return op, e
			}
			if source.Region.Slug != i.Region {
				return op, &Error{Code: "来源 Droplet 不在同一地区"}
			}
		}
		op.IP.NewIP = change.Address
		op.IP.SourceInstanceID = change.SourceInstanceID
	} else if change.Method == "do-reserved-allocate" {
		if change.Address != "" || change.SourceInstanceID != "" {
			return op, &Error{Code: "创建 Reserved IP 不能携带已有地址"}
		}
	} else {
		return op, &Error{Code: "未知 DigitalOcean 换 IP 方法"}
	}
	op.IP.Notice = "Reserved IP 是独立服务地址，Droplet 原始公网 IPv4 保持不变；请切换 DNS/服务入口并检测连通性。旧 Reserved IP 不会自动释放。"
	return op, nil
}

func (d *DigitalOcean) AdvanceIPChange(ctx context.Context, op Operation) (Operation, bool, error) {
	return d.advanceIPChange(ctx, op, true)
}
func (d *DigitalOcean) ReconcileIPChange(ctx context.Context, op Operation) (Operation, bool, error) {
	return d.advanceIPChange(ctx, op, false)
}

func (d *DigitalOcean) advanceIPChange(ctx context.Context, op Operation, write bool) (Operation, bool, error) {
	if op.IP == nil || op.IP.TargetID != op.ResourceID || op.IP.Region != op.Region || op.IP.RequestID == "" {
		return op, false, &Error{Code: "Reserved IP 操作账本身份无效"}
	}
	if op.IP.Step == "completed" {
		return op, true, nil
	}
	if op.IP.Step == "validated" {
		info, err := d.IPInfo(ctx, Instance{ID: op.ResourceID, Region: op.Region})
		if err != nil {
			return op, false, err
		}
		if info.Snapshot != op.IP.Snapshot || info.PrimaryIPv4 != op.IP.OldIP {
			return op, false, &Error{Code: "Droplet 或 Reserved IP 绑定已变化，未发送云写"}
		}
		if op.IP.Method == "do-reserved-assign" {
			found := false
			for _, a := range info.AvailableIPs {
				if a.Address == op.IP.NewIP && a.InstanceID == op.IP.SourceInstanceID && a.Region == op.Region {
					found = true
				}
			}
			if !found {
				return op, false, &Error{Code: "待分配 Reserved IP 已被其他操作改变"}
			}
		}
		if !write {
			return op, false, nil
		}
		if op.IP.Method == "do-reserved-allocate" {
			if err = ipCheckpoint(ctx, &op, "allocation-sent", "正在创建 Reserved IP", "create-reserved-ip", op.ResourceID); err != nil {
				return op, false, err
			}
			id, _ := strconv.Atoi(op.ResourceID)
			var response struct {
				ReservedIP doReservedIP `json:"reserved_ip"`
				Links      struct {
					Actions []struct {
						ID int `json:"id"`
					} `json:"actions"`
				} `json:"links"`
			}
			err = d.call(ctx, "POST", "/reserved_ips", map[string]any{"droplet_id": id}, &response)
			if err != nil {
				return op, false, err
			}
			if !screeningIPv4(response.ReservedIP.IP) || response.ReservedIP.Region.Slug != op.Region {
				return op, false, &Error{Code: "Reserved IP 创建响应缺少地址或地区", Uncertain: true}
			}
			op.IP.NewIP = response.ReservedIP.IP
			op.IP.NewResourceID = response.ReservedIP.IP
			op.IP.Resources = append(op.IP.Resources, RelatedResource{ID: "reserved-ip:" + response.ReservedIP.IP, Kind: "reserved-ip", State: "created-retained"})
			if len(response.Links.Actions) > 0 && response.Links.Actions[0].ID > 0 {
				op.ActionID = strconv.Itoa(response.Links.Actions[0].ID)
			}
		} else if op.IP.Method == "do-reserved-assign" {
			if err = ipCheckpoint(ctx, &op, "assignment-sent", "正在绑定 Reserved IP", "assign-reserved-ip", op.IP.NewIP); err != nil {
				return op, false, err
			}
			id, _ := strconv.Atoi(op.ResourceID)
			var response struct {
				Action struct {
					ID int `json:"id"`
				} `json:"action"`
			}
			err = d.call(ctx, "POST", "/reserved_ips/"+Escape(op.IP.NewIP)+"/actions", map[string]any{"type": "assign", "droplet_id": id}, &response)
			if err != nil {
				return op, false, err
			}
			if response.Action.ID <= 0 {
				return op, false, &Error{Code: "Reserved IP 分配响应缺少 Action ID", Uncertain: true}
			}
			op.ActionID = strconv.Itoa(response.Action.ID)
		} else {
			return op, false, &Error{Code: "未知 DigitalOcean IP 方法"}
		}
		if err = recordOperation(ctx, op); err != nil {
			return op, false, err
		}
		return op, false, nil
	}
	if op.IP.Step != "allocation-sent" && op.IP.Step != "assignment-sent" {
		return op, false, &Error{Code: "未知 Reserved IP 操作阶段，禁止重复云写", Uncertain: true}
	}
	if op.IP.NewIP == "" {
		return op, false, &Error{Code: "Reserved IP 创建响应丢失，地址身份未知；不会重发", Uncertain: true}
	}
	if op.ActionID != "" {
		var action struct {
			Action struct {
				ID     int    `json:"id"`
				Status string `json:"status"`
			} `json:"action"`
		}
		if err := d.call(ctx, "GET", "/reserved_ips/"+Escape(op.IP.NewIP)+"/actions/"+Escape(op.ActionID), nil, &action); err != nil {
			return op, false, err
		}
		if strconv.Itoa(action.Action.ID) != op.ActionID {
			return op, false, &Error{Code: "Reserved IP Action 身份不一致"}
		}
		if action.Action.Status == "errored" {
			return op, false, &Error{Code: "Reserved IP 云端分配 Action 失败"}
		}
		if action.Action.Status != "completed" {
			return op, false, nil
		}
	}
	var response struct {
		ReservedIP doReservedIP `json:"reserved_ip"`
	}
	if err := d.call(ctx, "GET", "/reserved_ips/"+Escape(op.IP.NewIP), nil, &response); err != nil {
		return op, false, err
	}
	item := response.ReservedIP
	owner, err := doReservedOwner(item)
	if err != nil {
		return op, false, err
	}
	if item.IP != op.IP.NewIP || item.Region.Slug != op.Region || owner != op.ResourceID {
		return op, false, nil
	}
	info, err := d.IPInfo(ctx, Instance{ID: op.ResourceID, Region: op.Region})
	if err != nil {
		return op, false, err
	}
	if info.PrimaryIPv4 != op.IP.OldIP || len(info.ServiceIPv4) != 1 || info.ServiceIPv4[0] != op.IP.NewIP {
		return op, false, &Error{Code: "Reserved IP 已绑定但原地址或服务地址回读不一致", Uncertain: true}
	}
	op.IP.After = &info
	op.IP.PrimaryChanged = false
	op.IP.AddressRepeated = op.IP.NewIP == op.IP.OldIP
	if op.IP.AddressRepeated {
		return op, false, &Error{Code: "Reserved IP 与原始公网 IPv4 相同，不能确认换得不同地址"}
	}
	if err = ipConfirmed(ctx, &op, "completed", "Reserved IP 已绑定；原始公网 IPv4 保留，仍需连通性检测"); err != nil {
		return op, false, err
	}
	return op, true, nil
}

var _ IPProvider = (*DigitalOcean)(nil)
