package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

// A short-lived test proxy bound to a private local interface. Its ONLY target
// is a fixed official HTTPS host set. It never changes system proxy settings.
func liveReadProxy(t *testing.T) (string, int) {
	t.Helper()
	addresses, e := net.InterfaceAddrs()
	if e != nil {
		t.Fatal(e)
	}
	host := ""
	for _, a := range addresses {
		p, e := netip.ParsePrefix(a.String())
		if e == nil && p.Addr().Is4() && p.Addr().IsPrivate() {
			host = p.Addr().String()
			break
		}
	}
	if host == "" {
		t.Skip("No private interface for controlled real-egress proxy")
	}
	ln, e := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if e != nil {
		t.Fatal(e)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(port)
	allowed := map[string]bool{"api64.ipify.org:443": true, "api.linode.com:443": true, "api.digitalocean.com:443": true, "management.azure.com:443": true, "ec2.us-east-1.amazonaws.com:443": true}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || !allowed[r.Host] {
			w.WriteHeader(403)
			return
		}
		up, e := net.DialTimeout("tcp", r.Host, 10*time.Second)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		defer up.Close()
		conn, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(25 * time.Second))
		up.SetDeadline(time.Now().Add(25 * time.Second))
		rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		go func() { io.Copy(up, rw); up.Close() }()
		io.Copy(conn, up)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close(); ln.Close() })
	return host, n
}
func TestLiveEgressReadOnly(t *testing.T) {
	if os.Getenv("NARBE_LIVE_NETWORK_TEST") != "1" {
		t.Skip("explicit live read-only diagnostics only")
	}
	host, port := liveReadProxy(t)
	h, e := cloud.NewEgressClient(cloud.Egress{Mode: "proxy", Protocol: "http", Host: host, Port: port}, net.JoinHostPort(host, strconv.Itoa(port)))
	if e != nil {
		t.Fatal(e)
	}
	defer h.CloseIdleConnections()
	targets := map[string]string{"AWS unsigned read": "https://ec2.us-east-1.amazonaws.com/?Action=DescribeRegions&Version=2016-11-15", "Azure anonymous read": "https://management.azure.com/subscriptions?api-version=2022-12-01", "Linode public regions": "https://api.linode.com/v4/regions", "DigitalOcean anonymous regions": "https://api.digitalocean.com/v2/regions", "proxy IP": "https://api64.ipify.org?format=json"}
	for label, target := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
		res, e := h.Do(req)
		if e != nil {
			t.Log(label, "NOT VERIFIED:", cloud.NetworkError(e))
			cancel()
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		cancel()
		if label == "proxy IP" {
			t.Log("controlled proxy IP response", string(raw))
		} else {
			t.Log(fmt.Sprintf("%s: HTTP %d via controlled private allowlisted proxy; no cloud credentials supplied, authorization NOT VERIFIED", label, res.StatusCode))
		}
	}
}
