package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (l *Linode) InspectScreening(ctx context.Context, s Spec, token string, op Operation) (ScreeningResource, error) {
	r := ScreeningResource{Method: "delete-recreate", Resources: []RelatedResource{}}
	if err := screeningIntent(s, token, op); err != nil {
		return r, err
	}
	if err := linodeID(op.ResourceID); err != nil {
		return r, err
	}
	var owner struct {
		linInstance
		Tags []string `json:"tags"`
	}
	if err := l.call(ctx, "GET", "/linode/instances/"+op.ResourceID, nil, &owner); err != nil {
		return r, err
	}
	if fmt.Sprint(owner.ID) != op.ResourceID || owner.Region != s.Region || owner.Type != s.Plan || owner.Image != s.Image || !contains(owner.Tags, "narbe-"+token) {
		return r, &Error{Code: "Linode 不属于本次创建任务或配置已变化，禁止自动删除"}
	}
	st, err := l.state(ctx, op.ResourceID)
	if err != nil {
		return r, err
	}
	if st.Instance.Region != s.Region || st.Instance.Plan != s.Plan || st.Instance.Image != s.Image || st.Instance.Name != owner.Label || len(st.Volumes) > 0 || len(st.Locks) > 0 || st.Managed || st.PlacementGroup || len(st.Disks) == 0 || len(st.Configs) != 1 {
		return r, &Error{Code: "Linode 存在变更、附属卷或复杂配置，已暂停自动删除"}
	}
	if st.Generation != "legacy_config" {
		return r, &Error{Code: "自动筛选仅删除本轮创建的标准旧式网络 Linode，接口代际已变化"}
	}
	for _, config := range st.Configs {
		for _, device := range config.Devices {
			if device.VolumeID != 0 {
				return r, &Error{Code: "Linode 已附加外部磁盘，禁止自动删除"}
			}
		}
		for _, i := range config.Interfaces {
			if i.Purpose != "public" || i.VPCID != 0 || i.Label != "" {
				return r, &Error{Code: "Linode 网络含 VPC/VLAN 或未知接口，禁止自动删除"}
			}
		}
	}
	var ips struct {
		IPv4 struct {
			Public []struct {
				Address  string `json:"address"`
				ID       int    `json:"linode_id"`
				Region   string `json:"region"`
				Public   *bool  `json:"public"`
				Reserved *bool  `json:"reserved"`
			} `json:"public"`
			Reserved []jsonRaw `json:"reserved"`
			Shared   []jsonRaw `json:"shared"`
			VPC      []jsonRaw `json:"vpc"`
		} `json:"ipv4"`
	}
	if err = l.call(ctx, "GET", "/linode/instances/"+op.ResourceID+"/ips", nil, &ips); err != nil {
		return r, err
	}
	if len(ips.IPv4.Public) != 1 || len(ips.IPv4.Reserved) != 0 || len(ips.IPv4.Shared) != 0 || len(ips.IPv4.VPC) != 0 {
		return r, &Error{Code: "Linode 不是单一普通公网 IPv4，禁止自动删除"}
	}
	ip := ips.IPv4.Public[0]
	if ip.Public == nil || !*ip.Public || ip.Reserved == nil || *ip.Reserved || fmt.Sprint(ip.ID) != op.ResourceID || ip.Region != s.Region || !screeningIPv4(ip.Address) || !contains(owner.IPv4, ip.Address) {
		return r, &Error{Code: "Linode 公网 IPv4 归属或非保留地址无法确认，禁止自动删除"}
	}
	// A firewall created by the create task must not leak each time a VM is
	// recreated. Reuse of a user-owned existing firewall is safe (never delete it).
	for _, id := range op.Owned {
		if id != "linode:"+op.ResourceID {
			return r, &Error{Code: "本轮 Linode 创建了独立收费或共享附属资源，请先人工核实，未自动删除"}
		}
	}
	bindings, err := l.attachedFirewalls(ctx, op.ResourceID, 0)
	if err != nil {
		return r, err
	}
	r.Instance = st.Instance
	r.Instance.IPs = []string{ip.Address}
	r.Resources = append(r.Resources, RelatedResource{ID: "linode:" + op.ResourceID, Kind: "instance", State: st.Instance.State})
	r.Fingerprint = fingerprint(struct {
		State, Tags, Bindings any
		Account               string
	}{st.Snapshot, owner.Tags, bindings, fingerprint(l.token)})
	return r, nil
}

func (l *Linode) ReplaceScreeningIP(ctx context.Context, s Spec, token string, op Operation, expected string) (Operation, error) {
	r, err := l.InspectScreening(ctx, s, token, op)
	if err != nil {
		return op, err
	}
	op, err = screeningStart(op, r, expected)
	if err != nil {
		return op, err
	}
	if r.Instance.State != "running" && r.Instance.State != "offline" {
		return op, &Error{Code: "Linode 状态不稳定，未派发自动删除"}
	}
	// Persist the intended deletion target before dispatch. Clear Create polling
	// metadata so later read-only reconciliation proves 404, not create readiness.
	op.Expected, op.Linode, op.PollURL, op.PollKind, op.ActionID = "deleted", nil, "", "", ""
	if err = screeningCheckpoint(ctx, &op, "deleting", "delete-instance", op.ResourceID); err != nil {
		return op, err
	}
	if err = l.call(ctx, "DELETE", "/linode/instances/"+op.ResourceID, nil, nil); err != nil {
		return op, err
	}
	op.Expected = "deleted"
	if err = screeningWait(ctx, func() (bool, error) {
		var v linInstance
		e := l.call(ctx, "GET", "/linode/instances/"+op.ResourceID, nil, &v)
		if screeningAbsent(e) {
			return true, nil
		}
		if e == nil && fmt.Sprint(v.ID) != op.ResourceID {
			return false, &Error{Code: "删除核实响应的 Linode 身份不一致", Uncertain: true}
		}
		return false, e
	}); err != nil {
		return op, screeningUncertain(err)
	}
	op.Ledger.Resources[0].State = "deleted-confirmed"
	err = screeningConfirmed(ctx, &op, "deleted-confirmed")
	return op, err
}

func (d *DigitalOcean) InspectScreening(ctx context.Context, s Spec, token string, op Operation) (ScreeningResource, error) {
	r := ScreeningResource{Method: "delete-recreate", Resources: []RelatedResource{}}
	if err := screeningIntent(s, token, op); err != nil {
		return r, err
	}
	if err := linodeID(op.ResourceID); err != nil {
		return r, err
	}
	var data struct {
		Droplet struct {
			doDroplet
			Tags      []string `json:"tags"`
			VolumeIDs []string `json:"volume_ids"`
			VPCUUID   string   `json:"vpc_uuid"`
			Features  []string `json:"features"`
		} `json:"droplet"`
	}
	if err := d.call(ctx, "GET", "/droplets/"+op.ResourceID, nil, &data); err != nil {
		return r, err
	}
	v := data.Droplet
	if strconv.Itoa(v.ID) != op.ResourceID || v.Region.Slug != s.Region || v.Size.Slug != s.Plan || strconv.Itoa(v.Image.ID) != s.Image || !contains(v.Tags, "narbe-"+token) || len(v.VolumeIDs) != 0 {
		return r, &Error{Code: "Droplet 不属于本次创建任务、配置已变化或存在附属卷，禁止自动删除"}
	}
	var assoc struct {
		Reserved        []jsonRaw `json:"reserved_ips"`
		Floating        []jsonRaw `json:"floating_ips"`
		Volumes         []jsonRaw `json:"volumes"`
		Snapshots       []jsonRaw `json:"snapshots"`
		VolumeSnapshots []jsonRaw `json:"volume_snapshots"`
	}
	if err := d.call(ctx, "GET", "/droplets/"+op.ResourceID+"/destroy_with_associated_resources", nil, &assoc); err != nil {
		return r, err
	}
	if assoc.Volumes == nil || assoc.Snapshots == nil || assoc.VolumeSnapshots == nil || len(assoc.Volumes)+len(assoc.Snapshots)+len(assoc.VolumeSnapshots)+len(assoc.Reserved)+len(assoc.Floating) > 0 {
		return r, &Error{Code: "Droplet 附属计费资源不为空或无法确认，未自动删除"}
	}
	// A Reserved IP is separate from the Droplet primary IPv4. A primary-IP
	// lookup returning 404 cannot prove there is no secondary reserved address.
	if err := d.screeningReservedIPs(ctx, op.ResourceID); err != nil {
		return r, err
	}
	public := []string{}
	for _, n := range v.Networks.V4 {
		if n.Type == "public" {
			public = append(public, n.Ip_address)
		}
	}
	if len(public) != 1 || !screeningIPv4(public[0]) {
		return r, &Error{Code: "Droplet 尚无唯一有效公网 IPv4"}
	}
	var reserved struct {
		ReservedIP jsonRaw `json:"reserved_ip"`
	}
	err := d.call(ctx, "GET", "/reserved_ips/"+Escape(public[0]), nil, &reserved)
	if err == nil || !screeningAbsent(err) {
		if err != nil && !screeningAbsent(err) {
			return r, err
		}
		return r, &Error{Code: "Droplet 使用 Reserved IP，未自动删除"}
	}
	r.Instance = Instance{ID: op.ResourceID, Name: v.Name, Region: s.Region, State: v.Status, IPs: public, Plan: v.Size.Slug, Image: s.Image}
	r.Resources = append(r.Resources, RelatedResource{ID: "droplet:" + op.ResourceID, Kind: "instance", State: v.Status})
	r.Fingerprint = fingerprint(struct {
		ID                                      int
		Name, Region, Plan, Image, VPC, Account string
		Tags, Features, Volumes                 []string
		Backups                                 bool
	}{v.ID, v.Name, v.Region.Slug, v.Size.Slug, s.Image, v.VPCUUID, fingerprint(d.token), v.Tags, v.Features, v.VolumeIDs, contains(v.Features, "backups")})
	return r, nil
}

func (d *DigitalOcean) screeningReservedIPs(ctx context.Context, target string) error {
	seen := map[string]bool{}
	bytesRead := 0
	for page := 1; page <= 10; page++ {
		var response struct {
			Items []jsonRaw `json:"reserved_ips"`
		}
		if err := d.call(ctx, "GET", fmt.Sprintf("/reserved_ips?per_page=200&page=%d", page), nil, &response); err != nil {
			return err
		}
		if response.Items == nil || len(response.Items) > 200 {
			return &Error{Code: "Reserved IP 列表缺失或数量异常，不能排除附属计费资源"}
		}
		for _, raw := range response.Items {
			bytesRead += len(raw)
			if bytesRead > 2<<20 {
				return &Error{Code: "Reserved IP 归属查询超过安全大小，未自动删除"}
			}
			var item map[string]jsonRaw
			var ip string
			if json.Unmarshal(raw, &item) != nil || json.Unmarshal(item["ip"], &ip) != nil || !screeningIPv4(ip) || seen[ip] {
				return &Error{Code: "Reserved IP 地址或列表身份无效，未自动删除"}
			}
			seen[ip] = true
			binding, present := item["droplet"]
			if !present {
				return &Error{Code: "Reserved IP 绑定状态缺失，请检查 reserved_ip:read 与 droplet:read 权限"}
			}
			if strings.TrimSpace(string(binding)) == "null" {
				continue
			}
			var owner struct {
				ID int `json:"id"`
			}
			if json.Unmarshal(binding, &owner) != nil || owner.ID <= 0 {
				return &Error{Code: "Reserved IP 的 Droplet 归属无法核实，未自动删除"}
			}
			if strconv.Itoa(owner.ID) == target {
				return &Error{Code: "本轮 Droplet 绑定了独立 Reserved IP，未自动删除"}
			}
		}
		if len(response.Items) < 200 {
			return nil
		}
	}
	return &Error{Code: "Reserved IP 分页超过安全上限，未自动删除"}
}

func (d *DigitalOcean) ReplaceScreeningIP(ctx context.Context, s Spec, token string, op Operation, expected string) (Operation, error) {
	r, err := d.InspectScreening(ctx, s, token, op)
	if err != nil {
		return op, err
	}
	op, err = screeningStart(op, r, expected)
	if err != nil {
		return op, err
	}
	if r.Instance.State != "active" && r.Instance.State != "off" {
		return op, &Error{Code: "Droplet 状态不稳定，未派发自动删除"}
	}
	op.Expected, op.PollURL, op.PollKind, op.ActionID = "deleted", "", "", ""
	if err = screeningCheckpoint(ctx, &op, "deleting", "delete-instance", op.ResourceID); err != nil {
		return op, err
	}
	if err = d.call(ctx, "DELETE", "/droplets/"+op.ResourceID, nil, nil); err != nil {
		return op, err
	}
	op.Expected = "deleted"
	if err = screeningWait(ctx, func() (bool, error) {
		var data struct{ Droplet doDroplet }
		e := d.call(ctx, "GET", "/droplets/"+op.ResourceID, nil, &data)
		if screeningAbsent(e) {
			return true, nil
		}
		if e == nil && strconv.Itoa(data.Droplet.ID) != op.ResourceID {
			return false, &Error{Code: "删除核实响应的 Droplet 身份不一致", Uncertain: true}
		}
		return false, e
	}); err != nil {
		return op, screeningUncertain(err)
	}
	op.Ledger.Resources[0].State = "deleted-confirmed"
	err = screeningConfirmed(ctx, &op, "deleted-confirmed")
	return op, err
}
