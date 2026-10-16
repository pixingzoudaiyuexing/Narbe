package cloud

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Egress is a global default, not a guest operating-system proxy. Secrets must
// only be serialized inside the Store's authenticated encrypted settings value.
type Egress struct {
	Mode     string `json:"mode"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (e Egress) Validate() error {
	if e.Mode == "direct" {
		if e.Protocol != "" || e.Host != "" || e.Port != 0 || e.Username != "" || e.Password != "" {
			return errors.New("直连模式不能附带代理设置")
		}
		return nil
	}
	if e.Mode != "proxy" || !contains([]string{"http", "https", "socks5", "socks5h"}, e.Protocol) {
		return errors.New("请选择有效的出口模式及代理协议")
	}
	if e.Port < 1 || e.Port > 65535 || len(e.Host) == 0 || len(e.Host) > 253 || strings.ContainsAny(e.Host, "/:@?#\\\r\n \t%") && net.ParseIP(e.Host) == nil {
		return errors.New("代理仅接受主机名或IP和1–65535端口，不接受URL")
	}
	if net.ParseIP(e.Host) == nil {
		for _, label := range strings.Split(e.Host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return errors.New("代理主机格式无效")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return errors.New("代理主机格式无效")
				}
			}
		}
	}
	if len(e.Username) > 255 || len(e.Password) > 255 || strings.ContainsAny(e.Username+e.Password, "\x00\r\n") || e.Password != "" && e.Username == "" {
		return errors.New("代理认证字段无效（最多255字节，密码需要用户名）")
	}
	return nil
}

var ec2Host = regexp.MustCompile(`^ec2\.[a-z]{2}(-[a-z]+)+-[0-9]+\.amazonaws\.com$`)

func allowedTarget(req *http.Request) bool {
	u := req.URL
	h := strings.ToLower(u.Hostname())
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || req.Host != "" && req.Host != u.Host {
		return false
	}
	return h == "api.linode.com" || h == "api.digitalocean.com" || h == "management.azure.com" || h == "login.microsoftonline.com" || h == "api64.ipify.org" || ec2Host.MatchString(h)
}

// Reserved and translation networks are never proxy endpoints. RFC1918/ULA
// endpoints additionally require an operator-supplied exact IP:port allowlist.
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "168.63.129.16/32", "192.0.0.0/24", "192.88.99.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20", "fec0::/10"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}

type dialPolicy struct {
	lookup  func(context.Context, string) ([]netip.Addr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
	private map[string]bool
}

func (p dialPolicy) connect(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, errors.New("出口地址无效")
	}
	ips, e := p.lookup(ctx, host)
	if e != nil || len(ips) == 0 {
		return nil, errors.New("出口DNS解析失败")
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		if !publicIP(ip) && !(ip.IsPrivate() && p.private[net.JoinHostPort(ip.String(), port)]) {
			return nil, errors.New("出口地址被安全策略拒绝")
		}
	}
	var last error
	for _, ip := range ips {
		conn, e := p.dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	_ = last
	return nil, errors.New("出口连接失败")
}

type guardedTransport struct{ transport *http.Transport }

func (g *guardedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !allowedTarget(r) {
		return nil, errors.New("非官方云API或检测目标已拒绝")
	}
	resp, e := g.transport.RoundTrip(r)
	if e != nil {
		return nil, errors.New(NetworkError(e))
	}
	return resp, nil
}
func (g *guardedTransport) CloseIdleConnections() { g.transport.CloseIdleConnections() }
func NetworkError(e error) string {
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.HostnameError
	switch {
	case errors.As(e, &cert) || errors.As(e, &unknown) || errors.As(e, &invalid):
		return "TLS证书或主机校验失败"
	case strings.Contains(e.Error(), "代理认证") || strings.Contains(strings.ToLower(e.Error()), "407") || strings.Contains(strings.ToLower(e.Error()), "authentication") || strings.Contains(strings.ToLower(e.Error()), "username/password"):
		return "代理认证失败"
	case strings.Contains(strings.ToLower(e.Error()), "tls"):
		return "TLS握手失败"
	case strings.Contains(e.Error(), "安全策略"):
		return "代理或目标地址被安全策略拒绝"
	default:
		return "网络或代理连接失败（未切换直连）"
	}
}

// NewEgressClient never reads HTTP_PROXY/HTTPS_PROXY/ALL_PROXY. The only private
// exception is an exact RFC1918/ULA IP:port configured by the server operator.
func NewEgressClient(e Egress, privateEndpoints string) (*http.Client, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	policy := dialPolicy{dial: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, private: map[string]bool{}}
	// Adapt LookupNetIP's network argument without losing context cancellation.
	policy.lookup = func(ctx context.Context, h string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
	}
	for _, v := range strings.Split(privateEndpoints, ",") {
		if v == "" {
			continue
		}
		host, port, err := net.SplitHostPort(v)
		ip, iperr := netip.ParseAddr(host)
		n, nerr := strconv.Atoi(port)
		if err != nil || iperr != nil || !ip.IsPrivate() || nerr != nil || n < 1 || n > 65535 {
			return nil, errors.New("私网代理授权必须为精确私网IP:端口")
		}
		if e.Mode == "proxy" {
			policy.private[net.JoinHostPort(ip.String(), port)] = true
		}
	}
	if e.Mode == "proxy" {
		if ip, err := netip.ParseAddr(e.Host); err == nil && !publicIP(ip) && !(ip.IsPrivate() && policy.private[net.JoinHostPort(ip.Unmap().String(), strconv.Itoa(e.Port))]) {
			return nil, errors.New("代理地址被安全策略拒绝")
		}
	}
	t := &http.Transport{Proxy: nil, DialContext: policy.connect, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 16, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 12, ForceAttemptHTTP2: true}
	if e.Mode == "proxy" {
		proxyURL := &url.URL{Scheme: e.Protocol, Host: net.JoinHostPort(e.Host, strconv.Itoa(e.Port))}
		if e.Username != "" {
			proxyURL.User = url.UserPassword(e.Username, e.Password)
		}
		t.Proxy = http.ProxyURL(proxyURL)
		t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != proxyURL.Host {
				return nil, errors.New("代理模式拒绝绕行拨号")
			}
			return policy.connect(ctx, network, address)
		}
		// net/http's SOCKS5 and SOCKS5H both pass target hostnames to the proxy.
	}
	return &http.Client{Transport: &guardedTransport{t}, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
