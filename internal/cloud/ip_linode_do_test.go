package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type linIPFixture struct {
	mu              sync.Mutex
	generation      string
	state           string
	newIP           string
	existingExtra   string
	candidateSource bool
	sourceCount     int
	quota           bool
	lost            bool
	interfaceReject bool
	interfaceQuota  bool
	interfaceLost   bool
	interfaceMutate bool
	reverseOrder    bool
	writes          []string
}

func (f *linIPFixture) writeList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.writes...)
}
func (f *linIPFixture) update(fn func(*linIPFixture)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func testLinodeIP(t *testing.T, generation string) (*Linode, *linIPFixture) {
	t.Helper()
	f := &linIPFixture{generation: generation, state: "running"}
	h := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		old := map[string]any{"address": "8.8.8.8", "linode_id": 123, "region": "us-east", "public": true, "reserved": false, "type": "ipv4"}
		if generation == "linode" {
			old["interface_id"] = 456
		}
		public := []any{old}
		addresses := []any{map[string]any{"address": "8.8.8.8", "primary": true}}
		if f.existingExtra != "" {
			public = append(public, map[string]any{"address": f.existingExtra, "linode_id": 123, "region": "us-east", "public": true, "reserved": false, "type": "ipv4"})
			addresses = append(addresses, map[string]any{"address": f.existingExtra, "primary": false})
		}
		if f.newIP != "" {
			added := map[string]any{"address": f.newIP, "linode_id": 123, "region": "us-east", "public": true, "reserved": false, "type": "ipv4"}
			if generation == "linode" {
				added["interface_id"] = 456
			}
			public = append(public, added)
			addresses = append(addresses, map[string]any{"address": f.newIP, "primary": false})
		}
		instanceIPs := []string{"8.8.8.8"}
		if f.existingExtra != "" {
			instanceIPs = append(instanceIPs, f.existingExtra)
		}
		if f.newIP != "" {
			instanceIPs = append(instanceIPs, f.newIP)
		}
		if f.reverseOrder && len(public) > 1 {
			public[0], public[1] = public[1], public[0]
			instanceIPs[0], instanceIPs[1] = instanceIPs[1], instanceIPs[0]
		}
		if r.Method != "GET" {
			f.writes = append(f.writes, r.Method+" "+r.URL.Path)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v4/linode/instances/123":
			screeningJSON(w, 200, map[string]any{"id": 123, "label": "vm", "type": "g6", "image": "linode/debian", "region": "us-east", "status": f.state, "interface_generation": generation, "ipv4": instanceIPs})
		case "GET /v4/linode/instances/123/ips":
			screeningJSON(w, 200, map[string]any{"ipv4": map[string]any{"public": public}})
		case "GET /v4/linode/instances/456":
			screeningJSON(w, 200, map[string]any{"id": 456, "label": "source", "type": "g6", "region": "us-east", "status": "running", "interface_generation": "legacy_config", "ipv4": []string{"4.4.4.4", "9.9.9.9"}})
		case "GET /v4/linode/instances/456/ips":
			source := []any{map[string]any{"address": "4.4.4.4", "linode_id": 456, "region": "us-east", "public": true, "reserved": false, "type": "ipv4"}}
			if f.sourceCount > 1 {
				source = append(source, map[string]any{"address": "9.9.9.9", "linode_id": 456, "region": "us-east", "public": true, "reserved": true, "type": "ipv4"})
			}
			screeningJSON(w, 200, map[string]any{"ipv4": map[string]any{"public": source}})
		case "GET /v4/linode/instances/123/interfaces":
			iface := map[string]any{"id": 456, "public": map[string]any{"ipv4": map[string]any{"addresses": addresses}}, "default_route": map[string]any{"ipv4": true}}
			screeningJSON(w, 200, map[string]any{"interfaces": []any{iface}})
		case "GET /v4/networking/ips":
			items := append([]any{}, public...)
			if f.newIP != "9.9.9.9" {
				candidate := map[string]any{"address": "9.9.9.9", "linode_id": nil, "region": "us-east", "public": true, "reserved": true, "type": "ipv4"}
				if f.candidateSource {
					candidate["linode_id"] = 456
					candidate["assigned_entity"] = map[string]any{"id": 456, "type": "linode"}
				}
				items = append(items, candidate)
			}
			screeningJSON(w, 200, map[string]any{"data": items, "pages": 1})
		case "POST /v4/linode/instances/123/shutdown":
			f.state = "offline"
			screeningJSON(w, 200, map[string]any{})
		case "POST /v4/linode/instances/123/boot":
			f.state = "running"
			screeningJSON(w, 200, map[string]any{})
		case "POST /v4/linode/instances/123/ips":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["type"] != "ipv4" || body["public"] != true || len(body) != 2 {
				t.Errorf("legacy allocation payload: %#v", body)
			}
			if f.quota {
				screeningJSON(w, 400, map[string]any{"errors": []any{map[string]any{"reason": "You have reached the IPv4 limit"}}})
				return
			}
			f.newIP = "1.1.1.1"
			if f.lost {
				screeningLost(w)
				return
			}
			screeningJSON(w, 200, map[string]any{"address": f.newIP})
		case "PUT /v4/linode/instances/123/interfaces/456":
			if f.state != "offline" {
				t.Error("interface updated while running")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			pub, _ := body["public"].(map[string]any)
			ipv4, _ := pub["ipv4"].(map[string]any)
			list, _ := ipv4["addresses"].([]any)
			if len(body) != 1 || len(list) != 2 || list[0].(map[string]any)["address"] != "8.8.8.8" || list[0].(map[string]any)["primary"] != true || list[1].(map[string]any)["address"] != "auto" {
				t.Errorf("interface payload lost old IP or changed routing: %#v", body)
			}
			if f.interfaceReject {
				if f.interfaceMutate {
					f.newIP = "1.1.1.1"
				}
				reason := "Additional IPv4 addresses require approval"
				if f.interfaceQuota {
					reason = "IPv4 limit exceeded"
				}
				screeningJSON(w, 400, map[string]any{"errors": []any{map[string]any{"reason": reason}}})
				return
			}
			f.newIP = "1.1.1.1"
			if f.interfaceLost {
				screeningLost(w)
				return
			}
			addresses = append(addresses, map[string]any{"address": f.newIP, "primary": false})
			screeningJSON(w, 200, map[string]any{"id": 456, "public": map[string]any{"ipv4": map[string]any{"addresses": addresses}}})
		case "POST /v4/networking/ips/assign":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["region"] != "us-east" || len(body["assignments"].([]any)) != 1 {
				t.Errorf("assign payload: %#v", body)
			}
			f.newIP = "9.9.9.9"
			if f.candidateSource {
				f.sourceCount = 1
			}
			screeningJSON(w, 200, map[string]any{})
		default:
			t.Errorf("unexpected Linode request: %s %s", r.Method, r.URL.String())
			screeningJSON(w, 404, map[string]any{})
		}
	})
	return &Linode{rest: rest{base: "https://api.linode.com/v4", h: h, token: "fixture"}}, f
}

func TestLinodeIPAllocationGenerationsAndSingleWrite(t *testing.T) {
	for _, generation := range []string{"legacy_config", "linode"} {
		t.Run(generation, func(t *testing.T) {
			l, f := testLinodeIP(t, generation)
			i := Instance{ID: "123", Region: "us-east"}
			info, err := l.IPInfo(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			if info.PrimaryIPv4 != "8.8.8.8" || info.NetworkGeneration != generation || len(info.AvailableIPs) != 1 {
				t.Fatal(info)
			}
			op, err := l.BeginIPChange(context.Background(), i, IPChange{Method: "linode-allocate", Snapshot: info.Snapshot, RiskAccepted: true}, "request-token")
			if err != nil {
				t.Fatal(err)
			}
			var saved Operation
			ctx := screeningRecord(&saved)
			done := false
			for turn := 0; turn < 8 && !done; turn++ {
				before := len(f.writeList())
				op, done, err = l.AdvanceIPChange(ctx, op)
				if err != nil {
					t.Fatal(err)
				}
				if len(f.writeList())-before > 1 {
					t.Fatal("multiple writes in one Advance", f.writeList())
				}
			}
			wantPrimary := "8.8.8.8"
			if generation == "legacy_config" {
				wantPrimary = ""
			}
			if !done || op.IP.NewIP != "1.1.1.1" || op.IP.PrimaryChanged || op.IP.After == nil || op.IP.After.PrimaryIPv4 != wantPrimary || len(op.IP.After.Addresses) != 2 {
				t.Fatal(op.IP)
			}
			if generation == "legacy_config" {
				for _, a := range op.IP.After.Addresses {
					if a.Primary || a.Kind != "additional" {
						t.Fatal("legacy multi-IP claimed a primary", a)
					}
				}
				if !strings.Contains(op.IP.Notice, "主次关系无法") {
					t.Fatal(op.IP.Notice)
				}
			}
			if generation == "linode" && strings.Join(f.writeList(), ",") != "POST /v4/linode/instances/123/shutdown,PUT /v4/linode/instances/123/interfaces/456,POST /v4/linode/instances/123/boot" {
				t.Fatal(f.writeList())
			}
			if generation == "legacy_config" && len(f.writeList()) != 1 {
				t.Fatal(f.writeList())
			}
		})
	}
}

func TestLinodeLegacyMultipleIPsUnknownPrimaryStableSnapshot(t *testing.T) {
	l, f := testLinodeIP(t, "legacy_config")
	f.update(func(f *linIPFixture) { f.existingExtra = "4.4.4.4" })
	i := Instance{ID: "123", Region: "us-east"}
	info, err := l.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	if info.PrimaryIPv4 != "" || len(info.Addresses) != 2 || len(info.ServiceIPv4) != 2 || !strings.Contains(strings.Join(info.Warnings, " "), "主 IPv4 未知") {
		t.Fatal(info)
	}
	for _, a := range info.Addresses {
		if a.Primary || a.Kind != "additional" {
			t.Fatal(a)
		}
	}
	op, err := l.BeginIPChange(context.Background(), i, IPChange{Method: "linode-allocate", Snapshot: info.Snapshot, RiskAccepted: true}, "request-token")
	if err != nil || op.IP.OldIP == "" {
		t.Fatal(op, err)
	}
	f.update(func(f *linIPFixture) { f.reverseOrder = true })
	again, err := l.IPInfo(context.Background(), i)
	if err != nil || again.Snapshot != info.Snapshot {
		t.Fatal(info.Snapshot, again.Snapshot, err)
	}
	ctx := screeningRecord(&Operation{})
	op, _, err = l.AdvanceIPChange(ctx, op)
	if err != nil {
		t.Fatal("array order alone rejected operation", err)
	}
	op, done, err := l.AdvanceIPChange(ctx, op)
	if err != nil || !done || op.IP.After == nil || op.IP.After.PrimaryIPv4 != "" || len(op.IP.After.Addresses) != 3 {
		t.Fatal(op.IP, done, err)
	}
	for _, old := range []string{"4.4.4.4", "8.8.8.8"} {
		found := false
		for _, a := range op.IP.After.Addresses {
			if a.Address == old {
				found = true
			}
		}
		if !found {
			t.Fatalf("old IP %s missing after allocation", old)
		}
	}
}

func TestLinodeIPQuotaAndLostResponseNeverReplay(t *testing.T) {
	if errors.Is(linQuotaError(&Error{Status: 400, Code: "Additional IPv4 addresses require approval"}), ErrIPQuota) {
		t.Fatal("support approval refusal must not trigger quota fallback")
	}
	l, f := testLinodeIP(t, "legacy_config")
	i := Instance{ID: "123", Region: "us-east"}
	info, err := l.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	f.update(func(f *linIPFixture) { f.quota = true })
	op, err := l.BeginIPChange(context.Background(), i, IPChange{Method: "linode-allocate", Snapshot: info.Snapshot, RiskAccepted: true}, "request-token")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err = l.AdvanceIPChange(screeningRecord(&Operation{}), op)
	if !errors.Is(err, ErrIPQuota) || len(f.writeList()) != 1 {
		t.Fatal(err, f.writeList())
	}
	f.update(func(f *linIPFixture) { f.quota = false; f.lost = true; f.writes = nil })
	op, err = l.BeginIPChange(context.Background(), i, IPChange{Method: "linode-allocate", Snapshot: info.Snapshot, RiskAccepted: true}, "request-token-2")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err = l.AdvanceIPChange(screeningRecord(&Operation{}), op)
	if !Uncertain(err) {
		t.Fatal(err)
	}
	_, _, err = l.ReconcileIPChange(context.Background(), op)
	if !Uncertain(err) || len(f.writeList()) != 1 {
		t.Fatal(err, f.writeList())
	}
}

func TestLinodeReservedAssignmentChecksSourceAndRegion(t *testing.T) {
	l, f := testLinodeIP(t, "legacy_config")
	f.update(func(f *linIPFixture) { f.candidateSource = true; f.sourceCount = 1 })
	i := Instance{ID: "123", Region: "us-east"}
	info, err := l.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.AvailableIPs) != 1 || info.AvailableIPs[0].InstanceID != "456" {
		t.Fatal(info.AvailableIPs)
	}
	change := IPChange{Method: "linode-assign", Snapshot: info.Snapshot, Address: "9.9.9.9", SourceInstanceID: "456", RiskAccepted: true}
	if _, err = l.BeginIPChange(context.Background(), i, change, "request-token"); err == nil {
		t.Fatal("source with only one public address accepted")
	}
	f.update(func(f *linIPFixture) { f.sourceCount = 2 })
	change.SourceInstanceID = "999"
	if _, err = l.BeginIPChange(context.Background(), i, change, "request-token"); err == nil {
		t.Fatal("source mismatch accepted")
	}
	change.SourceInstanceID = "456"
	op, err := l.BeginIPChange(context.Background(), i, change, "request-token")
	if err != nil {
		t.Fatal(err)
	}
	var saved Operation
	ctx := screeningRecord(&saved)
	op, done, err := l.AdvanceIPChange(ctx, op)
	if err != nil || done || len(f.writeList()) != 1 {
		t.Fatal(op, done, err, f.writeList())
	}
	op, done, err = l.ReconcileIPChange(ctx, op)
	if err != nil || !done || op.IP.NewIP != "9.9.9.9" || op.IP.PrimaryChanged {
		t.Fatal(op, done, err)
	}
	source, err := l.linIPView(context.Background(), "456")
	if err != nil || len(source.Public) != 1 || source.Public[0].Address != "4.4.4.4" {
		t.Fatal("source did not retain its other address", source, err)
	}
	if op.IP.After == nil || len(op.IP.After.Addresses) != 2 {
		t.Fatal("target lost its original address", op.IP.After)
	}
}

func TestLinodeInterfaceRejectedRestoresOnlyOriginalRunningInstance(t *testing.T) {
	for _, original := range []string{"running", "offline", "running-quota"} {
		t.Run(original, func(t *testing.T) {
			l, f := testLinodeIP(t, "linode")
			f.update(func(f *linIPFixture) {
				f.state = strings.TrimSuffix(original, "-quota")
				f.interfaceReject = true
				f.interfaceQuota = strings.HasSuffix(original, "-quota")
			})
			i := Instance{ID: "123", Region: "us-east"}
			info, err := l.IPInfo(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			op, err := l.BeginIPChange(context.Background(), i, IPChange{Method: "linode-allocate", Snapshot: info.Snapshot, RiskAccepted: true}, "request-token")
			if err != nil {
				t.Fatal(err)
			}
			var saved Operation
			ctx := screeningRecord(&saved)
			done := false
			for turn := 0; turn < 8 && !done; turn++ {
				before := len(f.writeList())
				op, done, err = l.AdvanceIPChange(ctx, op)
				if err != nil {
					t.Fatal(err)
				}
				if len(f.writeList())-before > 1 {
					t.Fatal("multiple writes", f.writeList())
				}
				if op.IP.Step == "restore-after-rejection" {
					read, ok, e := l.ReconcileIPChange(context.Background(), op)
					if e != nil || ok || read.IP.Step != "restore-after-rejection" || len(f.writeList()) != before+1 {
						t.Fatal(read, ok, e, f.writeList())
					}
					if op.IP.Step == "restore-boot-sent" {
						read, ok, e := l.ReconcileIPChange(context.Background(), op)
						if e != nil || !ok || read.IP.Step != "completed" || read.IP.Failure == "" || len(f.writeList()) != before+1 {
							t.Fatal(read, ok, e, f.writeList())
						}
						op, done = read, true
					}
				}
			}
			if !done || op.IP.Failure == "" || op.IP.NewIP != "" || op.IP.After == nil || op.IP.After.PrimaryIPv4 != "8.8.8.8" || op.IP.After.State != strings.TrimSuffix(original, "-quota") {
				t.Fatal(op.IP)
			}
			writes := f.writeList()
			if strings.HasPrefix(original, "running") && strings.Join(writes, ",") != "POST /v4/linode/instances/123/shutdown,PUT /v4/linode/instances/123/interfaces/456,POST /v4/linode/instances/123/boot" {
				t.Fatal(writes)
			}
			if original == "offline" && strings.Join(writes, ",") != "PUT /v4/linode/instances/123/interfaces/456" {
				t.Fatal(writes)
			}
			_, again, e := l.AdvanceIPChange(ctx, op)
			if e != nil || !again || len(f.writeList()) != len(writes) {
				t.Fatal(e, f.writeList())
			}
		})
	}
}

func TestLinodeInterfaceRejectChangedOrLostNeverBoots(t *testing.T) {
	for _, mode := range []string{"changed", "lost"} {
		t.Run(mode, func(t *testing.T) {
			l, f := testLinodeIP(t, "linode")
			f.update(func(f *linIPFixture) {
				f.interfaceReject = mode == "changed"
				f.interfaceMutate = mode == "changed"
				f.interfaceLost = mode == "lost"
			})
			i := Instance{ID: "123", Region: "us-east"}
			info, err := l.IPInfo(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			op, err := l.BeginIPChange(context.Background(), i, IPChange{Method: "linode-allocate", Snapshot: info.Snapshot, RiskAccepted: true}, "request-token")
			if err != nil {
				t.Fatal(err)
			}
			ctx := screeningRecord(&Operation{})
			for step := 0; step < 2; step++ {
				op, _, err = l.AdvanceIPChange(ctx, op)
				if err != nil {
					t.Fatal(err)
				}
			}
			op, _, err = l.AdvanceIPChange(ctx, op)
			if !Uncertain(err) || len(f.writeList()) != 2 || op.IP.Step != "interface-sent" {
				t.Fatal(op.IP, err, f.writeList())
			}
			_, _, err = l.ReconcileIPChange(context.Background(), op)
			if len(f.writeList()) != 2 {
				t.Fatal("reconcile dispatched a write", f.writeList())
			}
		})
	}
}

type doIPFixture struct {
	mu       sync.Mutex
	reserved []map[string]any
	writes   []string
	lost     bool
}

func (f *doIPFixture) writeList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.writes...)
}
func (f *doIPFixture) update(fn func(*doIPFixture)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func testDOIP(t *testing.T) (*DigitalOcean, *doIPFixture) {
	t.Helper()
	region := map[string]any{"slug": "nyc3"}
	droplet := func(id int) map[string]any {
		return map[string]any{"id": id, "region": region, "status": "active", "networks": map[string]any{"v4": []any{map[string]any{"type": "public", "ip_address": "8.8.8.8"}}}}
	}
	f := &doIPFixture{reserved: []map[string]any{{"ip": "9.9.9.9", "region": region, "locked": false, "droplet": nil}, {"ip": "4.4.4.4", "region": region, "locked": false, "droplet": droplet(456)}}}
	h := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != "GET" {
			f.writes = append(f.writes, r.Method+" "+r.URL.Path)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v2/droplets/123":
			screeningJSON(w, 200, map[string]any{"droplet": droplet(123)})
		case "GET /v2/droplets/456":
			screeningJSON(w, 200, map[string]any{"droplet": droplet(456)})
		case "GET /v2/reserved_ips":
			items := []any{}
			for _, v := range f.reserved {
				items = append(items, v)
			}
			screeningJSON(w, 200, map[string]any{"reserved_ips": items})
		case "POST /v2/reserved_ips":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 || body["droplet_id"] != float64(123) {
				t.Errorf("create payload: %#v", body)
			}
			item := map[string]any{"ip": "1.1.1.1", "region": region, "locked": false, "droplet": droplet(123)}
			f.reserved = append(f.reserved, item)
			if f.lost {
				screeningLost(w)
				return
			}
			screeningJSON(w, 202, map[string]any{"reserved_ip": item})
		case "POST /v2/reserved_ips/4.4.4.4/actions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["type"] != "assign" || body["droplet_id"] != float64(123) || len(body) != 2 {
				t.Errorf("assign payload: %#v", body)
			}
			f.reserved[1]["droplet"] = droplet(123)
			screeningJSON(w, 201, map[string]any{"action": map[string]any{"id": 99}})
		case "GET /v2/reserved_ips/4.4.4.4/actions/99":
			screeningJSON(w, 200, map[string]any{"action": map[string]any{"id": 99, "status": "completed"}})
		case "GET /v2/reserved_ips/1.1.1.1":
			screeningJSON(w, 200, map[string]any{"reserved_ip": f.reserved[len(f.reserved)-1]})
		case "GET /v2/reserved_ips/4.4.4.4":
			screeningJSON(w, 200, map[string]any{"reserved_ip": f.reserved[1]})
		default:
			t.Errorf("unexpected DO request: %s %s", r.Method, r.URL.String())
			screeningJSON(w, 404, map[string]any{})
		}
	})
	return &DigitalOcean{rest: rest{base: "https://api.digitalocean.com/v2", h: h, token: "fixture"}}, f
}

func TestDOReservedAllocateAndAssignPreserveOriginal(t *testing.T) {
	for _, method := range []string{"do-reserved-allocate", "do-reserved-assign"} {
		t.Run(method, func(t *testing.T) {
			d, f := testDOIP(t)
			i := Instance{ID: "123", Region: "nyc3"}
			info, err := d.IPInfo(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			if info.PrimaryIPv4 != "8.8.8.8" || len(info.AvailableIPs) != 2 {
				t.Fatal(info)
			}
			change := IPChange{Method: method, Snapshot: info.Snapshot, RiskAccepted: true}
			if method == "do-reserved-assign" {
				change.Address = "4.4.4.4"
				change.SourceInstanceID = "456"
			}
			op, err := d.BeginIPChange(context.Background(), i, change, "request-token")
			if err != nil {
				t.Fatal(err)
			}
			var saved Operation
			ctx := screeningRecord(&saved)
			before := len(f.writeList())
			op, done, err := d.AdvanceIPChange(ctx, op)
			if err != nil || done || len(f.writeList())-before != 1 {
				t.Fatal(op, done, err, f.writeList())
			}
			before = len(f.writeList())
			op, done, err = d.ReconcileIPChange(ctx, op)
			if err != nil || !done || len(f.writeList()) != before {
				t.Fatal(op, done, err, f.writeList())
			}
			if op.IP.After.PrimaryIPv4 != "8.8.8.8" || op.IP.PrimaryChanged || len(op.IP.After.ServiceIPv4) != 1 || op.IP.After.ServiceIPv4[0] != op.IP.NewIP {
				t.Fatal(op.IP)
			}
		})
	}
}

func TestDOReservedFailClosedAndLostAllocationNoReplay(t *testing.T) {
	d, f := testDOIP(t)
	i := Instance{ID: "123", Region: "nyc3"}
	info, err := d.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.BeginIPChange(context.Background(), i, IPChange{Method: "do-reserved-assign", Address: "4.4.4.4", SourceInstanceID: "999", Snapshot: info.Snapshot, RiskAccepted: true}, "request-token")
	if err == nil {
		t.Fatal("source mismatch accepted")
	}
	f.update(func(f *doIPFixture) { delete(f.reserved[0], "droplet") })
	if _, err = d.IPInfo(context.Background(), i); err == nil {
		t.Fatal("missing reserved binding accepted")
	}
	f.update(func(f *doIPFixture) { f.reserved[0]["droplet"] = nil; f.lost = true })
	op, err := d.BeginIPChange(context.Background(), i, IPChange{Method: "do-reserved-allocate", Snapshot: info.Snapshot, RiskAccepted: true}, "request-token-2")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err = d.AdvanceIPChange(screeningRecord(&Operation{}), op)
	if !Uncertain(err) || len(f.writeList()) != 1 {
		t.Fatal(err, f.writeList())
	}
	_, _, err = d.ReconcileIPChange(context.Background(), op)
	if !Uncertain(err) || len(f.writeList()) != 1 {
		t.Fatal(err, f.writeList())
	}
}
