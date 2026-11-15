package cloud

import (
	"context"
	"encoding/base64"
	"net/url"
	"regexp"
)

type screeningAWSInstance struct {
	ID          string `xml:"instanceId"`
	Image       string `xml:"imageId"`
	Plan        string `xml:"instanceType"`
	ClientToken string `xml:"clientToken"`
	KeyName     string `xml:"keyName"`
	Subnet      string `xml:"subnetId"`
	VPC         string `xml:"vpcId"`
	RootType    string `xml:"rootDeviceType"`
	RootName    string `xml:"rootDeviceName"`
	IP          string `xml:"ipAddress"`
	State       struct {
		Name string `xml:"name"`
	} `xml:"instanceState"`
	Tags []struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	} `xml:"tagSet>item"`
	Groups []struct {
		ID string `xml:"groupId"`
	} `xml:"groupSet>item"`
	Disks []struct {
		Device string `xml:"deviceName"`
		Volume string `xml:"ebs>volumeId"`
		Delete bool   `xml:"ebs>deleteOnTermination"`
	} `xml:"blockDeviceMapping>item"`
	Interfaces []struct {
		ID         string `xml:"networkInterfaceId"`
		Subnet     string `xml:"subnetId"`
		Attachment struct {
			ID     string `xml:"attachmentId"`
			Index  int    `xml:"deviceIndex"`
			Delete bool   `xml:"deleteOnTermination"`
		} `xml:"attachment"`
		Private []struct {
			IP      string `xml:"privateIpAddress"`
			Primary bool   `xml:"primary"`
		} `xml:"privateIpAddressesSet>item"`
		Groups []struct {
			ID string `xml:"groupId"`
		} `xml:"groupSet>item"`
	} `xml:"networkInterfaceSet>item"`
}

func (a *AWS) InspectScreening(ctx context.Context, s Spec, token string, op Operation) (ScreeningResource, error) {
	r, _, err := a.inspectScreeningAWS(ctx, s, token, op)
	return r, err
}
func (a *AWS) inspectScreeningAWS(ctx context.Context, s Spec, token string, op Operation) (ScreeningResource, screeningAWSInstance, error) {
	r := ScreeningResource{Method: "aws-stop-start", Resources: []RelatedResource{}}
	v := screeningAWSInstance{}
	if err := screeningIntent(s, token, op); err != nil {
		return r, v, err
	}
	if !regexp.MustCompile(`^i-[a-f0-9]{8,32}$`).MatchString(op.ResourceID) {
		return r, v, &Error{Code: "AWS 实例 ID 无效"}
	}
	var data struct {
		Reservations []struct {
			Owner string                 `xml:"ownerId"`
			Items []screeningAWSInstance `xml:"instancesSet>item"`
		} `xml:"reservationSet>item"`
	}
	if err := a.call(ctx, s.Region, "DescribeInstances", url.Values{"InstanceId.1": {op.ResourceID}}, &data); err != nil {
		return r, v, err
	}
	if len(data.Reservations) != 1 || data.Reservations[0].Owner == "" || len(data.Reservations[0].Items) != 1 {
		return r, v, &Error{Code: "AWS 未返回唯一实例及账号身份"}
	}
	v = data.Reservations[0].Items[0]
	owned := false
	for _, tag := range v.Tags {
		if tag.Key == "narbe-job" && tag.Value == token {
			owned = true
		}
	}
	if v.ID != op.ResourceID || v.ClientToken != token || !owned || v.Image != s.Image || v.Plan != s.Plan || v.Subnet != s.Network || v.KeyName != s.Key || v.RootType != "ebs" || v.RootName == "" || len(v.Disks) == 0 || len(v.Interfaces) != 1 || len(v.Groups) != 1 || v.Groups[0].ID != s.Group || v.VPC == "" {
		return r, v, &Error{Code: "AWS 不属于本次创建任务或不是可安全换 IP 的标准 EBS 实例"}
	}
	ni := v.Interfaces[0]
	if ni.ID == "" || ni.Subnet != s.Network || ni.Attachment.ID == "" || ni.Attachment.Index != 0 || !ni.Attachment.Delete || len(ni.Private) != 1 || !ni.Private[0].Primary || len(ni.Groups) != 1 || ni.Groups[0].ID != s.Group {
		return r, v, &Error{Code: "AWS 存在复杂网卡或附加私有 IP，未自动停止/启动"}
	}
	root := false
	for _, d := range v.Disks {
		if d.Volume == "" || d.Device == "" {
			return r, v, &Error{Code: "AWS 磁盘身份不完整"}
		}
		root = root || d.Device == v.RootName
		r.Resources = append(r.Resources, RelatedResource{ID: d.Volume, Kind: "ebs-volume", State: "attached"})
	}
	if !root {
		return r, v, &Error{Code: "AWS 根磁盘归属无法确认"}
	}
	var addresses struct {
		Set *struct {
			Items []struct {
				IP string `xml:"publicIp"`
			} `xml:"item"`
		} `xml:"addressesSet"`
	}
	if err := a.call(ctx, s.Region, "DescribeAddresses", url.Values{"Filter.1.Name": {"network-interface-id"}, "Filter.1.Value.1": {ni.ID}}, &addresses); err != nil {
		return r, v, err
	}
	if addresses.Set == nil || len(addresses.Set.Items) != 0 {
		return r, v, &Error{Code: "AWS 网卡含 Elastic IP 或地址归属无法确认，未自动停止/启动"}
	}
	var attr struct {
		Data *struct {
			Value string `xml:"value"`
		} `xml:"userData"`
	}
	if err := a.call(ctx, s.Region, "DescribeInstanceAttribute", url.Values{"InstanceId": {op.ResourceID}, "Attribute": {"userData"}}, &attr); err != nil {
		return r, v, err
	}
	if attr.Data == nil || attr.Data.Value != base64.StdEncoding.EncodeToString([]byte(StartupPayload(s))) {
		return r, v, &Error{Code: "AWS 初始化数据已变化，未自动停止/启动"}
	}
	r.Instance = Instance{ID: v.ID, Name: s.Name, Region: s.Region, State: v.State.Name, Plan: v.Plan, Image: v.Image, IPs: []string{}}
	if screeningIPv4(v.IP) {
		r.Instance.IPs = append(r.Instance.IPs, v.IP)
	}
	r.Resources = append(r.Resources, RelatedResource{ID: v.ID, Kind: "instance", State: v.State.Name}, RelatedResource{ID: ni.ID, Kind: "network-interface", State: "attached"})
	stable := v
	stable.IP, stable.State.Name = "", ""
	r.Fingerprint = fingerprint(struct {
		Account, Egress, Data string
		Instance              screeningAWSInstance
	}{data.Reservations[0].Owner, fingerprint(a.c), attr.Data.Value, stable})
	return r, v, nil
}

func (a *AWS) ReplaceScreeningIP(ctx context.Context, s Spec, token string, op Operation, expected string) (Operation, error) {
	r, v, err := a.inspectScreeningAWS(ctx, s, token, op)
	if err != nil {
		return op, err
	}
	op, err = screeningStart(op, r, expected)
	if err != nil {
		return op, err
	}
	if v.State.Name != "running" || len(r.Instance.IPs) != 1 {
		return op, &Error{Code: "AWS 只对运行中且具有普通公网 IPv4 的本轮实例自动换 IP"}
	}
	if err = screeningCheckpoint(ctx, &op, "stopping", "stop-instance", op.ResourceID); err != nil {
		return op, err
	}
	if err = a.call(ctx, s.Region, "StopInstances", url.Values{"InstanceId.1": {op.ResourceID}}, nil); err != nil {
		return op, err
	}
	if err = screeningWait(ctx, func() (bool, error) {
		current, _, e := a.inspectScreeningAWS(ctx, s, token, op)
		if e != nil {
			return false, e
		}
		if current.Fingerprint != expected {
			return false, &Error{Code: "AWS 停机期间配置变化，停止后续操作", Uncertain: true}
		}
		if current.Instance.State != "stopping" && current.Instance.State != "stopped" {
			return false, &Error{Code: "AWS 停机期间出现其他状态，停止后续操作", Uncertain: true}
		}
		return current.Instance.State == "stopped", nil
	}); err != nil {
		return op, screeningUncertain(err)
	}
	if err = screeningConfirmed(ctx, &op, "stopped"); err != nil {
		return op, err
	}
	// Repeat the full ownership/address/configuration read immediately before boot.
	check, _, err := a.inspectScreeningAWS(ctx, s, token, op)
	if err != nil || check.Fingerprint != expected || check.Instance.State != "stopped" {
		return op, &Error{Code: "AWS 自动开机前身份或状态无法确认；已暂停", Uncertain: true}
	}
	if err = screeningCheckpoint(ctx, &op, "starting", "start-instance", op.ResourceID); err != nil {
		return op, err
	}
	if err = a.call(ctx, s.Region, "StartInstances", url.Values{"InstanceId.1": {op.ResourceID}}, nil); err != nil {
		return op, err
	}
	if err = screeningWait(ctx, func() (bool, error) {
		current, _, e := a.inspectScreeningAWS(ctx, s, token, op)
		if e != nil {
			return false, e
		}
		if current.Fingerprint != expected {
			return false, &Error{Code: "AWS 开机期间配置变化，已暂停", Uncertain: true}
		}
		if current.Instance.State != "pending" && current.Instance.State != "running" {
			return false, &Error{Code: "AWS 开机期间出现其他状态，已暂停", Uncertain: true}
		}
		return current.Instance.State == "running" && len(current.Instance.IPs) == 1, nil
	}); err != nil {
		return op, screeningUncertain(err)
	}
	op.Expected = "running"
	err = screeningConfirmed(ctx, &op, "completed")
	return op, err
}

var _ ScreeningProvider = (*AWS)(nil)
var _ ScreeningProvider = (*Linode)(nil)
var _ ScreeningProvider = (*DigitalOcean)(nil)
