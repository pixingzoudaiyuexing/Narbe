package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type azureIPState struct {
	vm, nic, oldPIP                map[string]any
	nicID, configID, oldPIPID      string
	vmStable, nicStable, pipStable string
	etag                           string
	available                      bool
	reason                         string
}

func azureIPClean(v any) any {
	return azureScreeningStable(v)
}

func azureIPNICStable(v map[string]any) string {
	raw, _ := json.Marshal(azureIPClean(v))
	var copy map[string]any
	_ = json.Unmarshal(raw, &copy)
	configs, _ := azProperties(copy)["ipConfigurations"].([]any)
	for _, config := range configs {
		p := azProperties(azObject(config))
		if id := azID(p["publicIPAddress"]); id != "" {
			p["publicIPAddress"] = map[string]any{"id": id}
		}
	}
	return fingerprint(copy)
}

func azureIPPIPStable(v map[string]any) string {
	raw, _ := json.Marshal(azureIPClean(v))
	var copy map[string]any
	_ = json.Unmarshal(raw, &copy)
	delete(azProperties(copy), "ipConfiguration")
	return fingerprint(copy)
}

func (a *Azure) inspectIP(ctx context.Context, i Instance) (IPInfo, azureIPState, error) {
	info, state := IPInfo{InstanceID: i.ID, Region: i.Region, Addresses: []IPAddress{}, AvailableIPs: []IPAddress{}, Methods: []IPMethod{}, Warnings: []string{}}, azureIPState{}
	if !a.screeningID(i.ID, "Microsoft.Compute/virtualMachines") || i.Region == "" {
		return info, state, &Error{Code: "Azure VM 身份或地区无效"}
	}
	var err error
	state.vm, err = a.screeningResource(ctx, i.ID, "2024-07-01&$expand=instanceView")
	if err != nil {
		return info, state, err
	}
	if !azSame(azString(state.vm["location"]), i.Region) {
		return info, state, &Error{Code: "Azure VM 地区响应不一致"}
	}
	vp := azProperties(state.vm)
	interfaces, ok := azObject(vp["networkProfile"])["networkInterfaces"].([]any)
	if !ok || len(interfaces) != 1 || !a.screeningID(azID(interfaces[0]), "Microsoft.Network/networkInterfaces") {
		state.reason = "VM 使用多个 NIC 或跨资源组 NIC，需单独迁移网络配置"
	} else {
		state.nicID = azID(interfaces[0])
	}
	if state.nicID != "" {
		state.nic, err = a.screeningResource(ctx, state.nicID, "2024-05-01")
		if err != nil {
			return info, state, err
		}
		if !azSame(azString(state.nic["location"]), i.Region) || !azSame(azID(azProperties(state.nic)["virtualMachine"]), i.ID) {
			return info, state, &Error{Code: "Azure NIC 与目标 VM 关联不一致"}
		}
		state.etag = azString(state.nic["etag"])
		configs, ok := azProperties(state.nic)["ipConfigurations"].([]any)
		if !ok || len(configs) != 1 {
			state.reason = "NIC 包含多个或未知 IP Configuration，需单独迁移"
		} else {
			config := azObject(configs[0])
			cp := azProperties(config)
			state.configID = azString(config["id"])
			state.oldPIPID = azID(cp["publicIPAddress"])
			if !azSame(state.configID, state.nicID+"/ipConfigurations/"+azString(config["name"])) || !a.screeningID(state.oldPIPID, "Microsoft.Network/publicIPAddresses") || azID(cp["subnet"]) == "" || cp["primary"] != true {
				state.reason = "NIC 主 IP、子网或 Public IP 关联无法核实"
			}
			for _, field := range []string{"loadBalancerBackendAddressPools", "loadBalancerInboundNatRules", "applicationGatewayBackendAddressPools", "gatewayLoadBalancer"} {
				if val := cp[field]; val != nil {
					if list, isList := val.([]any); !isList || len(list) != 0 {
						state.reason = "NIC 含负载均衡或网关绑定，需单独迁移"
					}
				}
			}
		}
	}
	if state.oldPIPID != "" {
		state.oldPIP, err = a.screeningResource(ctx, state.oldPIPID, "2024-05-01")
		if err != nil {
			return info, state, err
		}
		pp := azProperties(state.oldPIP)
		ip := azString(pp["ipAddress"])
		if !azSame(azString(state.oldPIP["location"]), i.Region) || !azSame(azID(pp["ipConfiguration"]), state.configID) || azID(pp["natGateway"]) != "" || azID(pp["publicIPPrefix"]) != "" || azID(pp["linkedPublicIPAddress"]) != "" || azString(pp["publicIPAllocationMethod"]) != "Static" || azString(pp["publicIPAddressVersion"]) != "IPv4" || azString(azObject(state.oldPIP["sku"])["name"]) != "Standard" {
			state.reason = "旧 Public IP 关联或规格不支持安全原机替换"
		}
		if screeningIPv4(ip) {
			info.PrimaryIPv4 = ip
			info.Addresses = append(info.Addresses, IPAddress{Address: ip, Kind: "public-ip", ResourceID: state.oldPIPID, InterfaceID: state.nicID, InstanceID: i.ID, Region: i.Region, Primary: true})
			info.ServiceIPv4 = []string{ip}
		} else {
			state.reason = "当前 Public IP 地址尚未分配"
		}
	}
	state.vmStable = fingerprint(azureIPClean(state.vm))
	if state.nic != nil {
		state.nicStable = azureIPNICStable(state.nic)
	}
	if state.oldPIP != nil {
		state.pipStable = azureIPPIPStable(state.oldPIP)
	}
	info.State = azString(vp["provisioningState"])
	for _, status := range func() []any { x, _ := azObject(vp["instanceView"])["statuses"].([]any); return x }() {
		if code := azString(azObject(status)["code"]); strings.HasPrefix(code, "PowerState/") {
			info.State = strings.TrimPrefix(code, "PowerState/")
		}
	}
	if state.reason == "" && info.State != "running" {
		state.reason = "VM 当前不处于运行状态"
	}
	if state.reason == "" && state.etag == "" {
		state.reason = "NIC 缺少 ETag，无法核对更新版本"
	}
	state.available = state.reason == ""
	info.Methods = append(info.Methods, IPMethod{ID: "azure-public-ip", Name: "替换 Public IP", Available: state.available, Reason: state.reason, ExtraCharge: true, Description: "创建新静态 Standard Public IP 并更新 NIC；旧 Public IP 保留且可能计费"})
	if state.oldPIPID != "" {
		info.Warnings = append(info.Warnings, "旧 Public IP 默认保留且可能继续计费")
	}
	info.Snapshot = fingerprint(struct{ Credentials, VM, NIC, PIP, IP, State string }{fingerprint(a.c), state.vmStable, state.nicStable, state.pipStable, info.PrimaryIPv4, info.State})
	return info, state, nil
}

func (a *Azure) IPInfo(ctx context.Context, i Instance) (IPInfo, error) {
	info, _, err := a.inspectIP(ctx, i)
	return info, err
}

// The Network RP does not document an atomic create-only guarantee for this
// header. A pre-read, deterministic ID, task tag and readback remain required.
func (a *Azure) createIPPIP(ctx context.Context, id string, body map[string]any) error {
	if !a.screeningID(id, "Microsoft.Network/publicIPAddresses") {
		return &Error{Code: "Azure 新 Public IP 身份无效"}
	}
	token, err := a.token(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", "https://management.azure.com"+id+"?api-version=2024-05-01", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-None-Match", "*")
	if err = beforeCloudWrite(ctx); err != nil {
		return err
	}
	res, err := a.h.Do(req)
	if err != nil {
		return &Error{Code: NetworkError(err), Uncertain: true}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		response, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return &Error{Status: res.StatusCode, Code: providerErrorReason(response, raw, token), Uncertain: res.StatusCode >= 200 && res.StatusCode < 300 || res.StatusCode >= 500 || res.StatusCode == 408}
	}
	return nil
}

func (a *Azure) BeginIPChange(ctx context.Context, i Instance, req IPChange, token string) (Operation, error) {
	info, state, err := a.inspectIP(ctx, i)
	if err != nil {
		return Operation{}, err
	}
	op, err := NewIPOperation(i, req, info, token)
	if err != nil {
		return op, err
	}
	if req.Method != "azure-public-ip" {
		return op, &Error{Code: "Azure 换 IP 方式无效"}
	}
	if req.Address != "" || req.AllocationID != "" || req.SourceInstanceID != "" || req.InterfaceID != "" && !azSame(req.InterfaceID, state.nicID) {
		return op, &Error{Code: "Azure 所选 Public IP 或网卡参数与方式不一致"}
	}
	newID := a.group() + "/providers/Microsoft.Network/publicIPAddresses/narbe-ip-" + digest([]byte(token + ":" + i.ID))[:24]
	op.IP.NewResourceID, op.IP.OldResourceID, op.IP.InterfaceID = newID, state.oldPIPID, state.nicID
	op.IP.Checkpoint["vm-stable"], op.IP.Checkpoint["nic-stable"], op.IP.Checkpoint["pip-stable"] = state.vmStable, state.nicStable, state.pipStable
	op.IP.Checkpoint["nic-etag"], op.IP.Checkpoint["config-id"] = state.etag, state.configID
	op.IP.Checkpoint["credentials"] = fingerprint(a.c)
	op.IP.Resources = append(op.IP.Resources, RelatedResource{ID: i.ID, Kind: "virtual-machine", State: "existing-retained"}, RelatedResource{ID: state.nicID, Kind: "network-interface", State: "existing-retained"}, RelatedResource{ID: state.oldPIPID, Kind: "public-ip", State: "existing-retained"}, RelatedResource{ID: newID, Kind: "public-ip", State: "planned"})
	return op, nil
}

func (a *Azure) AdvanceIPChange(ctx context.Context, op Operation) (Operation, bool, error) {
	return a.advanceIP(ctx, op, true)
}
func (a *Azure) ReconcileIPChange(ctx context.Context, op Operation) (Operation, bool, error) {
	return a.advanceIP(ctx, op, false)
}

func (a *Azure) advanceIP(ctx context.Context, op Operation, write bool) (Operation, bool, error) {
	if op.IP == nil || op.IP.Method != "azure-public-ip" || !a.screeningID(op.ResourceID, "Microsoft.Compute/virtualMachines") || op.IP.TargetID != op.ResourceID || op.IP.Region != op.Region || !a.screeningID(op.IP.NewResourceID, "Microsoft.Network/publicIPAddresses") {
		return op, false, &Error{Code: "Azure IP 操作账本无效"}
	}
	if op.IP.Checkpoint["credentials"] != fingerprint(a.c) {
		return op, false, &Error{Code: "Azure 账号凭据已变化，无法核实原操作", Uncertain: true}
	}
	if op.IP.Step == "completed" {
		return op, true, nil
	}
	info, state, err := a.inspectIP(ctx, Instance{ID: op.ResourceID, Region: op.Region})
	if err != nil {
		return op, false, err
	}
	if state.vmStable != op.IP.Checkpoint["vm-stable"] || state.nicID != op.IP.InterfaceID || state.configID != op.IP.Checkpoint["config-id"] {
		return op, false, &Error{Code: "Azure VM 或 NIC 身份已变化，暂停后续操作", Uncertain: true}
	}
	beforeSwap := op.IP.Step == "validated" || op.IP.Step == "creating" || op.IP.Step == "created"
	if beforeSwap && (state.nicStable != op.IP.Checkpoint["nic-stable"] || state.pipStable != op.IP.Checkpoint["pip-stable"] || !azSame(state.oldPIPID, op.IP.OldResourceID)) {
		return op, false, &Error{Code: "Azure NIC 或旧 Public IP 配置发生外部变化", Uncertain: true}
	}
	if op.IP.Step == "validated" {
		if info.Snapshot != op.IP.Snapshot {
			return op, false, &Error{Code: "Azure 换 IP 前配置已变化，请重新确认"}
		}
		var existing map[string]any
		err = a.call(ctx, "GET", op.IP.NewResourceID+"?api-version=2024-05-01", nil, &existing)
		if !screeningAbsent(err) {
			if err != nil {
				return op, false, err
			}
			return op, false, &Error{Code: "计划中的新 Public IP 已存在，拒绝覆盖", Uncertain: true}
		}
		if !write {
			return op, false, nil
		}
		if err = ipCheckpoint(ctx, &op, "creating", "正在创建新 Public IP", "create-public-ip", op.IP.NewResourceID); err != nil {
			return op, false, err
		}
		body := map[string]any{"location": op.Region, "tags": map[string]string{"narbe-job": op.IP.RequestID}, "sku": map[string]any{"name": "Standard"}, "properties": map[string]any{"publicIPAllocationMethod": "Static", "publicIPAddressVersion": "IPv4"}}
		if zones, ok := state.oldPIP["zones"]; ok {
			body["zones"] = zones
		}
		return op, false, a.createIPPIP(ctx, op.IP.NewResourceID, body)
	}
	var newPIP map[string]any
	if op.IP.Step == "creating" || op.IP.Step == "created" || op.IP.Step == "updating-nic" {
		newPIP, err = a.screeningResource(ctx, op.IP.NewResourceID, "2024-05-01")
		if err != nil {
			if screeningAbsent(err) && op.IP.Step == "creating" {
				return op, false, nil
			}
			return op, false, err
		}
		p := azProperties(newPIP)
		if !azTag(newPIP, op.IP.RequestID) || !azSame(azString(newPIP["location"]), op.Region) || azString(azObject(newPIP["sku"])["name"]) != "Standard" || azString(p["publicIPAllocationMethod"]) != "Static" || azString(p["publicIPAddressVersion"]) != "IPv4" || azID(p["natGateway"]) != "" || azID(p["publicIPPrefix"]) != "" {
			return op, false, &Error{Code: "Azure 新 Public IP 身份或规格不匹配", Uncertain: true}
		}
		if azString(p["provisioningState"]) != "Succeeded" || !screeningIPv4(azString(p["ipAddress"])) {
			return op, false, nil
		}
		if op.IP.NewIP != "" && op.IP.NewIP != azString(p["ipAddress"]) {
			return op, false, &Error{Code: "Azure 新 Public IP 与首次读回地址不一致", Uncertain: true}
		}
		op.IP.NewIP = azString(p["ipAddress"])
		if op.IP.NewIP == op.IP.OldIP {
			return op, false, &Error{Code: "Azure 新地址与旧地址重复，未更新 NIC", Uncertain: true}
		}
		if op.IP.Step == "creating" {
			if azID(p["ipConfiguration"]) != "" {
				return op, false, &Error{Code: "新 Public IP 已关联其他资源", Uncertain: true}
			}
			for n := range op.IP.Resources {
				if azSame(op.IP.Resources[n].ID, op.IP.NewResourceID) {
					op.IP.Resources[n].State = "verified-unattached"
				}
			}
			err = ipConfirmed(ctx, &op, "created", "新 Public IP 已核实")
			return op, false, err
		}
	}
	if op.IP.Step == "created" {
		if !write {
			return op, false, nil
		}
		if state.etag != op.IP.Checkpoint["nic-etag"] {
			return op, false, &Error{Code: "Azure NIC ETag 已变化，停止覆盖", Uncertain: true}
		}
		raw, _ := json.Marshal(state.nic)
		var nicBody map[string]any
		_ = json.Unmarshal(raw, &nicBody)
		configs := azProperties(nicBody)["ipConfigurations"].([]any)
		azProperties(azObject(configs[0]))["publicIPAddress"] = map[string]any{"id": op.IP.NewResourceID}
		op.IP.Checkpoint["expected-nic"] = azureIPNICStable(nicBody)
		if err = ipCheckpoint(ctx, &op, "updating-nic", "正在关联新 Public IP", "update-nic", op.IP.InterfaceID); err != nil {
			return op, false, err
		}
		return op, false, a.screeningNICPut(ctx, op.IP.InterfaceID, nicBody, state.etag)
	}
	if op.IP.Step == "updating-nic" {
		if azSame(state.oldPIPID, op.IP.OldResourceID) {
			if state.nicStable != op.IP.Checkpoint["nic-stable"] || state.pipStable != op.IP.Checkpoint["pip-stable"] {
				return op, false, &Error{Code: "Azure NIC 更新结果未确认且配置已变化", Uncertain: true}
			}
			return op, false, nil
		}
		oldPIP, oldErr := a.screeningResource(ctx, op.IP.OldResourceID, "2024-05-01")
		if oldErr != nil {
			return op, false, oldErr
		}
		if !azSame(state.oldPIPID, op.IP.NewResourceID) || state.nicStable != op.IP.Checkpoint["expected-nic"] || azureIPPIPStable(oldPIP) != op.IP.Checkpoint["pip-stable"] || info.PrimaryIPv4 != op.IP.NewIP {
			return op, false, &Error{Code: "Azure NIC 或 Public IP 回读与授权变更不一致", Uncertain: true}
		}
		oldLink, newLink := azID(azProperties(oldPIP)["ipConfiguration"]), azID(azProperties(newPIP)["ipConfiguration"])
		if azSame(oldLink, op.IP.Checkpoint["config-id"]) || newLink == "" {
			return op, false, nil
		}
		if oldLink != "" || !azSame(newLink, op.IP.Checkpoint["config-id"]) {
			return op, false, &Error{Code: "Azure Public IP 反向关联异常", Uncertain: true}
		}
		op.IP.After, op.IP.PrimaryChanged, op.IP.AddressRepeated = &info, info.PrimaryIPv4 != op.IP.OldIP, info.PrimaryIPv4 == op.IP.OldIP
		op.IP.Notice = "旧 Public IP 已保留，可能继续计费；请核实服务连通性"
		for n := range op.IP.Resources {
			if azSame(op.IP.Resources[n].ID, op.IP.NewResourceID) {
				op.IP.Resources[n].State = "verified-attached"
			}
		}
		err = ipConfirmed(ctx, &op, "completed", "新 Public IP 关联已核实")
		return op, err == nil, err
	}
	return op, false, &Error{Code: "Azure IP 操作阶段无效", Uncertain: true}
}

var _ IPProvider = (*Azure)(nil)
