package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Azure struct {
	c         Credentials
	h         *http.Client
	mu        sync.Mutex
	access    string
	expires   time.Time
	pollMu    sync.Mutex
	notBefore map[string]time.Time
}

func (a *Azure) prefix() string { return "/subscriptions/" + Escape(a.c["subscriptionId"]) }
func (a *Azure) group() string  { return a.prefix() + "/resourceGroups/" + Escape(a.c["resourceGroup"]) }
func (a *Azure) token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Now().Before(a.expires) {
		return a.access, nil
	}
	v := url.Values{"grant_type": {"client_credentials"}, "client_id": {a.c["clientId"]}, "client_secret": {a.c["clientSecret"]}, "scope": {"https://management.azure.com/.default"}}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://login.microsoftonline.com/"+Escape(a.c["tenantId"])+"/oauth2/v2.0/token", strings.NewReader(v.Encode()))
	if e != nil {
		return "", e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, e := a.h.Do(req)
	if e != nil {
		return "", &Error{Code: "Azure 认证服务不可达"}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", &Error{Status: res.StatusCode, Code: "Azure 凭据无效或没有应用权限"}
	}
	var data struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if e = decodeReader(res.Body, &data); e != nil || data.Token == "" {
		return "", &Error{Code: "Azure 认证响应无效"}
	}
	a.access = data.Token
	a.expires = time.Now().Add(time.Duration(data.Expires-60) * time.Second)
	return a.access, nil
}
func (a *Azure) pathOK(path string) bool {
	u, e := url.Parse(path)
	if e != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(strings.ToLower(u.Path), strings.ToLower(a.prefix())+"/") {
		return false
	}
	return !strings.Contains(u.Path, "/../") && !strings.Contains(u.Path, "\\")
}
func (a *Azure) call(ctx context.Context, method, path string, body, out any) error {
	if !a.pathOK(path) {
		return &Error{Code: "拒绝越界的 Azure 资源路径"}
	}
	token, e := a.token(ctx)
	if e != nil {
		return e
	}
	return (rest{h: a.h, base: "https://management.azure.com", token: token}).call(ctx, method, path, body, out)
}
func (a *Azure) list(ctx context.Context, path string, out any) error {
	all := []jsonRaw{}
	for page := 0; page < 100; page++ {
		var res struct {
			Value []jsonRaw
			Next  string `json:"nextLink"`
		}
		if e := a.call(ctx, "GET", path, nil, &res); e != nil {
			return e
		}
		if res.Value == nil {
			return &Error{Code: "Azure 列表响应缺少数据，不能确认资源数量"}
		}
		all = append(all, res.Value...)
		if strings.Contains(path, "/virtualMachines?") && len(all) > InventoryLimit {
			return &Error{Code: "Azure 虚拟机数量超限，不能确认总数"}
		}
		if res.Next == "" {
			return decode(marshal(all), out)
		}
		u, e := url.Parse(res.Next)
		if e != nil || u.Scheme != "https" || u.Host != "management.azure.com" || u.User != nil {
			return &Error{Code: "Azure 分页链接越界"}
		}
		path = u.RequestURI()
		if !a.pathOK(path) {
			return &Error{Code: "Azure 分页订阅越界"}
		}
	}
	return &Error{Code: "Azure 分页超限"}
}

type azVM struct {
	ID, Name, Location string
	Properties         struct {
		ProvisioningState string
		HardwareProfile   struct{ VMSize string }
		StorageProfile    struct {
			OSDisk struct {
				DiskSizeGB int
				OSType     string
			}
			ImageReference struct{ Publisher, Offer, SKU, Version string }
		}
		NetworkProfile struct {
			Interfaces []struct{ ID string } `json:"networkInterfaces"`
		}
		InstanceView struct{ Statuses []struct{ Code string } }
	}
	Tags map[string]string
}

func (a *Azure) Catalog(ctx context.Context, region, plan string) (Catalog, error) {
	c := Catalog{}
	var regions []struct{ Name, DisplayName string }
	if e := a.list(ctx, a.prefix()+"/locations?api-version=2022-12-01", &regions); e != nil {
		return c, e
	}
	for _, r := range regions {
		c.Regions = append(c.Regions, Choice{ID: r.Name, Name: r.DisplayName})
	}
	if region == "" {
		return c, nil
	}
	if _, ok := Find(c.Regions, region); !ok {
		return c, &Error{Code: "Azure 地区不可用"}
	}
	var skus []struct {
		Name, ResourceType string
		Locations          []string
		Capabilities       []struct{ Name, Value string }
		Restrictions       []jsonRaw
	}
	if e := a.list(ctx, a.prefix()+"/providers/Microsoft.Compute/skus?api-version=2021-07-01&$filter="+url.QueryEscape("location eq '"+region+"'"), &skus); e != nil {
		return c, e
	}
	for _, s := range skus {
		if s.ResourceType != "virtualMachines" || !contains(s.Locations, region) || len(s.Restrictions) > 0 {
			continue
		}
		p := Choice{ID: s.Name, Name: s.Name, Architecture: "x86_64", Extra: map[string]string{}}
		for _, cap := range s.Capabilities {
			switch cap.Name {
			case "vCPUs":
				p.CPU = mustInt(cap.Value)
			case "MemoryGB":
				var gb float64
				fmt.Sscan(cap.Value, &gb)
				p.Memory = int(gb * 1024)
			case "CpuArchitectureType":
				p.Architecture = Arch(cap.Value)
			case "HyperVGenerations":
				p.Extra["hyperV"] = cap.Value
			}
		}
		c.Plans = append(c.Plans, p)
	}
	base := a.prefix() + "/providers/Microsoft.Compute/locations/" + Escape(region) + "/publishers/"
	for _, pub := range []string{"Canonical", "Debian", "MicrosoftWindowsServer"} {
		var offers []struct{ Name string }
		if e := a.call(ctx, "GET", base+pub+"/artifacttypes/vmimage/offers?api-version=2024-03-01", nil, &offers); e != nil {
			if ce, ok := e.(*Error); ok && ce.Status == 404 {
				continue
			}
			return c, e
		}
		for _, offer := range offers {
			o := strings.ToLower(offer.Name)
			if pub == "MicrosoftWindowsServer" && o != "windowsserver" {
				continue
			}
			if pub == "Canonical" && !strings.Contains(o, "ubuntu") {
				continue
			}
			var skus []struct{ Name string }
			if e := a.call(ctx, "GET", base+pub+"/artifacttypes/vmimage/offers/"+Escape(offer.Name)+"/skus?api-version=2024-03-01", nil, &skus); e != nil {
				return c, e
			}
			for _, sku := range skus {
				var im struct {
					Name       string
					Properties struct {
						Architecture, HyperVGeneration string
						OSDiskImage                    struct {
							OperatingSystem string
							SizeInGB        int `json:"sizeInGB"`
						}
						Plan                   any
						ImageDeprecationStatus struct{ ImageState string }
					}
				}
				path := base + pub + "/artifacttypes/vmimage/offers/" + Escape(offer.Name) + "/skus/" + Escape(sku.Name) + "/versions/latest?api-version=2024-03-01"
				if e := a.call(ctx, "GET", path, nil, &im); e != nil {
					if ce, ok := e.(*Error); ok && ce.Status == 404 {
						continue
					}
					return c, e
				}
				if im.Name == "" || im.Properties.Plan != nil || strings.EqualFold(im.Properties.ImageDeprecationStatus.ImageState, "Deprecated") {
					continue
				}
				arch := Arch(im.Properties.Architecture)
				if arch == "" {
					continue
				}
				if p, ok := Find(c.Plans, plan); ok {
					if arch != p.Architecture {
						continue
					}
					if hv := p.Extra["hyperV"]; hv != "" && !strings.Contains(hv, im.Properties.HyperVGeneration) {
						continue
					}
				}
				c.Images = append(c.Images, Choice{ID: pub + "|" + offer.Name + "|" + sku.Name + "|" + im.Name, Name: offer.Name + " / " + sku.Name + " / " + im.Name, Architecture: arch, Family: Family(pub + offer.Name), Disk: im.Properties.OSDiskImage.SizeInGB, Extra: map[string]string{"hyperV": im.Properties.HyperVGeneration}})
			}
		}
	}
	var nets []struct {
		ID, Name, Location string
		Properties         struct{ Subnets []struct{ ID, Name string } }
	}
	if e := a.list(ctx, a.group()+"/providers/Microsoft.Network/virtualNetworks?api-version=2024-05-01", &nets); e != nil {
		return c, e
	}
	for _, n := range nets {
		if n.Location == region {
			for _, s := range n.Properties.Subnets {
				c.Networks = append(c.Networks, Choice{ID: s.ID, Name: n.Name + " / " + s.Name})
			}
		}
	}
	return c, nil
}
func (a *Azure) Instances(ctx context.Context, region string) ([]Instance, error) {
	var data []azVM
	if e := a.list(ctx, a.group()+"/providers/Microsoft.Compute/virtualMachines?api-version=2024-07-01", &data); e != nil {
		return nil, e
	}
	out := []Instance{}
	for _, v := range data {
		if region != "" && region != v.Location {
			continue
		}
		i, e := a.hydrateVM(ctx, v, false)
		if e != nil {
			return nil, e
		}
		out = append(out, i)
	}
	return out, nil
}

// optionalNetwork is used only by the display inventory. Fresh mutation preflight
// always passes false and retains the previous strict error behavior.
func (a *Azure) hydrateVM(ctx context.Context, v azVM, optionalNetwork bool) (Instance, error) {
	var full azVM
	if e := a.call(ctx, "GET", v.ID+"?$expand=instanceView&api-version=2024-07-01", nil, &full); e != nil {
		return Instance{}, e
	}
	state := strings.ToLower(full.Properties.ProvisioningState)
	for _, s := range full.Properties.InstanceView.Statuses {
		if strings.HasPrefix(s.Code, "PowerState/") {
			state = strings.TrimPrefix(s.Code, "PowerState/")
		}
	}
	i := Instance{ID: v.ID, Name: v.Name, Region: v.Location, State: state, Plan: v.Properties.HardwareProfile.VMSize, Image: v.Properties.StorageProfile.ImageReference.Offer + " / " + v.Properties.StorageProfile.ImageReference.SKU, IPs: []string{}, Actions: Actions(state), Disk: v.Properties.StorageProfile.OSDisk.DiskSizeGB, DiskKind: "Managed Disk 系统盘"}
	if state == "running" {
		i.Actions = append(i.Actions, "poweroff")
	}
	for _, nic := range v.Properties.NetworkProfile.Interfaces {
		var n struct {
			Properties struct {
				Configurations []struct {
					Properties struct {
						Public struct{ ID string } `json:"publicIPAddress"`
					}
				} `json:"ipConfigurations"`
			}
		}
		if e := a.call(ctx, "GET", nic.ID+"?api-version=2024-05-01", nil, &n); e != nil {
			if optionalNetwork {
				i.MetadataIncomplete = true
				continue
			}
			return Instance{}, e
		}
		for _, conf := range n.Properties.Configurations {
			if conf.Properties.Public.ID != "" {
				var ip struct {
					Properties struct {
						Address string `json:"ipAddress"`
					}
				}
				if e := a.call(ctx, "GET", conf.Properties.Public.ID+"?api-version=2024-05-01", nil, &ip); e != nil {
					if optionalNetwork {
						i.MetadataIncomplete = true
						continue
					}
					return Instance{}, e
				}
				if ip.Properties.Address != "" {
					i.IPs = append(i.IPs, ip.Properties.Address)
				}
			}
		}
	}
	return i, nil
}

func (a *Azure) waitResource(ctx context.Context, id, version string) error {
	for n := 0; n < 180; n++ {
		var r struct {
			Properties struct {
				State string `json:"provisioningState"`
			}
		}
		if e := a.call(ctx, "GET", id+"?api-version="+version, nil, &r); e != nil {
			return e
		}
		if r.Properties.State == "Succeeded" {
			return nil
		}
		if r.Properties.State == "Failed" || r.Properties.State == "Canceled" {
			return &Error{Code: "Azure 资源配置失败", Uncertain: true}
		}
		select {
		case <-ctx.Done():
			return &Error{Code: "操作等待中断", Uncertain: true}
		case <-time.After(3 * time.Second):
		}
	}
	return &Error{Code: "Azure 配置等待超时", Uncertain: true}
}
func (a *Azure) Create(ctx context.Context, s Spec, token string) (Operation, error) {
	op := a.Plan(s, token)
	vmid := op.ResourceID
	name := vmid[strings.LastIndex(vmid, "/")+1:]
	pip, nic, nsg := op.Owned[0], op.Owned[1], op.Owned[2]
	tags := map[string]string{"narbe-job": token, "narbe-name": s.Name}
	body := map[string]any{"location": s.Region, "tags": tags, "sku": map[string]string{"name": "Standard"}, "properties": map[string]any{"publicIPAllocationMethod": "Static", "publicIPAddressVersion": "IPv4"}}
	for _, id := range []string{vmid, pip, nic, nsg, op.Owned[3]} {
		var existing struct{ Tags map[string]string }
		version := "2024-05-01"
		if id == vmid {
			version = "2024-07-01"
		}
		if id == op.Owned[3] {
			version = "2024-03-02"
		}
		e := a.call(ctx, "GET", id+"?api-version="+version, nil, &existing)
		if e == nil {
			return op, &Error{Code: "目标资源已存在，拒绝覆盖", Uncertain: true}
		}
		ce, ok := e.(*Error)
		if !ok || ce.Status != 404 {
			return op, e
		}
	}
	if e := a.call(ctx, "PUT", pip+"?api-version=2024-05-01", body, nil); e != nil {
		return op, e
	}
	if e := a.waitResource(ctx, pip, "2024-05-01"); e != nil {
		return op, e
	}
	port := "22"
	if Family(s.Image) == "windows" {
		port = "3389"
	}
	nsgBody := map[string]any{"location": s.Region, "tags": tags, "properties": map[string]any{"securityRules": []any{map[string]any{"name": "management", "properties": map[string]any{"protocol": "Tcp", "sourcePortRange": "*", "destinationPortRange": port, "sourceAddressPrefix": s.ManagementCIDR, "destinationAddressPrefix": "*", "access": "Allow", "priority": 1000, "direction": "Inbound"}}}}}
	if e := a.call(ctx, "PUT", nsg+"?api-version=2024-05-01", nsgBody, nil); e != nil {
		if errors.Is(e, ErrDispatchExpired) {
			return op, e
		}
		return op, &Error{Code: "安全组创建未完成，请核实关联资源", Uncertain: true}
	}
	if e := a.waitResource(ctx, nsg, "2024-05-01"); e != nil {
		return op, e
	}
	body = map[string]any{"location": s.Region, "tags": tags, "properties": map[string]any{"networkSecurityGroup": map[string]string{"id": nsg}, "ipConfigurations": []any{map[string]any{"name": "primary", "properties": map[string]any{"subnet": map[string]string{"id": s.Network}, "publicIPAddress": map[string]string{"id": pip}}}}}}
	if e := a.call(ctx, "PUT", nic+"?api-version=2024-05-01", body, nil); e != nil {
		return op, &Error{Code: "网卡创建未完成；请检查任务记录中的关联资源", Uncertain: true}
	}
	if e := a.waitResource(ctx, nic, "2024-05-01"); e != nil {
		return op, e
	}
	image := strings.Split(s.Image, "|")
	if len(image) != 4 {
		return op, &Error{Code: "镜像标识无效", Uncertain: true}
	}
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
	body = map[string]any{"location": s.Region, "tags": tags, "properties": map[string]any{"hardwareProfile": map[string]string{"vmSize": s.Plan}, "storageProfile": map[string]any{"imageReference": map[string]string{"publisher": image[0], "offer": image[1], "sku": image[2], "version": image[3]}, "osDisk": disk}, "osProfile": profile, "networkProfile": map[string]any{"networkInterfaces": []any{map[string]any{"id": nic, "properties": map[string]any{"primary": true, "deleteOption": "Detach"}}}}}}
	if e := a.submit(ctx, "PUT", vmid+"?api-version=2024-07-01", body, &op); e != nil {
		return op, &Error{Code: "虚拟机创建未完成；请核实关联资源", Uncertain: true}
	}
	return op, nil
}
func (a *Azure) Action(ctx context.Context, i Instance, action, token string) (Operation, error) {
	op := Operation{ResourceID: i.ID, Region: i.Region, Expected: "running"}
	if !strings.HasPrefix(strings.ToLower(i.ID), strings.ToLower(a.group()+"/providers/Microsoft.Compute/virtualMachines/")) {
		return op, &Error{Code: "实例不属于当前资源组"}
	}
	if action == "delete" {
		op.Expected = "deleted"
		e := a.submit(ctx, "DELETE", i.ID+"?api-version=2024-07-01", nil, &op)
		return op, e
	}
	act := map[string]string{"start": "start", "stop": "deallocate", "reboot": "restart", "poweroff": "powerOff"}[action]
	if act == "" {
		return op, &Error{Code: "不支持的操作"}
	}
	if action == "stop" {
		op.Expected = "deallocated"
	}
	if action == "poweroff" {
		op.Expected = "stopped"
	}
	e := a.submit(ctx, "POST", i.ID+"/"+act+"?api-version=2024-07-01", map[string]any{}, &op)
	if action == "reboot" && op.PollURL == "" {
		op.Expected = "accepted"
	}
	return op, e
}
func (a *Azure) Poll(ctx context.Context, op Operation) (bool, error) {
	if op.Expected == "accepted" {
		return true, nil
	}
	if op.PollURL != "" {
		done, e := a.pollOperation(ctx, op)
		if e != nil || !done {
			return false, e
		}
	}

	var vm azVM
	e := a.call(ctx, "GET", op.ResourceID+"?$expand=instanceView&api-version=2024-07-01", nil, &vm)
	if e != nil {
		if ce, ok := e.(*Error); ok && ce.Status == 404 && op.Expected == "deleted" {
			return true, nil
		}
		return false, e
	}
	if vm.Properties.ProvisioningState == "Failed" {
		return false, &Error{Code: "Azure 虚拟机配置失败"}
	}
	for _, s := range vm.Properties.InstanceView.Statuses {
		if s.Code == "PowerState/"+op.Expected && vm.Properties.ProvisioningState == "Succeeded" {
			return true, nil
		}
	}
	return false, nil
}

func (a *Azure) submit(ctx context.Context, method, path string, body any, op *Operation) error {
	if !a.pathOK(path) {
		return &Error{Code: "Azure 资源路径越界"}
	}
	token, e := a.token(ctx)
	if e != nil {
		return e
	}
	headers, e := (rest{h: a.h, base: "https://management.azure.com", token: token}).callHeaders(ctx, method, path, body, nil)
	if e != nil {
		return e
	}
	poll := headers.Get("Azure-AsyncOperation")
	op.PollKind = "async"
	if poll == "" {
		poll = headers.Get("Location")
		op.PollKind = "location"
	}
	if poll != "" {
		u, e := url.Parse(poll)
		if e != nil || u.Scheme != "https" || u.Host != "management.azure.com" || u.User != nil || !a.pathOK(u.RequestURI()) {
			return &Error{Code: "Azure 异步状态链接越界", Uncertain: true}
		}
		op.PollURL = u.RequestURI()
		op.NextPoll = time.Now().Add(retryDelay(headers)).Unix()
	}
	return nil
}

func (a *Azure) Plan(s Spec, token string) Operation {
	name := s.Name[:min(len(s.Name), 40)] + "-" + strings.ToLower(digest([]byte(token))[:16])
	suffix := strings.ToLower(digest([]byte(token))[:16])
	return Operation{ResourceID: a.group() + "/providers/Microsoft.Compute/virtualMachines/" + name, Region: s.Region, Expected: "running", Owned: []string{a.group() + "/providers/Microsoft.Network/publicIPAddresses/narbe-" + suffix, a.group() + "/providers/Microsoft.Network/networkInterfaces/narbe-" + suffix, a.group() + "/providers/Microsoft.Network/networkSecurityGroups/narbe-" + suffix, a.group() + "/providers/Microsoft.Compute/disks/narbe-" + suffix}}
}

func retryDelay(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if n, e := strconv.Atoi(v); e == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(v); e == nil && t.After(time.Now()) {
		return time.Until(t)
	}
	return 0
}
func (a *Azure) pollOperation(ctx context.Context, op Operation) (bool, error) {
	if !a.pathOK(op.PollURL) {
		return false, &Error{Code: "Azure 异步轮询路径越界"}
	}
	a.pollMu.Lock()
	next := time.Unix(op.NextPoll, 0)
	if t, ok := a.notBefore[op.PollURL]; ok {
		next = t
	}
	a.pollMu.Unlock()
	if delay := time.Until(next); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
		}
	}
	token, e := a.token(ctx)
	if e != nil {
		return false, e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", "https://management.azure.com"+op.PollURL, nil)
	if e != nil {
		return false, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, e := a.h.Do(req)
	if e != nil {
		return false, &Error{Code: "Azure 操作状态查询中断"}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return false, &Error{Status: res.StatusCode, Code: errorCategory(raw)}
	}
	a.pollMu.Lock()
	if a.notBefore == nil {
		a.notBefore = map[string]time.Time{}
	}
	a.notBefore[op.PollURL] = time.Now().Add(retryDelay(res.Header))
	a.pollMu.Unlock()
	if op.PollKind == "location" {
		return res.StatusCode == 200 || res.StatusCode == 204, nil
	}
	var status struct{ Status string }
	if decode(raw, &status) != nil {
		return false, &Error{Code: "Azure 异步状态响应无效"}
	}
	switch status.Status {
	case "Succeeded":
		return true, nil
	case "Failed", "Canceled":
		return false, &Error{Code: "Azure 异步操作失败或已取消"}
	default:
		return false, nil
	}
}

// MatchesCreation validates a ledger against the current account scope and VM
// task tag. A same-name VM rebuilt outside this task must not inherit its ledger.
func (a *Azure) MatchesCreation(ctx context.Context, s Spec, id string, prior Operation, target string) bool {
	expected := a.Plan(s, id)
	if !strings.EqualFold(prior.ResourceID, expected.ResourceID) || !strings.EqualFold(target, expected.ResourceID) || !strings.EqualFold(prior.Region, s.Region) || len(prior.Owned) != len(expected.Owned) {
		return false
	}
	for n := range expected.Owned {
		if !strings.EqualFold(prior.Owned[n], expected.Owned[n]) {
			return false
		}
	}
	var vm azVM
	if a.call(ctx, "GET", target+"?api-version=2024-07-01", nil, &vm) != nil {
		return false
	}
	return strings.EqualFold(vm.ID, target) && strings.EqualFold(vm.Location, s.Region) && vm.Tags["narbe-job"] == id
}

func azureComputerName(name string) string {
	computer := strings.Trim(name[:min(len(name), 15)], "-")
	if computer == "" {
		return "narbe"
	}
	numeric := true
	for _, ch := range computer {
		if ch < '0' || ch > '9' {
			numeric = false
			break
		}
	}
	if numeric {
		computer = "n" + computer[:min(len(computer), 14)]
	}
	return computer
}
