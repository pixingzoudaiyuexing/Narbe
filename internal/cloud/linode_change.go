package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func LinodeAction(action string) bool {
	return contains([]string{"rebuild", "resize", "password-reset", "clone", "migrate", "firewall-create", "firewall-rules", "firewall-status", "firewall-delete", "firewall-bind"}, action)
}
func linodeLabel(s string) error {
	if len(s) < 3 || len(s) > 32 {
		return errors.New("Linode名称需要3–32字符")
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return errors.New("Linode名称仅允许字母、数字、连字符和下划线")
		}
	}
	return nil
}
func (l *Linode) ValidateChange(ctx context.Context, id, action string, c LinodeChange) error {
	fields := map[string][]string{
		"rebuild": {"image", "password", "key", "sshPublicKey", "startup", "metadataAcknowledged"}, "resize": {"plan"}, "password-reset": {"password", "diskId"}, "clone": {"plan", "region", "label", "firewall"}, "migrate": {"region"}, "firewall-create": {"label", "rules"}, "firewall-rules": {"rules"}, "firewall-status": {"status"}, "firewall-delete": {}, "firewall-bind": {"interfaceId", "firewall", "bindingSnapshot"}}
	allowed := map[string]bool{"snapshot": true, "acknowledge": true}
	for _, k := range fields[action] {
		allowed[k] = true
	}
	raw, _ := json.Marshal(c)
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	for k := range values {
		if !allowed[k] {
			return errors.New("本操作包含不适用的高级参数，请重新填写")
		}
	}
	if !LinodeAction(action) || !c.Acknowledge {
		return errors.New("需要确认本操作的数据、停机、IP或计费影响")
	}
	if action == "firewall-create" {
		if id != "" || linodeLabel(c.Label) != nil || c.Rules == nil {
			return errors.New("防火墙创建参数无效")
		}
		return ValidateFirewallRules(*c.Rules)
	}
	if strings.HasPrefix(action, "firewall-") && action != "firewall-bind" {
		f, e := l.firewall(ctx, id)
		if e != nil {
			return e
		}
		if f.Snapshot != c.Snapshot {
			return errors.New("防火墙规则或共享绑定已变化，请重新检查影响范围")
		}
		switch action {
		case "firewall-rules":
			if c.Rules == nil {
				return errors.New("缺少完整双向规则")
			}
			return ValidateFirewallRules(*c.Rules)
		case "firewall-status":
			if c.Status != "enabled" && c.Status != "disabled" {
				return errors.New("防火墙启用状态无效")
			}
		case "firewall-delete":
			if len(f.Devices) > 0 {
				return errors.New("防火墙仍绑定服务器或网络接口，请先显式解绑；不会自动影响其他服务器")
			}
		}
		return nil
	}
	s, e := l.state(ctx, id)
	if e != nil {
		return e
	}
	if s.Snapshot != c.Snapshot {
		return errors.New("服务器配置、磁盘或网络已变化，请刷新详情重新确认")
	}
	if s.Instance.State != "running" && s.Instance.State != "offline" {
		return errors.New("服务器正在执行其他操作，请等待云端任务结束")
	}
	if action == "firewall-bind" {
		if c.Firewall == nil {
			return errors.New("请选择防火墙模式")
		}
		if s.Generation == "legacy_config" && c.InterfaceID != 0 {
			return errors.New("传统配置只能绑定实例")
		}
		if s.Generation == "linode" {
			valid := false
			for _, i := range s.Interfaces {
				if i.ID == c.InterfaceID && i.ID > 0 && (i.Public != nil || i.VPC != nil) {
					valid = true
				}
			}
			if !valid {
				return errors.New("请选择该服务器的public/VPC接口；VLAN不支持云防火墙")
			}
		}
		attached, e := l.attachedFirewalls(ctx, id, c.InterfaceID)
		if e != nil {
			return e
		}
		if c.BindingSnapshot != fingerprint(attached) {
			return errors.New("该实例或接口的绑定已变化，请重新确认")
		}
		return l.ValidateFirewallSelection(ctx, *c.Firewall)
	}
	if _, e = l.eventBaseline(ctx, id, action); e != nil {
		return e
	}
	for _, d := range s.Disks {
		if d.Status != "ready" {
			return errors.New("磁盘尚未就绪，未发送操作")
		}
	}
	switch action {
	case "password-reset":
		if s.Instance.State != "offline" {
			return errors.New("重置root密码需要先关机；Narbe不会自动关机，请确认关机后再操作")
		}
		if c.Password == "" {
			return errors.New("请输入新root密码")
		}
		if e = ValidateLinodePassword(c.Password); e != nil {
			return e
		}
		valid := false
		for _, d := range s.Disks {
			if d.ID == c.DiskID && (d.Filesystem == "ext3" || d.Filesystem == "ext4") {
				valid = true
			}
		}
		if !valid {
			return errors.New("请选择该服务器可重置的ext3/ext4系统磁盘；swap/raw或特殊结构需云控制台处理")
		}
	case "rebuild":
		if contains(s.Locks, "cannot_delete") || contains(s.Locks, "cannot_delete_with_subresources") {
			return errors.New("服务器删除锁禁止重装，Narbe不会自动解除锁")
		}
		cat, e := l.Catalog(ctx, s.Instance.Region, s.Instance.Plan)
		if e != nil {
			return e
		}
		im, ok := Find(cat.Images, c.Image)
		if !ok || im.Deprecated || im.Disk > s.Instance.Disk {
			return errors.New("重装镜像不可用或大于当前套餐磁盘")
		}
		if !c.MetadataAcknowledged {
			return errors.New("请确认重装删除磁盘及Metadata不会自动延续")
		}
		startup := c.Startup.Spec(c.Image, s.Instance.Region)
		if err := ValidateStartup("linode", startup, cat); err != nil {
			return err
		}
		if startup.ScriptMode == "stackscript" {
			if strings.HasPrefix(c.Image, "private/") {
				return errors.New("Linode Rebuild不支持私有镜像与StackScript组合")
			}
			if err := l.ValidateStackScript(ctx, startup); err != nil {
				return err
			}
		}
		if c.Password == "" && c.SSHPublicKey == "" && c.Key == "" {
			return errors.New("重装需要root密码或SSH公钥")
		}
		if e = ValidateLinodePassword(c.Password); e != nil {
			return e
		}
		if c.Key != "" {
			if _, ok := Find(cat.Keys, c.Key); !ok {
				return errors.New("SSH密钥不可用")
			}
		}
	case "resize", "clone", "migrate":
		region := s.Instance.Region
		plan := c.Plan
		if action == "migrate" || action == "clone" {
			region = c.Region
		}
		if action == "migrate" {
			plan = s.Instance.Plan
		}
		if action == "resize" && plan == s.Instance.Plan {
			return errors.New("目标套餐与当前相同")
		}
		if action == "migrate" && region == s.Instance.Region {
			return errors.New("数据中心迁移必须选择不同地区，不是宿主机迁移")
		}
		cat, e := l.Catalog(ctx, region, plan)
		if e != nil {
			return e
		}
		target, ok := Find(cat.Plans, plan)
		if !ok {
			return errors.New("目标地区不支持该套餐或容量暂不可用")
		}
		allocated := 0
		for _, d := range s.Disks {
			allocated += d.Size
		}
		if allocated > target.Disk*1024 {
			return errors.New("实际已分配磁盘大于目标套餐，不能自动缩盘或删除数据满足降配/克隆要求")
		}
		if len(s.Volumes) > 0 {
			return errors.New("存在附加Volume，本候选不自动脱离或迁移卷；请在云控制台处理后重新核实")
		}
		if s.PlacementGroup || s.Managed {
			return errors.New("服务器属于placement group或托管集群，本候选不能保证保留管理关系，请在控制台处理")
		}
		if action == "clone" {
			if e = linodeLabel(c.Label); e != nil {
				return e
			}
			if s.Instance.State != "offline" {
				return errors.New("为保证磁盘副本一致，Narbe要求源服务器先关机，不会自动关机")
			}
			if c.Firewall == nil {
				return errors.New("请选择新克隆服务器的防火墙模式")
			}
			if e = l.ValidateFirewallSelection(ctx, *c.Firewall); e != nil {
				return e
			}
		}
		if action != "resize" {
			source, e := l.Catalog(ctx, s.Instance.Region, "")
			if e != nil {
				return e
			}
			from, _ := Find(source.Regions, s.Instance.Region)
			to, _ := Find(cat.Regions, region)
			if from.Extra["siteType"] != to.Extra["siteType"] || (action == "clone" && (from.Extra["siteType"] == "distributed" || to.Extra["siteType"] == "distributed")) {
				return errors.New("不支持core/distributed跨类型操作或distributed克隆")
			}
			for _, cfg := range s.Configs {
				for _, i := range cfg.Interfaces {
					if i.Purpose != "public" && i.Purpose != "" {
						return errors.New("存在VPC/VLAN接口，本候选不自动改写跨地区网络，请使用云控制台")
					}
				}
			}
			for _, i := range s.Interfaces {
				if i.VPC != nil || i.VLAN != nil {
					return errors.New("存在VPC/VLAN接口，跨地区操作需要专门网络安排")
				}
			}
			if s.Generation == "linode" && to.Extra["linodeInterfaces"] != "true" {
				return errors.New("目标地区未确认支持Linode Interfaces")
			}
		}
	}
	return nil
}
func (l *Linode) Change(ctx context.Context, id, action string, c LinodeChange, token string) (op Operation, err error) {
	if action == "rebuild" {
		if e := l.ValidateChange(ctx, id, action, c); e != nil {
			return op, e
		}
	}
	op = Operation{ResourceID: id, Linode: &LinodeOperation{Action: action, Label: c.Label, Plan: c.Plan, Image: c.Image, SourceID: id, Firewall: c.Firewall, InterfaceID: c.InterfaceID, DiskID: c.DiskID, StartedAt: time.Now().Unix()}}
	if action == "firewall-create" || action == "firewall-rules" {
		op.Linode.ExpectedRules = c.Rules
	}
	if action == "firewall-create" {
		op.Linode.ExpectedStatus = "enabled"
	}
	if action == "firewall-status" {
		op.Linode.ExpectedStatus = c.Status
	}
	accepted := false
	defer func() {
		if err != nil && accepted && !Uncertain(err) {
			err = &Error{Code: "部分云端步骤已受理，后续未完成，请按任务账本人工核实", Uncertain: true}
		}
	}()
	if strings.HasPrefix(action, "firewall-") && action != "firewall-bind" {
		if e := recordOperation(ctx, op); e != nil {
			return op, e
		}
		if action != "firewall-create" {
			f, e := l.firewall(ctx, id)
			if e != nil {
				return op, e
			}
			if c.Snapshot != "" && f.Snapshot != c.Snapshot {
				return op, errors.New("派发前防火墙共享绑定或配置变化，未发送操作")
			}
		}
		switch action {
		case "firewall-create":
			fid, e := l.createFirewall(ctx, c.Label, *c.Rules, &op)
			if e != nil {
				return op, e
			}
			accepted = true
			op.ResourceID = fmt.Sprint(fid)
		case "firewall-rules":
			err = l.call(ctx, "PUT", "/networking/firewalls/"+id+"/rules", c.Rules, nil)
		case "firewall-status":
			err = l.call(ctx, "PUT", "/networking/firewalls/"+id, map[string]string{"status": c.Status}, nil)
		case "firewall-delete":
			err = l.call(ctx, "DELETE", "/networking/firewalls/"+id, nil, nil)
		}
		if err != nil {
			return op, err
		}
		accepted = true
		if action != "firewall-delete" {
			f, e := l.firewall(ctx, op.ResourceID)
			if e != nil {
				return op, e
			}
			op.Linode.FirewallSnapshot = f.Snapshot
			op.Linode.ExpectedRules = c.Rules
			op.Linode.ExpectedStatus = c.Status
			if action == "firewall-create" {
				op.Linode.ExpectedStatus = "enabled"
			}
		}
		return op, recordOperation(ctx, op)
	}
	s, e := l.state(ctx, id)
	if e != nil {
		return op, e
	}
	if c.Snapshot != "" && c.Snapshot != s.Snapshot {
		return op, errors.New("派发前资源配置已变化，未发送操作")
	}
	if (action == "password-reset" || action == "clone") && s.Instance.State != "offline" {
		return op, errors.New("派发前服务器已不是关机状态，未发送操作")
	}
	op.Region = s.Instance.Region
	if action == "firewall-bind" {
		if c.BindingSnapshot != "" {
			attached, e := l.attachedFirewalls(ctx, id, c.InterfaceID)
			if e != nil {
				return op, e
			}
			if fingerprint(attached) != c.BindingSnapshot {
				return op, errors.New("派发前绑定已变化，未发送操作")
			}
		}
		fid, e := l.selectionID(ctx, c.Firewall, token, &op)
		if e != nil {
			return op, e
		}
		if fid > 0 {
			f, e := l.firewall(ctx, fmt.Sprint(fid))
			if e != nil {
				return op, e
			}
			op.Linode.Firewall = &FirewallSelection{Mode: "existing", ID: fid, Rules: &f.Rules}
		}
		// A compound binding write can partially succeed even without creating resources.
		if e = l.bindFirewall(ctx, s, c.InterfaceID, fid, &op); e != nil {
			return op, e
		}
		accepted = true
		return op, recordOperation(ctx, op)
	}
	after, e := l.eventBaseline(ctx, id, action)
	if e != nil {
		return op, e
	}
	op.Linode.EventAfter = after
	if e = recordOperation(ctx, op); e != nil {
		return op, e
	}
	path := "/linode/instances/" + id + "/"
	var body any
	switch action {
	case "rebuild":
		path += "rebuild"
		b := map[string]any{"image": c.Image, "booted": true}
		startup := c.Startup.Spec(c.Image, s.Instance.Region)
		if startup.ScriptMode == "shell" || startup.ScriptMode == "cloud-config" {
			b["metadata"] = map[string]string{"user_data": base64.StdEncoding.EncodeToString([]byte(startup.UserData))}
		}
		if startup.ScriptMode == "stackscript" {
			b["stackscript_id"] = startup.StackScriptID
			data := startup.StackScriptData
			if data == nil {
				data = map[string]string{}
			}
			b["stackscript_data"] = data
		}
		if c.Password != "" {
			b["root_pass"] = c.Password
		}
		if c.Key != "" {
			cat, e := l.Catalog(ctx, s.Instance.Region, s.Instance.Plan)
			if e != nil {
				return op, e
			}
			k, _ := Find(cat.Keys, c.Key)
			c.SSHPublicKey = k.Extra["publicKey"]
		}
		if c.SSHPublicKey != "" {
			b["authorized_keys"] = []string{c.SSHPublicKey}
		}
		body = b
	case "resize":
		path += "resize"
		body = map[string]any{"type": c.Plan, "migration_type": "cold", "allow_auto_disk_resize": false}
	case "password-reset":
		path = fmt.Sprintf("/linode/instances/%s/disks/%d/password", id, c.DiskID)
		body = map[string]string{"password": c.Password}
	case "migrate":
		path += "migrate"
		op.Region = c.Region
		body = map[string]any{"region": c.Region, "type": "cold", "upgrade": false}
	case "clone":
		path += "clone"
		op.Region = c.Region
		referenced := map[int]bool{}
		for _, cfg := range s.Configs {
			for _, d := range cfg.Devices {
				referenced[d.DiskID] = true
			}
		}
		extras := []int{}
		for _, d := range s.Disks {
			if !referenced[d.ID] {
				extras = append(extras, d.ID)
			}
		}
		body = map[string]any{"label": c.Label, "region": c.Region, "type": c.Plan, "disks": extras}
	default:
		return op, errors.New("不支持的高级操作")
	}
	if action == "clone" {
		var v linInstance
		err = l.call(ctx, "POST", path, body, &v)
		if err != nil {
			return op, err
		}
		accepted = true
		if v.ID < 1 || fmt.Sprint(v.ID) == id {
			return op, &Error{Code: "克隆未返回可信的新实例ID", Uncertain: true}
		}
		op.ResourceID = fmt.Sprint(v.ID)
		op.Owned = append(op.Owned, "linode:"+op.ResourceID)
	} else {
		err = l.call(ctx, "POST", path, body, nil)
		if err != nil {
			return op, err
		}
		accepted = true
	}
	if e = recordOperation(ctx, op); e != nil {
		return op, e
	}
	if action == "clone" {
		for {
			ready, e := l.eventFinished(ctx, id, action, after, func() int { n, _ := strconv.Atoi(op.ResourceID); return n }())
			if e != nil {
				return op, e
			}
			if ready {
				break
			}
			select {
			case <-ctx.Done():
				return op, &Error{Code: "克隆等待中断，云端可能仍在创建，请勿重复克隆", Uncertain: true}
			case <-time.After(4 * time.Second):
			}
		}
		dest, e := l.state(ctx, op.ResourceID)
		if e != nil {
			return op, e
		}
		if dest.Instance.ID != op.ResourceID || dest.Instance.Name != c.Label || dest.Instance.Region != c.Region || dest.Instance.Plan != c.Plan {
			return op, &Error{Code: "克隆目标名称、地区或规格不符，未修改网络，请人工核实", Uncertain: true}
		}
		if dest.Instance.State != "offline" {
			return op, &Error{Code: "克隆目标未确认处于关机状态，未修改其防火墙", Uncertain: true}
		}
		fid, e := l.selectionID(ctx, c.Firewall, token, &op)
		if e != nil {
			return op, e
		}
		if fid > 0 {
			f, e := l.firewall(ctx, fmt.Sprint(fid))
			if e != nil {
				return op, e
			}
			op.Linode.Firewall = &FirewallSelection{Mode: "existing", ID: fid, Rules: &f.Rules}
		}
		if dest.Generation == "linode" {
			for _, i := range dest.Interfaces {
				if i.Public != nil || i.VPC != nil {
					if e = l.bindFirewall(ctx, dest, i.ID, fid, &op); e != nil {
						return op, e
					}
				}
			}
		} else {
			if e = l.bindFirewall(ctx, dest, 0, fid, &op); e != nil {
				return op, e
			}
		}
		op.Linode.Configured = true
		if e = recordOperation(ctx, op); e != nil {
			return op, e
		}
	}
	return op, nil
}
