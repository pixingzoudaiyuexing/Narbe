package cloud

import "context"

func (a *Azure) CleanupScreeningIP(ctx context.Context, op Operation, allowedID, ownerToken string) (Operation, bool, error) {
	if op.IP == nil || op.IP.Method != "azure-public-ip" || op.IP.Step != "completed" || op.IP.After == nil || op.IP.After.InstanceID != op.ResourceID || op.IP.After.Region != op.Region || op.IP.After.PrimaryIPv4 != op.IP.NewIP || op.IP.TargetID != op.ResourceID || op.IP.Region != op.Region || !screeningToken.MatchString(ownerToken) || !azSame(allowedID, op.IP.OldResourceID) || !a.screeningID(allowedID, "Microsoft.Network/publicIPAddresses") || !a.screeningID(op.IP.NewResourceID, "Microsoft.Network/publicIPAddresses") || !a.screeningID(op.IP.InterfaceID, "Microsoft.Network/networkInterfaces") || !a.screeningID(op.ResourceID, "Microsoft.Compute/virtualMachines") || azSame(allowedID, op.IP.NewResourceID) {
		return op, false, &Error{Code: "Azure 自动清理缺少可信操作和旧资源归属", Uncertain: true}
	}
	if op.IP.Checkpoint["cleanup-step"] == "deleted" {
		return op, true, nil
	}
	vm, err := a.screeningResource(ctx, op.ResourceID, "2024-07-01")
	if err != nil {
		return op, false, err
	}
	if !azSame(azString(vm["location"]), op.Region) {
		return op, false, &Error{Code: "Azure VM 地区已变化，旧 IP 保留", Uncertain: true}
	}
	vmNICs, _ := azObject(azProperties(vm)["networkProfile"])["networkInterfaces"].([]any)
	if len(vmNICs) != 1 || !azSame(azID(vmNICs[0]), op.IP.InterfaceID) {
		return op, false, &Error{Code: "Azure VM 网卡关联已变化，旧 IP 保留", Uncertain: true}
	}
	nic, err := a.screeningResource(ctx, op.IP.InterfaceID, "2024-05-01")
	if err != nil {
		return op, false, err
	}
	if !azSame(azString(nic["location"]), op.Region) || !azSame(azID(azProperties(nic)["virtualMachine"]), op.ResourceID) {
		return op, false, &Error{Code: "Azure NIC 与目标 VM 关联已变化，旧 IP 保留", Uncertain: true}
	}
	configs, _ := azProperties(nic)["ipConfigurations"].([]any)
	if len(configs) != 1 || !azSame(azString(azObject(configs[0])["id"]), op.IP.Checkpoint["config-id"]) || !azSame(azID(azProperties(azObject(configs[0]))["publicIPAddress"]), op.IP.NewResourceID) {
		return op, false, &Error{Code: "Azure 新 Public IP 已不在目标 NIC，旧 IP 保留", Uncertain: true}
	}
	newPIP, err := a.screeningResource(ctx, op.IP.NewResourceID, "2024-05-01")
	if err != nil {
		return op, false, err
	}
	newProps := azProperties(newPIP)
	if !azTag(newPIP, op.IP.RequestID) || !azSame(azString(newPIP["location"]), op.Region) || azString(azObject(newPIP["sku"])["name"]) != "Standard" || azString(newProps["publicIPAllocationMethod"]) != "Static" || azString(newProps["publicIPAddressVersion"]) != "IPv4" || !azSame(azID(newProps["ipConfiguration"]), op.IP.Checkpoint["config-id"]) || azString(newProps["ipAddress"]) != op.IP.NewIP || !screeningIPv4(op.IP.NewIP) {
		return op, false, &Error{Code: "Azure 新 Public IP 身份或地址已变化，旧 IP 保留", Uncertain: true}
	}
	var old map[string]any
	err = a.call(ctx, "GET", allowedID+"?api-version=2024-05-01", nil, &old)
	if screeningAbsent(err) {
		if op.IP.Checkpoint["cleanup-step"] != "deleting" {
			return op, false, &Error{Code: "Azure 旧 Public IP 在本次删除前已消失，不能证明清理归属", Uncertain: true}
		}
		for n := range op.IP.Resources {
			if azSame(op.IP.Resources[n].ID, allowedID) {
				op.IP.Resources[n].State = "deleted-confirmed"
			}
		}
		op.IP.Checkpoint["cleanup-step"] = "deleted"
		err = ipConfirmed(ctx, &op, "completed", "本轮旧 Public IP 删除已核实")
		return op, err == nil, err
	}
	if err != nil {
		return op, false, err
	}
	if !azSame(azString(old["id"]), allowedID) || !azSame(azString(old["location"]), op.Region) || !azTag(old, ownerToken) || azString(azObject(old["sku"])["name"]) != "Standard" || azString(azProperties(old)["publicIPAllocationMethod"]) != "Static" || azString(azProperties(old)["publicIPAddressVersion"]) != "IPv4" || azString(azProperties(old)["provisioningState"]) != "Succeeded" || azString(azProperties(old)["ipAddress"]) != op.IP.OldIP || azID(azProperties(old)["ipConfiguration"]) != "" || azID(azProperties(old)["natGateway"]) != "" || azID(azProperties(old)["publicIPPrefix"]) != "" || azID(azProperties(old)["linkedPublicIPAddress"]) != "" || azID(azProperties(old)["servicePublicIPAddress"]) != "" {
		return op, false, &Error{Code: "Azure 旧 Public IP 归属或未引用状态无法核实，保留资源", Uncertain: true}
	}
	if op.IP.Checkpoint["cleanup-step"] == "deleting" {
		return op, false, nil
	}
	if op.IP.Checkpoint["cleanup-step"] != "" {
		return op, false, &Error{Code: "Azure 旧 Public IP 清理账本阶段无效", Uncertain: true}
	}
	op.IP.Checkpoint["cleanup-step"] = "deleting"
	for n := range op.IP.Resources {
		if azSame(op.IP.Resources[n].ID, allowedID) {
			op.IP.Resources[n].State = "deleting"
		}
	}
	if err := ipCheckpoint(ctx, &op, "completed", "正在删除本轮旧 Public IP", "delete-old-public-ip", allowedID); err != nil {
		return op, false, err
	}
	return op, false, a.call(ctx, "DELETE", allowedID+"?api-version=2024-05-01", nil, nil)
}

var _ ScreeningIPCleaner = (*Azure)(nil)
