package probe

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicIPv4AndStrictTargetPolicy(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "10.0.0.1", "100.100.100.200", "127.0.0.1", "169.254.169.254", "172.16.0.1", "192.0.0.192", "192.0.2.1", "192.88.99.1", "192.168.0.1", "192.31.196.1", "192.52.193.1", "192.175.48.1", "168.63.129.16", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "255.255.255.255", "::1", "::ffff:8.8.8.8", "8.8.8.008", "8.8.8.8%eth0", "2130706433", "localhost", "8.8.8.8\n"} {
		if PublicIPv4(ip) {
			t.Errorf("accepted forbidden address %q", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "223.5.5.5"} {
		if !PublicIPv4(ip) {
			t.Errorf("rejected public address %q", ip)
		}
	}
	base := Target{IP: "8.8.8.8", Port: 443, Protocol: "https"}
	if err := ValidateTarget(base, []int{443}); err != nil {
		t.Fatal(err)
	}
	if ValidateTarget(base, nil) == nil || ValidateTarget(base, []int{80}) == nil {
		t.Fatal("empty or nonmatching local allowlist allowed target")
	}
	cases := []Target{
		{IP: "8.8.8.8", Port: 0, Protocol: "tcp"}, {IP: "8.8.8.8", Port: 65536, Protocol: "tcp"},
		{IP: "8.8.8.8", Port: 443, Protocol: "icmp"},
		{IP: "8.8.8.8", Port: 443, Protocol: "https", Path: "//169.254.169.254/latest"},
		{IP: "8.8.8.8", Port: 443, Protocol: "https", Path: "https://localhost/"},
		{IP: "8.8.8.8", Port: 443, Protocol: "https", Path: "/\r\nHost: localhost"},
		{IP: "8.8.8.8", Port: 443, Protocol: "https", ServerName: "x@localhost"},
		{IP: "8.8.8.8", Port: 443, Protocol: "https", ServerName: "localhost:80"},
		{IP: "8.8.8.8", Port: 443, Protocol: "https", ExpectedStatus: 302},
		{IP: "8.8.8.8", Port: 443, Protocol: "tcp", Challenge: "secret"},
	}
	for _, tt := range cases {
		if ValidateTarget(tt, []int{tt.Port}) == nil {
			t.Errorf("accepted malformed target %+v", tt)
		}
	}
}

func localDial(t *testing.T, server *httptest.Server, expectedAddr string, calls *int) dialContext {
	t.Helper()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		*calls++
		if network != "tcp4" || addr != expectedAddr {
			t.Errorf("unpinned dial %s %s", network, addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp4", server.Listener.Addr().String())
	}
}

func TestHTTPPinnedIPChallengeAndNoProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "health.example.com" || r.URL.Path != "/ready" {
			t.Errorf("unexpected application target %s %s", r.Host, r.URL.Path)
		}
		io.WriteString(w, "one-time-narbe-challenge")
	}))
	defer server.Close()
	calls := 0
	target := Target{IP: "8.8.8.8", Port: 8080, Protocol: "http", Path: "/ready", ServerName: "health.example.com", Challenge: "one-time-narbe-challenge"}
	r := check(context.Background(), target, time.Second, localDial(t, server, "8.8.8.8:8080", &calls))
	if !r.Success || calls != 1 || r.StatusCode != 200 || r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) {
		t.Fatalf("check failed %+v calls=%d", r, calls)
	}
	target.Challenge = "different-value"
	r = check(context.Background(), target, time.Second, localDial(t, server, "8.8.8.8:8080", &calls))
	if r.Success || r.Classification != "challenge_mismatch" || strings.Contains(r.Error, "one-time") {
		t.Fatalf("challenge failure leaked or accepted %+v", r)
	}
}

func TestHTTPRedirectSizeStatusAndTLSFailClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		h    http.HandlerFunc
		want string
	}{
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://169.254.169.254/latest/meta-data")
			w.WriteHeader(302)
		}, "redirect_rejected"},
		{"oversize", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, strings.Repeat("X", MaxResponseBytes+1))
		}, "response_too_large"},
		{"status", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "SUPER-SECRET", 500) }, "http_status"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := httptest.NewServer(test.h)
			defer s.Close()
			calls := 0
			r := check(context.Background(), Target{IP: "8.8.8.8", Port: 80, Protocol: "http"}, time.Second, localDial(t, s, "8.8.8.8:80", &calls))
			if r.Success || r.Classification != test.want || calls != 1 || strings.Contains(r.Error, "SUPER-SECRET") {
				t.Fatalf("unsafe result %+v calls=%d", r, calls)
			}
		})
	}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer s.Close()
	calls := 0
	r := check(context.Background(), Target{IP: "8.8.8.8", Port: 443, Protocol: "tls"}, time.Second, localDial(t, s, "8.8.8.8:443", &calls))
	if r.Success || r.Classification != "tls_error" {
		t.Fatalf("untrusted TLS certificate accepted %+v", r)
	}
}

func TestInvalidTargetAndTimeoutNeverDial(t *testing.T) {
	calls := 0
	dial := func(context.Context, string, string) (net.Conn, error) { calls++; panic("unsafe target dialed") }
	for _, target := range []Target{{IP: "127.0.0.1", Port: 80, Protocol: "http"}, {IP: "169.254.169.254", Port: 80, Protocol: "http"}, {IP: "8.8.8.8", Port: 80, Protocol: "file"}} {
		if r := check(context.Background(), target, time.Second, dial); r.Success || r.Classification != "invalid_target" {
			t.Fatal(r)
		}
	}
	if r := check(context.Background(), Target{IP: "8.8.8.8", Port: 80, Protocol: "http"}, time.Minute, dial); r.Classification != "invalid_target" || calls != 0 {
		t.Fatal("unbounded timeout accepted", r)
	}
}

func TestTCPConnectAndHTTPTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	calls := 0
	dial := localDial(t, s, "8.8.8.8:80", &calls)
	r := check(context.Background(), Target{IP: "8.8.8.8", Port: 80, Protocol: "tcp"}, time.Second, dial)
	if !r.Success || calls != 1 {
		t.Fatalf("TCP connect failed %+v", r)
	}
	r = check(context.Background(), Target{IP: "8.8.8.8", Port: 80, Protocol: "http"}, 100*time.Millisecond, dial)
	if r.Success || r.Classification != "timeout" || r.LatencyMS > 1000 {
		t.Fatalf("HTTP deadline not bounded %+v", r)
	}
}

func TestTLSAndHTTPSWithVerifiedTrustRoot(t *testing.T) {
	// A subprocess gives this test its own normal system-root initialization;
	// production does not gain an insecure-TLS option or a custom root API.
	if addr := os.Getenv("NARBE_TEST_TLS_LOOPBACK"); addr != "" {
		cert, err := os.ReadFile(os.Getenv("SSL_CERT_FILE"))
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(cert) {
			t.Fatal("test trust root invalid")
		}
		x509.SetFallbackRoots(roots)
		dial := func(ctx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp4" || target != "8.8.8.8:443" {
				t.Fatal("TLS target was not pinned")
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp4", addr)
		}
		for _, protocol := range []string{"tls", "https"} {
			r := check(context.Background(), Target{IP: "8.8.8.8", Port: 443, Protocol: protocol, ServerName: os.Getenv("NARBE_TEST_TLS_NAME")}, time.Second, dial)
			if !r.Success {
				t.Fatalf("verified %s failed %+v", protocol, r)
			}
		}
		return
	}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer s.Close()
	d := t.TempDir()
	certPath := filepath.Join(d, "roots.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTLSAndHTTPSWithVerifiedTrustRoot$")
	cmd.Env = append(os.Environ(), "NARBE_TEST_TLS_LOOPBACK="+s.Listener.Addr().String(), "NARBE_TEST_TLS_NAME="+s.Certificate().DNSNames[0], "SSL_CERT_FILE="+certPath, "SSL_CERT_DIR="+d, "GODEBUG="+os.Getenv("GODEBUG")+",x509usefallbackroots=1")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("TLS verification subprocess: %s %v", b, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEgressFixedServiceLookupAndMalformedEvidence(t *testing.T) {
	for _, tc := range []struct {
		body, lookup string
		ok           bool
	}{
		{`{"ip":"223.5.5.5","country":"CN"}`, "", true},
		{`{"ip":"8.8.8.8","country":"US"}`, "8.8.8.8", true},
		{`{"ip":"1.1.1.1","country":"US"}`, "8.8.8.8", false},
		{`{"ip":"127.0.0.1","country":"CN"}`, "", false},
		{`{"ip":"8.8.8.8","country":null}`, "", false},
		{`{"ip":"::ffff:8.8.8.8","country":"CN"}`, "", false},
		{`{"ip":"8.8.8.8","country":"cn"}`, "", false},
		{`{"ip":"8.8.8.8","country":"ZZ"}`, "", false},
		{strings.Repeat("x", 4097), "", false},
	} {
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != EgressSource+tc.lookup || req.Method != "GET" {
				t.Fatal("untrusted egress URL")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}
		e, err := observeEgress(context.Background(), tc.lookup, client)
		if (err == nil) != tc.ok || tc.ok && (e.Source != EgressSource || e.ObservedAt.IsZero()) {
			t.Fatalf("body %q result %+v err %v", tc.body, e, err)
		}
	}
	if _, err := LookupEgress(context.Background(), "169.254.169.254"); err == nil {
		t.Fatal("metadata egress lookup permitted")
	}
	tr := directClient(time.Second).Transport.(*http.Transport)
	if tr.Proxy != nil || tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.MinVersion == 0 {
		t.Fatal("unsafe direct transport")
	}
}

func TestProtocolJSONExactNames(t *testing.T) {
	b, err := json.Marshal(ResultRequest{TaskID: "task", LeaseID: "lease", Target: Target{IP: "8.8.8.8", Port: 443, Protocol: "tls"}})
	if err != nil || !strings.Contains(string(b), `"taskId"`) || !strings.Contains(string(b), `"leaseId"`) || !strings.Contains(string(b), `"classification"`) {
		t.Fatalf("protocol mismatch %s %v", b, err)
	}
}
