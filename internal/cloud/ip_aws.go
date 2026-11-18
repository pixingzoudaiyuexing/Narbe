package cloud

import (
	"context"
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
)

type awsIPRawInstance struct {
	ID        string `xml:"instanceId"`
	Image     string `xml:"imageId"`
	Plan      string `xml:"instanceType"`
	Key       string `xml:"keyName"`
	Subnet    string `xml:"subnetId"`
	VPC       string `xml:"vpcId"`
	RootType  string `xml:"rootDeviceType"`
	RootName  string `xml:"rootDeviceName"`
	Lifecycle string `xml:"instanceLifecycle"`
	IP        string `xml:"ipAddress"`
	Tags      []struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	} `xml:"tagSet>item"`
	Groups []struct {
		ID string `xml:"groupId"`
	} `xml:"groupSet>item"`
	State struct {
		Name string `xml:"name"`
	} `xml:"instanceState"`
	Disks []struct {
		Device string `xml:"deviceName"`
		Volume string `xml:"ebs>volumeId"`
	} `xml:"blockDeviceMapping>item"`
	Interfaces []struct {
		ID     string `xml:"networkInterfaceId"`
		Subnet string `xml:"subnetId"`
		Type   string `xml:"interfaceType"`
		Groups []struct {
			ID string `xml:"groupId"`
		} `xml:"groupSet>item"`
		Attachment struct {
			Index int `xml:"deviceIndex"`
		} `xml:"attachment"`
		Private []struct {
			IP      string `xml:"privateIpAddress"`
			Primary bool   `xml:"primary"`
		} `xml:"privateIpAddressesSet>item"`
	} `xml:"networkInterfaceSet>item"`
}

type awsIPAddress struct {
	IP          string `xml:"publicIp"`
	Allocation  string `xml:"allocationId"`
	Association string `xml:"associationId"`
	Instance    string `xml:"instanceId"`
	Interface   string `xml:"networkInterfaceId"`
	Private     string `xml:"privateIpAddress"`
	Domain      string `xml:"domain"`
	Tags        []struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	} `xml:"tagSet>item"`
}

type awsIPState struct {
	instance         awsIPRawInstance
	owner            string
	dataHash         string
	addresses        []awsIPAddress
	stable           string
	primaryEIP       string
	primaryInterface string
}

var awsIPID = regexp.MustCompile(`^i-[a-f0-9]{8,32}$`)
var awsEIPID = regexp.MustCompile(`^eipalloc-[a-f0-9]{8,32}$`)

func (a *AWS) inspectIP(ctx context.Context, i Instance) (IPInfo, awsIPState, error) {
	info, state := IPInfo{InstanceID: i.ID, Region: i.Region, Addresses: []IPAddress{}, AvailableIPs: []IPAddress{}, Methods: []IPMethod{}, Warnings: []string{}}, awsIPState{}
	if !awsIPID.MatchString(i.ID) || !awsRegion.MatchString(i.Region) {
		return info, state, &Error{Code: "AWS 实例或地区身份无效"}
	}
	var instances struct {
		Reservations []struct {
			Owner string             `xml:"ownerId"`
			Items []awsIPRawInstance `xml:"instancesSet>item"`
		} `xml:"reservationSet>item"`
	}
	if err := a.call(ctx, i.Region, "DescribeInstances", url.Values{"InstanceId.1": {i.ID}}, &instances); err != nil {
		return info, state, err
	}
	if len(instances.Reservations) != 1 || len(instances.Reservations[0].Items) != 1 || instances.Reservations[0].Owner == "" {
		return info, state, &Error{Code: "AWS 无法确认唯一实例和账号身份"}
	}
	state.owner, state.instance = instances.Reservations[0].Owner, instances.Reservations[0].Items[0]
	v := state.instance
	if v.ID != i.ID {
		return info, state, &Error{Code: "AWS 实例响应身份不一致"}
	}
	var addresses struct {
		Items []awsIPAddress `xml:"addressesSet>item"`
	}
	if err := a.call(ctx, i.Region, "DescribeAddresses", nil, &addresses); err != nil {
		return info, state, err
	}
	state.addresses = addresses.Items
	var attr struct {
		Data *struct {
			Value string `xml:"value"`
		} `xml:"userData"`
	}
	if err := a.call(ctx, i.Region, "DescribeInstanceAttribute", url.Values{"InstanceId": {i.ID}, "Attribute": {"userData"}}, &attr); err != nil {
		return info, state, err
	}
	if attr.Data == nil {
		return info, state, &Error{Code: "AWS 初始化数据身份无法确认"}
	}
	if _, err := base64.StdEncoding.DecodeString(attr.Data.Value); err != nil {
		return info, state, &Error{Code: "AWS 初始化数据格式无效"}
	}
	state.dataHash = digest([]byte(attr.Data.Value))
	info.State, info.PrimaryIPv4 = v.State.Name, v.IP
	if len(v.Interfaces) == 1 {
		state.primaryInterface = v.Interfaces[0].ID
	}
	for _, addr := range addresses.Items {
		if !screeningIPv4(addr.IP) || !awsEIPID.MatchString(addr.Allocation) {
			continue
		}
		ip := IPAddress{Address: addr.IP, Kind: "elastic", ResourceID: addr.Allocation, InterfaceID: addr.Interface, InstanceID: addr.Instance, Region: i.Region, Primary: addr.IP == v.IP}
		if addr.Instance == i.ID || addr.Interface == state.primaryInterface && state.primaryInterface != "" {
			info.Addresses = append(info.Addresses, ip)
			if ip.Primary {
				state.primaryEIP = addr.Allocation
			}
		} else if addr.Association == "" || addr.Instance != "" {
			info.AvailableIPs = append(info.AvailableIPs, ip)
		}
	}
	if screeningIPv4(v.IP) && state.primaryEIP == "" {
		info.Addresses = append(info.Addresses, IPAddress{Address: v.IP, Kind: "automatic", InterfaceID: state.primaryInterface, InstanceID: i.ID, Region: i.Region, Primary: true})
	}
	if screeningIPv4(v.IP) {
		info.ServiceIPv4 = []string{v.IP}
	}
	safeNIC := len(v.Interfaces) == 1 && v.Interfaces[0].ID != "" && v.Interfaces[0].Attachment.Index == 0 && len(v.Interfaces[0].Private) == 1 && v.Interfaces[0].Private[0].IP != "" && v.Interfaces[0].Private[0].Primary && (v.Interfaces[0].Type == "" || v.Interfaces[0].Type == "interface")
	root, disksComplete := false, len(v.Disks) > 0
	for _, disk := range v.Disks {
		if disk.Device == v.RootName && disk.Volume != "" {
			root = true
		}
		if disk.Volume == "" {
			disksComplete = false
		}
	}
	standard := safeNIC && root && disksComplete && v.RootType == "ebs" && v.Lifecycle == "" && v.State.Name == "running"
	reason := ""
	for _, tag := range v.Tags {
		if tag.Key == "aws:autoscaling:groupName" && tag.Value != "" {
			reason = "Auto Scaling 管理的实例可能被替换，需先移出伸缩组"
			standard = false
		}
	}
	switch {
	case reason != "":
	case v.Lifecycle != "":
		reason = "Spot 或特殊购买方式不支持该原机换 IP 操作"
	case v.RootType != "ebs" || !root || !disksComplete:
		reason = "根磁盘不是可核实的 EBS，不能安全停机"
	case !safeNIC:
		reason = "复杂 ENI 或附加私有 IPv4 配置需在云控制台处理"
	case v.State.Name != "running":
		reason = "实例当前不处于运行状态"
	}
	stopOK := standard && state.primaryEIP == "" && screeningIPv4(v.IP)
	stopReason := reason
	if state.primaryEIP != "" {
		stopReason = "当前使用 Elastic IP，停机启动不会替换该地址"
	}
	info.Methods = append(info.Methods,
		IPMethod{ID: "aws-stop-start", Name: "停机启动更换自动公网 IPv4", Available: stopOK, Reason: stopReason, RequiresStop: true, Description: "会中断服务；EBS 磁盘和实例保留，重新启动可能分配相同地址"},
		IPMethod{ID: "aws-eip-allocate", Name: "申请并关联新 Elastic IP", Available: standard, Reason: reason, ExtraCharge: true, Description: "新旧 Elastic IP 可能同时计费；旧分配保留"},
		IPMethod{ID: "aws-eip-associate", Name: "关联已有 Elastic IP", Available: standard && len(info.AvailableIPs) > 0, Reason: reason, ExtraCharge: true, Description: "从其他实例转移会中断其服务；旧分配保留"})
	if state.primaryEIP != "" {
		info.Warnings = append(info.Warnings, "现有 Elastic IP 保留且可能继续计费")
	}
	stable := v
	stable.IP, stable.State.Name = "", ""
	state.stable = fingerprint(struct {
		Account, Credentials, UserData string
		Instance                       awsIPRawInstance
	}{state.owner, fingerprint(a.c), state.dataHash, stable})
	info.Snapshot = fingerprint(struct {
		Stable, State, IP, EIP string
		Addresses              []awsIPAddress
	}{state.stable, v.State.Name, v.IP, state.primaryEIP, addresses.Items})
	return info, state, nil
}

func (a *AWS) IPInfo(ctx context.Context, i Instance) (IPInfo, error) {
	info, _, err := a.inspectIP(ctx, i)
	return info, err
}

func (a *AWS) BeginIPChange(ctx context.Context, i Instance, req IPChange, token string) (Operation, error) {
	info, state, err := a.inspectIP(ctx, i)
	if err != nil {
		return Operation{}, err
	}
	op, err := NewIPOperation(i, req, info, token)
	if err != nil {
		return op, err
	}
	if !strings.HasPrefix(req.Method, "aws-") {
		return op, &Error{Code: "AWS 换 IP 方式无效"}
	}
	if req.InterfaceID != "" && req.InterfaceID != state.primaryInterface {
		return op, &Error{Code: "所选 AWS 网卡与目标主网卡不一致"}
	}
	if req.Method != "aws-eip-associate" && (req.Address != "" || req.AllocationID != "" || req.SourceInstanceID != "") {
		return op, &Error{Code: "当前 AWS 换 IP 方式不接受已有 EIP 参数"}
	}
	if req.Method == "aws-eip-associate" {
		var chosen *awsIPAddress
		for n := range state.addresses {
			if state.addresses[n].Allocation == req.AllocationID {
				chosen = &state.addresses[n]
				break
			}
		}
		if chosen == nil || chosen.Domain != "vpc" || !screeningIPv4(chosen.IP) || chosen.IP == info.PrimaryIPv4 || req.Address != "" && req.Address != chosen.IP || chosen.Instance != req.SourceInstanceID || chosen.Association != "" && chosen.Instance == "" || chosen.Interface != "" && chosen.Instance == "" {
			return op, &Error{Code: "所选 EIP、来源实例或目标地址不匹配"}
		}
		op.IP.NewResourceID, op.IP.NewIP, op.IP.SourceInstanceID = chosen.Allocation, chosen.IP, chosen.Instance
	}
	op.IP.Checkpoint["stable"] = state.stable
	op.IP.Checkpoint["owner"] = state.owner
	op.IP.InterfaceID = state.primaryInterface
	op.IP.OldResourceID = state.primaryEIP
	op.IP.Resources = append(op.IP.Resources, RelatedResource{ID: i.ID, Kind: "instance", State: "existing-retained"})
	for _, d := range state.instance.Disks {
		op.IP.Resources = append(op.IP.Resources, RelatedResource{ID: d.Volume, Kind: "ebs-volume", State: "existing-retained"})
	}
	if state.primaryEIP != "" {
		op.IP.Resources = append(op.IP.Resources, RelatedResource{ID: state.primaryEIP, Kind: "elastic-ip", State: "existing-retained"})
	}
	return op, nil
}

func (a *AWS) AdvanceIPChange(ctx context.Context, op Operation) (Operation, bool, error) {
	return a.advanceIP(ctx, op, true)
}
func (a *AWS) ReconcileIPChange(ctx context.Context, op Operation) (Operation, bool, error) {
	return a.advanceIP(ctx, op, false)
}

func (a *AWS) advanceIP(ctx context.Context, op Operation, write bool) (Operation, bool, error) {
	if op.IP == nil || op.IP.TargetID != op.ResourceID || op.IP.Region != op.Region || !strings.HasPrefix(op.IP.Method, "aws-") {
		return op, false, &Error{Code: "AWS IP 操作账本无效"}
	}
	info, state, err := a.inspectIP(ctx, Instance{ID: op.ResourceID, Region: op.Region})
	if err != nil {
		return op, false, err
	}
	if state.stable != op.IP.Checkpoint["stable"] || state.owner != op.IP.Checkpoint["owner"] || state.primaryInterface != op.IP.InterfaceID {
		return op, false, &Error{Code: "AWS 实例、磁盘、网络或账号身份已变化，暂停后续操作", Uncertain: true}
	}
	if op.IP.Step == "completed" {
		return op, true, nil
	}
	if op.IP.Step == "validated" && info.Snapshot != op.IP.Snapshot {
		return op, false, &Error{Code: "AWS 换 IP 前配置已变化，请重新确认"}
	}
	switch op.IP.Method {
	case "aws-stop-start":
		switch op.IP.Step {
		case "validated":
			if !write {
				return op, false, nil
			}
			if err = ipCheckpoint(ctx, &op, "stopping", "正在停止实例", "stop-instance", op.ResourceID); err != nil {
				return op, false, err
			}
			return op, false, a.call(ctx, op.Region, "StopInstances", url.Values{"InstanceId.1": {op.ResourceID}}, nil)
		case "stopping":
			if info.State == "stopping" || info.State == "running" {
				return op, false, nil
			}
			if info.State != "stopped" {
				return op, false, &Error{Code: "AWS 停机后状态异常", Uncertain: true}
			}
			err = ipConfirmed(ctx, &op, "stopped", "实例已停止")
			return op, false, err
		case "stopped":
			if !write {
				return op, false, nil
			}
			if info.State != "stopped" || state.primaryEIP != "" {
				return op, false, &Error{Code: "AWS 开机前状态变化", Uncertain: true}
			}
			if err = ipCheckpoint(ctx, &op, "starting", "正在启动实例", "start-instance", op.ResourceID); err != nil {
				return op, false, err
			}
			return op, false, a.call(ctx, op.Region, "StartInstances", url.Values{"InstanceId.1": {op.ResourceID}}, nil)
		case "starting":
			if info.State == "pending" || info.State == "stopped" {
				return op, false, nil
			}
			if info.State != "running" || !screeningIPv4(info.PrimaryIPv4) || state.primaryEIP != "" {
				return op, false, &Error{Code: "AWS 启动后地址或状态无法核实", Uncertain: true}
			}
			return awsIPFinish(ctx, op, info)
		}
	case "aws-eip-allocate", "aws-eip-associate":
		if op.IP.Step == "validated" && op.IP.Method == "aws-eip-allocate" {
			if !write {
				return op, false, nil
			}
			op.IP.Resources = append(op.IP.Resources, RelatedResource{ID: "aws:eip:tag:narbe-job=" + op.IP.RequestID, Kind: "elastic-ip", State: "planned"})
			if err = ipCheckpoint(ctx, &op, "allocating", "正在申请 Elastic IP", "allocate-eip", op.IP.RequestID); err != nil {
				return op, false, err
			}
			var allocated struct {
				Allocation string `xml:"allocationId"`
				IP         string `xml:"publicIp"`
			}
			err = a.call(ctx, op.Region, "AllocateAddress", url.Values{"Domain": {"vpc"}, "TagSpecification.1.ResourceType": {"elastic-ip"}, "TagSpecification.1.Tag.1.Key": {"narbe-job"}, "TagSpecification.1.Tag.1.Value": {op.IP.RequestID}}, &allocated)
			if err != nil {
				return op, false, err
			}
			if !awsEIPID.MatchString(allocated.Allocation) || !screeningIPv4(allocated.IP) {
				return op, false, &Error{Code: "AWS EIP 申请响应缺少身份", Uncertain: true}
			}
			op.IP.NewResourceID, op.IP.NewIP = allocated.Allocation, allocated.IP
			return op, false, ipCheckpoint(ctx, &op, "allocating", "正在核实新 Elastic IP", "", "")
		}
		if op.IP.Step == "allocating" {
			matched := []awsIPAddress{}
			for _, address := range state.addresses {
				for _, tag := range address.Tags {
					if tag.Key == "narbe-job" && tag.Value == op.IP.RequestID {
						matched = append(matched, address)
					}
				}
			}
			if len(matched) == 0 {
				return op, false, nil
			}
			if len(matched) != 1 || matched[0].Association != "" || op.IP.NewResourceID != "" && matched[0].Allocation != op.IP.NewResourceID {
				return op, false, &Error{Code: "AWS 新 EIP 无法唯一核实，禁止再次申请", Uncertain: true}
			}
			if matched[0].Allocation == "" || matched[0].IP == "" {
				return op, false, nil
			}
			if !awsEIPID.MatchString(matched[0].Allocation) || !screeningIPv4(matched[0].IP) {
				return op, false, &Error{Code: "AWS 新 EIP 地址格式无效", Uncertain: true}
			}
			if op.IP.NewIP != "" && op.IP.NewIP != matched[0].IP {
				return op, false, &Error{Code: "AWS 新 EIP 地址与首次响应不一致", Uncertain: true}
			}
			op.IP.NewResourceID, op.IP.NewIP = matched[0].Allocation, matched[0].IP
			if op.IP.NewIP == op.IP.OldIP {
				return op, false, &Error{Code: "AWS 新 EIP 与旧地址相同，未关联", Uncertain: true}
			}
			for n := range op.IP.Resources {
				if op.IP.Resources[n].State == "planned" {
					op.IP.Resources[n].ID, op.IP.Resources[n].State = matched[0].Allocation, "allocated"
				}
			}
			err = ipConfirmed(ctx, &op, "allocated", "新 Elastic IP 已核实")
			return op, false, err
		}
		if op.IP.Step == "validated" || op.IP.Step == "allocated" {
			if !write {
				return op, false, nil
			}
			if info.PrimaryIPv4 != op.IP.OldIP || state.primaryEIP != op.IP.OldResourceID || info.State != "running" {
				return op, false, &Error{Code: "AWS 目标主 IP 或实例状态已变化", Uncertain: true}
			}
			var selected *awsIPAddress
			for n := range state.addresses {
				if state.addresses[n].Allocation == op.IP.NewResourceID {
					selected = &state.addresses[n]
					break
				}
			}
			if selected == nil || selected.IP != op.IP.NewIP || selected.IP == info.PrimaryIPv4 || selected.Instance != op.IP.SourceInstanceID || selected.Association != "" && selected.Instance == "" {
				return op, false, &Error{Code: "AWS 待关联 EIP 来源或地址已变化", Uncertain: true}
			}
			if op.IP.Method == "aws-eip-allocate" {
				if selected.Association != "" {
					return op, false, &Error{Code: "新 EIP 已被其他资源占用", Uncertain: true}
				}
			}
			if err = ipCheckpoint(ctx, &op, "associating", "正在关联 Elastic IP", "associate-eip", op.IP.NewResourceID); err != nil {
				return op, false, err
			}
			return op, false, a.call(ctx, op.Region, "AssociateAddress", url.Values{"AllocationId": {op.IP.NewResourceID}, "NetworkInterfaceId": {op.IP.InterfaceID}, "AllowReassociation": {"true"}}, nil)
		}
		if op.IP.Step == "associating" {
			for _, address := range state.addresses {
				if address.Allocation == op.IP.NewResourceID && address.Instance == op.ResourceID && address.Interface == op.IP.InterfaceID && address.Association != "" && address.IP == op.IP.NewIP && info.PrimaryIPv4 == address.IP {
					return awsIPFinish(ctx, op, info)
				}
			}
			return op, false, nil
		}
	}
	return op, false, &Error{Code: "AWS IP 操作阶段无效", Uncertain: true}
}

func awsIPFinish(ctx context.Context, op Operation, info IPInfo) (Operation, bool, error) {
	op.IP.NewIP, op.IP.After = info.PrimaryIPv4, &info
	op.IP.PrimaryChanged = op.IP.OldIP != info.PrimaryIPv4
	op.IP.AddressRepeated = !op.IP.PrimaryChanged
	if op.IP.OldResourceID != "" {
		op.IP.Notice = "旧 Elastic IP 分配已保留，可能继续计费"
	}
	if op.IP.Method == "aws-eip-allocate" {
		op.IP.Notice = "新 Elastic IP 可能计费；旧分配保留"
	}
	err := ipConfirmed(ctx, &op, "completed", "公网 IP 操作已核实")
	return op, err == nil, err
}

var _ IPProvider = (*AWS)(nil)
