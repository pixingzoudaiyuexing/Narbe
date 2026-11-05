package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func rebuildStartupFixture(t *testing.T) (*Linode, *linodeChangeFixture) {
	l, f := newLinodeChangeFixture(t)
	base := l.h.Transport
	l.h = client(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/stackscripts/") {
			return response(200, `{"id":7,"label":"trusted","images":["linode/debian13"],"script":"#!/bin/bash\necho fixture","user_defined_fields":[{"name":"SECRET","label":"Secret"}],"is_public":true,"username":"fixture","updated":"2026-10-16"}`), nil
		}
		res, e := base.RoundTrip(r)
		if e != nil {
			return res, e
		}
		if r.Method == "GET" && (r.URL.Path == "/v4/images" || r.URL.Path == "/v4/regions") {
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			var data map[string]any
			json.Unmarshal(raw, &data)
			for _, item := range data["data"].([]any) {
				v := item.(map[string]any)
				if r.URL.Path == "/v4/images" {
					v["capabilities"] = []string{"cloud-init"}
				} else {
					v["capabilities"] = []string{"Linodes", "Linode Interfaces", "Metadata"}
				}
			}
			raw, _ = json.Marshal(data)
			res.Body = io.NopCloser(strings.NewReader(string(raw)))
		}
		return res, nil
	})
	return l, f
}
func TestLinodeRebuildStartupWireAndUnchangedFirewall(t *testing.T) {
	for _, mode := range []string{"none", "shell", "cloud-config", "stackscript"} {
		t.Run(mode, func(t *testing.T) {
			l, f := rebuildStartupFixture(t)
			ctx := context.Background()
			s, e := l.state(ctx, "1")
			if e != nil {
				t.Fatal(e)
			}
			input := &StartupInput{ScriptMode: mode}
			if mode == "shell" {
				input.UserData = "#!/bin/bash\nprintf '%s' '中文 $HOME'\n"
			}
			if mode == "cloud-config" {
				input.UserData = "#cloud-config\nruncmd:\n - echo '中文'\n"
			}
			if mode == "stackscript" {
				ss, e := l.StackScript(ctx, 7)
				if e != nil {
					t.Fatal(e)
				}
				input.StackScriptID = 7
				input.StackScriptSnapshot = ss.Snapshot
				input.StackScriptName = ss.Label
				input.StackScriptData = map[string]string{"SECRET": "private fixture"}
			}
			c := LinodeChange{Snapshot: s.Snapshot, Image: "linode/debian13", SSHPublicKey: "ssh-ed25519 fixture", Acknowledge: true, MetadataAcknowledged: true, Startup: input}
			if e = l.ValidateChange(ctx, "1", "rebuild", c); e != nil {
				t.Fatal(e)
			}
			if _, e = l.Change(ctx, "1", "rebuild", c, "rebuild-token"); e != nil {
				t.Fatal(e)
			}
			if len(f.writes) != 1 || f.writes[0].Path != "/linode/instances/1/rebuild" {
				t.Fatal(f.writes)
			}
			b := f.writes[0].Body
			for _, key := range []string{"firewall_id", "firewall_ids", "interfaces", "interface_generation", "type", "region"} {
				if _, ok := b[key]; ok {
					t.Fatal("rebuild changed binding/identity", key)
				}
			}
			if mode == "shell" || mode == "cloud-config" {
				encoded := b["metadata"].(map[string]any)["user_data"].(string)
				raw, e := base64.StdEncoding.DecodeString(encoded)
				if e != nil || string(raw) != input.UserData {
					t.Fatal("metadata encoding changed")
				}
				if b["stackscript_id"] != nil {
					t.Fatal("mixed startup")
				}
			}
			if mode == "stackscript" {
				if b["metadata"] != nil || b["stackscript_id"] != float64(7) || b["stackscript_data"].(map[string]any)["SECRET"] != "private fixture" {
					t.Fatal("bad stackscript wire")
				}
			}
			if mode == "none" && (b["metadata"] != nil || b["stackscript_id"] != nil) {
				t.Fatal("none sent initialization")
			}
		})
	}
}
func TestLinodeRebuildStartupInvalidBeforeWriteAndUnknown(t *testing.T) {
	l, f := rebuildStartupFixture(t)
	ctx := context.Background()
	s, _ := l.state(ctx, "1")
	c := LinodeChange{Snapshot: s.Snapshot, Image: "linode/debian13", SSHPublicKey: "ssh-ed25519 fixture", Acknowledge: true, Startup: &StartupInput{ScriptMode: "cloud-config", UserData: "#cloud-config\n"}}
	withoutStartup := c
	withoutStartup.Startup = nil
	if _, e := l.Change(ctx, "1", "rebuild", withoutStartup, "fixture"); e == nil || len(f.writes) != 0 {
		t.Fatal("omitting startup bypassed Metadata acknowledgement")
	}
	if _, e := l.Change(ctx, "1", "rebuild", c, "fixture"); e == nil || len(f.writes) != 0 {
		t.Fatal("missing metadata acknowledgement wrote")
	}
	c.MetadataAcknowledged = true
	c.Startup.UserData = strings.Repeat("a", 49150)
	if _, e := l.Change(ctx, "1", "rebuild", c, "fixture"); e == nil || len(f.writes) != 0 {
		t.Fatal("invalid/oversize wrote")
	}
	c.Startup = &StartupInput{ScriptMode: "stackscript", StackScriptID: 7, StackScriptName: "trusted", StackScriptSnapshot: strings.Repeat("0", 64)}
	if _, e := l.Change(ctx, "1", "rebuild", c, "fixture"); e == nil || len(f.writes) != 0 {
		t.Fatal("stale source wrote")
	}
	c.Startup = &StartupInput{ScriptMode: "shell", UserData: "#!/bin/bash\necho fixture\n"}
	f.loseReply = true
	if _, e := l.Change(ctx, "1", "rebuild", c, "fixture"); e == nil || !Uncertain(e) || len(f.writes) != 1 {
		t.Fatal("lost proxy reply not unknown", e, f.writes)
	}
}

func TestLinodeRebuildStartupCapabilityAndPermissionRechecked(t *testing.T) {
	for _, failure := range []string{"private-image", "metadata-lost", "script-revoked"} {
		t.Run(failure, func(t *testing.T) {
			l, f := rebuildStartupFixture(t)
			ctx := context.Background()
			s, _ := l.state(ctx, "1")
			ss, _ := l.StackScript(ctx, 7)
			c := LinodeChange{Snapshot: s.Snapshot, Image: "linode/debian13", SSHPublicKey: "ssh-ed25519 fixture", Acknowledge: true, MetadataAcknowledged: true, Startup: &StartupInput{ScriptMode: "stackscript", StackScriptID: 7, StackScriptName: ss.Label, StackScriptSnapshot: ss.Snapshot, StackScriptData: map[string]string{"SECRET": "fixture"}}}
			base := l.h.Transport
			l.h = client(func(r *http.Request) (*http.Response, error) {
				if failure == "script-revoked" && strings.Contains(r.URL.Path, "/stackscripts/") {
					return response(403, `{"errors":[{"reason":"private fixture detail"}]}`), nil
				}
				res, err := base.RoundTrip(r)
				if err != nil || r.Method != "GET" || (r.URL.Path != "/v4/images" && r.URL.Path != "/v4/regions") {
					return res, err
				}
				raw, _ := io.ReadAll(res.Body)
				res.Body.Close()
				if failure == "private-image" {
					raw = []byte(strings.ReplaceAll(string(raw), "linode/debian13", "private/123"))
				}
				if failure == "metadata-lost" {
					raw = []byte(strings.ReplaceAll(string(raw), `"Metadata"`, `"NoMetadata"`))
				}
				res.Body = io.NopCloser(strings.NewReader(string(raw)))
				return res, nil
			})
			if failure == "private-image" {
				c.Image = "private/123"
			}
			if failure == "metadata-lost" {
				c.Startup = &StartupInput{ScriptMode: "shell", UserData: "#!/bin/bash\necho fixture\n"}
			}
			if _, err := l.Change(ctx, "1", "rebuild", c, "fixture"); err == nil || len(f.writes) != 0 {
				t.Fatal("changed capability/permission reached a write", err, f.writes)
			}
		})
	}
}
