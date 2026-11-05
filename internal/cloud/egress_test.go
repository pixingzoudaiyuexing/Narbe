package cloud

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

func fixtureCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Narbe test only"}, DNSNames: []string{"api.linode.com", "api.digitalocean.com", "management.azure.com", "login.microsoftonline.com", "ec2.us-east-1.amazonaws.com", "api64.ipify.org"}, IPAddresses: []net.IP{net.ParseIP("8.8.8.8")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	pk, _ := x509.MarshalECPrivateKey(key)
	cert, _ := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: pk}))
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return cert, pool
}
func TestEgressProtocolsAuthTLSAndFourCloudWire(t *testing.T) {
	cert, pool := fixtureCert(t)
	for _, protocol := range []string{"http", "https", "socks5", "socks5h"} {
		for _, auth := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/no-auth", true: "/auth"}[auth], func(t *testing.T) {
				var mu sync.Mutex
				hosts := []string{}
				oauth, aws := false, false
				target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					hosts = append(hosts, r.Host)
					mu.Unlock()
					if r.Header.Get("Proxy-Authorization") != "" {
						t.Error("proxy credential reached cloud")
					}
					switch r.Host {
					case "ec2.us-east-1.amazonaws.com":
						// Verify at the HTTPS destination, after the proxy tunnel.
						// The existing signer test alone only sees a pre-transport request.
						raw, _ := io.ReadAll(r.Body)
						signed := r.Clone(r.Context())
						signed.URL.Scheme, signed.URL.Host = "https", r.Host
						signed.Header = make(http.Header)
						signedNames := strings.Split(strings.Split(r.Header.Get("Authorization"), "SignedHeaders=")[1], ",")[0]
						for _, name := range strings.Split(signedNames, ";") {
							if name != "host" {
								signed.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), r.Header.Values(name)...)
							}
						}
						stamp, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
						if err != nil {
							t.Error(err)
						}
						err = v4.NewSigner().SignHTTP(context.Background(), sdk.Credentials{AccessKeyID: "fixture", SecretAccessKey: "fixture"}, signed, digest(raw), "ec2", "us-east-1", stamp)
						if err != nil || signed.Header.Get("Authorization") != r.Header.Get("Authorization") {
							t.Error("AWS signature invalid after proxy", err)
						}
						r.Body = io.NopCloser(strings.NewReader(string(raw)))
						r.ParseForm()
						if r.Form.Get("Action") != "DescribeRegions" || !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") || r.URL.Path != "/" {
							t.Error("AWS host/body signature changed")
						}
						aws = true
						io.WriteString(w, `<DescribeRegionsResponse/>`)
					case "login.microsoftonline.com":
						oauth = true
						r.ParseForm()
						if r.Form.Get("client_secret") != "fixture-secret" {
							t.Error("OAuth altered")
						}
						io.WriteString(w, `{"access_token":"fixture-token","expires_in":3600}`)
					case "management.azure.com":
						if r.Header.Get("Authorization") != "Bearer fixture-token" {
							t.Error("token lost")
						}
						io.WriteString(w, `{"value":[]}`)
					case "api.linode.com":
						io.WriteString(w, `{"data":[],"pages":1}`)
					case "api.digitalocean.com":
						io.WriteString(w, `{"droplets":[],"links":{}}`)
					case "api64.ipify.org":
						io.WriteString(w, `{"ip":"8.8.4.4"}`)
					default:
						t.Error("unexpected target")
					}
				}))
				target.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
				target.StartTLS()
				defer target.Close()
				dest := target.Listener.Addr().String()
				var tunnels []string
				tunnel := func(conn net.Conn, rd io.Reader, host string) {
					mu.Lock()
					tunnels = append(tunnels, host)
					mu.Unlock()
					up, e := net.Dial("tcp", dest)
					if e != nil {
						conn.Close()
						return
					}
					go func() { io.Copy(up, rd); up.Close() }()
					io.Copy(conn, up)
					conn.Close()
				}
				var proxyAddr string
				var closeProxy func()
				if protocol == "http" || protocol == "https" {
					proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != "CONNECT" {
							t.Error("non CONNECT")
							w.WriteHeader(400)
							return
						}
						if auth && r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:fixture-pass")) {
							w.WriteHeader(407)
							return
						}
						conn, rw, e := w.(http.Hijacker).Hijack()
						if e != nil {
							return
						}
						rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
						rw.Flush()
						tunnel(conn, rw, r.Host)
					}))
					if protocol == "https" {
						proxy.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
						proxy.StartTLS()
					} else {
						proxy.Start()
					}
					proxyAddr = proxy.Listener.Addr().String()
					closeProxy = proxy.Close
				} else {
					ln, e := net.Listen("tcp", "127.0.0.1:0")
					if e != nil {
						t.Fatal(e)
					}
					proxyAddr = ln.Addr().String()
					closeProxy = func() { ln.Close() }
					go func() {
						for {
							c, e := ln.Accept()
							if e != nil {
								return
							}
							go func() {
								rd := bufio.NewReader(c)
								hdr := make([]byte, 2)
								if _, e := io.ReadFull(rd, hdr); e != nil {
									return
								}
								methods := make([]byte, int(hdr[1]))
								io.ReadFull(rd, methods)
								method := byte(0)
								if auth {
									method = 2
								}
								c.Write([]byte{5, method})
								if auth {
									io.ReadFull(rd, hdr)
									u := make([]byte, int(hdr[1]))
									io.ReadFull(rd, u)
									n, _ := rd.ReadByte()
									p := make([]byte, int(n))
									io.ReadFull(rd, p)
									if string(u) != "user" || string(p) != "fixture-pass" {
										c.Write([]byte{1, 1})
										c.Close()
										return
									}
									c.Write([]byte{1, 0})
								}
								req := make([]byte, 4)
								io.ReadFull(rd, req)
								if req[3] != 3 {
									t.Error("SOCKS target DNS was resolved locally")
									c.Close()
									return
								}
								n, _ := rd.ReadByte()
								host := make([]byte, int(n))
								io.ReadFull(rd, host)
								port := make([]byte, 2)
								io.ReadFull(rd, port)
								if port[0] != 1 || port[1] != 187 {
									t.Error("wrong target port")
								}
								c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
								tunnel(c, rd, string(host)+":443")
							}()
						}
					}()
				}
				defer closeProxy()
				config := Egress{Mode: "proxy", Protocol: protocol, Host: "8.8.8.8", Port: 8080}
				if auth {
					config.Username = "user"
					config.Password = "fixture-pass"
				}
				h, e := NewEgressClient(config, "")
				if e != nil {
					t.Fatal(e)
				}
				defer h.CloseIdleConnections()
				tr := h.Transport.(*guardedTransport).transport
				tr.TLSClientConfig.RootCAs = pool
				// Test-only socket routing: production policy separately tested below. Exact
				// public proxy endpoint is mapped to our loopback fixture, never live cloud.
				tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != "8.8.8.8:8080" {
						t.Fatalf("bypass dial %s", address)
					}
					return (&net.Dialer{}).DialContext(ctx, network, proxyAddr)
				}
				a := &AWS{h: h, c: Credentials{"accessKeyId": "fixture", "secretAccessKey": "fixture"}}
				if e = a.call(context.Background(), "us-east-1", "DescribeRegions", nil, nil); e != nil {
					t.Fatal(e)
				}
				az := &Azure{h: h, c: Credentials{"tenantId": "tenant", "clientId": "id", "clientSecret": "fixture-secret", "subscriptionId": "sub", "resourceGroup": "rg"}}
				if _, e = az.Instances(context.Background(), ""); e != nil {
					t.Fatal(e)
				}
				for _, kind := range []string{"linode", "digitalocean"} {
					p, _ := NewWithClient(kind, Credentials{"token": "fixture"}, h)
					if _, e = p.Instances(context.Background(), ""); e != nil {
						t.Fatal(kind, e)
					}
				}
				res, e := h.Get("https://api64.ipify.org?format=json")
				if e != nil {
					t.Fatal(e)
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				mu.Lock()
				defer mu.Unlock()
				if !oauth || !aws || len(hosts) < 6 || len(tunnels) < 5 {
					t.Fatal("traffic missing", hosts, tunnels)
				}
			})
		}
	}
}
func TestEgressDNSRebindingAndPrivateExceptions(t *testing.T) {
	allowed := []string{"8.8.8.8"}
	dials := []string{}
	policy := dialPolicy{lookup: func(context.Context, string) ([]netip.Addr, error) {
		out := []netip.Addr{}
		for _, s := range allowed {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}, dial: func(_ context.Context, _, address string) (net.Conn, error) {
		dials = append(dials, address)
		c, p := net.Pipe()
		p.Close()
		return c, nil
	}, private: map[string]bool{"10.4.3.2:1080": true}}
	c, e := policy.connect(context.Background(), "tcp", "proxy.test:1080")
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	for _, ip := range []string{"127.0.0.1", "::1", "169.254.169.254", "169.254.170.2", "100.100.100.200", "10.4.3.3", "198.18.0.1", "64:ff9b::7f00:1", "168.63.129.16", "fec0::1", "2002:7f00:1::1", "3fff::1"} {
		allowed = []string{ip}
		if _, e = policy.connect(context.Background(), "tcp", "proxy.test:1080"); e == nil {
			t.Fatal("rebinding accepted", ip)
		}
	}
	allowed = []string{"8.8.8.8", "127.0.0.1"}
	if _, e = policy.connect(context.Background(), "tcp", "proxy.test:1080"); e == nil {
		t.Fatal("mixed DNS accepted")
	}
	allowed = []string{"10.4.3.2"}
	c, e = policy.connect(context.Background(), "tcp", "proxy.test:1080")
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	if len(dials) != 2 {
		t.Fatal("forbidden IP dialed", dials)
	}
}
func TestEgressTLSFailureTargetsAndNoDirectFallback(t *testing.T) {
	for _, protocol := range []string{"file", "ftp", "http://bad"} {
		if _, e := NewEgressClient(Egress{Mode: "proxy", Protocol: protocol, Host: "8.8.8.8", Port: 80}, ""); e == nil {
			t.Fatal("protocol accepted")
		}
	}
	for _, h := range []string{"127.0.0.1", "169.254.169.254", "::1", "10.0.0.1"} {
		if _, e := NewEgressClient(Egress{Mode: "proxy", Protocol: "http", Host: h, Port: 80}, ""); e == nil {
			t.Fatal("private proxy accepted", h)
		}
	}
	h, _ := NewEgressClient(Egress{Mode: "proxy", Protocol: "http", Host: "8.8.8.8", Port: 8080}, "")
	defer h.CloseIdleConnections()
	calls := 0
	h.Transport.(*guardedTransport).transport.DialContext = func(context.Context, string, string) (net.Conn, error) { calls++; return nil, io.ErrClosedPipe }
	for _, u := range []string{"http://api.linode.com/v4", "https://evil.test/", "https://api.linode.com.evil.test/", "https://127.0.0.1/", "https://api.linode.com:444/"} {
		if _, e := h.Get(u); e == nil {
			t.Fatal("unsafe target accepted")
		}
	}
	if calls != 0 {
		t.Fatal("unsafe target dialed")
	}
	_, e := h.Get("https://api.linode.com/v4")
	if e == nil || calls != 1 {
		t.Fatal("fallback or no error", calls, e)
	}
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	direct, _ := NewEgressClient(Egress{Mode: "direct"}, "")
	defer direct.CloseIdleConnections()
	tr := direct.Transport.(*guardedTransport).transport
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}
	_, e = direct.Get("https://api.linode.com/v4")
	if e == nil || !strings.Contains(NetworkError(e), "TLS") {
		t.Fatal("TLS bypass", e)
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	d, _ := NewEgressClient(Egress{Mode: "direct"}, "")
	if d.Transport.(*guardedTransport).transport.Proxy != nil {
		t.Fatal("direct obeyed environment")
	}
}

func TestEgressProxyAuthFailureAndRedirectNeverSendCredentials(t *testing.T) {
	cert, pool := fixtureCert(t)
	for _, code := range []int{407, 302, 307} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://evil.test/collect")
				w.WriteHeader(code)
			}))
			defer proxy.Close()
			h, _ := NewEgressClient(Egress{Mode: "proxy", Protocol: "http", Host: "8.8.8.8", Port: 80, Username: "user", Password: "secret-not-in-error"}, "")
			defer h.CloseIdleConnections()
			tr := h.Transport.(*guardedTransport).transport
			dials := 0
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				dials++
				return (&net.Dialer{}).DialContext(ctx, network, proxy.Listener.Addr().String())
			}
			req, _ := http.NewRequest("GET", "https://api.linode.com/v4/linode/instances", nil)
			req.Header.Set("Authorization", "Bearer private-cloud-token")
			_, e := h.Do(req)
			if e == nil || dials != 1 || strings.Contains(e.Error(), "secret-not-in-error") || strings.Contains(e.Error(), "private-cloud-token") {
				t.Fatal("auth/redirect failure leaked/fell back", dials, e)
			}
			if code == 407 && !strings.Contains(NetworkError(e), "代理认证") {
				t.Fatal("proxy auth classification", e)
			}
		})
	}
	hits := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Location", "https://evil.test/steal")
		w.WriteHeader(307)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	server.StartTLS()
	defer server.Close()
	h, _ := NewEgressClient(Egress{Mode: "direct"}, "")
	defer h.CloseIdleConnections()
	tr := h.Transport.(*guardedTransport).transport
	tr.TLSClientConfig.RootCAs = pool
	tr.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, server.Listener.Addr().String())
	}
	res, e := h.Get("https://api.linode.com/v4")
	if e != nil || res.StatusCode != 307 || hits != 1 {
		t.Fatal("redirect followed", hits, e)
	}
	res.Body.Close()
}
