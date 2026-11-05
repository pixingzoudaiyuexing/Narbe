package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

func TestRebuildStartupEncryptedConfirmationIdentityAndUnknown(t *testing.T) {
	h, f, aid := advancedSetup(t)
	f.changeErr = &cloud.Error{Code: "proxy reply lost", Uncertain: true}
	b := advancedIntent(aid, "rebuild")
	adv := b["advanced"].(map[string]any)
	adv["image"] = "linode/debian13"
	adv["metadataAcknowledged"] = true
	adv["startup"] = map[string]any{"scriptMode": "cloud-config", "userData": "#cloud-config\nruncmd:\n - echo 'rebuild-secret-中文'\n"}
	confirmAdvanced(t, h, b)
	tampered := map[string]any{}
	data, _ := json.Marshal(b)
	json.Unmarshal(data, &tampered)
	tampered["advanced"].(map[string]any)["startup"].(map[string]any)["userData"] = "#cloud-config\necho changed\n"
	if h.request("POST", "jobs", tampered, true).Code == 202 {
		t.Fatal("old token accepted changed script")
	}
	if h.request("POST", "jobs", b, true).Code != 202 {
		t.Fatal("enqueue")
	}
	var payload []byte
	h.a.S.DB.QueryRow("SELECT payload FROM jobs WHERE id=?", b["id"]).Scan(&payload)
	if strings.Contains(string(payload), "rebuild-secret") {
		t.Fatal("plaintext startup")
	}
	h.a.runOne(context.Background())
	h.a.runOne(context.Background())
	if f.calls != 1 {
		t.Fatal("unknown rebuild replay", f.calls)
	}
	result := h.request("GET", "jobs", nil, true)
	if strings.Contains(result.Body.String(), "rebuild-secret") || !strings.Contains(result.Body.String(), "submission-unknown") {
		t.Fatal(result.Body.String())
	}
	if h.request("POST", "jobs", b, true).Code != 200 {
		t.Fatal("duplicate failed")
	}
}
