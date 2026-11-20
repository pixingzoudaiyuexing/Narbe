package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real HTTP test doubles: every cloud request is redirected to this local wire
// server, never to a provider, and the provider's method/path/body are preserved.
func screeningHTTP(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	return &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		cloned := r.Clone(r.Context())
		target := *r.URL
		target.Scheme, target.Host = u.Scheme, u.Host
		cloned.URL = &target
		return http.DefaultTransport.RoundTrip(cloned)
	}), Timeout: time.Second * 5}
}
func screeningJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func screeningLost(w http.ResponseWriter) {
	c, _, e := w.(http.Hijacker).Hijack()
	if e == nil {
		_ = c.Close()
	}
}
func screeningRecord(last *Operation) context.Context {
	return WithOperationRecorder(context.Background(), func(op Operation) error { raw, _ := json.Marshal(op); return json.Unmarshal(raw, last) })
}

type screeningDOFixture struct {
	mu                      sync.Mutex
	data                    map[string]any
	associated              map[string]any
	deleted, lost, reserved bool
	writes                  []string
	reservedPages           [][]any
	reservedStatus          int
	reservedListCalls       int
}

func screeningDO(t *testing.T) (*DigitalOcean, *screeningDOFixture, Spec, Operation) {
	f := &screeningDOFixture{data: map[string]any{"id": 123, "name": "fixture", "status": "active", "region": map[string]any{"slug": "nyc3"}, "size": map[string]any{"slug": "s-1vcpu-1gb"}, "image": map[string]any{"id": 9}, "tags": []string{"narbe-screening-token"}, "networks": map[string]any{"v4": []any{map[string]any{"type": "public", "ip_address": "8.8.8.8"}}}, "volume_ids": []string{}, "features": []string{}, "vpc_uuid": "vpc-same"}, associated: map[string]any{"snapshots": []any{}, "volumes": []any{}, "volume_snapshots": []any{}}, writes: []string{}, reservedPages: [][]any{{}}, reservedStatus: 200}
	h := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == "DELETE" {
			if r.URL.Path != "/v2/droplets/123" {
				t.Error("delete was not exact instance", r.URL.Path)
			}
			f.writes = append(f.writes, r.Method+" "+r.URL.Path)
			f.deleted = true
			if f.lost {
				screeningLost(w)
				return
			}
			w.WriteHeader(204)
			return
		}
		switch r.URL.Path {
		case "/v2/droplets/123":
			if f.deleted {
				screeningJSON(w, 404, map[string]any{})
				return
			}
			screeningJSON(w, 200, map[string]any{"droplet": f.data})
		case "/v2/droplets/123/destroy_with_associated_resources":
			screeningJSON(w, 200, f.associated)
		case "/v2/reserved_ips":
			f.reservedListCalls++
			if r.URL.Query().Get("per_page") != "200" {
				t.Error("Reserved IP list omitted bounded page size")
			}
			if f.reservedStatus != 200 {
				screeningJSON(w, f.reservedStatus, map[string]any{"id": "forbidden", "message": "reserved_ip:read required"})
				return
			}
			if f.reserved {
				screeningJSON(w, 200, map[string]any{"reserved_ips": []any{map[string]any{"ip": "8.8.4.4", "droplet": map[string]any{"id": 123}}}})
				return
			}
			page := mustInt(r.URL.Query().Get("page"))
			if page < 1 || page > len(f.reservedPages) {
				t.Error("unexpected Reserved IP page", page)
				screeningJSON(w, 500, map[string]any{})
				return
			}
			screeningJSON(w, 200, map[string]any{"reserved_ips": f.reservedPages[page-1]})
		case "/v2/reserved_ips/8.8.8.8":
			if f.reserved {
				screeningJSON(w, 200, map[string]any{"reserved_ip": map[string]any{}})
			} else {
				screeningJSON(w, 404, map[string]any{})
			}
		default:
			t.Error("unexpected cloud path", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	return &DigitalOcean{rest{h: h, base: "https://api.digitalocean.com/v2", token: "fixture-private-token"}}, f, Spec{Name: "fixture", Region: "nyc3", Plan: "s-1vcpu-1gb", Image: "9"}, Operation{ResourceID: "123", Region: "nyc3", Expected: "active"}
}
func TestScreeningDODeletesOnlyOwnedExactIDAndConfirmsAbsence(t *testing.T) {
	p, f, s, op := screeningDO(t)
	r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	var saved Operation
	got, e := p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	if got.Expected != "deleted" || saved.Screening.Stage != "deleted-confirmed" || saved.Screening.Writes[0].State != "confirmed" {
		t.Fatalf("no confirmed durable deletion: %+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 1 {
		t.Fatal(f.writes)
	}
}
func TestScreeningDORejectsUnownedChangedComplexOrUnrecorded(t *testing.T) {
	for _, name := range []string{"unowned", "wrong-region", "wrong-image", "volume", "reserved", "associated", "malformed-associated", "changed-fingerprint", "no-recorder", "recorder-failure"} {
		t.Run(name, func(t *testing.T) {
			p, f, s, op := screeningDO(t)
			r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			switch name {
			case "unowned":
				f.data["tags"] = []string{"someone-else"}
			case "wrong-region":
				f.data["region"] = map[string]string{"slug": "other"}
			case "wrong-image":
				f.data["image"] = map[string]int{"id": 8}
			case "volume":
				f.data["volume_ids"] = []string{"unowned-volume"}
			case "reserved":
				f.reserved = true
			case "associated":
				f.associated["volumes"] = []any{map[string]any{"id": "user-volume"}}
			case "malformed-associated":
				delete(f.associated, "snapshots")
			case "changed-fingerprint":
				f.data["name"] = "owner-edited"
			}
			f.mu.Unlock()
			if contains([]string{"unowned", "wrong-region", "wrong-image", "volume", "reserved", "associated", "malformed-associated"}, name) {
				if _, inspectErr := p.InspectScreening(context.Background(), s, "screening-token", op); inspectErr == nil {
					t.Fatal("unsafe resource passed ownership inspection")
				}
			}
			var saved Operation
			ctx := screeningRecord(&saved)
			if name == "no-recorder" {
				ctx = context.Background()
			}
			if name == "recorder-failure" {
				ctx = WithOperationRecorder(context.Background(), func(Operation) error { return errors.New("db offline") })
			}
			_, e = p.ReplaceScreeningIP(ctx, s, "screening-token", op, r.Fingerprint)
			if e == nil {
				t.Fatal("unsafe deletion accepted")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.writes) != 0 {
				t.Fatal("unsafe cloud write", f.writes)
			}
		})
	}
}
func TestScreeningDOResponseLossNeverReplaysOrCreates(t *testing.T) {
	p, f, s, op := screeningDO(t)
	r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.lost = true
	f.mu.Unlock()
	var saved Operation
	_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if !Uncertain(e) {
		t.Fatalf("lost delete result not unknown: %v", e)
	}
	if saved.Screening.Stage != "deleting" || saved.Screening.Writes[0].State != "dispatching" {
		t.Fatal("write not durably journaled before lost response")
	}
	_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", saved, r.Fingerprint)
	if e == nil {
		t.Fatal("unknown delete replayed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 1 {
		t.Fatal("unknown replayed", f.writes)
	}
}

func TestScreeningDOSecondaryReservedIPIsNeverMissed(t *testing.T) {
	for _, name := range []string{"attached-secondary", "attached-page-two", "missing-droplet", "malformed-droplet", "invalid-droplet-id", "string-droplet-id", "invalid-ip", "scope-forbidden", "null-list", "duplicate-ip"} {
		t.Run(name, func(t *testing.T) {
			p, f, s, op := screeningDO(t)
			baseline, err := p.InspectScreening(context.Background(), s, "screening-token", op)
			if err != nil {
				t.Fatal(err)
			}
			entry := map[string]any{"ip": "8.8.4.4", "droplet": map[string]any{"id": 123}}
			f.mu.Lock()
			switch name {
			case "attached-secondary":
				f.reservedPages = [][]any{{entry}}
			case "attached-page-two":
				first := []any{}
				for n := 1; n <= 200; n++ {
					first = append(first, map[string]any{"ip": fmt.Sprintf("11.0.0.%d", n), "droplet": nil})
				}
				f.reservedPages = [][]any{first, {entry}}
			case "missing-droplet":
				delete(entry, "droplet")
				f.reservedPages = [][]any{{entry}}
			case "malformed-droplet":
				entry["droplet"] = map[string]any{}
				f.reservedPages = [][]any{{entry}}
			case "invalid-droplet-id":
				entry["droplet"] = map[string]any{"id": 0}
				f.reservedPages = [][]any{{entry}}
			case "string-droplet-id":
				entry["droplet"] = map[string]any{"id": "123"}
				f.reservedPages = [][]any{{entry}}
			case "invalid-ip":
				entry["ip"] = "127.0.0.1"
				f.reservedPages = [][]any{{entry}}
			case "scope-forbidden":
				f.reservedStatus = 403
			case "null-list":
				f.reservedPages = [][]any{nil}
			case "duplicate-ip":
				entry["droplet"] = nil
				f.reservedPages = [][]any{{entry, entry}}
			}
			f.mu.Unlock()
			var saved Operation
			_, err = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, baseline.Fingerprint)
			if err == nil {
				t.Fatal("unsafe deletion accepted despite secondary Reserved IP or unverifiable list")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.writes) != 0 {
				t.Fatal("Reserved IP uncertainty issued cloud delete", f.writes)
			}
			if name == "attached-page-two" && f.reservedListCalls < 3 {
				t.Fatal("complete paginated list was not read")
			}
		})
	}
}

func TestScreeningDOUnrelatedReservedIPsDoNotLockAccount(t *testing.T) {
	p, f, s, op := screeningDO(t)
	f.mu.Lock()
	f.reservedPages = [][]any{{map[string]any{"ip": "8.8.4.4", "droplet": nil}, map[string]any{"ip": "9.9.9.9", "droplet": map[string]any{"id": 456}}}}
	f.mu.Unlock()
	r, err := p.InspectScreening(context.Background(), s, "screening-token", op)
	if err != nil {
		t.Fatal(err)
	}
	var saved Operation
	_, err = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 1 || f.writes[0] != "DELETE /v2/droplets/123" {
		t.Fatal("unrelated Reserved IP blocked task or got deleted", f.writes)
	}
}

type screeningAWSFixture struct {
	mu                                       sync.Mutex
	state, ip, token, root, volume, userdata string
	eip, extraNIC, lost                      bool
	writes                                   []string
}

func screeningAWS(t *testing.T) (*AWS, *screeningAWSFixture, Spec, Operation) {
	f := &screeningAWSFixture{state: "running", ip: "8.8.8.8", token: "screening-token", root: "ebs", volume: "vol-original", writes: []string{}}
	h := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = r.ParseForm()
		action := r.Form.Get("Action")
		w.Header().Set("Content-Type", "application/xml")
		switch action {
		case "DescribeInstances":
			if r.Form.Get("InstanceId.1") != "i-12345678" {
				t.Error("instance lookup not exact")
			}
			ni := `<item><networkInterfaceId>eni-owned</networkInterfaceId><subnetId>subnet-original</subnetId><attachment><attachmentId>attach-original</attachmentId><deviceIndex>0</deviceIndex><deleteOnTermination>true</deleteOnTermination></attachment><privateIpAddressesSet><item><privateIpAddress>10.0.0.5</privateIpAddress><primary>true</primary></item></privateIpAddressesSet><groupSet><item><groupId>sg-original</groupId></item></groupSet></item>`
			if f.extraNIC {
				ni += ni
			}
			fmt.Fprintf(w, `<r><reservationSet><item><ownerId>123456789012</ownerId><instancesSet><item><instanceId>i-12345678</instanceId><imageId>ami-original</imageId><instanceType>t3.nano</instanceType><clientToken>%s</clientToken><subnetId>subnet-original</subnetId><vpcId>vpc-original</vpcId><rootDeviceType>%s</rootDeviceType><rootDeviceName>/dev/xvda</rootDeviceName><ipAddress>%s</ipAddress><instanceState><name>%s</name></instanceState><tagSet><item><key>narbe-job</key><value>%s</value></item></tagSet><groupSet><item><groupId>sg-original</groupId></item></groupSet><blockDeviceMapping><item><deviceName>/dev/xvda</deviceName><ebs><volumeId>%s</volumeId><deleteOnTermination>true</deleteOnTermination></ebs></item></blockDeviceMapping><networkInterfaceSet>%s</networkInterfaceSet></item></instancesSet></item></reservationSet></r>`, f.token, f.root, f.ip, f.state, f.token, f.volume, ni)
		case "DescribeAddresses":
			if r.Form.Get("Filter.1.Name") != "network-interface-id" || r.Form.Get("Filter.1.Value.1") != "eni-owned" {
				t.Error("EIP check not target ENI")
			}
			if f.eip {
				fmt.Fprint(w, `<r><addressesSet><item><publicIp>8.8.8.8</publicIp></item></addressesSet></r>`)
			} else {
				fmt.Fprint(w, `<r><addressesSet/></r>`)
			}
		case "DescribeInstanceAttribute":
			fmt.Fprintf(w, `<r><userData><value>%s</value></userData></r>`, f.userdata)
		case "StopInstances", "StartInstances":
			if r.Form.Get("InstanceId.1") != "i-12345678" {
				t.Error("wrong write target")
			}
			f.writes = append(f.writes, action)
			if action == "StopInstances" {
				f.state = "stopped"
				f.ip = ""
			} else {
				f.state = "running"
				f.ip = "8.8.4.4"
			}
			if f.lost {
				screeningLost(w)
				return
			}
			fmt.Fprint(w, `<r/>`)
		default:
			t.Error("unexpected AWS action", action)
			w.WriteHeader(500)
		}
	})
	return &AWS{c: Credentials{"accessKeyId": "fixture", "secretAccessKey": "fixture-secret"}, h: h}, f, Spec{Name: "fixture", Region: "us-east-1", Plan: "t3.nano", Image: "ami-original", Network: "subnet-original", Group: "sg-original"}, Operation{ResourceID: "i-12345678", Region: "us-east-1", Expected: "running"}
}
func TestScreeningAWSStopStartKeepsDiskAndConfirmsNewAddress(t *testing.T) {
	p, f, s, op := screeningAWS(t)
	r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	var saved Operation
	op, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	now, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	if now.Fingerprint != r.Fingerprint || now.Instance.IPs[0] == r.Instance.IPs[0] || saved.Screening.Stage != "completed" {
		t.Fatalf("bad stop/start result: %+v", now)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(f.writes, ",") != "StopInstances,StartInstances" || f.volume != "vol-original" {
		t.Fatal(f.writes)
	}
}
func TestScreeningAWSRefusesEIPInstanceStoreUnknownAndChanged(t *testing.T) {
	for _, name := range []string{"eip", "instance-store", "unowned", "extra-nic", "changed-disk", "changed-data", "no-recorder"} {
		t.Run(name, func(t *testing.T) {
			p, f, s, op := screeningAWS(t)
			r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			switch name {
			case "eip":
				f.eip = true
			case "instance-store":
				f.root = "instance-store"
			case "unowned":
				f.token = "old-unrelated-token"
			case "extra-nic":
				f.extraNIC = true
			case "changed-disk":
				f.volume = "vol-other"
			case "changed-data":
				f.userdata = "dW5hdXRob3JpemVk"
			}
			f.mu.Unlock()
			if contains([]string{"eip", "instance-store", "unowned", "extra-nic", "changed-data"}, name) {
				if _, inspectErr := p.InspectScreening(context.Background(), s, "screening-token", op); inspectErr == nil {
					t.Fatal("unsafe AWS passed ownership inspection")
				}
			}
			var saved Operation
			ctx := screeningRecord(&saved)
			if name == "no-recorder" {
				ctx = context.Background()
			}
			if _, e = p.ReplaceScreeningIP(ctx, s, "screening-token", op, r.Fingerprint); e == nil {
				t.Fatal("unsafe stop/start accepted")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.writes) != 0 {
				t.Fatal("unsafe cloud writes", f.writes)
			}
		})
	}
}
func TestScreeningAWSLostStopDoesNotStartOrReplay(t *testing.T) {
	p, f, s, op := screeningAWS(t)
	r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.lost = true
	f.mu.Unlock()
	var saved Operation
	_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if !Uncertain(e) {
		t.Fatal("lost stop was not unknown", e)
	}
	_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", saved, r.Fingerprint)
	if e == nil {
		t.Fatal("unknown replay accepted")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(f.writes, ",") != "StopInstances" {
		t.Fatal(f.writes)
	}
}

type screeningAzureFixture struct {
	mu                          sync.Mutex
	resources                   map[string]map[string]any
	writes                      []string
	loss                        string
	leaveOldAttached, mutateNIC bool
}

func screeningAzure(t *testing.T) (*Azure, *screeningAzureFixture, Spec, Operation) {
	p := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture-auth", expires: time.Now().Add(time.Hour)}
	s := Spec{Name: "fixture", Region: "eastus", Plan: "Standard_B1s", Image: "Canonical|ubuntu|sku|v1", Username: "narbe", Network: p.group() + "/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"}
	op := p.Plan(s, "screening-token")
	nic, pip, nsg, disk := op.Owned[1], op.Owned[0], op.Owned[2], op.Owned[3]
	config := nic + "/ipConfigurations/primary"
	f := &screeningAzureFixture{resources: map[string]map[string]any{}, writes: []string{}}
	resource := func(id string, props map[string]any) map[string]any {
		return map[string]any{"id": id, "name": id[strings.LastIndex(id, "/")+1:], "location": s.Region, "tags": map[string]any{"narbe-job": "screening-token"}, "etag": "etag-original", "properties": props}
	}
	f.resources[op.ResourceID] = resource(op.ResourceID, map[string]any{"vmId": "unique-VM-identity", "provisioningState": "Succeeded", "hardwareProfile": map[string]any{"vmSize": s.Plan}, "storageProfile": map[string]any{"imageReference": map[string]any{"publisher": "Canonical", "offer": "ubuntu", "sku": "sku", "version": "v1"}, "osDisk": map[string]any{"managedDisk": map[string]any{"id": disk}}, "dataDisks": []any{}}, "networkProfile": map[string]any{"networkInterfaces": []any{map[string]any{"id": nic}}}, "instanceView": map[string]any{"statuses": []any{map[string]any{"code": "PowerState/running"}}}, "osProfile": map[string]any{"adminUsername": "narbe"}})
	f.resources[nic] = resource(nic, map[string]any{"provisioningState": "Succeeded", "virtualMachine": map[string]any{"id": op.ResourceID}, "networkSecurityGroup": map[string]any{"id": nsg}, "enableIPForwarding": false, "dnsSettings": map[string]any{"dnsServers": []any{"1.1.1.1"}}, "ipConfigurations": []any{map[string]any{"name": "primary", "id": config, "properties": map[string]any{"subnet": map[string]any{"id": s.Network}, "privateIPAddress": "10.0.0.5", "publicIPAddress": map[string]any{"id": pip}}}}})
	f.resources[pip] = resource(pip, map[string]any{"provisioningState": "Succeeded", "publicIPAllocationMethod": "Static", "publicIPAddressVersion": "IPv4", "ipAddress": "8.8.8.8", "ipConfiguration": map[string]any{"id": config}})
	f.resources[pip]["sku"] = map[string]any{"name": "Standard", "tier": "Regional"}
	f.resources[nsg] = resource(nsg, map[string]any{"provisioningState": "Succeeded", "securityRules": []any{map[string]any{"name": "unchanged-rule"}}})
	f.resources[disk] = resource(disk, map[string]any{"provisioningState": "Succeeded", "diskSizeGB": 30})
	f.resources[disk]["managedBy"] = op.ResourceID
	p.h = screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.URL.Path
		if r.Method == "GET" {
			if body, ok := f.resources[id]; ok {
				screeningJSON(w, 200, body)
			} else {
				screeningJSON(w, 404, map[string]any{})
			}
			return
		}
		f.writes = append(f.writes, r.Method+" "+id)
		if r.Method == "PUT" {
			var body map[string]any
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
			if strings.Contains(id, "/publicIPAddresses/") {
				body["id"] = id
				body["name"] = id[strings.LastIndex(id, "/")+1:]
				body["etag"] = "etag-new-pip"
				azProperties(body)["provisioningState"] = "Succeeded"
				azProperties(body)["ipAddress"] = "8.8.4.4"
				f.resources[id] = body
				if f.mutateNIC {
					azProperties(f.resources[nic])["enableIPForwarding"] = true
				}
			} else if id == nic {
				if r.Header.Get("If-Match") != "etag-original" {
					t.Error("NIC ETag not forwarded")
				}
				if azString(azProperties(body)["dnsSettings"].(map[string]any)["dnsServers"].([]any)[0]) != "1.1.1.1" {
					t.Error("NIC DNS was lost")
				}
				newID := azID(azProperties(azObject(azProperties(body)["ipConfigurations"].([]any)[0]))["publicIPAddress"])
				if newID == pip {
					t.Error("IP did not change")
				}
				azProperties(f.resources[newID])["ipConfiguration"] = map[string]any{"id": config}
				if !f.leaveOldAttached {
					delete(azProperties(f.resources[pip]), "ipConfiguration")
				}
				f.resources[nic] = body
			} else {
				t.Error("must not update VM/disk/NSG", id)
			}
		} else if r.Method == "DELETE" {
			if id != pip {
				t.Error("must only delete original owned public IP", id)
			}
			if azID(azProperties(f.resources[id])["ipConfiguration"]) != "" {
				t.Error("deleted attached PIP")
			}
			delete(f.resources, id)
		} else {
			t.Error("unexpected method", r.Method)
		}
		if f.loss != "" && strings.Contains(r.Method+" "+id, f.loss) {
			screeningLost(w)
			return
		}
		screeningJSON(w, 200, map[string]any{})
	})
	return p, f, s, op
}
func TestScreeningAzureSwapsPIPPreservesVMAndCleansOnlyDetachedOwnedPIP(t *testing.T) {
	p, f, s, op := screeningAzure(t)
	r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	var saved Operation
	op, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	if saved.Screening.Stage != "completed" || len(saved.Screening.Writes) != 3 || saved.Screening.Writes[2].State != "confirmed" || op.Screening.OldPublicIPID == op.Screening.NewPublicIPID {
		t.Fatalf("incomplete durable replacement: %+v", saved)
	}
	post, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	if post.Instance.ID != r.Instance.ID || post.Instance.IPs[0] == r.Instance.IPs[0] {
		t.Fatal("VM replaced or IP did not change")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 3 || f.resources[r.Instance.ID] == nil || f.resources[op.Owned[3]] == nil || f.resources[op.Owned[2]] == nil {
		t.Fatal("unexpected resource changes", f.writes)
	}
}
func TestScreeningAzureRefusesUnownedMalformedAndExternalChanges(t *testing.T) {
	for _, name := range []string{"wrong-subscription", "unowned", "extra-nic", "changed-osdisk", "foreign-pip", "fingerprint", "missing-etag", "no-recorder", "external-edit-after-pip-create", "old-still-attached"} {
		t.Run(name, func(t *testing.T) {
			p, f, s, op := screeningAzure(t)
			r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			switch name {
			case "wrong-subscription":
				op.ResourceID = strings.Replace(op.ResourceID, "/sub/", "/foreign/", 1)
			case "unowned":
				f.resources[op.ResourceID]["tags"] = map[string]any{"narbe-job": "other-token"}
			case "extra-nic":
				azObject(azProperties(f.resources[op.ResourceID])["networkProfile"])["networkInterfaces"] = []any{map[string]any{"id": op.Owned[1]}, map[string]any{"id": "unowned"}}
			case "changed-osdisk":
				azObject(azObject(azProperties(f.resources[op.ResourceID])["storageProfile"])["osDisk"])["managedDisk"] = map[string]any{"id": "foreign-disk"}
			case "foreign-pip":
				azProperties(f.resources[op.Owned[0]])["ipConfiguration"] = map[string]any{"id": "other-nic"}
			case "fingerprint":
				azProperties(f.resources[op.Owned[2]])["securityRules"] = []any{}
			case "missing-etag":
				delete(f.resources[op.Owned[1]], "etag")
			case "external-edit-after-pip-create":
				f.mutateNIC = true
			case "old-still-attached":
				f.leaveOldAttached = true
			}
			f.mu.Unlock()
			var saved Operation
			ctx := screeningRecord(&saved)
			if name == "no-recorder" {
				ctx = context.Background()
			}
			_, e = p.ReplaceScreeningIP(ctx, s, "screening-token", op, r.Fingerprint)
			if e == nil {
				t.Fatal("unsafe Azure operation accepted")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			max := 0
			if name == "external-edit-after-pip-create" {
				max = 1
			}
			if name == "old-still-attached" {
				max = 2
			}
			if len(f.writes) > max {
				t.Fatal("unsafe Azure write", f.writes)
			}
			if max > 0 && !Uncertain(e) {
				t.Fatal("existing newly billed resources not reported unknown", e)
			}
		})
	}
}
func TestScreeningAzureLostWriteAtEveryStageStopsAndRetainsResourceLedger(t *testing.T) {
	for _, stage := range []string{"PUT /subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/", "PUT /subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/", "DELETE "} {
		t.Run(stage, func(t *testing.T) {
			p, f, s, op := screeningAzure(t)
			r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			f.loss = stage
			f.mu.Unlock()
			var saved Operation
			_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
			if !Uncertain(e) {
				t.Fatalf("response loss was not unknown: %v", e)
			}
			if saved.Screening.NewPublicIPID == "" || saved.Screening.OldPublicIPID == "" || len(saved.Ledger.Resources) != 6 {
				t.Fatal("planned/remaining resources not retained")
			}
			before := len(saved.Screening.Writes)
			_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", saved, r.Fingerprint)
			if e == nil {
				t.Fatal("unknown Azure write replayed")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.writes) != before {
				t.Fatal("additional write after response loss", f.writes)
			}
		})
	}
}

func TestScreeningLedgerDoesNotExposeCloudCredentials(t *testing.T) {
	p, _, s, op := screeningDO(t)
	r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	var saved Operation
	_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(saved)
	for _, secret := range []string{"fixture-private-token", "user_data", "Password", "root_pass"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret in public ledger")
		}
	}
}

type screeningLinodeFixture struct {
	mu                                 sync.Mutex
	instance                           map[string]any
	disks, configs, volumes, firewalls []any
	ips                                map[string]any
	deleted, lost                      bool
	writes                             []string
}

func screeningLinode(t *testing.T) (*Linode, *screeningLinodeFixture, Spec, Operation) {
	f := &screeningLinodeFixture{instance: map[string]any{"id": 123, "label": "fixture", "region": "us-east", "type": "g6-nanode-1", "image": "linode/ubuntu24.04", "status": "running", "tags": []string{"narbe-screening-token"}, "ipv4": []string{"8.8.8.8"}, "interface_generation": "legacy_config", "locks": []any{}, "has_user_data": false}, disks: []any{map[string]any{"id": 11, "label": "root", "filesystem": "ext4", "size": 20000, "status": "ready"}}, configs: []any{map[string]any{"id": 12, "label": "normal", "devices": map[string]any{"sda": map[string]any{"disk_id": 11, "volume_id": 0}}, "interfaces": []any{map[string]any{"purpose": "public"}}}}, volumes: []any{}, firewalls: []any{}, ips: map[string]any{"ipv4": map[string]any{"public": []any{map[string]any{"address": "8.8.8.8", "linode_id": 123, "region": "us-east", "public": true, "reserved": false}}, "reserved": []any{}, "shared": []any{}, "vpc": []any{}}}, writes: []string{}}
	h := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/v4")
		if r.Method != "GET" {
			f.writes = append(f.writes, r.Method+" "+path)
			if r.Method != "DELETE" || path != "/linode/instances/123" {
				t.Error("wrong Linode write", r.Method, path)
			}
			f.deleted = true
			if f.lost {
				screeningLost(w)
				return
			}
			screeningJSON(w, 200, map[string]any{})
			return
		}
		switch path {
		case "/linode/instances/123":
			if f.deleted {
				screeningJSON(w, 404, map[string]any{})
			} else {
				screeningJSON(w, 200, f.instance)
			}
		case "/linode/instances/123/ips":
			screeningJSON(w, 200, f.ips)
		case "/linode/instances/123/disks":
			screeningJSON(w, 200, map[string]any{"data": f.disks, "pages": 1})
		case "/linode/instances/123/configs":
			screeningJSON(w, 200, map[string]any{"data": f.configs, "pages": 1})
		case "/linode/instances/123/volumes":
			screeningJSON(w, 200, map[string]any{"data": f.volumes, "pages": 1})
		case "/linode/instances/123/firewalls":
			screeningJSON(w, 200, map[string]any{"data": f.firewalls, "pages": 1})
		case "/linode/instances/123/interfaces":
			screeningJSON(w, 200, map[string]any{"interfaces": []any{map[string]any{"id": 21, "public": map[string]any{}}}})
		default:
			t.Error("unexpected Linode path", path)
			screeningJSON(w, 500, map[string]any{})
		}
	})
	return &Linode{rest{h: h, base: "https://api.linode.com/v4", token: "private-fixture-token"}}, f, Spec{Name: "fixture", Region: "us-east", Plan: "g6-nanode-1", Image: "linode/ubuntu24.04"}, Operation{ResourceID: "123", Region: "us-east", Expected: "running", Owned: []string{"linode:123"}}
}
func TestScreeningLinodeExactOwnedDeleteWithNoAttachedResourceLeak(t *testing.T) {
	p, f, s, op := screeningLinode(t)
	r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	var saved Operation
	got, e := p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	if got.Expected != "deleted" || saved.Screening.Stage != "deleted-confirmed" || saved.Ledger.Resources[0].State != "deleted-confirmed" {
		t.Fatal("deletion not confirmed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 1 || f.writes[0] != "DELETE /linode/instances/123" {
		t.Fatal(f.writes)
	}
}
func TestScreeningLinodeRejectsExternalMutationsReservedIPsAndComplexTopology(t *testing.T) {
	for _, name := range []string{"unowned", "foreign-region", "foreign-image", "volume", "changed-disk", "reserved", "reserved-unknown", "shared", "extra-ip", "new-interfaces", "vlan", "own-created-firewall", "changed-firewall", "no-recorder"} {
		t.Run(name, func(t *testing.T) {
			p, f, s, op := screeningLinode(t)
			r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
			if e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			ips := azObject(f.ips["ipv4"])
			ip := azObject(ips["public"].([]any)[0])
			switch name {
			case "unowned":
				f.instance["tags"] = []string{"other-token"}
			case "foreign-region":
				f.instance["region"] = "other"
			case "foreign-image":
				f.instance["image"] = "other"
			case "volume":
				f.volumes = []any{map[string]any{"id": 91, "label": "unowned", "size": 30}}
			case "changed-disk":
				f.disks[0].(map[string]any)["size"] = 90000
			case "reserved":
				ip["reserved"] = true
			case "reserved-unknown":
				delete(ip, "reserved")
			case "shared":
				ips["shared"] = []any{map[string]any{"address": "8.8.4.4"}}
			case "extra-ip":
				ips["public"] = []any{ip, ip}
			case "new-interfaces":
				f.instance["interface_generation"] = "linode"
			case "vlan":
				f.configs[0].(map[string]any)["interfaces"] = []any{map[string]any{"purpose": "vlan", "label": "unrelated"}}
			case "own-created-firewall":
				op.Owned = append(op.Owned, "firewall:900")
			case "changed-firewall":
				f.firewalls = []any{map[string]any{"id": 77, "label": "external", "status": "enabled"}}
			}
			f.mu.Unlock()
			var saved Operation
			ctx := screeningRecord(&saved)
			if name == "no-recorder" {
				ctx = context.Background()
			}
			_, e = p.ReplaceScreeningIP(ctx, s, "screening-token", op, r.Fingerprint)
			if e == nil {
				t.Fatal("unsafe Linode delete accepted")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.writes) != 0 {
				t.Fatal(f.writes)
			}
		})
	}
}
func TestScreeningLinodeDeleteLostNeverReplaysOrCreates(t *testing.T) {
	p, f, s, op := screeningLinode(t)
	r, e := p.InspectScreening(context.Background(), s, "screening-token", op)
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.lost = true
	f.mu.Unlock()
	var saved Operation
	_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
	if !Uncertain(e) {
		t.Fatal("lost delete should be unknown", e)
	}
	_, e = p.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", saved, r.Fingerprint)
	if e == nil {
		t.Fatal("unknown deletion replayed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 1 {
		t.Fatal(f.writes)
	}
}

func TestScreeningDeletedUnknownReadOnlyReconciliationUses404(t *testing.T) {
	for _, kind := range []string{"linode", "digitalocean"} {
		t.Run(kind, func(t *testing.T) {
			var provider Provider
			var sp ScreeningProvider
			var s Spec
			var op Operation
			var writes func() int
			if kind == "linode" {
				p, f, spec, initial := screeningLinode(t)
				initial.Linode = &LinodeOperation{Action: "create"}
				provider, sp, s, op = p, p, spec, initial
				f.mu.Lock()
				f.lost = true
				f.mu.Unlock()
				writes = func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.writes) }
			} else {
				p, f, spec, initial := screeningDO(t)
				provider, sp, s, op = p, p, spec, initial
				f.mu.Lock()
				f.lost = true
				f.mu.Unlock()
				writes = func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.writes) }
			}
			r, e := sp.InspectScreening(context.Background(), s, "screening-token", op)
			if e != nil {
				t.Fatal(e)
			}
			var saved Operation
			_, e = sp.ReplaceScreeningIP(screeningRecord(&saved), s, "screening-token", op, r.Fingerprint)
			if !Uncertain(e) || saved.Expected != "deleted" || saved.Linode != nil {
				t.Fatalf("delete intent not durable before lost response: %+v %v", saved, e)
			}
			for n := 0; n < 3; n++ {
				done, err := provider.Poll(context.Background(), saved)
				if err != nil || !done {
					t.Fatalf("404 failed read-only reconciliation: %t %v", done, err)
				}
			}
			if writes() != 1 {
				t.Fatal("read-only reconciliation dispatched additional writes")
			}
		})
	}
}

// The live HTTP dispatch guard applies to every actual mutation, including
// writes reached after read-only provider validation. It adds no account lock.
func TestScreeningLiveDispatchGuardStopsBeforeAnyHTTPRequest(t *testing.T) {
	requests, guards := 0, 0
	h := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(204) })
	ctx := WithWriteGuard(context.Background(), func() error { guards++; return errors.New("stopped immediately before dispatch") })
	aws := &AWS{c: Credentials{"accessKeyId": "fixture", "secretAccessKey": "fixture"}, h: h}
	if _, e := aws.Action(ctx, Instance{ID: "i-owned", Region: "us-east-1"}, "stop", "fixture-token"); e == nil {
		t.Fatal("AWS stop bypassed stop guard")
	}
	d := &DigitalOcean{rest: rest{h: h, base: "https://api.digitalocean.com/v2", token: "fixture"}}
	if _, e := d.Action(ctx, Instance{ID: "123", Region: "nyc3"}, "delete", "fixture-token"); e == nil {
		t.Fatal("REST delete bypassed stop guard")
	}
	if requests != 0 || guards != 2 {
		t.Fatalf("unexpected dispatched requests=%d guards=%d", requests, guards)
	}
}
