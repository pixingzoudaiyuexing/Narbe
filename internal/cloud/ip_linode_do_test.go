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
	candidateSource bool
	sourceCount     int
	quota           bool
	lost            bool
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
		if f.newIP != "" {
			added := map[string]any{"address": f.newIP, "linode_id": 123, "region": "us-east", "public": true, "reserved": false, "type": "ipv4"}
			if generation == "linode" {
				added["interface_id"] = 456
			}
			public = append(public, added)
			addresses = append(addresses, map[string]any{"address": f.newIP, "primary": false})
		}
		if r.Method != "GET" {
			f.writes = append(f.writes, r.Method+" "+r.URL.Path)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v4/linode/instances/123":
			screeningJSON(w, 200, map[string]any{"id": 123, "label": "vm", "type": "g6", "image": "linode/debian", "region": "us-east", "status": f.state, "interface_generation": generation, "ipv4": []string{"8.8.8.8"}})
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
			f.newIP = "1.1.1.1"
			addresses = append(addresses, map[string]any{"address": f.newIP, "primary": false})
			screeningJSON(w, 200, map[string]any{"id": 456, "public": map[string]any{"ipv4": map[string]any{"addresses": addresses}}})
		case "POST /v4/networking/ips/assign":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["region"] != "us-east" || len(body["assignments"].([]any)) != 1 {
				t.Errorf("assign payload: %#v", body)
			}
			f.newIP = "9.9.9.9"
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
			if !done || op.IP.NewIP != "1.1.1.1" || op.IP.PrimaryChanged || op.IP.After == nil || op.IP.After.PrimaryIPv4 != "8.8.8.8" || len(op.IP.After.Addresses) != 2 {
				t.Fatal(op.IP)
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

func TestLinodeIPQuotaAndLostResponseNeverReplay(t *testing.T) {
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
