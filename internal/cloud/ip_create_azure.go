package cloud

import (
	"context"
	"errors"
	"net/netip"
	"strings"
)

func (a *Azure) BeginScreeningCreate(_ context.Context, s Spec, token string) (Operation, error) {
	if err := ValidateStartupText("azure", s); err != nil {
		return Operation{}, err
	}
	if s.UserData != "" && Family(s.Image) == "windows" {
		return Operation{}, errors.New("Azure Windows customData不会自动执行脚本")
	}
	subnetAt := strings.LastIndex(s.Network, "/subnets/")
	if !screeningToken.MatchString(token) || s.Region == "" || s.Plan == "" || len(strings.Split(s.Image, "|")) != 4 || s.Name == "" || s.Username == "" || subnetAt < 1 || !a.screeningID(s.Network[:max(0, subnetAt)], "Microsoft.Network/virtualNetworks") || s.Network[subnetAt+len("/subnets/"):] == "" {
		return Operation{}, &Error{Code: "Azure 自动创建配置、子网或任务身份无效"}
	}
	if prefix, err := netip.ParsePrefix(s.ManagementCIDR); err != nil || !prefix.Addr().Is4() {
		return Operation{}, &Error{Code: "Azure 管理网段无效"}
	}
	op := a.Plan(s, token)
	op.CreateStep = "planned"
	op.Ledger = &ResourceLedger{Status: "planned", SourceJobID: token, SourceState: fingerprint(s), Resources: []RelatedResource{}}
	for _, r := range []struct{ id, kind string }{{op.ResourceID, "virtual-machine"}, {op.Owned[0], "public-ip"}, {op.Owned[1], "network-interface"}, {op.Owned[2], "network-security-group"}, {op.Owned[3], "managed-disk"}} {
		op.Ledger.Resources = append(op.Ledger.Resources, RelatedResource{ID: r.id, Kind: r.kind, State: "planned"})
	}
	return op, nil
}

func (a *Azure) screeningCreateCheck(s Spec, token string, op Operation) error {
	if !screeningToken.MatchString(token) || op.CreateStep == "" || op.Ledger == nil || op.Ledger.SourceJobID != token || op.Ledger.SourceState != fingerprint(s) || op.Region != s.Region {
		return &Error{Code: "Azure 自动创建账本与当前任务配置不一致", Uncertain: true}
	}
	plan := a.Plan(s, token)
	if !azSame(op.ResourceID, plan.ResourceID) || len(op.Owned) != len(plan.Owned) || len(op.Ledger.Resources) != 5 {
		return &Error{Code: "Azure 自动创建计划资源身份不一致", Uncertain: true}
	}
	for n := range plan.Owned {
		if !azSame(op.Owned[n], plan.Owned[n]) {
			return &Error{Code: "Azure 自动创建附属资源身份不一致", Uncertain: true}
		}
	}
	for n, id := range []string{op.ResourceID, op.Owned[0], op.Owned[1], op.Owned[2], op.Owned[3]} {
		if !azSame(op.Ledger.Resources[n].ID, id) {
			return &Error{Code: "Azure 自动创建账本资源被修改", Uncertain: true}
		}
	}
	return nil
}

func azureCreateVersion(id string) string {
	if strings.Contains(id, "/Microsoft.Compute/virtualMachines/") {
		return "2024-07-01"
	}
	if strings.Contains(id, "/Microsoft.Compute/disks/") {
		return "2024-03-02"
	}
	return "2024-05-01"
}

func (a *Azure) screeningCreateAbsent(ctx context.Context, id string) (bool, error) {
	var data map[string]any
	err := a.call(ctx, "GET", id+"?api-version="+azureCreateVersion(id), nil, &data)
	if screeningAbsent(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func azureCreateMark(ctx context.Context, op *Operation, step string, resource int, state string) error {
	if _, ok := ctx.Value(linodeRecorderKey{}).(func(Operation) error); !ok {
		return &Error{Code: "Azure 自动创建缺少持久化账本，未发送云写"}
	}
	op.CreateStep = step
	op.Ledger.Status = "creating"
	if resource >= 0 {
		op.Ledger.Resources[resource].State = state
	}
	return recordOperation(ctx, *op)
}

func azureCreateBody(s Spec, op Operation, token, kind string) map[string]any {
	tags := map[string]string{"narbe-job": token, "narbe-name": s.Name}
	if kind == "pip" {
		return map[string]any{"location": s.Region, "tags": tags, "sku": map[string]string{"name": "Standard"}, "properties": map[string]any{"publicIPAllocationMethod": "Static", "publicIPAddressVersion": "IPv4"}}
	}
	if kind == "nsg" {
		port := "22"
		if Family(s.Image) == "windows" {
			port = "3389"
		}
		return map[string]any{"location": s.Region, "tags": tags, "properties": map[string]any{"securityRules": []any{map[string]any{"name": "management", "properties": map[string]any{"protocol": "Tcp", "sourcePortRange": "*", "destinationPortRange": port, "sourceAddressPrefix": s.ManagementCIDR, "destinationAddressPrefix": "*", "access": "Allow", "priority": 1000, "direction": "Inbound"}}}}}
	}
	if kind == "nic" {
		return map[string]any{"location": s.Region, "tags": tags, "properties": map[string]any{"networkSecurityGroup": map[string]string{"id": op.Owned[2]}, "ipConfigurations": []any{map[string]any{"name": "primary", "properties": map[string]any{"subnet": map[string]string{"id": s.Network}, "publicIPAddress": map[string]string{"id": op.Owned[0]}}}}}}
	}
	name := op.ResourceID[strings.LastIndex(op.ResourceID, "/")+1:]
	image := strings.Split(s.Image, "|")
	profile := map[string]any{"computerName": azureComputerName(name), "adminUsername": s.Username}
	if s.Password != "" {
		profile["adminPassword"] = s.Password
	}
	if Family(s.Image) != "windows" {
		profile["linuxConfiguration"] = map[string]any{"disablePasswordAuthentication": s.Password == "", "ssh": map[string]any{"publicKeys": []any{map[string]string{"path": "/home/" + s.Username + "/.ssh/authorized_keys", "keyData": s.SSHPublicKey}}}}
	}
	if s.UserData != "" {
		profile["customData"] = b64(s.UserData)
	}
	disk := map[string]any{"name": op.Owned[3][strings.LastIndex(op.Owned[3], "/")+1:], "createOption": "FromImage", "deleteOption": "Detach", "managedDisk": map[string]string{"storageAccountType": "StandardSSD_LRS"}}
	if s.Disk > 0 {
		disk["diskSizeGB"] = s.Disk
	}
	return map[string]any{"location": s.Region, "tags": tags, "properties": map[string]any{"hardwareProfile": map[string]string{"vmSize": s.Plan}, "storageProfile": map[string]any{"imageReference": map[string]string{"publisher": image[0], "offer": image[1], "sku": image[2], "version": image[3]}, "osDisk": disk}, "osProfile": profile, "networkProfile": map[string]any{"networkInterfaces": []any{map[string]any{"id": op.Owned[1], "properties": map[string]any{"primary": true, "deleteOption": "Detach"}}}}}}
}

func azureCreateResourceOK(r map[string]any, id, region, token, name, kind string, s Spec, op Operation) bool {
	if !azSame(azString(r["id"]), id) || !azSame(azString(r["location"]), region) || !azTag(r, token) || azString(azObject(r["tags"])["narbe-name"]) != name {
		return false
	}
	p := azProperties(r)
	if p == nil {
		return false
	}
	switch kind {
	case "pip":
		return azString(azObject(r["sku"])["name"]) == "Standard" && azString(p["publicIPAllocationMethod"]) == "Static" && azString(p["publicIPAddressVersion"]) == "IPv4"
	case "nsg":
		rules, _ := p["securityRules"].([]any)
		if len(rules) != 1 {
			return false
		}
		rule := azProperties(azObject(rules[0]))
		port := "22"
		if Family(s.Image) == "windows" {
			port = "3389"
		}
		return azString(azObject(rules[0])["name"]) == "management" && azString(rule["destinationPortRange"]) == port && azString(rule["sourceAddressPrefix"]) == s.ManagementCIDR && azString(rule["access"]) == "Allow"
	case "nic":
		if !azSame(azID(p["networkSecurityGroup"]), op.Owned[2]) {
			return false
		}
		configs, _ := p["ipConfigurations"].([]any)
		if len(configs) != 1 || azString(azObject(configs[0])["name"]) != "primary" {
			return false
		}
		cp := azProperties(azObject(configs[0]))
		return azSame(azID(cp["subnet"]), s.Network) && azSame(azID(cp["publicIPAddress"]), op.Owned[0])
	case "vm":
		if azString(azObject(p["hardwareProfile"])["vmSize"]) != s.Plan {
			return false
		}
		storage := azObject(p["storageProfile"])
		image := azObject(storage["imageReference"])
		parts := strings.Split(s.Image, "|")
		if len(parts) != 4 || azString(image["publisher"]) != parts[0] || azString(image["offer"]) != parts[1] || azString(image["sku"]) != parts[2] || azString(image["version"]) != parts[3] || !azSame(azID(azObject(storage["osDisk"])["managedDisk"]), op.Owned[3]) {
			return false
		}
		interfaces, _ := azObject(p["networkProfile"])["networkInterfaces"].([]any)
		return len(interfaces) == 1 && azSame(azID(interfaces[0]), op.Owned[1])
	}
	return false
}

func (a *Azure) AdvanceScreeningCreate(ctx context.Context, s Spec, token string, op Operation) (Operation, bool, error) {
	if err := a.screeningCreateCheck(s, token, op); err != nil {
		return op, false, err
	}
	if op.CreateStep == "complete" {
		return op, true, nil
	}
	if op.CreateStep == "planned" {
		if _, err := a.screeningResource(ctx, s.Network, "2024-05-01"); err != nil {
			return op, false, err
		}
		for _, id := range []string{op.ResourceID, op.Owned[0], op.Owned[1], op.Owned[2], op.Owned[3]} {
			absent, err := a.screeningCreateAbsent(ctx, id)
			if err != nil {
				return op, false, err
			}
			if !absent {
				return op, false, &Error{Code: "Azure 计划资源已存在，拒绝覆盖", Uncertain: true}
			}
		}
		if err := azureCreateMark(ctx, &op, "pip-put", 1, "dispatching"); err != nil {
			return op, false, err
		}
		return op, false, a.call(ctx, "PUT", op.Owned[0]+"?api-version=2024-05-01", azureCreateBody(s, op, token, "pip"), nil)
	}
	steps := map[string]struct {
		id, version, kind, next string
		index                   int
	}{
		"pip-put": {op.Owned[0], "2024-05-01", "pip", "pip-read", 1},
		"nsg-put": {op.Owned[2], "2024-05-01", "nsg", "nsg-read", 3},
		"nic-put": {op.Owned[1], "2024-05-01", "nic", "nic-read", 2},
		"vm-put":  {op.ResourceID, "2024-07-01&$expand=instanceView", "vm", "vm-poll", 0},
		"vm-poll": {op.ResourceID, "2024-07-01&$expand=instanceView", "vm", "vm-poll", 0},
	}
	if step, ok := steps[op.CreateStep]; ok {
		var r map[string]any
		err := a.call(ctx, "GET", step.id+"?api-version="+step.version, nil, &r)
		if screeningAbsent(err) {
			return op, false, nil
		}
		if err != nil {
			return op, false, err
		}
		if !azureCreateResourceOK(r, step.id, s.Region, token, s.Name, step.kind, s, op) {
			return op, false, &Error{Code: "Azure 新资源身份或配置与创建计划不一致", Uncertain: true}
		}
		state := azString(azProperties(r)["provisioningState"])
		if state == "Failed" || state == "Canceled" {
			return op, false, &Error{Code: "Azure 资源创建失败，保留计划资源供核实", Uncertain: true}
		}
		if state != "Succeeded" {
			return op, false, nil
		}
		if step.kind == "pip" && !screeningIPv4(azString(azProperties(r)["ipAddress"])) {
			return op, false, nil
		}
		if step.kind == "vm" {
			statuses, _ := azObject(azProperties(r)["instanceView"])["statuses"].([]any)
			running := false
			for _, st := range statuses {
				running = running || azString(azObject(st)["code"]) == "PowerState/running"
			}
			if !running {
				return op, false, nil
			}
			var disk map[string]any
			if err := a.call(ctx, "GET", op.Owned[3]+"?api-version=2024-03-02", nil, &disk); err != nil {
				return op, false, err
			}
			if !azSame(azString(disk["id"]), op.Owned[3]) || !azSame(azString(disk["location"]), s.Region) || !azSame(azString(disk["managedBy"]), op.ResourceID) {
				return op, false, &Error{Code: "Azure 系统盘身份未核实", Uncertain: true}
			}
			op.Ledger.Resources[4].State = "verified-attached"
			if err := azureCreateMark(ctx, &op, "complete", 0, "verified-present"); err != nil {
				return op, false, err
			}
			op.Ledger.Status = "verified"
			if err := recordOperation(ctx, op); err != nil {
				return op, false, err
			}
			return op, true, nil
		}
		return op, false, azureCreateMark(ctx, &op, step.next, step.index, "verified-present")
	}
	write := map[string]struct {
		id, kind, next, version string
		index                   int
	}{
		"pip-read": {op.Owned[2], "nsg", "nsg-put", "2024-05-01", 3},
		"nsg-read": {op.Owned[1], "nic", "nic-put", "2024-05-01", 2},
		"nic-read": {op.ResourceID, "vm", "vm-put", "2024-07-01", 0},
	}
	if step, ok := write[op.CreateStep]; ok {
		absent, err := a.screeningCreateAbsent(ctx, step.id)
		if err != nil {
			return op, false, err
		}
		if !absent {
			return op, false, &Error{Code: "Azure 下一计划资源已存在，拒绝覆盖", Uncertain: true}
		}
		if err := azureCreateMark(ctx, &op, step.next, step.index, "dispatching"); err != nil {
			return op, false, err
		}
		body := azureCreateBody(s, op, token, step.kind)
		if step.kind == "vm" {
			return op, false, a.submit(ctx, "PUT", step.id+"?api-version="+step.version, body, &op)
		}
		return op, false, a.call(ctx, "PUT", step.id+"?api-version="+step.version, body, nil)
	}
	return op, false, &Error{Code: "Azure 自动创建阶段无效", Uncertain: true}
}

var _ IncrementalCreateProvider = (*Azure)(nil)
