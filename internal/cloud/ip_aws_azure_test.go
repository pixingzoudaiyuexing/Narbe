package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type awsIPFixture struct {
	mu                                                              sync.Mutex
	state, ip, root, lifecycle                                      string
	eipID, eipIP, eipInstance, eipInterface, eipAssociation, eipTag string
	oldID, oldIP                                                    string
	lostAction                                                      string
	managedASG                                                      bool
	writes                                                          []string
}

func (f *awsIPFixture) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.writes...)
}

func testAWSIP(t *testing.T, f *awsIPFixture) *AWS {
	t.Helper()
	h := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = r.ParseForm()
		action := r.Form.Get("Action")
		if r.Method != "POST" {
			t.Errorf("AWS Query API must POST: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/xml")
		if !strings.HasPrefix(action, "Describe") {
			f.writes = append(f.writes, action)
			switch action {
			case "StopInstances":
				if r.Form.Get("InstanceId.1") != "i-12345678" {
					t.Error("stop target mismatch")
				}
				f.state = "stopped"
			case "StartInstances":
				if r.Form.Get("InstanceId.1") != "i-12345678" {
					t.Error("start target mismatch")
				}
				f.state, f.ip = "running", "8.8.4.4"
			case "AllocateAddress":
				if r.Form.Get("Domain") != "vpc" || r.Form.Get("TagSpecification.1.ResourceType") != "elastic-ip" || r.Form.Get("TagSpecification.1.Tag.1.Key") != "narbe-job" {
					t.Error("EIP allocation scope or tag mismatch")
				}
				f.eipID, f.eipIP, f.eipTag = "eipalloc-12345678", "1.1.1.1", r.Form.Get("TagSpecification.1.Tag.1.Value")
			case "AssociateAddress":
				if r.Form.Get("AllocationId") != f.eipID || r.Form.Get("NetworkInterfaceId") != "eni-12345678" || r.Form.Get("AllowReassociation") != "true" {
					t.Error("EIP association target mismatch")
				}
				f.eipInstance, f.eipInterface, f.eipAssociation, f.ip = "i-12345678", "eni-12345678", "eipassoc-12345678", f.eipIP
			}
			if action == f.lostAction {
				screeningLost(w)
				f.lostAction = ""
				return
			}
			if action == "AllocateAddress" {
				fmt.Fprintf(w, `<r><allocationId>%s</allocationId><publicIp>%s</publicIp></r>`, f.eipID, f.eipIP)
				return
			}
			fmt.Fprint(w, `<r/>`)
			return
		}
		switch action {
		case "DescribeInstances":
			tag := ""
			if f.managedASG {
				tag = `<tagSet><item><key>aws:autoscaling:groupName</key><value>asg</value></item></tagSet>`
			}
			fmt.Fprintf(w, `<r><reservationSet><item><ownerId>123456789012</ownerId><instancesSet><item><instanceId>i-12345678</instanceId><imageId>ami-12345678</imageId><instanceType>t3.nano</instanceType><keyName>key</keyName><subnetId>subnet-1</subnetId><vpcId>vpc-1</vpcId><rootDeviceType>%s</rootDeviceType><rootDeviceName>/dev/xvda</rootDeviceName><instanceLifecycle>%s</instanceLifecycle><ipAddress>%s</ipAddress><instanceState><name>%s</name></instanceState>%s<groupSet><item><groupId>sg-1</groupId></item></groupSet><blockDeviceMapping><item><deviceName>/dev/xvda</deviceName><ebs><volumeId>vol-12345678</volumeId></ebs></item></blockDeviceMapping><networkInterfaceSet><item><networkInterfaceId>eni-12345678</networkInterfaceId><subnetId>subnet-1</subnetId><interfaceType>interface</interfaceType><attachment><deviceIndex>0</deviceIndex></attachment><privateIpAddressesSet><item><privateIpAddress>10.0.0.1</privateIpAddress><primary>true</primary></item></privateIpAddressesSet></item></networkInterfaceSet></item></instancesSet></item></reservationSet></r>`, f.root, f.lifecycle, f.ip, f.state, tag)
		case "DescribeAddresses":
			fmt.Fprint(w, `<r><addressesSet>`)
			if f.oldID != "" {
				fmt.Fprintf(w, `<item><publicIp>%s</publicIp><allocationId>%s</allocationId><domain>vpc</domain><associationId>eipassoc-old</associationId><instanceId>i-12345678</instanceId><networkInterfaceId>eni-12345678</networkInterfaceId></item>`, f.oldIP, f.oldID)
			}
			if f.eipID != "" {
				fmt.Fprintf(w, `<item><publicIp>%s</publicIp><allocationId>%s</allocationId><domain>vpc</domain><instanceId>%s</instanceId><networkInterfaceId>%s</networkInterfaceId><associationId>%s</associationId><tagSet><item><key>narbe-job</key><value>%s</value></item></tagSet></item>`, f.eipIP, f.eipID, f.eipInstance, f.eipInterface, f.eipAssociation, f.eipTag)
			}
			fmt.Fprint(w, `</addressesSet></r>`)
		case "DescribeInstanceAttribute":
			fmt.Fprint(w, `<r><userData><value></value></userData></r>`)
		default:
			t.Errorf("unexpected AWS action %s", action)
		}
	})
	return &AWS{c: Credentials{"accessKeyId": "key", "secretAccessKey": "secret"}, h: h}
}

func TestAWSIPStopStartAndLostResponseNeverReplays(t *testing.T) {
	f := &awsIPFixture{state: "running", ip: "8.8.8.8", root: "ebs", lostAction: "StopInstances"}
	a := testAWSIP(t, f)
	i := Instance{ID: "i-12345678", Region: "us-east-1"}
	info, err := a.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	var saved Operation
	ctx := screeningRecord(&saved)
	op, err := a.BeginIPChange(ctx, i, IPChange{Method: "aws-stop-start", Snapshot: info.Snapshot, RiskAccepted: true}, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err = a.AdvanceIPChange(ctx, op)
	if err == nil || !Uncertain(err) || len(f.actions()) != 1 || saved.IP.Step != "stopping" {
		t.Fatalf("lost stop must be persisted and uncertain: %#v %v %v", saved.IP, err, f.actions())
	}
	before := append([]string{}, f.actions()...)
	op, done, err := a.ReconcileIPChange(ctx, saved)
	if err != nil || done || op.IP.Step != "stopped" || !reflect.DeepEqual(before, f.actions()) {
		t.Fatalf("read-only stop proof failed: %v %v %v", op.IP.Step, err, f.actions())
	}
	op, done, err = a.AdvanceIPChange(ctx, op)
	if err != nil || done || len(f.actions()) != 2 || f.actions()[1] != "StartInstances" {
		t.Fatalf("start: %v %v", err, f.actions())
	}
	op, done, err = a.ReconcileIPChange(ctx, op)
	if err != nil || !done || op.IP.NewIP != "8.8.4.4" || !op.IP.PrimaryChanged || len(f.actions()) != 2 {
		t.Fatalf("verify: %v %#v %v", err, op.IP, f.actions())
	}
}

func TestAWSIPEIPAllocateRetainsOldAllocationAndReconcilesLostCreate(t *testing.T) {
	f := &awsIPFixture{state: "running", ip: "8.8.8.8", root: "ebs", oldID: "eipalloc-aaaaaaaa", oldIP: "8.8.8.8", lostAction: "AllocateAddress"}
	a := testAWSIP(t, f)
	i := Instance{ID: "i-12345678", Region: "us-east-1"}
	info, err := a.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	var saved Operation
	ctx := screeningRecord(&saved)
	op, err := a.BeginIPChange(ctx, i, IPChange{Method: "aws-eip-allocate", Snapshot: info.Snapshot, RiskAccepted: true}, "job-2")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.AdvanceIPChange(ctx, op)
	if !Uncertain(err) || saved.IP.Step != "allocating" {
		t.Fatalf("lost allocation must be uncertain: %v %#v", err, saved.IP)
	}
	op, done, err := a.ReconcileIPChange(ctx, saved)
	if err != nil || done || op.IP.Step != "allocated" || len(f.actions()) != 1 {
		t.Fatalf("reconcile: %v %#v %v", err, op.IP, f.actions())
	}
	op, done, err = a.AdvanceIPChange(ctx, op)
	if err != nil || done || f.actions()[1] != "AssociateAddress" {
		t.Fatalf("associate: %v %v", err, f.actions())
	}
	op, done, err = a.ReconcileIPChange(ctx, op)
	if err != nil || !done || !op.IP.PrimaryChanged || op.IP.OldResourceID != f.oldID || len(f.actions()) != 2 {
		t.Fatalf("finish: %v %#v %v", err, op.IP, f.actions())
	}
	for _, action := range f.actions() {
		if action == "ReleaseAddress" {
			t.Fatal("old EIP released")
		}
	}
}

func TestAWSIPRejectsComplexOrSameEIPBeforeWrite(t *testing.T) {
	for _, f := range []*awsIPFixture{{state: "running", ip: "8.8.8.8", root: "instance-store"}, {state: "running", ip: "8.8.8.8", root: "ebs", lifecycle: "spot"}, {state: "running", ip: "8.8.8.8", root: "ebs", managedASG: true}, {state: "running", ip: "8.8.8.8", root: "ebs", eipID: "eipalloc-12345678", eipIP: "8.8.8.8"}} {
		a := testAWSIP(t, f)
		info, err := a.IPInfo(context.Background(), Instance{ID: "i-12345678", Region: "us-east-1"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.BeginIPChange(context.Background(), Instance{ID: "i-12345678", Region: "us-east-1"}, IPChange{Method: "aws-eip-associate", Snapshot: info.Snapshot, AllocationID: f.eipID, RiskAccepted: true}, "job-3")
		if err == nil || len(f.actions()) != 0 {
			t.Fatalf("unsafe EIP accepted: %#v", f)
		}
	}
}

func TestAWSIPAssociateExistingRequiresExactSourceAndNeverReplaysLostWrite(t *testing.T) {
	f := &awsIPFixture{state: "running", ip: "8.8.8.8", root: "ebs", eipID: "eipalloc-12345678", eipIP: "1.1.1.1", eipInstance: "i-87654321", eipAssociation: "eipassoc-source", lostAction: "AssociateAddress"}
	a := testAWSIP(t, f)
	i := Instance{ID: "i-12345678", Region: "us-east-1"}
	info, err := a.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.AvailableIPs) != 1 || info.AvailableIPs[0].InstanceID != "i-87654321" {
		t.Fatalf("source EIP missing: %#v", info.AvailableIPs)
	}
	_, err = a.BeginIPChange(context.Background(), i, IPChange{Method: "aws-eip-associate", Snapshot: info.Snapshot, AllocationID: f.eipID, RiskAccepted: true}, "job-existing")
	if err == nil {
		t.Fatal("source instance can be omitted")
	}
	var saved Operation
	ctx := screeningRecord(&saved)
	op, err := a.BeginIPChange(ctx, i, IPChange{Method: "aws-eip-associate", Snapshot: info.Snapshot, AllocationID: f.eipID, SourceInstanceID: "i-87654321", RiskAccepted: true}, "job-existing")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.AdvanceIPChange(ctx, op)
	if !Uncertain(err) || saved.IP.Step != "associating" || len(f.actions()) != 1 {
		t.Fatalf("lost associate: %v %#v %v", err, saved.IP, f.actions())
	}
	op, done, err := a.ReconcileIPChange(ctx, saved)
	if err != nil || !done || op.IP.NewIP != "1.1.1.1" || len(f.actions()) != 1 {
		t.Fatalf("read proof: %v %#v %v", err, op.IP, f.actions())
	}
}

type azureIPFixture struct {
	mu                 sync.Mutex
	vm, nic, old, next map[string]any
	vmID, nicID, oldID string
	lost               string
	writes             []string
}

func (f *azureIPFixture) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.writes...)
}

func (f *azureIPFixture) setLost(step string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lost = step
}

func testAzureIP(t *testing.T) (*Azure, *azureIPFixture) {
	t.Helper()
	base := "/subscriptions/sub/resourceGroups/rg/providers/"
	f := &azureIPFixture{vmID: base + "Microsoft.Compute/virtualMachines/vm", nicID: base + "Microsoft.Network/networkInterfaces/nic", oldID: base + "Microsoft.Network/publicIPAddresses/old", writes: []string{}}
	configID := f.nicID + "/ipConfigurations/primary"
	f.vm = map[string]any{"id": f.vmID, "location": "eastus", "properties": map[string]any{"provisioningState": "Succeeded", "storageProfile": map[string]any{"osDisk": map[string]any{"managedDisk": map[string]any{"id": "disk-1"}}, "dataDisks": []any{map[string]any{"name": "data"}}}, "networkProfile": map[string]any{"networkInterfaces": []any{map[string]any{"id": f.nicID}}}, "instanceView": map[string]any{"statuses": []any{map[string]any{"code": "PowerState/running"}}}}}
	f.nic = map[string]any{"id": f.nicID, "location": "eastus", "etag": "etag-1", "properties": map[string]any{"virtualMachine": map[string]any{"id": f.vmID}, "networkSecurityGroup": map[string]any{"id": "nsg-1"}, "ipConfigurations": []any{map[string]any{"id": configID, "name": "primary", "properties": map[string]any{"primary": true, "subnet": map[string]any{"id": "subnet-1"}, "publicIPAddress": map[string]any{"id": f.oldID}}}}}}
	f.old = map[string]any{"id": f.oldID, "location": "eastus", "sku": map[string]any{"name": "Standard"}, "properties": map[string]any{"provisioningState": "Succeeded", "publicIPAllocationMethod": "Static", "publicIPAddressVersion": "IPv4", "ipAddress": "8.8.8.8", "ipConfiguration": map[string]any{"id": configID}}}
	h := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/oauth2/v2.0/token") {
			screeningJSON(w, 200, map[string]any{"access_token": "test-token", "expires_in": 3600})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		path := r.URL.Path
		if r.Method != "GET" {
			f.writes = append(f.writes, r.Method+" "+path)
		}
		switch {
		case r.Method == "GET" && path == f.vmID:
			screeningJSON(w, 200, f.vm)
		case r.Method == "GET" && path == f.nicID:
			screeningJSON(w, 200, f.nic)
		case r.Method == "GET" && path == f.oldID:
			screeningJSON(w, 200, f.old)
		case r.Method == "GET" && f.next != nil && path == f.next["id"]:
			screeningJSON(w, 200, f.next)
		case r.Method == "GET":
			screeningJSON(w, 404, map[string]any{"error": map[string]any{"code": "ResourceNotFound"}})
		case r.Method == "PUT" && strings.Contains(path, "/publicIPAddresses/"):
			if r.Header.Get("If-None-Match") != "*" {
				t.Errorf("missing create-only conditional header")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if azString(azObject(body["tags"])["narbe-job"]) == "" || azString(azObject(body["sku"])["name"]) != "Standard" || azString(azProperties(body)["publicIPAllocationMethod"]) != "Static" {
				t.Error("new PIP body missing identity or SKU")
			}
			f.next = body
			f.next["id"] = path
			azProperties(f.next)["ipAddress"] = "1.1.1.1"
			azProperties(f.next)["provisioningState"] = "Succeeded"
			if f.lost == "pip" {
				f.lost = ""
				screeningLost(w)
				return
			}
			screeningJSON(w, 201, f.next)
		case r.Method == "PUT" && path == f.nicID:
			if r.Header.Get("If-Match") != "etag-1" {
				t.Errorf("missing NIC ETag: %q", r.Header.Get("If-Match"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.nic = body
			f.nic["etag"] = "etag-2"
			delete(azProperties(f.old), "ipConfiguration")
			azProperties(f.next)["ipConfiguration"] = map[string]any{"id": configID}
			if f.lost == "nic" {
				f.lost = ""
				screeningLost(w)
				return
			}
			screeningJSON(w, 200, f.nic)
		default:
			t.Errorf("unexpected Azure HTTP %s %s", r.Method, path)
			w.WriteHeader(500)
		}
	})
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg", "tenantId": "tenant", "clientId": "client", "clientSecret": "secret"}, h: h}
	return a, f
}

func TestAzureIPSwapRetainsVMDisksAndOldPIP(t *testing.T) {
	a, f := testAzureIP(t)
	i := Instance{ID: f.vmID, Region: "eastus"}
	info, err := a.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	var saved Operation
	ctx := screeningRecord(&saved)
	op, err := a.BeginIPChange(ctx, i, IPChange{Method: "azure-public-ip", Snapshot: info.Snapshot, RiskAccepted: true}, "job-azure")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err = a.AdvanceIPChange(ctx, op)
	if err != nil || len(f.actions()) != 1 {
		t.Fatalf("create: %v %v", err, f.actions())
	}
	op, _, err = a.ReconcileIPChange(ctx, op)
	if err != nil || op.IP.Step != "created" || len(f.actions()) != 1 {
		t.Fatalf("read new PIP: %v %s %v", err, op.IP.Step, f.actions())
	}
	op, _, err = a.AdvanceIPChange(ctx, op)
	if err != nil || len(f.actions()) != 2 || !strings.HasPrefix(f.actions()[1], "PUT "+f.nicID) {
		t.Fatalf("swap: %v %v", err, f.actions())
	}
	op, done, err := a.ReconcileIPChange(ctx, op)
	if err != nil || !done || !op.IP.PrimaryChanged || op.IP.After == nil || op.IP.After.PrimaryIPv4 != "1.1.1.1" {
		t.Fatalf("finish: %v %#v", err, op.IP)
	}
	f.mu.Lock()
	dataDisks, _ := azObject(azProperties(f.vm)["storageProfile"])["dataDisks"].([]any)
	resourcesPreserved := len(dataDisks) == 1 && azID(azProperties(f.nic)["networkSecurityGroup"]) == "nsg-1" && azProperties(f.old)["ipAddress"] == "8.8.8.8"
	f.mu.Unlock()
	if len(f.actions()) != 2 || !resourcesPreserved {
		t.Fatalf("unrelated resource changed: %v", f.actions())
	}
}

func TestAzureIPLostCreateOnlyReadReconcilesAndExternalNICEditBlocks(t *testing.T) {
	a, f := testAzureIP(t)
	i := Instance{ID: f.vmID, Region: "eastus"}
	info, _ := a.IPInfo(context.Background(), i)
	var saved Operation
	ctx := screeningRecord(&saved)
	op, err := a.BeginIPChange(ctx, i, IPChange{Method: "azure-public-ip", Snapshot: info.Snapshot, RiskAccepted: true}, "job-azure-lost")
	if err != nil {
		t.Fatal(err)
	}
	f.setLost("pip")
	_, _, err = a.AdvanceIPChange(ctx, op)
	if !Uncertain(err) || saved.IP.Step != "creating" {
		t.Fatalf("lost create: %v %#v", err, saved.IP)
	}
	op, _, err = a.ReconcileIPChange(ctx, saved)
	if err != nil || op.IP.Step != "created" || len(f.actions()) != 1 {
		t.Fatalf("read proof: %v %#v %v", err, op.IP, f.actions())
	}
	f.mu.Lock()
	azProperties(f.nic)["networkSecurityGroup"] = map[string]any{"id": "external-edit"}
	f.mu.Unlock()
	_, _, err = a.AdvanceIPChange(ctx, op)
	if err == nil || !Uncertain(err) || len(f.actions()) != 1 {
		t.Fatalf("external edit overwritten: %v %v", err, f.actions())
	}
}

func TestAzureIPLostNICWriteReconcilesWithoutSecondPUT(t *testing.T) {
	a, f := testAzureIP(t)
	i := Instance{ID: f.vmID, Region: "eastus"}
	info, err := a.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	var saved Operation
	ctx := screeningRecord(&saved)
	op, err := a.BeginIPChange(ctx, i, IPChange{Method: "azure-public-ip", Snapshot: info.Snapshot, RiskAccepted: true}, "job-nic-lost")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err = a.AdvanceIPChange(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	op, _, err = a.AdvanceIPChange(ctx, op)
	if err != nil || op.IP.Step != "created" {
		t.Fatalf("PIP read: %v %#v", err, op.IP)
	}
	f.setLost("nic")
	_, _, err = a.AdvanceIPChange(ctx, op)
	if !Uncertain(err) || saved.IP.Step != "updating-nic" || len(f.actions()) != 2 {
		t.Fatalf("lost NIC: %v %#v %v", err, saved.IP, f.actions())
	}
	op, done, err := a.ReconcileIPChange(ctx, saved)
	if err != nil || !done || op.IP.After == nil || len(f.actions()) != 2 {
		t.Fatalf("read NIC proof: %v %#v %v", err, op.IP, f.actions())
	}
}

func TestAzureIPComplexNICUnavailable(t *testing.T) {
	a, f := testAzureIP(t)
	f.mu.Lock()
	azProperties(f.nic)["ipConfigurations"] = append(azProperties(f.nic)["ipConfigurations"].([]any), map[string]any{"name": "extra"})
	f.mu.Unlock()
	info, err := a.IPInfo(context.Background(), Instance{ID: f.vmID, Region: "eastus"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Methods[0].Available || info.Methods[0].Reason == "" {
		t.Fatalf("complex NIC accepted: %#v", info.Methods)
	}
}
