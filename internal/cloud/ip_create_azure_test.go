package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type azureCreateFixture struct {
	mu        sync.Mutex
	resources map[string]map[string]any
	subnetID  string
	writes    []string
	lost      string
}

func (f *azureCreateFixture) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.writes...)
}

func incrementalAzureFixture(t *testing.T) (*Azure, Spec, *azureCreateFixture) {
	t.Helper()
	f := &azureCreateFixture{resources: map[string]map[string]any{}, writes: []string{}}
	client := screeningHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/oauth2/v2.0/token") {
			screeningJSON(w, 200, map[string]any{"access_token": "test-token", "expires_in": 3600})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.URL.Path
		if r.Method == "GET" {
			if id == f.subnetID {
				screeningJSON(w, 200, map[string]any{"id": id, "properties": map[string]any{"addressPrefix": "10.0.0.0/24"}})
				return
			}
			resource := f.resources[id]
			if resource == nil {
				screeningJSON(w, 404, map[string]any{"error": map[string]any{"code": "ResourceNotFound"}})
				return
			}
			screeningJSON(w, 200, resource)
			return
		}
		if r.Method != "PUT" {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(500)
			return
		}
		f.writes = append(f.writes, id)
		if f.resources[id] != nil {
			t.Errorf("creation overwrote %s", id)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		body["id"] = id
		properties := azProperties(body)
		properties["provisioningState"] = "Succeeded"
		if strings.Contains(id, "/publicIPAddresses/") {
			properties["ipAddress"] = "8.8.4.4"
		}
		if strings.Contains(id, "/virtualMachines/") {
			properties["instanceView"] = map[string]any{"statuses": []any{map[string]any{"code": "PowerState/running"}}}
			storage := azObject(properties["storageProfile"])
			osDisk := azObject(storage["osDisk"])
			managed := azObject(osDisk["managedDisk"])
			managed["id"] = id[:strings.LastIndex(id, "/providers/")] + "/providers/Microsoft.Compute/disks/" + azString(osDisk["name"])
			f.resources[managed["id"].(string)] = map[string]any{"id": managed["id"], "location": body["location"], "managedBy": id}
		}
		f.resources[id] = body
		if f.lost != "" && strings.Contains(id, f.lost) {
			f.lost = ""
			screeningLost(w)
			return
		}
		screeningJSON(w, 201, body)
	})
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg", "tenantId": "tenant", "clientId": "client", "clientSecret": "secret"}, h: client}
	s := Spec{Name: "fixture", Region: "eastus", Plan: "Standard_B1s", Image: "Canonical|ubuntu|22_04|latest", Network: a.group() + "/providers/Microsoft.Network/virtualNetworks/net/subnets/default", Username: "admin", SSHPublicKey: "ssh-ed25519 fixture", ManagementCIDR: "192.0.2.1/32"}
	f.subnetID = s.Network
	return a, s, f
}

func TestAzureIncrementalCreateOneWritePerTickAndCompleteReadback(t *testing.T) {
	a, s, f := incrementalAzureFixture(t)
	op, err := a.BeginScreeningCreate(context.Background(), s, "create-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.actions()) != 0 || op.CreateStep != "planned" || len(op.Owned) != 4 {
		t.Fatalf("begin unexpectedly wrote: %#v", op)
	}
	var saved Operation
	ctx := screeningRecord(&saved)
	steps := []string{"pip-put", "pip-read", "nsg-put", "nsg-read", "nic-put", "nic-read", "vm-put", "complete"}
	lastWrites := 0
	for _, expected := range steps {
		var done bool
		op, done, err = a.AdvanceScreeningCreate(ctx, s, "create-token", op)
		if err != nil || op.CreateStep != expected || len(f.actions())-lastWrites > 1 {
			t.Fatalf("step %s: %v %#v writes=%v", expected, err, op, f.actions())
		}
		if expected == "complete" && !done {
			t.Fatal("VM not completed")
		}
		lastWrites = len(f.actions())
	}
	if saved.CreateStep != "complete" || op.Ledger.Status != "verified" || len(f.actions()) != 4 {
		t.Fatalf("ledger or write count: %#v %v", saved, f.actions())
	}
	if f.actions()[0] != op.Owned[0] || f.actions()[1] != op.Owned[2] || f.actions()[2] != op.Owned[1] || f.actions()[3] != op.ResourceID {
		t.Fatalf("wrong creation order: %v", f.actions())
	}
}

func TestAzureIncrementalCreateLostResponseNeverReplays(t *testing.T) {
	a, s, f := incrementalAzureFixture(t)
	f.lost = "/publicIPAddresses/"
	op, err := a.BeginScreeningCreate(context.Background(), s, "create-lost")
	if err != nil {
		t.Fatal(err)
	}
	var saved Operation
	ctx := screeningRecord(&saved)
	_, done, err := a.AdvanceScreeningCreate(ctx, s, "create-lost", op)
	if !Uncertain(err) || done || saved.CreateStep != "pip-put" || len(f.actions()) != 1 {
		t.Fatalf("lost response: %v %#v", err, saved)
	}
	op, done, err = a.AdvanceScreeningCreate(ctx, s, "create-lost", saved)
	if err != nil || done || op.CreateStep != "pip-read" || len(f.actions()) != 1 {
		t.Fatalf("read proof: %v %#v %v", err, op, f.actions())
	}
	op, done, err = a.AdvanceScreeningCreate(ctx, s, "create-lost", op)
	if err != nil || done || op.CreateStep != "nsg-put" || len(f.actions()) != 2 {
		t.Fatalf("next write: %v %#v %v", err, op, f.actions())
	}
}

func TestAzureIncrementalCreateRefusesExistingAndNeedsRecorder(t *testing.T) {
	a, s, f := incrementalAzureFixture(t)
	op, err := a.BeginScreeningCreate(context.Background(), s, "create-existing")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.AdvanceScreeningCreate(context.Background(), s, "create-existing", op)
	if err == nil || len(f.actions()) != 0 {
		t.Fatalf("missing recorder allowed write: %v", err)
	}
	f.mu.Lock()
	f.resources[op.Owned[0]] = map[string]any{"id": op.Owned[0], "properties": map[string]any{}}
	f.mu.Unlock()
	_, _, err = a.AdvanceScreeningCreate(screeningRecord(new(Operation)), s, "create-existing", op)
	if err == nil || len(f.actions()) != 0 {
		t.Fatalf("existing PIP overwritten: %v", err)
	}
}
