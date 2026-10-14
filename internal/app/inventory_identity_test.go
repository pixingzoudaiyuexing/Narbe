package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

type scopedInventoryFake struct{ *fakeProvider }

func (p *scopedInventoryFake) Instances(ctx context.Context, region string) ([]cloud.Instance, error) {
	items, e := p.fakeProvider.Instances(ctx, region)
	if e != nil {
		return nil, e
	}
	out := []cloud.Instance{}
	for _, i := range items {
		if region == "" || i.Region == region {
			out = append(out, i)
		}
	}
	return out, nil
}
func TestInventoryNeverAuthorizesCrossAccountOrCrossRegionMutation(t *testing.T) {
	h := setup(t)
	defer h.a.stopInventories()
	factories := map[string]*scopedInventoryFake{}
	for _, token := range []string{"account-A", "account-B"} {
		factories[token] = &scopedInventoryFake{&fakeProvider{items: []cloud.Instance{{ID: "same-id", Name: "same-name", Region: "region-1", State: "running", Actions: []string{"stop", "delete"}}, {ID: "same-id", Name: "same-name", Region: "region-2", State: "running", Actions: []string{"stop", "delete"}}}}}
	}
	h.a.Factory = func(_ string, c cloud.Credentials) (cloud.Provider, error) { return factories[c["token"]], nil }
	ids := []string{}
	for _, token := range []string{"account-A", "account-B"} {
		r := h.request("POST", "accounts", map[string]any{"name": token, "provider": "linode", "credentials": map[string]string{"token": token}}, true)
		if r.Code != 201 {
			t.Fatal(r.Body.String())
		}
		var a account
		json.Unmarshal(r.Body.Bytes(), &a)
		ids = append(ids, a.ID)
	}
	for _, id := range ids {
		v := inventoryResult(t, h, id)
		if v.Stats.Total != 2 || v.AccountID != id {
			t.Fatal(v)
		}
	}
	base := mutation{ID: store.ID(), AccountID: ids[0], Action: "stop", Region: "region-1", Target: "same-id", Name: "same-name", ConfirmName: "same-name"}
	confirmed := h.confirm(t, base)
	wrongAccount := confirmed
	wrongAccount.AccountID = ids[1]
	if r := h.request("POST", "jobs", wrongAccount, true); r.Code != 409 {
		t.Fatal("account confirmation reused", r.Code, r.Body.String())
	}
	wrongRegion := confirmed
	wrongRegion.Region = "region-2"
	if r := h.request("POST", "jobs", wrongRegion, true); r.Code != 409 {
		t.Fatal("region confirmation reused", r.Code, r.Body.String())
	}
	// A stale inventory row is not enough: provider state is re-read at confirmation.
	factories["account-A"].items = []cloud.Instance{}
	stale := base
	stale.ID = store.ID()
	if r := h.request("POST", "confirm", stale, true); r.Code == 200 {
		t.Fatal("stale cached row authorized mutation")
	}
	for _, p := range factories {
		if p.calls != 0 {
			t.Fatal("cloud writes issued during identity rejection")
		}
	}
}
