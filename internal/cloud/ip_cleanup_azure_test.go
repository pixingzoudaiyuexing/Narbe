package cloud

import (
	"context"
	"strings"
	"testing"
)

func completeAzureIPForCleanup(t *testing.T) (*Azure, *azureIPFixture, Operation, context.Context) {
	t.Helper()
	a, f := testAzureIP(t)
	i := Instance{ID: f.vmID, Region: "eastus"}
	info, err := a.IPInfo(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	ctx := screeningRecord(new(Operation))
	op, err := a.BeginIPChange(ctx, i, IPChange{Method: "azure-public-ip", Snapshot: info.Snapshot, RiskAccepted: true}, "new-ip-token")
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 4; n++ {
		op, _, err = a.AdvanceIPChange(ctx, op)
		if err != nil {
			t.Fatalf("IP step %d: %v", n, err)
		}
	}
	if op.IP.Step != "completed" || op.IP.After == nil {
		t.Fatalf("IP change not complete: %#v", op.IP)
	}
	return a, f, op, ctx
}

func TestAzureScreeningCleanupDeletesOnlyVerifiedDetachedOldPIP(t *testing.T) {
	a, f, op, ctx := completeAzureIPForCleanup(t)
	if len(f.actions()) != 2 {
		t.Fatalf("unexpected IP writes: %v", f.actions())
	}
	op, done, err := a.CleanupScreeningIP(ctx, op, f.oldID, "original-create-token")
	if err != nil || done || len(f.actions()) != 3 || f.actions()[2] != "DELETE "+f.oldID || op.IP.Checkpoint["cleanup-step"] != "deleting" {
		t.Fatalf("delete dispatch: %v %#v %v", err, op.IP, f.actions())
	}
	op, done, err = a.CleanupScreeningIP(ctx, op, f.oldID, "original-create-token")
	if err != nil || !done || len(f.actions()) != 3 || op.IP.Checkpoint["cleanup-step"] != "deleted" {
		t.Fatalf("delete verify: %v %#v %v", err, op.IP, f.actions())
	}
	deleted := false
	for _, r := range op.IP.Resources {
		deleted = deleted || azSame(r.ID, f.oldID) && r.State == "deleted-confirmed"
	}
	if !deleted {
		t.Fatalf("deleted state missing: %#v", op.IP.Resources)
	}
}

func TestAzureScreeningCleanupLostDeleteNeverReplays(t *testing.T) {
	a, f, op, ctx := completeAzureIPForCleanup(t)
	f.setLost("delete")
	var saved Operation
	ctx = screeningRecord(&saved)
	_, done, err := a.CleanupScreeningIP(ctx, op, f.oldID, "original-create-token")
	if !Uncertain(err) || done || saved.IP.Checkpoint["cleanup-step"] != "deleting" || len(f.actions()) != 3 {
		t.Fatalf("lost delete: %v %#v %v", err, saved.IP, f.actions())
	}
	op, done, err = a.CleanupScreeningIP(ctx, saved, f.oldID, "original-create-token")
	if err != nil || !done || len(f.actions()) != 3 {
		t.Fatalf("read proof: %v %#v %v", err, op.IP, f.actions())
	}
}

func TestAzureScreeningCleanupRejectsUnownedReferencedAndMissingRecorder(t *testing.T) {
	a, f, op, _ := completeAzureIPForCleanup(t)
	for _, token := range []string{"wrong-owner-token", ""} {
		_, _, err := a.CleanupScreeningIP(screeningRecord(new(Operation)), op, f.oldID, token)
		if err == nil || len(f.actions()) != 2 {
			t.Fatalf("unowned old PIP deleted: %v %v", err, f.actions())
		}
	}
	_, _, err := a.CleanupScreeningIP(screeningRecord(new(Operation)), op, strings.Replace(f.oldID, "/old", "/other", 1), "original-create-token")
	if err == nil || len(f.actions()) != 2 {
		t.Fatalf("wrong old ID deleted: %v %v", err, f.actions())
	}
	f.mu.Lock()
	azProperties(f.old)["natGateway"] = map[string]any{"id": "gateway"}
	f.mu.Unlock()
	_, _, err = a.CleanupScreeningIP(screeningRecord(new(Operation)), op, f.oldID, "original-create-token")
	if err == nil || len(f.actions()) != 2 {
		t.Fatalf("referenced old PIP deleted: %v %v", err, f.actions())
	}
	f.mu.Lock()
	delete(azProperties(f.old), "natGateway")
	f.mu.Unlock()
	_, _, err = a.CleanupScreeningIP(context.Background(), op, f.oldID, "original-create-token")
	if err == nil || len(f.actions()) != 2 {
		t.Fatalf("missing recorder allowed delete: %v %v", err, f.actions())
	}
}
