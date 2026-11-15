package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type screeningAzureState struct {
	vm, nic, pip, nsg, disk               map[string]any
	nicID, pipID, nsgID, diskID, configID string
}

func azObject(v any) map[string]any                { m, _ := v.(map[string]any); return m }
func azString(v any) string                        { s, _ := v.(string); return s }
func azProperties(v map[string]any) map[string]any { return azObject(v["properties"]) }
func azID(v any) string                            { return azString(azObject(v)["id"]) }
func azSame(a, b string) bool                      { return a != "" && b != "" && strings.EqualFold(a, b) }
func azTag(v map[string]any, token string) bool {
	return azString(azObject(v["tags"])["narbe-job"]) == token
}
func (a *Azure) screeningResource(ctx context.Context, id, version string) (map[string]any, error) {
	var data map[string]any
	if err := a.call(ctx, "GET", id+"?api-version="+version, nil, &data); err != nil {
		return nil, err
	}
	if !azSame(azString(data["id"]), id) || azProperties(data) == nil {
		return nil, &Error{Code: "Azure 资源响应身份或属性缺失"}
	}
	return data, nil
}
func (a *Azure) screeningID(id, kind string) bool {
	prefix := a.group() + "/providers/" + kind + "/"
	if len(id) <= len(prefix) || !strings.EqualFold(id[:len(prefix)], prefix) {
		return false
	}
	return regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`).MatchString(id[len(prefix):])
}
func azureScreeningStable(v any) any {
	switch x := v.(type) {
	case map[string]any:
		y := map[string]any{}
		for k, child := range x {
			switch strings.ToLower(k) {
			case "etag", "provisioningstate", "instanceview", "statuses", "ipaddress":
				continue
			}
			y[k] = azureScreeningStable(child)
		}
		return y
	case []any:
		y := make([]any, len(x))
		for n := range x {
			y[n] = azureScreeningStable(x[n])
		}
		return y
	default:
		return v
	}
}

func (a *Azure) InspectScreening(ctx context.Context, s Spec, token string, op Operation) (ScreeningResource, error) {
	r, _, err := a.inspectScreeningAzure(ctx, s, token, op)
	return r, err
}
func (a *Azure) inspectScreeningAzure(ctx context.Context, s Spec, token string, op Operation) (ScreeningResource, screeningAzureState, error) {
	r := ScreeningResource{Method: "azure-public-ip", Resources: []RelatedResource{}}
	v := screeningAzureState{}
	if err := screeningIntent(s, token, op); err != nil {
		return r, v, err
	}
	plan := a.Plan(s, token)
	if !azSame(op.ResourceID, plan.ResourceID) || !a.screeningID(op.ResourceID, "Microsoft.Compute/virtualMachines") || len(op.Owned) < 4 {
		return r, v, &Error{Code: "Azure 自动筛选创建账本不可信，禁止更换 IP"}
	}
	for _, id := range plan.Owned {
		found := false
		for _, owned := range op.Owned {
			found = found || azSame(id, owned)
		}
		if !found {
			return r, v, &Error{Code: "Azure 原始创建附属账本不完整"}
		}
	}
	var err error
	v.vm, err = a.screeningResource(ctx, op.ResourceID, "2024-07-01&$expand=instanceView")
	if err != nil {
		return r, v, err
	}
	p := azProperties(v.vm)
	if !azTag(v.vm, token) || !azSame(azString(v.vm["location"]), s.Region) || azString(p["vmId"]) == "" || azString(azObject(p["hardwareProfile"])["vmSize"]) != s.Plan {
		return r, v, &Error{Code: "Azure VM 归属或配置不符合本轮创建账本"}
	}
	image := strings.Split(s.Image, "|")
	storage := azObject(p["storageProfile"])
	im := azObject(storage["imageReference"])
	if len(image) != 4 || azString(im["publisher"]) != image[0] || azString(im["offer"]) != image[1] || azString(im["sku"]) != image[2] || azString(im["version"]) != image[3] {
		return r, v, &Error{Code: "Azure 镜像配置已变化，禁止自动换 IP"}
	}
	dataDisks, ok := storage["dataDisks"].([]any)
	if storage["dataDisks"] != nil && !ok || len(dataDisks) > 0 {
		return r, v, &Error{Code: "Azure 存在附属数据盘或磁盘配置未知，禁止自动换 IP"}
	}
	v.diskID = azID(azObject(storage["osDisk"])["managedDisk"])
	interfaces, ok := azObject(p["networkProfile"])["networkInterfaces"].([]any)
	if !ok || len(interfaces) != 1 || !azSame(azID(interfaces[0]), plan.Owned[1]) || !azSame(v.diskID, plan.Owned[3]) {
		return r, v, &Error{Code: "Azure 磁盘或网卡身份不符合本轮创建账本"}
	}
	v.nicID, v.nsgID = plan.Owned[1], plan.Owned[2]
	v.nic, err = a.screeningResource(ctx, v.nicID, "2024-05-01")
	if err != nil {
		return r, v, err
	}
	np := azProperties(v.nic)
	if !azTag(v.nic, token) || !azSame(azString(v.nic["location"]), s.Region) || !azSame(azID(np["virtualMachine"]), op.ResourceID) || !azSame(azID(np["networkSecurityGroup"]), v.nsgID) || azString(v.nic["etag"]) == "" {
		return r, v, &Error{Code: "Azure 网卡归属、安全组或版本无法确认"}
	}
	configs, ok := np["ipConfigurations"].([]any)
	if !ok || len(configs) != 1 {
		return r, v, &Error{Code: "Azure 复杂 IP 配置不支持自动更换"}
	}
	c := azObject(configs[0])
	cp := azProperties(c)
	v.configID = azString(c["id"])
	v.pipID = azID(cp["publicIPAddress"])
	if !azSame(v.configID, v.nicID+"/ipConfigurations/"+azString(c["name"])) || !a.screeningID(v.pipID, "Microsoft.Network/publicIPAddresses") || !azSame(azID(cp["subnet"]), s.Network) {
		return r, v, &Error{Code: "Azure 公网 IP 配置或子网无法确认"}
	}
	ownedPIP := false
	for _, id := range op.Owned {
		ownedPIP = ownedPIP || azSame(id, v.pipID)
	}
	if !ownedPIP {
		return r, v, &Error{Code: "Azure 当前公网 IP 不在本次创建资源账本内"}
	}
	for _, field := range []string{"loadBalancerBackendAddressPools", "loadBalancerInboundNatRules", "applicationGatewayBackendAddressPools", "gatewayLoadBalancer"} {
		value := cp[field]
		if list, array := value.([]any); value != nil && (!array || len(list) > 0) {
			return r, v, &Error{Code: "Azure 公网 IP 配置包含其他网络绑定，已暂停"}
		}
	}
	v.pip, err = a.screeningResource(ctx, v.pipID, "2024-05-01")
	if err != nil {
		return r, v, err
	}
	pp := azProperties(v.pip)
	if !azTag(v.pip, token) || !azSame(azString(v.pip["location"]), s.Region) || azString(pp["publicIPAllocationMethod"]) != "Static" || azString(pp["publicIPAddressVersion"]) != "IPv4" || azString(azObject(v.pip["sku"])["name"]) != "Standard" || !azSame(azID(pp["ipConfiguration"]), v.configID) || azID(pp["natGateway"]) != "" || azID(pp["publicIPPrefix"]) != "" || azID(pp["linkedPublicIPAddress"]) != "" {
		return r, v, &Error{Code: "Azure 公网 IP 并非本轮独占静态 IPv4，禁止自动更换"}
	}
	v.nsg, err = a.screeningResource(ctx, v.nsgID, "2024-05-01")
	if err != nil {
		return r, v, err
	}
	if !azTag(v.nsg, token) || !azSame(azString(v.nsg["location"]), s.Region) {
		return r, v, &Error{Code: "Azure 安全组归属发生变化"}
	}
	v.disk, err = a.screeningResource(ctx, v.diskID, "2024-03-02")
	if err != nil {
		return r, v, err
	}
	if !azSame(azString(v.disk["managedBy"]), op.ResourceID) || !azSame(azString(v.disk["location"]), s.Region) {
		return r, v, &Error{Code: "Azure 系统盘归属发生变化"}
	}
	state := "unknown"
	for _, st := range func() []any { list, _ := azObject(p["instanceView"])["statuses"].([]any); return list }() {
		if code := azString(azObject(st)["code"]); strings.HasPrefix(code, "PowerState/") {
			state = strings.TrimPrefix(code, "PowerState/")
		}
	}
	if azString(p["provisioningState"]) != "Succeeded" {
		state = "provisioning"
	}
	r.Instance = Instance{ID: op.ResourceID, Name: azString(v.vm["name"]), Region: s.Region, State: state, Plan: s.Plan, Image: s.Image, IPs: []string{}}
	if ip := azString(pp["ipAddress"]); screeningIPv4(ip) {
		r.Instance.IPs = []string{ip}
	}
	for _, item := range []struct{ id, kind string }{{op.ResourceID, "instance"}, {v.pipID, "public-ip"}, {v.nicID, "network-interface"}, {v.nsgID, "network-security-group"}, {v.diskID, "managed-disk"}} {
		r.Resources = append(r.Resources, RelatedResource{ID: item.id, Kind: item.kind, State: "verified-present"})
	}
	r.Fingerprint = fingerprint(struct {
		Account                 string
		VM, NIC, PIP, NSG, Disk any
	}{fingerprint(a.c), azureScreeningStable(v.vm), azureScreeningStable(v.nic), azureScreeningStable(v.pip), azureScreeningStable(v.nsg), azureScreeningStable(v.disk)})
	return r, v, nil
}

// Azure's NIC REST contract has no documented atomic If-Match parameter. We send
// the observed ETag defensively, re-read immediately before PUT, preserve the
// entire NIC representation and verify the sole intended change afterwards.
// This is not a claim of an atomic lock against Azure portal changes.
func (a *Azure) screeningNICPut(ctx context.Context, id string, body map[string]any, etag string) error {
	if !a.screeningID(id, "Microsoft.Network/networkInterfaces") || etag == "" {
		return &Error{Code: "Azure 网卡更新缺少身份或 ETag"}
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
	req.Header.Set("If-Match", etag)
	if err = beforeCloudWrite(ctx); err != nil {
		return err
	}
	res, err := a.h.Do(req)
	if err != nil {
		return &Error{Code: NetworkError(err), Uncertain: true}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return &Error{Status: res.StatusCode, Code: providerErrorReason(raw, nil, token), Uncertain: res.StatusCode >= 500 || res.StatusCode == 408}
	}
	return nil
}

func (a *Azure) ReplaceScreeningIP(ctx context.Context, s Spec, token string, op Operation, expected string) (Operation, error) {
	r, v, err := a.inspectScreeningAzure(ctx, s, token, op)
	if err != nil {
		return op, err
	}
	op, err = screeningStart(op, r, expected)
	if err != nil {
		return op, err
	}
	if r.Instance.State != "running" || len(r.Instance.IPs) != 1 {
		return op, &Error{Code: "Azure VM 尚未就绪，未自动更换公网 IP"}
	}
	newID := a.group() + "/providers/Microsoft.Network/publicIPAddresses/narbe-" + digest([]byte(token + ":" + v.pipID))[:24]
	var existing map[string]any
	err = a.call(ctx, "GET", newID+"?api-version=2024-05-01", nil, &existing)
	if !screeningAbsent(err) {
		if err != nil {
			return op, err
		}
		return op, &Error{Code: "新 Azure 公网 IP 资源已存在，拒绝覆盖或重放", Uncertain: true}
	}
	op.Screening.OldPublicIPID, op.Screening.NewPublicIPID = v.pipID, newID
	op.Owned = append(append([]string{}, op.Owned...), newID)
	op.Ledger.Resources = append(op.Ledger.Resources, RelatedResource{ID: newID, Kind: "public-ip", State: "planned"})
	if err = screeningCheckpoint(ctx, &op, "creating-public-ip", "create-public-ip", newID); err != nil {
		return op, err
	}
	body := map[string]any{"location": s.Region, "tags": map[string]string{"narbe-job": token, "narbe-name": s.Name}, "sku": v.pip["sku"], "properties": map[string]any{"publicIPAllocationMethod": "Static", "publicIPAddressVersion": "IPv4"}}
	if zones, ok := v.pip["zones"]; ok {
		body["zones"] = zones
	}
	if err = a.call(ctx, "PUT", newID+"?api-version=2024-05-01", body, nil); err != nil {
		return op, err
	}
	var newPIP map[string]any
	err = screeningWait(ctx, func() (bool, error) {
		var e error
		newPIP, e = a.screeningResource(ctx, newID, "2024-05-01")
		if e != nil {
			return false, e
		}
		p := azProperties(newPIP)
		if !azTag(newPIP, token) || !azSame(azString(newPIP["location"]), s.Region) || azID(p["ipConfiguration"]) != "" {
			return false, &Error{Code: "新 Azure 公网 IP 归属或绑定异常", Uncertain: true}
		}
		return azString(p["provisioningState"]) == "Succeeded" && screeningIPv4(azString(p["ipAddress"])), nil
	})
	if err != nil {
		return op, screeningUncertain(err)
	}
	op.Ledger.Resources[len(op.Ledger.Resources)-1].State = "verified-present-unattached"
	if err = screeningConfirmed(ctx, &op, "public-ip-created"); err != nil {
		return op, err
	}
	// An external edit after the first read must stop before the NIC write.
	check, current, err := a.inspectScreeningAzure(ctx, s, token, op)
	if err != nil || check.Fingerprint != expected {
		return op, &Error{Code: "Azure 创建新 IP 后原实例配置变化；新 IP 仍可能计费，已暂停", Uncertain: true}
	}
	v = current
	var nicBody map[string]any
	raw, _ := json.Marshal(v.nic)
	_ = json.Unmarshal(raw, &nicBody)
	configs := azProperties(nicBody)["ipConfigurations"].([]any)
	azProperties(azObject(configs[0]))["publicIPAddress"] = map[string]any{"id": newID}
	if err = screeningCheckpoint(ctx, &op, "updating-nic", "associate-public-ip", v.nicID); err != nil {
		return op, err
	}
	if err = a.screeningNICPut(ctx, v.nicID, nicBody, azString(v.nic["etag"])); err != nil {
		return op, err
	}
	// Read the exact resource and compare all non-transient properties. A fresh
	// full Inspect also verifies VM/disk/NSG ownership and the new PIP backlink.
	err = screeningWait(ctx, func() (bool, error) {
		nic, e := a.screeningResource(ctx, v.nicID, "2024-05-01")
		if e != nil {
			return false, e
		}
		if azString(azProperties(nic)["provisioningState"]) != "Succeeded" {
			return false, nil
		}
		if fingerprint(azureScreeningStable(nic)) != fingerprint(azureScreeningStable(nicBody)) {
			return false, &Error{Code: "Azure 网卡更新后的配置与唯一授权变更不一致", Uncertain: true}
		}
		return true, nil
	})
	if err != nil {
		return op, screeningUncertain(err)
	}
	post, verified, err := a.inspectScreeningAzure(ctx, s, token, op)
	if err != nil || len(post.Instance.IPs) != 1 {
		return op, &Error{Code: "Azure 新公网 IP 绑定后未能完整核实；请核实账本资源", Uncertain: true}
	}
	for _, pair := range [][2]map[string]any{{v.vm, verified.vm}, {v.nsg, verified.nsg}, {v.disk, verified.disk}} {
		if fingerprint(azureScreeningStable(pair[0])) != fingerprint(azureScreeningStable(pair[1])) {
			return op, &Error{Code: "Azure 换 IP 期间 VM、系统盘或安全组发生其他变更，已暂停", Uncertain: true}
		}
	}
	if err = screeningConfirmed(ctx, &op, "public-ip-associated"); err != nil {
		return op, err
	}
	old, e := a.screeningResource(ctx, v.pipID, "2024-05-01")
	if e != nil || !azTag(old, token) || !azSame(azString(old["location"]), s.Region) || azID(azProperties(old)["ipConfiguration"]) != "" || azID(azProperties(old)["natGateway"]) != "" {
		return op, &Error{Code: "Azure 旧公网 IP 未确认独占且解绑，未删除；仍可能计费", Uncertain: true}
	}
	beforeOld, afterOld := azObject(azureScreeningStable(v.pip)), azObject(azureScreeningStable(old))
	delete(azProperties(beforeOld), "ipConfiguration")
	delete(azProperties(afterOld), "ipConfiguration")
	if fingerprint(beforeOld) != fingerprint(afterOld) {
		return op, &Error{Code: "Azure 旧公网 IP 配置已变化，未自动删除；请核实计费资源", Uncertain: true}
	}
	if err = screeningCheckpoint(ctx, &op, "deleting-old-public-ip", "delete-public-ip", v.pipID); err != nil {
		return op, err
	}
	if err = a.call(ctx, "DELETE", v.pipID+"?api-version=2024-05-01", nil, nil); err != nil {
		return op, err
	}
	err = screeningWait(ctx, func() (bool, error) {
		var old map[string]any
		e := a.call(ctx, "GET", v.pipID+"?api-version=2024-05-01", nil, &old)
		if screeningAbsent(e) {
			return true, nil
		}
		return false, e
	})
	if err != nil {
		return op, screeningUncertain(err)
	}
	for n := range op.Ledger.Resources {
		if azSame(op.Ledger.Resources[n].ID, v.pipID) {
			op.Ledger.Resources[n].State = "deleted-confirmed"
		}
		if azSame(op.Ledger.Resources[n].ID, newID) {
			op.Ledger.Resources[n].State = "verified-attached"
		}
	}
	op.Expected = "running"
	err = screeningConfirmed(ctx, &op, "completed")
	return op, err
}

var _ ScreeningProvider = (*Azure)(nil)
