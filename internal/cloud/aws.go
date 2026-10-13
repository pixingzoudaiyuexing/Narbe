package cloud

import (
	"context"

	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	sdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type awsImage struct {
	ID         string `xml:"imageId"`
	Name       string `xml:"name"`
	Arch       string `xml:"architecture"`
	Date       string `xml:"creationDate"`
	Deprecated string `xml:"deprecationTime"`
	Root       string `xml:"rootDeviceName"`
	Disks      []struct {
		Device string `xml:"deviceName"`
		Size   int    `xml:"ebs>volumeSize"`
	} `xml:"blockDeviceMapping>item"`
}
type AWS struct {
	c Credentials
	h *http.Client
}

var awsRegion = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]+$`)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (a *AWS) call(ctx context.Context, region, action string, params url.Values, out any) error {
	if !awsRegion.MatchString(region) {
		return &Error{Code: "AWS 地区格式无效"}
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2016-11-15")
	body := params.Encode()
	host := "ec2." + region + ".amazonaws.com"
	req, e := http.NewRequestWithContext(ctx, "POST", "https://"+host+"/", strings.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	if e = v4.NewSigner().SignHTTP(ctx, sdk.Credentials{AccessKeyID: a.c["accessKeyId"], SecretAccessKey: a.c["secretAccessKey"], SessionToken: a.c["sessionToken"]}, req, digest([]byte(body)), "ec2", region, time.Now().UTC()); e != nil {
		return &Error{Code: "AWS 请求签名失败"}
	}
	resp, e := a.h.Do(req)
	mut := !strings.HasPrefix(action, "Describe")
	if e != nil {
		return &Error{Code: "网络中断，请核实云端结果", Uncertain: mut}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var er struct {
			Errors struct {
				Items []struct{ Code string } `xml:"Error"`
			}
		}
		_ = xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&er)
		code := http.StatusText(resp.StatusCode)
		if len(er.Errors.Items) > 0 && regexp.MustCompile(`^[A-Za-z0-9.]{1,100}$`).MatchString(er.Errors.Items[0].Code) {
			code = er.Errors.Items[0].Code
		}
		return &Error{Status: resp.StatusCode, Code: code, Uncertain: mut && (resp.StatusCode >= 500 || resp.StatusCode == 408)}
	}
	if out != nil {
		if e = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); e != nil {
			return &Error{Code: "AWS 响应不完整", Uncertain: mut}
		}
	}
	return nil
}

type awsInstance struct {
	ID    string `xml:"instanceId"`
	Name  string
	Image string `xml:"imageId"`
	Plan  string `xml:"instanceType"`
	State struct {
		Name string `xml:"name"`
	} `xml:"instanceState"`
	IP   string `xml:"ipAddress"`
	Tags []struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	} `xml:"tagSet>item"`
	IPv6 []struct {
		Addresses []struct {
			IP string `xml:"ipv6Address"`
		} `xml:"ipv6AddressesSet>item"`
	} `xml:"networkInterfaceSet>item"`
}

func (a *AWS) Catalog(ctx context.Context, region, plan string) (Catalog, error) {
	c := Catalog{}
	var regs struct {
		Items []struct {
			ID     string `xml:"regionName"`
			Status string `xml:"optInStatus"`
		} `xml:"regionInfo>item"`
	}
	home := a.c["defaultRegion"]
	if home == "" {
		home = "us-east-1"
	}
	if e := a.call(ctx, home, "DescribeRegions", nil, &regs); e != nil {
		return c, e
	}
	for _, r := range regs.Items {
		if r.Status != "not-opted-in" {
			c.Regions = append(c.Regions, Choice{ID: r.ID, Name: r.ID})
		}
	}
	sort.SliceStable(c.Regions, func(i, j int) bool { return c.Regions[i].ID == home && c.Regions[j].ID != home })
	if region == "" {
		return c, nil
	}
	if _, ok := Find(c.Regions, region); !ok {
		return c, &Error{Code: "AWS 地区未启用"}
	}
	offerings := map[string]bool{}
	next := ""
	for page := 0; page < 100; page++ {
		v := url.Values{"LocationType": {"region"}, "MaxResults": {"1000"}}
		if next != "" {
			v.Set("NextToken", next)
		}
		var r struct {
			Items []struct {
				Type string `xml:"instanceType"`
			} `xml:"instanceTypeOfferingSet>item"`
			Next string `xml:"nextToken"`
		}
		if e := a.call(ctx, region, "DescribeInstanceTypeOfferings", v, &r); e != nil {
			return c, e
		}
		for _, p := range r.Items {
			offerings[p.Type] = true
		}
		next = r.Next
		if next == "" {
			break
		}
		if page == 99 {
			return c, &Error{Code: "AWS 规格分页超限"}
		}
	}
	next = ""
	for page := 0; page < 100; page++ {
		v := url.Values{"MaxResults": {"100"}}
		if next != "" {
			v.Set("NextToken", next)
		}
		var r struct {
			Items []struct {
				ID     string   `xml:"instanceType"`
				CPU    int      `xml:"vCpuInfo>defaultVCpus"`
				Memory int      `xml:"memoryInfo>sizeInMiB"`
				Arch   []string `xml:"processorInfo>supportedArchitectures>item"`
			} `xml:"instanceTypeSet>item"`
			Next string `xml:"nextToken"`
		}
		if e := a.call(ctx, region, "DescribeInstanceTypes", v, &r); e != nil {
			return c, e
		}
		for _, p := range r.Items {
			if offerings[p.ID] && len(p.Arch) > 0 && (p.Arch[0] == "x86_64" || p.Arch[0] == "arm64") {
				c.Plans = append(c.Plans, Choice{ID: p.ID, Name: p.ID, CPU: p.CPU, Memory: p.Memory, Architecture: p.Arch[0]})
			}
		}
		next = r.Next
		if next == "" {
			break
		}
		if page == 99 {
			return c, &Error{Code: "AWS 规格分页超限"}
		}
	}
	v := url.Values{"Owner.1": {"amazon"}, "Owner.2": {"099720109477"}, "Owner.3": {"136693071363"}, "Filter.1.Name": {"state"}, "Filter.1.Value.1": {"available"}, "Filter.2.Name": {"root-device-type"}, "Filter.2.Value.1": {"ebs"}, "Filter.3.Name": {"virtualization-type"}, "Filter.3.Value.1": {"hvm"}, "IncludeDeprecated": {"false"}}
	if p, ok := Find(c.Plans, plan); ok {
		v.Set("Filter.4.Name", "architecture")
		v.Set("Filter.4.Value.1", p.Architecture)
	}
	var imgs struct{ Items []awsImage }
	v.Set("MaxResults", "1000")
	for page := 0; page < 100; page++ {
		var batch struct {
			Items []awsImage `xml:"imagesSet>item"`
			Next  string     `xml:"nextToken"`
		}
		if e := a.call(ctx, region, "DescribeImages", v, &batch); e != nil {
			return c, e
		}
		imgs.Items = append(imgs.Items, batch.Items...)
		if batch.Next == "" {
			break
		}
		v.Set("NextToken", batch.Next)
		if page == 99 {
			return c, &Error{Code: "AWS 镜像分页超限"}
		}
	}

	sort.Slice(imgs.Items, func(i, j int) bool { return imgs.Items[i].Date > imgs.Items[j].Date })
	for _, im := range imgs.Items {
		f := Family(im.Name)
		if f == "other" {
			continue
		}
		deprecated := false
		if im.Deprecated != "" {
			t, e := time.Parse(time.RFC3339, im.Deprecated)
			deprecated = e == nil && t.Before(time.Now())
		}
		disk := 0
		for _, d := range im.Disks {
			if d.Device == im.Root {
				disk = d.Size
			}
		}
		c.Images = append(c.Images, Choice{ID: im.ID, Name: im.Name, Architecture: im.Arch, Family: f, Deprecated: deprecated, Disk: disk, Extra: map[string]string{"root": im.Root}})
	}
	var sub struct {
		Items []struct {
			ID        string `xml:"subnetId"`
			VPC       string `xml:"vpcId"`
			AZ        string `xml:"availabilityZone"`
			State     string `xml:"state"`
			Available int    `xml:"availableIpAddressCount"`
		} `xml:"subnetSet>item"`
	}
	if e := a.call(ctx, region, "DescribeSubnets", nil, &sub); e != nil {
		return c, e
	}
	for _, s := range sub.Items {
		if s.State == "available" && s.Available > 0 {
			c.Networks = append(c.Networks, Choice{ID: s.ID, Name: s.ID + " · " + s.AZ, Extra: map[string]string{"vpc": s.VPC}})
		}
	}
	var groups struct {
		Items []struct {
			ID   string `xml:"groupId"`
			Name string `xml:"groupName"`
			VPC  string `xml:"vpcId"`
		} `xml:"securityGroupInfo>item"`
	}
	if e := a.call(ctx, region, "DescribeSecurityGroups", nil, &groups); e != nil {
		return c, e
	}
	for _, g := range groups.Items {
		c.Groups = append(c.Groups, Choice{ID: g.ID, Name: g.Name, Extra: map[string]string{"vpc": g.VPC}})
	}
	var keys struct {
		Items []struct {
			Name string `xml:"keyName"`
			Type string `xml:"keyType"`
		} `xml:"keySet>item"`
	}
	if e := a.call(ctx, region, "DescribeKeyPairs", nil, &keys); e != nil {
		return c, e
	}
	for _, k := range keys.Items {
		c.Keys = append(c.Keys, Choice{ID: k.Name, Name: k.Name, Extra: map[string]string{"type": k.Type}})
	}
	return c, nil
}
func (a *AWS) Instances(ctx context.Context, region string) ([]Instance, error) {
	if region == "" {
		return nil, &Error{Code: "请先选择 AWS 地区"}
	}
	out := []Instance{}
	next := ""
	for page := 0; page < 100; page++ {
		v := url.Values{"MaxResults": {"1000"}}
		if next != "" {
			v.Set("NextToken", next)
		}
		var res struct {
			Reservations []struct {
				Items []awsInstance `xml:"instancesSet>item"`
			} `xml:"reservationSet>item"`
			Next string `xml:"nextToken"`
		}
		if e := a.call(ctx, region, "DescribeInstances", v, &res); e != nil {
			return nil, e
		}
		for _, r := range res.Reservations {
			for _, v := range r.Items {
				if v.State.Name == "terminated" {
					continue
				}
				name := v.ID
				for _, t := range v.Tags {
					if t.Key == "Name" {
						name = t.Value
					}
				}
				ips := []string{}
				if v.IP != "" {
					ips = append(ips, v.IP)
				}
				for _, n := range v.IPv6 {
					for _, ip := range n.Addresses {
						ips = append(ips, ip.IP)
					}
				}
				out = append(out, Instance{ID: v.ID, Name: name, Region: region, State: v.State.Name, IPs: ips, Plan: v.Plan, Image: v.Image, Actions: Actions(v.State.Name)})
			}
		}
		next = res.Next
		if next == "" {
			return out, nil
		}
	}
	return nil, &Error{Code: "AWS 实例分页超限"}
}
func (a *AWS) Create(ctx context.Context, s Spec, token string) (Operation, error) {
	v := url.Values{"ImageId": {s.Image}, "InstanceType": {s.Plan}, "MinCount": {"1"}, "MaxCount": {"1"}, "ClientToken": {token}, "NetworkInterface.1.DeviceIndex": {"0"}, "NetworkInterface.1.SubnetId": {s.Network}, "NetworkInterface.1.SecurityGroupId.1": {s.Group}, "NetworkInterface.1.AssociatePublicIpAddress": {"true"}, "TagSpecification.1.ResourceType": {"instance"}, "TagSpecification.1.Tag.1.Key": {"Name"}, "TagSpecification.1.Tag.1.Value": {s.Name}, "TagSpecification.1.Tag.2.Key": {"narbe-job"}, "TagSpecification.1.Tag.2.Value": {token}, "MetadataOptions.HttpTokens": {"required"}}
	if s.IPv6 {
		v.Set("NetworkInterface.1.Ipv6AddressCount", "1")
	}
	if s.Key != "" {
		v.Set("KeyName", s.Key)
	}
	if s.UserData != "" {
		v.Set("UserData", base64.StdEncoding.EncodeToString([]byte(s.UserData)))
	}
	var res struct {
		Items []awsInstance `xml:"instancesSet>item"`
	}
	e := a.call(ctx, s.Region, "RunInstances", v, &res)
	if e != nil {
		return Operation{}, e
	}
	if len(res.Items) != 1 {
		return Operation{}, &Error{Code: "未返回唯一实例标识", Uncertain: true}
	}
	return Operation{ResourceID: res.Items[0].ID, Region: s.Region, Expected: "running"}, nil
}
func (a *AWS) Action(ctx context.Context, i Instance, action, token string) (Operation, error) {
	op := Operation{ResourceID: i.ID, Region: i.Region, Expected: "running"}
	name := map[string]string{"start": "StartInstances", "stop": "StopInstances", "reboot": "RebootInstances", "delete": "TerminateInstances"}[action]
	if name == "" {
		return op, &Error{Code: "不支持的操作"}
	}
	if action == "stop" {
		op.Expected = "stopped"
	}
	if action == "delete" {
		op.Expected = "deleted"
	}
	if action == "reboot" {
		op.Expected = "accepted"
	}
	return op, a.call(ctx, i.Region, name, url.Values{"InstanceId.1": {i.ID}}, nil)
}
func (a *AWS) Poll(ctx context.Context, op Operation) (bool, error) {
	if op.Expected == "accepted" {
		return true, nil
	}
	items, e := a.Instances(ctx, op.Region)
	if e != nil {
		return false, e
	}
	for _, i := range items {
		if i.ID == op.ResourceID {
			return i.State == op.Expected, nil
		}
	}
	return op.Expected == "deleted", nil
}
