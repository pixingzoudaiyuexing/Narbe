// Package cloud contains independent adapters for the four supported providers.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Credentials map[string]string
type Choice struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Architecture   string            `json:"architecture,omitempty"`
	CPU            int               `json:"cpu,omitempty"`
	Memory         int               `json:"memory,omitempty"`
	Disk           int               `json:"disk,omitempty"`
	Price          float64           `json:"price,omitempty"`
	Family         string            `json:"family,omitempty"`
	Deprecated     bool              `json:"deprecated,omitempty"`
	Extra          map[string]string `json:"extra,omitempty"`
	Maintenance    string            `json:"maintenance,omitempty"`
	MaintenanceEnd string            `json:"maintenanceEnd,omitempty"`
	Pricing        *Pricing          `json:"pricing,omitempty"`
	DiskKind       string            `json:"diskKind,omitempty"`
}

// Pricing is an official advertised compute rate, never an all-in bill estimate.
type Pricing struct {
	Monthly  float64 `json:"monthly,omitempty"`
	Hourly   float64 `json:"hourly,omitempty"`
	Currency string  `json:"currency"`
	Source   string  `json:"source"`
}
type Instance struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Region       string   `json:"region"`
	State        string   `json:"state"`
	IPs          []string `json:"ips"`
	Plan         string   `json:"plan"`
	Image        string   `json:"image"`
	Actions      []string `json:"actions"`
	CPU          int      `json:"cpu,omitempty"`
	Memory       int      `json:"memory,omitempty"`
	Disk         int      `json:"disk,omitempty"`
	DiskKind     string   `json:"diskKind,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
	Pricing      *Pricing `json:"pricing,omitempty"`
	RegionName   string   `json:"regionName,omitempty"`
}
type Catalog struct {
	Regions  []Choice `json:"regions"`
	Plans    []Choice `json:"plans"`
	Images   []Choice `json:"images"`
	Networks []Choice `json:"networks"`
	Keys     []Choice `json:"keys"`
	Groups   []Choice `json:"groups"`
}
type Spec struct {
	Name           string `json:"name"`
	Region         string `json:"region"`
	Plan           string `json:"plan"`
	Image          string `json:"image"`
	Architecture   string `json:"architecture"`
	Network        string `json:"network"`
	Group          string `json:"group"`
	Key            string `json:"key"`
	SSHPublicKey   string `json:"sshPublicKey"`
	Password       string `json:"password"`
	Username       string `json:"username"`
	UserData       string `json:"userData"`
	IPv6           bool   `json:"ipv6"`
	Backups        bool   `json:"backups"`
	Disk           int    `json:"disk"`
	ManagementCIDR string `json:"managementCidr"`
}
type RelatedResource struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
}
type ResourceLedger struct {
	Status      string            `json:"status"`
	SourceJobID string            `json:"sourceJobId,omitempty"`
	SourceState string            `json:"sourceState,omitempty"`
	Resources   []RelatedResource `json:"resources,omitempty"`
}
type Operation struct {
	ResourceID string          `json:"resourceId"`
	PollURL    string          `json:"pollUrl,omitempty"`
	ActionID   string          `json:"actionId,omitempty"`
	Region     string          `json:"region"`
	Expected   string          `json:"expected"`
	Owned      []string        `json:"owned,omitempty"`
	PollKind   string          `json:"pollKind,omitempty"`
	NextPoll   int64           `json:"nextPoll,omitempty"`
	Ledger     *ResourceLedger `json:"ledger,omitempty"`
}
type Provider interface {
	Catalog(context.Context, string, string) (Catalog, error)
	Instances(context.Context, string) ([]Instance, error)
	Create(context.Context, Spec, string) (Operation, error)
	Action(context.Context, Instance, string, string) (Operation, error)
	Poll(context.Context, Operation) (bool, error)
}
type Error struct {
	Status    int
	Code      string
	Uncertain bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("云服务请求失败（HTTP %d，%s）", e.Status, e.Code)
}
func Uncertain(err error) bool { var e *Error; return errors.As(err, &e) && e.Uncertain }
func New(name string, c Credentials) (Provider, error) {
	h := &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	switch name {
	case "aws":
		return &AWS{c: c, h: h}, nil
	case "azure":
		return &Azure{c: c, h: h}, nil
	case "linode":
		return &Linode{rest: rest{h: h, base: "https://api.linode.com/v4", token: c["token"]}}, nil
	case "digitalocean":
		return &DigitalOcean{rest: rest{h: h, base: "https://api.digitalocean.com/v2", token: c["token"]}}, nil
	}
	return nil, errors.New("不支持的云厂商")
}
func ValidateCredentials(name string, c Credentials) error {
	required := map[string][]string{"aws": {"accessKeyId", "secretAccessKey"}, "azure": {"tenantId", "clientId", "clientSecret", "subscriptionId", "resourceGroup"}, "linode": {"token"}, "digitalocean": {"token"}}
	fields, ok := required[name]
	if !ok {
		return errors.New("不支持的云厂商")
	}
	allowed := map[string]bool{"sessionToken": name == "aws", "defaultRegion": name == "aws"}
	for _, k := range fields {
		allowed[k] = true
		if strings.TrimSpace(c[k]) == "" || len(c[k]) > 8192 {
			return fmt.Errorf("请填写有效的 %s", k)
		}
	}
	for k := range c {
		if !allowed[k] {
			return errors.New("凭据包含未知字段")
		}
	}
	if name == "aws" && c["defaultRegion"] != "" && !awsRegion.MatchString(c["defaultRegion"]) {
		return errors.New("AWS 验证地区格式无效")
	}
	if name == "azure" {
		for _, k := range []string{"tenantId", "clientId", "subscriptionId", "resourceGroup"} {
			if strings.ContainsAny(c[k], "/?#\\\r\n") || c[k] == "." || c[k] == ".." {
				return errors.New("Azure 标识无效")
			}
		}
	}
	return nil
}
func Find(items []Choice, id string) (Choice, bool) {
	for _, i := range items {
		if i.ID == id {
			return i, true
		}
	}
	return Choice{}, false
}
func ValidateSpec(s Spec, c Catalog) error {
	if len(s.Name) < 1 || len(s.Name) > 63 {
		return errors.New("名称长度应为 1–63 字符")
	}
	for _, ch := range s.Name {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
			return errors.New("名称仅支持英文字母、数字和连字符")
		}
	}
	if _, ok := Find(c.Regions, s.Region); !ok {
		return errors.New("地区不可用，请刷新")
	}
	p, ok := Find(c.Plans, s.Plan)
	if !ok {
		return errors.New("规格在该地区不可用")
	}
	im, ok := Find(c.Images, s.Image)
	if !ok || im.Deprecated {
		return errors.New("镜像不可用或已停止维护，请刷新")
	}
	if p.Architecture != "" && im.Architecture != "" && p.Architecture != im.Architecture {
		return errors.New("镜像与规格的 CPU 架构不兼容")
	}
	if s.Architecture != "" && p.Architecture != "" && s.Architecture != p.Architecture {
		return errors.New("请求架构与规格不兼容")
	}
	if s.Architecture != "" && im.Architecture != "" && s.Architecture != im.Architecture {
		return errors.New("镜像架构已变化，请重新选择")
	}
	if len(s.UserData) > 32768 {
		return errors.New("初始化脚本过长")
	}
	if s.Network != "" {
		if _, ok := Find(c.Networks, s.Network); !ok {
			return errors.New("网络不可用")
		}
	}
	if s.Key != "" {
		if _, ok := Find(c.Keys, s.Key); !ok {
			return errors.New("SSH 密钥不可用")
		}
	}
	if s.Group != "" {
		if _, ok := Find(c.Groups, s.Group); !ok {
			return errors.New("安全组不可用")
		}
	}
	return nil
}
func Arch(a string) string {
	switch a {
	case "amd64", "x64", "x86_64":
		return "x86_64"
	case "arm64", "aarch64", "Arm64":
		return "arm64"
	}
	return a
}
func Family(s string) string {
	s = strings.ToLower(s)
	if (strings.HasPrefix(s, "al20") && strings.Contains(s, "-ami-")) || strings.HasPrefix(s, "amzn2-ami-") || strings.HasPrefix(s, "amzn-ami-") {
		return "amazonlinux"
	}
	for _, f := range []string{"ubuntu", "debian", "windows", "rocky", "alma", "fedora", "centos"} {
		if strings.Contains(s, f) {
			return f
		}
	}
	return "other"
}

type rest struct {
	h           *http.Client
	base, token string
}

func (r rest) call(ctx context.Context, method, path string, body, out any) error {
	_, e := r.callHeaders(ctx, method, path, body, out)
	return e
}
func (r rest) callHeaders(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	var b io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		b = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, r.base+path, b)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	if method != "GET" {
		if e := beforeCloudWrite(ctx); e != nil {
			return nil, e
		}
	}
	resp, e := r.h.Do(req)
	if e != nil {
		return nil, &Error{Code: "网络中断，请核实云端结果", Uncertain: method != "GET"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return resp.Header, &Error{Status: resp.StatusCode, Code: errorCategory(raw), Uncertain: method != "GET" && (resp.StatusCode >= 500 || resp.StatusCode == 408)}
	}
	if out != nil {
		if e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); e != nil {
			return resp.Header, &Error{Status: resp.StatusCode, Code: "响应不完整", Uncertain: method != "GET"}
		}
	}
	return resp.Header, nil
}
func errorCategory(raw []byte) string {
	s := strings.ToLower(string(raw))
	for _, c := range []struct {
		words []string
		msg   string
	}{
		{[]string{"quota", "resource limit", "limit_exceeded", "limit exceeded"}, "云资源配额不足，请在云控制台检查配额"},
		{[]string{"capacity", "unavailable", "allocationfailed", "sku not available"}, "该地区资源或规格暂不可用"},
		{[]string{"password", "root_pass"}, "登录密码未满足云厂商规则"},
		{[]string{"ssh", "authorized_keys"}, "SSH 公钥配置无效"},
		{[]string{"image", "ami"}, "系统镜像无效或与当前配置不兼容"},
		{[]string{"unauthorized", "forbidden", "permission", "authorization", "credential", "authentication"}, "凭据无效或云资源权限不足"},
		{[]string{"region", "location"}, "所选地区不可用"},
		{[]string{"locked", "inprogress", "in progress", "conflict"}, "资源正在执行其他操作"},
		{[]string{"invalid", "validation"}, "配置未通过云厂商校验"},
	} {
		for _, w := range c.words {
			if strings.Contains(s, w) {
				return c.msg
			}
		}
	}
	return "云端拒绝请求，请在控制台检查权限、配额和配置"
}

func Escape(s string) string { return url.PathEscape(s) }
func Actions(state string) []string {
	switch state {
	case "running", "active", "on":
		return []string{"stop", "reboot", "delete"}
	case "stopped", "off", "offline", "deallocated":
		return []string{"start", "delete"}
	default:
		return []string{"delete"}
	}
}
