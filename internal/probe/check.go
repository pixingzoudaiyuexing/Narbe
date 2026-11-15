package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const MaxResponseBytes = 16 << 10

var deniedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("192.31.196.0/24"), netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.175.48.0/24"), netip.MustParsePrefix("168.63.129.16/32"),
}

// PublicIPv4 rejects all IANA special-use IPv4 ranges, IPv6 and mapped forms.
func PublicIPv4(s string) bool {
	ip, err := netip.ParseAddr(s)
	if err != nil || !ip.Is4() || ip.String() != s || !ip.IsGlobalUnicast() {
		return false
	}
	for _, p := range deniedIPv4 {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// ValidateTarget checks the independently configured local allowlist. An empty
// allowlist rejects every target rather than permitting arbitrary scans.
func ValidateTarget(t Target, allowedPorts []int) error {
	if !PublicIPv4(t.IP) {
		return errors.New("target must be a canonical public IPv4 address")
	}
	allowed := false
	for _, p := range allowedPorts {
		allowed = allowed || (p == t.Port && p > 0 && p <= 65535)
	}
	if !allowed {
		return errors.New("target port is not locally allowed")
	}
	switch t.Protocol {
	case "tcp", "tls", "http", "https":
	default:
		return errors.New("unsupported check protocol")
	}
	if t.ServerName != "" && !validServerName(t.ServerName) {
		return errors.New("invalid TLS server name")
	}
	if t.Path != "" {
		u, err := url.ParseRequestURI(t.Path)
		if err != nil || !strings.HasPrefix(t.Path, "/") || strings.HasPrefix(t.Path, "//") || u.IsAbs() || u.Host != "" || u.Fragment != "" || len(t.Path) > 1024 || strings.ContainsAny(t.Path, "\r\n") {
			return errors.New("invalid HTTP check path")
		}
	}
	if t.ExpectedStatus != 0 && (t.ExpectedStatus < 200 || t.ExpectedStatus > 299) {
		return errors.New("expected HTTP status must be 2xx")
	}
	if len(t.Challenge) > 256 || strings.ContainsAny(t.Challenge, "\r\n\x00") {
		return errors.New("invalid HTTP challenge")
	}
	if (t.Protocol == "tcp" || t.Protocol == "tls") && (t.Path != "" || t.ExpectedStatus != 0 || t.Challenge != "") {
		return errors.New("HTTP parameters require an HTTP check")
	}
	return nil
}

func validServerName(s string) bool {
	if len(s) > 253 || strings.HasSuffix(s, ".") || net.ParseIP(s) != nil {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

type dialContext func(context.Context, string, string) (net.Conn, error)

// Check performs one check, pinning the connection to the literal IP. The caller
// must additionally apply its own allowed-port policy with ValidateTarget.
func Check(ctx context.Context, target Target, timeout time.Duration) Result {
	d := net.Dialer{Timeout: timeout}
	return check(ctx, target, timeout, d.DialContext)
}

func check(ctx context.Context, t Target, timeout time.Duration, dial dialContext) (result Result) {
	result.StartedAt = time.Now().UTC()
	defer func() {
		result.CompletedAt = time.Now().UTC()
		result.LatencyMS = result.CompletedAt.Sub(result.StartedAt).Milliseconds()
	}()
	if err := ValidateTarget(t, []int{t.Port}); err != nil || timeout < 100*time.Millisecond || timeout > 30*time.Second {
		result.Classification, result.Error = "invalid_target", "检测目标或超时配置不合法"
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addr := net.JoinHostPort(t.IP, portString(t.Port))
	if t.Protocol == "tcp" || t.Protocol == "tls" {
		conn, err := dial(ctx, "tcp4", addr)
		if err != nil {
			result.Classification, result.Error = classify(err), safeError(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if t.Protocol == "tls" {
			name := t.ServerName
			if name == "" {
				name = t.IP
			}
			tlsConn := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name})
			if err = tlsConn.HandshakeContext(ctx); err != nil {
				result.Classification, result.Error = "tls_error", "TLS 握手或证书验证失败"
				return
			}
		}
		result.Success, result.Classification = true, "success"
		return
	}
	path := t.Path
	if path == "" {
		path = "/"
	}
	tr := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 8 << 10,
		DialContext: func(c context.Context, network, address string) (net.Conn, error) {
			// Ignore all DNS/redirect-derived addresses and always dial the approved IP.
			return dial(c, "tcp4", addr)
		},
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName},
		TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, t.Protocol+"://"+addr+path, nil)
	if t.ServerName != "" {
		req.Host = t.ServerName
	}
	req.Header.Set("User-Agent", "Narbe-Probe/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		result.Classification, result.Error = classify(err), safeError(err)
		return
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		result.Classification, result.Error = "redirect_rejected", "检测目标返回重定向，未跟随"
		return
	}
	expected := t.ExpectedStatus
	if expected == 0 {
		expected = http.StatusOK
	}
	if resp.StatusCode != expected {
		result.Classification, result.Error = "http_status", "HTTP 状态不符合成功条件"
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		result.Classification, result.Error = classify(err), safeError(err)
		return
	}
	if len(body) > MaxResponseBytes {
		result.Classification, result.Error = "response_too_large", "HTTP 响应超过大小限制"
		return
	}
	if t.Challenge != "" && string(body) != t.Challenge {
		result.Classification, result.Error = "challenge_mismatch", "HTTP 随机挑战不匹配"
		return
	}
	result.Success, result.Classification = true, "success"
	return
}

func classify(err error) string {
	var n net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &n) && n.Timeout() {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "connection_error"
}

func safeError(err error) string {
	switch classify(err) {
	case "timeout":
		return "连接或响应超时"
	case "canceled":
		return "检测已取消"
	default:
		return "连接失败；未读取或返回目标响应内容"
	}
}

func portString(port int) string {
	// Avoid allowing a text address or service name into the dial target.
	return strconv.Itoa(port)
}
