package cloud

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ErrIPQuota is returned only for a provider rejection that explicitly names
// the public IPv4 allocation limit. Permission failures never match it.
var ErrIPQuota = errors.New("公网 IPv4 配额不足")

type linIP struct {
	Address        string `json:"address"`
	Type           string `json:"type"`
	Region         string `json:"region"`
	LinodeID       *int   `json:"linode_id"`
	InterfaceID    *int   `json:"interface_id"`
	Public         *bool  `json:"public"`
	Reserved       *bool  `json:"reserved"`
	AssignedEntity *struct {
		ID   int    `json:"id"`
		Type string `json:"type"`
	} `json:"assigned_entity"`
}

type linPublicAddress struct {
	Address string `json:"address"`
	Primary bool   `json:"primary"`
}

type linIPView struct {
	Instance           linInstance
	Generation         string
	Public             []linIP
	Interfaces         []LinodeInterface
	Primary            string
	PublicInterfaceID  string
	InterfaceAddresses []linPublicAddress
}

func (l *Linode) linIPView(ctx context.Context, id string) (linIPView, error) {
	v := linIPView{}
	if err := linodeID(id); err != nil {
		return v, err
	}
	var instance struct {
		linInstance
		Generation string `json:"interface_generation"`
	}
	path := "/linode/instances/" + id
	if err := l.call(ctx, "GET", path, nil, &instance); err != nil {
		return v, err
	}
	if strconv.Itoa(instance.ID) != id || instance.Region == "" || (instance.Generation != "legacy_config" && instance.Generation != "linode") {
		return v, &Error{Code: "Linode 身份、地区或网络接口代际无法确认"}
	}
	v.Instance, v.Generation = instance.linInstance, instance.Generation
	sort.Strings(v.Instance.IPv4)
	var ipResponse struct {
		IPv4 struct {
			Public []linIP `json:"public"`
		} `json:"ipv4"`
	}
	if err := l.call(ctx, "GET", path+"/ips", nil, &ipResponse); err != nil {
		return v, err
	}
	if ipResponse.IPv4.Public == nil {
		return v, &Error{Code: "Linode 公网 IPv4 列表缺失"}
	}
	seen := map[string]bool{}
	for _, ip := range ipResponse.IPv4.Public {
		if !screeningIPv4(ip.Address) || ip.Public == nil || !*ip.Public || ip.LinodeID == nil || *ip.LinodeID != instance.ID || ip.Region != instance.Region || seen[ip.Address] {
			return v, &Error{Code: "Linode 公网 IPv4 归属或地址无效"}
		}
		seen[ip.Address] = true
		v.Public = append(v.Public, ip)
	}
	if len(v.Public) == 0 {
		return v, &Error{Code: "Linode 没有可核实的公网 IPv4"}
	}
	sort.Slice(v.Public, func(a, b int) bool { return v.Public[a].Address < v.Public[b].Address })
	if v.Generation == "linode" {
		var interfaces struct {
			Items []LinodeInterface `json:"interfaces"`
		}
		if err := l.call(ctx, "GET", path+"/interfaces", nil, &interfaces); err != nil {
			return v, err
		}
		if interfaces.Items == nil {
			return v, &Error{Code: "Linode Interfaces 列表缺失"}
		}
		v.Interfaces = interfaces.Items
		for _, iface := range interfaces.Items {
			if iface.Public == nil {
				continue
			}
			if v.PublicInterfaceID != "" || iface.ID < 1 {
				return v, &Error{Code: "Linode public interface 数量或身份异常"}
			}
			v.PublicInterfaceID = strconv.Itoa(iface.ID)
			var body struct {
				IPv4 struct {
					Addresses []linPublicAddress `json:"addresses"`
				} `json:"ipv4"`
			}
			if err := decode(marshal(iface.Public), &body); err != nil || body.IPv4.Addresses == nil {
				return v, &Error{Code: "Linode public interface IPv4 配置缺失"}
			}
			primaryCount := 0
			for _, a := range body.IPv4.Addresses {
				if !seen[a.Address] {
					return v, &Error{Code: "Linode interface 地址与公网 IPv4 列表不一致"}
				}
				if a.Primary {
					v.Primary = a.Address
					primaryCount++
				}
			}
			if primaryCount != 1 || len(body.IPv4.Addresses) != len(v.Public) {
				return v, &Error{Code: "Linode interface 主地址或完整地址列表无法确认"}
			}
			v.InterfaceAddresses = body.IPv4.Addresses
		}
		if v.PublicInterfaceID == "" {
			return v, &Error{Code: "Linode 尚无 public interface"}
		}
	} else if len(v.Public) == 1 {
		if !contains(v.Instance.IPv4, v.Public[0].Address) {
			return v, &Error{Code: "Linode 实例公网 IPv4 与地址列表不一致"}
		}
		v.Primary = v.Public[0].Address
	}
	return v, nil
}

func (l *Linode) linAccountIPs(ctx context.Context) ([]linIP, error) {
	var ips []linIP
	if err := l.pages(ctx, "/networking/ips", &ips); err != nil {
		return nil, err
	}
	return ips, nil
}

func (l *Linode) IPInfo(ctx context.Context, i Instance) (IPInfo, error) {
	info := IPInfo{InstanceID: i.ID, Region: i.Region, Addresses: []IPAddress{}, AvailableIPs: []IPAddress{}, ServiceIPv4: []string{}, Methods: []IPMethod{}, Warnings: []string{}}
	v, err := l.linIPView(ctx, i.ID)
	if err != nil {
		return info, err
	}
	if v.Instance.Region != i.Region {
		return info, &Error{Code: "Linode 地区与所选实例不一致"}
	}
	info.State, info.Region, info.NetworkGeneration, info.PrimaryIPv4 = v.Instance.Status, v.Instance.Region, v.Generation, v.Primary
	for _, ip := range v.Public {
		kind := "additional"
		if ip.Address == v.Primary {
			kind = "primary"
		}
		iface := ""
		if ip.InterfaceID != nil {
			iface = strconv.Itoa(*ip.InterfaceID)
		}
		info.Addresses = append(info.Addresses, IPAddress{Address: ip.Address, Kind: kind, ResourceID: ip.Address, InterfaceID: iface, InstanceID: i.ID, Region: i.Region, Primary: ip.Address == v.Primary})
		info.ServiceIPv4 = append(info.ServiceIPv4, ip.Address)
	}
	all, err := l.linAccountIPs(ctx)
	if err != nil {
		return info, err
	}
	for _, ip := range all {
		if ip.Type != "ipv4" || ip.Public == nil || !*ip.Public || ip.Reserved == nil || !*ip.Reserved || ip.Region != i.Region || !screeningIPv4(ip.Address) {
			continue
		}
		if ip.LinodeID != nil && *ip.LinodeID == v.Instance.ID {
			continue
		}
		source := ""
		if ip.LinodeID != nil && *ip.LinodeID > 0 {
			if ip.AssignedEntity == nil || ip.AssignedEntity.Type != "linode" || ip.AssignedEntity.ID != *ip.LinodeID {
				continue
			}
			source = strconv.Itoa(*ip.LinodeID)
		} else if ip.LinodeID != nil && *ip.LinodeID == 0 {
			continue
		}
		info.AvailableIPs = append(info.AvailableIPs, IPAddress{Address: ip.Address, Kind: "reserved", ResourceID: ip.Address, InstanceID: source, Region: ip.Region})
	}
	sort.Slice(info.Addresses, func(a, b int) bool { return info.Addresses[a].Address < info.Addresses[b].Address })
	sort.Slice(info.AvailableIPs, func(a, b int) bool { return info.AvailableIPs[a].Address < info.AvailableIPs[b].Address })
	sort.Strings(info.ServiceIPv4)
	allocate := IPMethod{ID: "linode-allocate", Name: "新增公网 IPv4", Available: v.Instance.Status == "running" || v.Instance.Status == "offline", ExtraCharge: true, Description: "增加额外公网 IPv4；原有公网地址与实例保留。额外地址可能计费，需核实系统网络配置和国内连通性。"}
	if v.Generation == "linode" {
		allocate.RequiresStop = v.Instance.Status == "running"
		allocate.Description += " Linode Interfaces 更新 public interface，运行中实例会先关机，完成后恢复。"
	}
	if !allocate.Available {
		allocate.Reason = "实例状态尚不稳定"
	}
	assign := IPMethod{ID: "linode-assign", Name: "分配已有保留 IP", Available: len(info.AvailableIPs) > 0, ExtraCharge: true, Description: "将本账号同地区保留 IP 分配到此实例；若地址仍绑定其他实例，来源实例将失去选中地址，服务可能中断，需明确接受风险。目标原有公网地址不释放。"}
	if !assign.Available {
		assign.Reason = "本账号同地区没有可分配的保留公网 IPv4"
	}
	info.Methods = append(info.Methods, allocate, assign)
	info.Warnings = append(info.Warnings, "额外 IPv4 配额及用户资源写权限没有可靠的只读确认接口；云端可能拒绝申请，请先确认支持配额。", "增加地址不会自动更换主 IPv4，来宾系统网络配置、DNS 与服务监听可能需调整；旧 IP 不会自动释放。")
	if v.Generation == "legacy_config" && len(v.Public) > 1 {
		info.Warnings = append(info.Warnings, "legacy_config 有多个公网 IPv4，云 API 未提供可信的主次标记；下方列出全部已验证地址，但主 IPv4 未知。")
	}
	info.Snapshot = fingerprint(struct {
		Instance   linInstance
		Generation string
		Public     []linIP
		Interfaces []LinodeInterface
	}{v.Instance, v.Generation, v.Public, v.Interfaces})
	return info, nil
}

func (l *Linode) BeginIPChange(ctx context.Context, i Instance, change IPChange, token string) (Operation, error) {
	info, err := l.IPInfo(ctx, i)
	if err != nil {
		return Operation{}, err
	}
	op, err := NewIPOperation(i, change, info, token)
	if err != nil {
		return op, err
	}
	if change.Method == "linode-assign" {
		found := false
		for _, item := range info.AvailableIPs {
			if item.Address == change.Address && item.InstanceID == change.SourceInstanceID && item.Region == i.Region {
				found = true
				break
			}
		}
		if !found || change.Address == info.PrimaryIPv4 {
			return op, &Error{Code: "保留 IP、来源实例或地区已变化，未分配"}
		}
		if change.SourceInstanceID != "" {
			source, e := l.linIPView(ctx, change.SourceInstanceID)
			if e != nil {
				return op, e
			}
			if source.Instance.Region != i.Region || len(source.Public) < 2 || source.Primary == change.Address {
				return op, &Error{Code: "来源 Linode 必须在同地区保留另一公网 IPv4，未迁移地址"}
			}
			owned := false
			for _, ip := range source.Public {
				if ip.Address == change.Address && ip.Reserved != nil && *ip.Reserved {
					owned = true
				}
			}
			if !owned {
				return op, &Error{Code: "来源 Linode 的保留 IP 绑定已变化"}
			}
			op.IP.Checkpoint["sourceSnapshot"] = fingerprint(source)
		}
		op.IP.NewIP = change.Address
		op.IP.SourceInstanceID = change.SourceInstanceID
	}
	if change.Method == "linode-allocate" && (change.Address != "" || change.SourceInstanceID != "") {
		return op, &Error{Code: "新增 IPv4 请求不能携带已有地址"}
	}
	if info.NetworkGeneration == "linode" {
		v, e := l.linIPView(ctx, i.ID)
		if e != nil {
			return op, e
		}
		op.IP.InterfaceID = v.PublicInterfaceID
		op.IP.Checkpoint["interfaceSnapshot"] = fingerprint(v.Interfaces)
		op.IP.Checkpoint["publicSnapshot"] = fingerprint(v.Public)
	}
	op.IP.GuestNetworkingRequired = true
	op.IP.Notice = "新增公网 IPv4 不自动替换原有地址；需要核实来宾系统网络配置与服务连通性。原 IP 保留，额外 IPv4 可能计费。"
	return op, nil
}

func linQuotaError(err error) error {
	var e *Error
	if errors.As(err, &e) && e.Status == 400 {
		reason := strings.ToLower(e.Code)
		if strings.Contains(reason, "quota") || strings.Contains(reason, "ipv4 limit") || strings.Contains(reason, "ip address limit") || strings.Contains(reason, "maximum number of public") {
			return fmt.Errorf("%w: %w", ErrIPQuota, err)
		}
	}
	return err
}

func (l *Linode) AdvanceIPChange(ctx context.Context, op Operation) (Operation, bool, error) {
	return l.advanceIPChange(ctx, op, true)
}
func (l *Linode) ReconcileIPChange(ctx context.Context, op Operation) (Operation, bool, error) {
	return l.advanceIPChange(ctx, op, false)
}

func (l *Linode) advanceIPChange(ctx context.Context, op Operation, write bool) (Operation, bool, error) {
	if op.IP == nil || op.IP.TargetID != op.ResourceID || op.IP.Region != op.Region || op.IP.RequestID == "" {
		return op, false, &Error{Code: "Linode IP 操作账本身份无效"}
	}
	if op.IP.Step == "completed" {
		return op, true, nil
	}
	v, err := l.linIPView(ctx, op.ResourceID)
	if err != nil {
		return op, false, err
	}
	if v.Instance.Region != op.Region || v.Generation != op.IP.Before.NetworkGeneration {
		return op, false, &Error{Code: "Linode 身份、地区或接口代际已变化，停止换 IP"}
	}
	current := map[string]bool{}
	for _, ip := range v.Public {
		current[ip.Address] = true
	}
	if !current[op.IP.OldIP] {
		return op, false, &Error{Code: "Linode 原公网 IPv4 已被外部修改，停止换 IP"}
	}
	if op.IP.Step == "validated" {
		info, e := l.IPInfo(ctx, Instance{ID: op.ResourceID, Region: op.Region})
		if e != nil {
			return op, false, e
		}
		if info.Snapshot != op.IP.Snapshot {
			return op, false, &Error{Code: "Linode IP 配置已变化，未发送云写"}
		}
		if op.IP.Method == "linode-assign" {
			selected := false
			for _, a := range info.AvailableIPs {
				if a.Address == op.IP.NewIP && a.InstanceID == op.IP.SourceInstanceID && a.Region == op.Region {
					selected = true
				}
			}
			if !selected {
				return op, false, &Error{Code: "Linode 所选保留 IP 绑定已变化，未分配"}
			}
		}
		if op.IP.Method == "linode-allocate" && v.Generation == "linode" && v.Instance.Status == "running" {
			if !write {
				return op, false, nil
			}
			if e = ipCheckpoint(ctx, &op, "shutdown-sent", "正在关机以更新公网接口", "shutdown", op.ResourceID); e != nil {
				return op, false, e
			}
			e = l.call(ctx, "POST", "/linode/instances/"+op.ResourceID+"/shutdown", map[string]any{}, nil)
			return op, false, e
		}
		return l.linDispatchIP(ctx, op, v, write)
	}
	if op.IP.Step == "shutdown-sent" {
		if v.Instance.Status != "offline" {
			return op, false, nil
		}
		if err = ipConfirmed(ctx, &op, "interface-ready", "实例已关机，准备更新公网接口"); err != nil {
			return op, false, err
		}
		return op, false, nil
	}
	if op.IP.Step == "interface-ready" {
		return l.linDispatchIP(ctx, op, v, write)
	}
	if op.IP.Step == "restore-after-rejection" {
		if err = linRejectedInterfaceUnchanged(op, v); err != nil {
			return op, false, err
		}
		if v.Instance.Status == "running" {
			return l.linFinishRejectedRestore(ctx, op, write)
		}
		if v.Instance.Status != "offline" {
			return op, false, nil
		}
		if !write {
			return op, false, nil
		}
		if err = ipCheckpoint(ctx, &op, "restore-boot-sent", "云端拒绝新增 IPv4，正在恢复原运行状态", "boot", op.ResourceID); err != nil {
			return op, false, err
		}
		err = l.call(ctx, "POST", "/linode/instances/"+op.ResourceID+"/boot", map[string]any{}, nil)
		return op, false, err
	}
	if op.IP.Step == "restore-boot-sent" {
		if err = linRejectedInterfaceUnchanged(op, v); err != nil {
			return op, false, err
		}
		if v.Instance.Status != "running" {
			return op, false, nil
		}
		return l.linFinishRejectedRestore(ctx, op, write)
	}
	if op.IP.Step == "allocation-sent" || op.IP.Step == "assignment-sent" || op.IP.Step == "interface-sent" {
		if op.IP.NewIP == "" {
			// A lost allocation response contains no provable address identity.
			return op, false, &Error{Code: "Linode 新地址响应缺失，不能只凭差集确认归属或重发申请", Uncertain: true}
		}
		if !current[op.IP.NewIP] {
			return op, false, nil
		}
		if op.IP.NewIP == op.IP.OldIP {
			op.IP.AddressRepeated = true
			return op, false, &Error{Code: "Linode 返回了原地址，没有获得不同的公网 IPv4"}
		}
		if op.IP.Method == "linode-allocate" && v.Generation == "linode" && v.Instance.Status == "offline" && op.IP.OriginalState == "running" {
			if err = ipConfirmed(ctx, &op, "boot-ready", "新 IPv4 已绑定，等待恢复实例运行"); err != nil {
				return op, false, err
			}
			return op, false, nil
		}
		return l.linFinishIP(ctx, op)
	}
	if op.IP.Step == "boot-ready" {
		if !write {
			return op, false, nil
		}
		if err = ipCheckpoint(ctx, &op, "boot-sent", "正在恢复实例运行", "boot", op.ResourceID); err != nil {
			return op, false, err
		}
		err = l.call(ctx, "POST", "/linode/instances/"+op.ResourceID+"/boot", map[string]any{}, nil)
		return op, false, err
	}
	if op.IP.Step == "boot-sent" {
		if v.Instance.Status != "running" || !current[op.IP.NewIP] {
			return op, false, nil
		}
		return l.linFinishIP(ctx, op)
	}
	return op, false, &Error{Code: "未知 Linode IP 操作阶段，禁止重发云写", Uncertain: true}
}

func (l *Linode) linDispatchIP(ctx context.Context, op Operation, v linIPView, write bool) (Operation, bool, error) {
	if !write {
		return op, false, nil
	}
	if op.IP.Method == "linode-allocate" && v.Generation == "linode" {
		if op.IP.Checkpoint["interfaceSnapshot"] == "" || fingerprint(v.Interfaces) != op.IP.Checkpoint["interfaceSnapshot"] || fingerprint(v.Public) != op.IP.Checkpoint["publicSnapshot"] {
			return op, false, &Error{Code: "Linode public interface 或地址元数据已变化，未更新"}
		}
		before := map[string]bool{}
		for _, a := range op.IP.Before.Addresses {
			before[a.Address] = true
		}
		if len(before) != len(v.InterfaceAddresses) {
			return op, false, &Error{Code: "Linode public interface 原有地址已变化，未更新"}
		}
		for _, a := range v.InterfaceAddresses {
			if !before[a.Address] {
				return op, false, &Error{Code: "Linode public interface 原有地址已变化，未更新"}
			}
		}
	}
	var err error
	switch op.IP.Method {
	case "linode-allocate":
		if v.Generation == "legacy_config" {
			if err = ipCheckpoint(ctx, &op, "allocation-sent", "正在申请额外公网 IPv4", "allocate-ip", op.ResourceID); err != nil {
				return op, false, err
			}
			var result linIP
			err = l.call(ctx, "POST", "/linode/instances/"+op.ResourceID+"/ips", map[string]any{"type": "ipv4", "public": true}, &result)
			if err != nil {
				return op, false, linQuotaError(err)
			}
			if !screeningIPv4(result.Address) {
				return op, false, &Error{Code: "Linode 新 IPv4 响应缺失", Uncertain: true}
			}
			op.IP.NewIP = result.Address
		} else {
			if v.Instance.Status != "offline" || v.PublicInterfaceID != op.IP.InterfaceID {
				return op, false, &Error{Code: "Linode public interface 未在已确认的关机状态"}
			}
			addresses := append([]linPublicAddress{}, v.InterfaceAddresses...)
			addresses = append(addresses, linPublicAddress{Address: "auto", Primary: false})
			if err = ipCheckpoint(ctx, &op, "interface-sent", "正在更新 Linode public interface", "add-interface-ip", op.IP.InterfaceID); err != nil {
				return op, false, err
			}
			var result LinodeInterface
			err = l.call(ctx, "PUT", "/linode/instances/"+op.ResourceID+"/interfaces/"+op.IP.InterfaceID, map[string]any{"public": map[string]any{"ipv4": map[string]any{"addresses": addresses}}}, &result)
			if err != nil {
				return l.linHandleInterfaceRejection(ctx, op, linQuotaError(err))
			}
			var body struct {
				IPv4 struct {
					Addresses []linPublicAddress `json:"addresses"`
				} `json:"ipv4"`
			}
			if e := decode(marshal(result.Public), &body); e == nil {
				before := map[string]bool{}
				for _, a := range v.InterfaceAddresses {
					before[a.Address] = true
				}
				for _, a := range body.IPv4.Addresses {
					if !before[a.Address] && screeningIPv4(a.Address) {
						op.IP.NewIP = a.Address
					}
				}
			}
			if op.IP.NewIP == "" {
				return op, false, &Error{Code: "Linode 接口更新响应未给出新 IPv4，保留已派发状态", Uncertain: true}
			}
		}
	case "linode-assign":
		if op.IP.SourceInstanceID != "" {
			source, e := l.linIPView(ctx, op.IP.SourceInstanceID)
			if e != nil {
				return op, false, e
			}
			if source.Instance.Region != op.Region || len(source.Public) < 2 || source.Primary == op.IP.NewIP || fingerprint(source) != op.IP.Checkpoint["sourceSnapshot"] {
				return op, false, &Error{Code: "来源 Linode 网络状态已变化，未迁移保留 IP"}
			}
		}
		if err = ipCheckpoint(ctx, &op, "assignment-sent", "正在分配已有保留 IP", "assign-ip", op.IP.NewIP); err != nil {
			return op, false, err
		}
		id, _ := strconv.Atoi(op.ResourceID)
		err = l.call(ctx, "POST", "/networking/ips/assign", map[string]any{"region": op.Region, "assignments": []map[string]any{{"address": op.IP.NewIP, "linode_id": id}}}, nil)
		if err != nil {
			return op, false, err
		}
	default:
		return op, false, &Error{Code: "未知 Linode 换 IP 方法"}
	}
	if err = recordOperation(ctx, op); err != nil {
		return op, false, err
	}
	return op, false, nil
}

func linRejectedInterfaceUnchanged(op Operation, v linIPView) error {
	if op.IP == nil || op.IP.Method != "linode-allocate" || v.Generation != "linode" || v.PublicInterfaceID != op.IP.InterfaceID || op.IP.Checkpoint["interfaceSnapshot"] == "" || op.IP.Checkpoint["publicSnapshot"] == "" || fingerprint(v.Interfaces) != op.IP.Checkpoint["interfaceSnapshot"] || fingerprint(v.Public) != op.IP.Checkpoint["publicSnapshot"] {
		return &Error{Code: "Linode 接口拒绝后的原网络状态无法完整核实，未恢复开机", Uncertain: true}
	}
	if len(v.Public) != len(op.IP.Before.Addresses) {
		return &Error{Code: "Linode 接口拒绝后公网 IPv4 数量已变化，未恢复开机", Uncertain: true}
	}
	before := map[string]bool{}
	for _, a := range op.IP.Before.Addresses {
		before[a.Address] = true
	}
	for _, a := range v.Public {
		if !before[a.Address] {
			return &Error{Code: "Linode 接口拒绝后原公网 IPv4 已变化，未恢复开机", Uncertain: true}
		}
	}
	return nil
}

func (l *Linode) linHandleInterfaceRejection(ctx context.Context, op Operation, rejected error) (Operation, bool, error) {
	var ce *Error
	if !errors.As(rejected, &ce) || ce.Status < 400 || ce.Status >= 500 || Uncertain(rejected) || op.IP.NewIP != "" || len(op.IP.Resources) != 0 {
		return op, false, rejected
	}
	v, err := l.linIPView(ctx, op.ResourceID)
	if err != nil {
		return op, false, &Error{Code: "Linode 拒绝接口更新后无法回读原网络；恢复状态未知", Uncertain: true}
	}
	if err = linRejectedInterfaceUnchanged(op, v); err != nil {
		return op, false, err
	}
	if v.Instance.Status != "offline" {
		return op, false, &Error{Code: "Linode 拒绝接口更新后状态不再是原关机状态；未自动开机", Uncertain: true}
	}
	op.IP.Failure = rejected.Error()
	op.IP.Writes[len(op.IP.Writes)-1].State = "rejected"
	if op.IP.OriginalState == "offline" {
		info, e := l.IPInfo(ctx, Instance{ID: op.ResourceID, Region: op.Region})
		if e != nil || info.Snapshot != op.IP.Before.Snapshot || info.State != "offline" {
			return op, false, &Error{Code: "Linode 拒绝后原本关机的实例配置无法完整核实", Uncertain: true}
		}
		op.IP.After = &info
		if err = ipCheckpoint(ctx, &op, "completed", "云端拒绝新增 IPv4；原本关机的实例与旧地址保持不变", "", ""); err != nil {
			return op, false, err
		}
		return op, true, nil
	}
	if op.IP.OriginalState != "running" {
		return op, false, &Error{Code: "Linode 原运行状态未知，未自动恢复", Uncertain: true}
	}
	if err = ipCheckpoint(ctx, &op, "restore-after-rejection", "云端拒绝新增 IPv4，旧接口已核实；待恢复原运行状态", "", ""); err != nil {
		return op, false, err
	}
	return op, false, nil
}

func (l *Linode) linFinishRejectedRestore(ctx context.Context, op Operation, persist bool) (Operation, bool, error) {
	info, err := l.IPInfo(ctx, Instance{ID: op.ResourceID, Region: op.Region})
	if err != nil {
		return op, false, err
	}
	if info.PrimaryIPv4 != op.IP.Before.PrimaryIPv4 || len(info.Addresses) != len(op.IP.Before.Addresses) || info.State != "running" || info.Snapshot != op.IP.Before.Snapshot {
		return op, false, &Error{Code: "Linode 恢复后原地址或接口配置与操作前不一致", Uncertain: true}
	}
	op.IP.After = &info
	if !persist {
		if op.IP.Step == "restore-boot-sent" && len(op.IP.Writes) > 0 {
			op.IP.Writes[len(op.IP.Writes)-1].State = "confirmed"
		}
		op.IP.Step, op.IP.Stage = "completed", "云端拒绝新增 IPv4；原实例与旧地址已恢复运行"
		return op, true, nil
	}
	if op.IP.Step == "restore-boot-sent" {
		if err = ipConfirmed(ctx, &op, "completed", "云端拒绝新增 IPv4；原实例与旧地址已恢复运行"); err != nil {
			return op, false, err
		}
	} else if err = ipCheckpoint(ctx, &op, "completed", "云端拒绝新增 IPv4；原实例与旧地址已恢复运行", "", ""); err != nil {
		return op, false, err
	}
	return op, true, nil
}

func (l *Linode) linFinishIP(ctx context.Context, op Operation) (Operation, bool, error) {
	info, err := l.IPInfo(ctx, Instance{ID: op.ResourceID, Region: op.Region})
	if err != nil {
		return op, false, err
	}
	found := false
	for _, a := range info.Addresses {
		if a.Address == op.IP.NewIP && a.InstanceID == op.ResourceID {
			found = true
		}
	}
	if !found {
		return op, false, nil
	}
	attached := map[string]bool{}
	for _, a := range info.Addresses {
		attached[a.Address] = true
	}
	for _, a := range op.IP.Before.Addresses {
		if !attached[a.Address] {
			return op, false, &Error{Code: "Linode 原有公网 IPv4 遗失，不能报告成功", Uncertain: true}
		}
	}
	op.IP.After = &info
	op.IP.PrimaryChanged = info.PrimaryIPv4 != "" && op.IP.Before.PrimaryIPv4 != "" && info.PrimaryIPv4 != op.IP.Before.PrimaryIPv4
	if info.NetworkGeneration == "legacy_config" && info.PrimaryIPv4 == "" {
		op.IP.Notice += " 多公网 IPv4 的主次关系无法由云 API 确认，已保留并回读全部原地址。"
	}
	if op.IP.Method == "linode-allocate" && op.IP.PrimaryChanged {
		return op, false, &Error{Code: "Linode 主 IPv4 意外变化，请人工核实", Uncertain: true}
	}
	if err = ipConfirmed(ctx, &op, "completed", "额外公网 IPv4 已回读确认；仍需进行连通性检测"); err != nil {
		return op, false, err
	}
	return op, true, nil
}

var _ IPProvider = (*Linode)(nil)
