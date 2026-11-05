package cloud

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAzureInventoryOptionalNetworkPermissionDoesNotHideKnownVM(t *testing.T) {
	vm := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/known"
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture", expires: time.Now().Add(time.Hour), h: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Error("mutating inventory request")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/locations"):
			return response(200, `{"value":[{"name":"eastus","displayName":"East US"}]}`), nil
		case strings.HasSuffix(r.URL.Path, "/virtualMachines"):
			return response(200, `{"value":[{"id":"`+vm+`","name":"known","location":"eastus","properties":{"hardwareProfile":{"vmSize":"Standard_B2s"},"networkProfile":{"networkInterfaces":[{"id":"/subscriptions/sub/resourceGroups/network/providers/Microsoft.Network/networkInterfaces/nic"}]}}}]}`), nil
		case r.URL.Path == vm:
			return response(200, `{"properties":{"instanceView":{"statuses":[{"code":"PowerState/running"}]}}}`), nil
		case strings.HasSuffix(r.URL.Path, "/skus"):
			return response(200, `{"value":[]}`), nil
		default:
			return response(403, `{"error":{"message":"fixture-secret-token"}}`), nil
		}
	})}
	v := ScanInventory(context.Background(), a, "azure", "", Inventory{}, func(Inventory) {})
	if !v.Complete || v.Stats.Total != 1 || v.Stats.Running != 1 || len(v.Warnings) == 0 || len(v.Items[0].IPs) != 0 {
		t.Fatal("optional IP metadata hid a known VM", v)
	}
	if _, e := a.Instances(context.Background(), ""); e == nil {
		t.Fatal("legacy fresh write preflight read unexpectedly became permissive")
	}
}
