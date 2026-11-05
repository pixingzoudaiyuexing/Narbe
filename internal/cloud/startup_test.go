package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func startupCatalog() Catalog {
	return Catalog{Images: []Choice{{ID: "linux", Family: "ubuntu", StartupSupport: "cloud-init"}, {ID: "win", Family: "windows"}}, Regions: []Choice{{ID: "r", Extra: map[string]string{"metadata": "true", "siteType": "core"}}}}
}
func TestStartupProviderByteBoundariesAndUnicode(t *testing.T) {
	for _, provider := range []string{"aws", "azure", "linode", "digitalocean"} {
		t.Run(provider, func(t *testing.T) {
			prefix := "#!/bin/bash\n#中文💻\n"
			limit := map[string]int{"aws": 16384, "azure": 65535, "digitalocean": 65536, "linode": 49149}[provider]
			s := Spec{ScriptMode: "shell", UserData: prefix + strings.Repeat("x", limit-len(prefix))}
			if e := ValidateStartupText(provider, s); e != nil {
				t.Fatal(e)
			}
			s.UserData += "x"
			if ValidateStartupText(provider, s) == nil {
				t.Fatal("one-byte overflow accepted")
			}
			s.UserData = s.UserData[:len(s.UserData)-1]
			s.UserData += "界"
			if ValidateStartupText(provider, s) == nil {
				t.Fatal("multibyte byte overflow accepted")
			}
		})
	}
}
func TestStartupStrictModesAndOSCompatibility(t *testing.T) {
	for _, tc := range []struct {
		provider string
		s        Spec
	}{{"aws", Spec{UserData: "#!/bin/bash\necho x"}}, {"linode", Spec{ScriptMode: "invalid", UserData: "#!/bin/sh\n"}}, {"aws", Spec{ScriptMode: "shell", UserData: base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\necho x"))}}, {"azure", Spec{ScriptMode: "powershell", UserData: "Write-Output x"}}, {"linode", Spec{ScriptMode: "stackscript", StackScriptID: 1, UserData: "#cloud-config\na: b"}}, {"aws", Spec{ScriptMode: "none", StackScriptID: 2}}, {"aws", Spec{ScriptMode: "shell", UserData: "#!/bin/sh\n\x00"}}, {"aws", Spec{ScriptMode: "shell", UserData: string([]byte{0xff})}}, {"digitalocean", Spec{ScriptMode: "cloud-config", UserData: "\n#cloud-config\nx: y"}}, {"aws", Spec{ScriptMode: "powershell", UserData: "<powershell>x</powershell>"}}} {
		if ValidateStartupText(tc.provider, tc.s) == nil {
			t.Fatal("invalid startup accepted", tc.provider, tc.s.ScriptMode)
		}
	}
	c := startupCatalog()
	for _, provider := range []string{"aws", "azure", "linode", "digitalocean"} {
		s := Spec{ScriptMode: "shell", Image: "win", Region: "r", UserData: "#!/bin/sh\necho x", ScriptCompatibilityAck: true}
		if ValidateStartup(provider, s, c) == nil {
			t.Fatal("linux to windows accepted", provider)
		}
		s.Image = "linux"
		s.ScriptMode = "powershell"
		s.UserData = "Write-Output x"
		if ValidateStartup(provider, s, c) == nil {
			t.Fatal("powershell to linux accepted", provider)
		}
	}
	s := Spec{ScriptMode: "shell", Image: "linux", Region: "r", UserData: "#!/usr/bin/env bash\necho 中文"}
	if ValidateStartup("aws", s, c) == nil {
		t.Fatal("unknown agent unacknowledged")
	}
	s.ScriptCompatibilityAck = true
	if e := ValidateStartup("aws", s, c); e != nil {
		t.Fatal(e)
	}
	s.ScriptCompatibilityAck = false
	if e := ValidateStartup("linode", s, c); e != nil {
		t.Fatal(e)
	}
	c.Images[0].StartupSupport = "unsupported"
	if ValidateStartup("linode", s, c) == nil {
		t.Fatal("missing dynamic cloud-init accepted")
	}
}
func TestStartupAWSWireShellCloudConfigPowerShell(t *testing.T) {
	for _, mode := range []string{"shell", "cloud-config", "powershell"} {
		t.Run(mode, func(t *testing.T) {
			text := map[string]string{"shell": "#!/bin/bash\nprintf '%s\\n' \"中文 $HOME\"\n", "cloud-config": "#cloud-config\nruncmd:\n - echo '你好'\n", "powershell": "Write-Output \"中文 $env:USERNAME\"\n"}[mode]
			a := &AWS{c: Credentials{"accessKeyId": "fake", "secretAccessKey": "fake"}, h: client(func(r *http.Request) (*http.Response, error) {
				r.ParseForm()
				encoded := r.Form.Get("UserData")
				raw, e := base64.StdEncoding.DecodeString(encoded)
				if e != nil || string(raw) != StartupPayload(Spec{ScriptMode: mode, UserData: text}) {
					t.Fatal("encoding or content changed", e)
				}
				if r.Form.Get("ClientToken") != "task-fixture" {
					t.Fatal("idempotence lost")
				}
				return response(200, `<RunInstancesResponse><instancesSet><item><instanceId>i-1</instanceId></item></instancesSet></RunInstancesResponse>`), nil
			})}
			if _, e := a.Create(context.Background(), Spec{ScriptMode: mode, UserData: text, Region: "us-east-1"}, "task-fixture"); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestStartupDigitalOceanWirePlainTextAndInvalidBeforeWrite(t *testing.T) {
	text := "#cloud-config\nruncmd:\n  - echo '中文\"<> & $PATH'\n"
	writes := 0
	d := &DigitalOcean{rest: rest{base: "https://api.digitalocean.com/v2", h: client(func(r *http.Request) (*http.Response, error) {
		writes++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if b["user_data"] != text {
			t.Fatal("DO encoded or truncated")
		}
		return response(200, `{"droplet":{"id":3}}`), nil
	})}}
	if _, e := d.Create(context.Background(), Spec{ScriptMode: "cloud-config", UserData: text}, "task"); e != nil {
		t.Fatal(e)
	}
	if _, e := d.Create(context.Background(), Spec{ScriptMode: "shell", UserData: strings.Repeat("x", 65537)}, "task"); e == nil || writes != 1 {
		t.Fatal("invalid reached provider")
	}
}
func TestStartupAzureWindowsAndLimitBeforeResourceWrites(t *testing.T) {
	writes := 0
	a := &Azure{h: client(func(r *http.Request) (*http.Response, error) { writes++; return response(200, `{}`), nil })}
	for _, s := range []Spec{{Image: "MicrosoftWindowsServer|WindowsServer|2022|v", ScriptMode: "shell", UserData: "#!/bin/bash\necho x"}, {ScriptMode: "shell", UserData: "#!/bin/sh\n" + strings.Repeat("x", 65535)}} {
		if _, e := a.Create(context.Background(), s, "token"); e == nil {
			t.Fatal("invalid azure accepted")
		}
	}
	if writes != 0 {
		t.Fatal("azure dependencies created before script validation")
	}
}
func TestStartupLinodeMetadataCapabilitiesAndExplicitFirewall(t *testing.T) {
	for _, mode := range []string{"shell", "cloud-config"} {
		text := map[string]string{"shell": "#!/bin/bash\necho \"中文\"\n", "cloud-config": "#cloud-config\npackages:\n - nginx\n"}[mode]
		writes := 0
		l := startupLinodeFixture(t, func(r *http.Request) (*http.Response, error) {
			writes++
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			raw, e := base64.StdEncoding.DecodeString(b["metadata"].(map[string]any)["user_data"].(string))
			if e != nil || string(raw) != text {
				t.Fatal("metadata not single encoding")
			}
			if b["interface_generation"] != "legacy_config" || b["firewall_id"] != float64(-1) {
				t.Fatal("firewall default changed")
			}
			if _, ok := b["stackscript_id"]; ok {
				t.Fatal("mode not exclusive")
			}
			return response(200, `{"id":123}`), nil
		})
		s := Spec{ScriptMode: mode, UserData: text, Image: "linux", Plan: "p", Region: "r", SSHPublicKey: "ssh-ed25519 fixture"}
		if _, e := l.Create(context.Background(), s, "fixture"); e != nil {
			t.Fatal(e)
		}
		if writes != 1 {
			t.Fatal(writes)
		}
	}
}
func startupLinodeFixture(t *testing.T, write transport) *Linode {
	t.Helper()
	return &Linode{rest: rest{base: "https://api.linode.com/v4", token: "fixture", h: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			return write(r)
		}
		switch r.URL.Path {
		case "/v4/regions":
			return response(200, `{"data":[{"id":"r","status":"ok","site_type":"core","capabilities":["Linodes","Metadata"]}],"pages":1}`), nil
		case "/v4/linode/types":
			return response(200, `{"data":[{"id":"p","disk":51200}],"pages":1}`), nil
		case "/v4/regions/r/availability":
			return response(200, `[{"region":"r","plan":"p","available":true}]`), nil
		case "/v4/images":
			return response(200, `{"data":[{"id":"linux","label":"Ubuntu","status":"available","capabilities":["cloud-init"]}],"pages":1}`), nil
		case "/v4/profile/sshkeys":
			return response(200, `{"data":[],"pages":1}`), nil
		}
		t.Fatalf("unexpected GET %s", r.URL.Path)
		return nil, nil
	})}}
}
func TestStartupPowerShellWrapCountsAgainstLimit(t *testing.T) {
	s := Spec{ScriptMode: "powershell", UserData: strings.Repeat("x", 16384-len(StartupPayload(Spec{ScriptMode: "powershell"})))}
	if e := ValidateStartupText("aws", s); e != nil {
		t.Fatal(e)
	}
	s.UserData += "x"
	if ValidateStartupText("aws", s) == nil {
		t.Fatal("wrapper budget omitted")
	}
}

func TestStartupLinodeStackScriptWireAndNoMetadata(t *testing.T) {
	var s Spec
	writes := 0
	l := startupLinodeFixture(t, func(r *http.Request) (*http.Response, error) {
		writes++
		var b map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&b)
		if string(b["stackscript_id"]) != "7" || string(b["firewall_id"]) != "-1" {
			t.Fatal("stackscript/firewall wire", string(marshal(b)))
		}
		if _, ok := b["metadata"]; ok {
			t.Fatal("dual initialization submitted")
		}
		var params map[string]string
		json.Unmarshal(b["stackscript_data"], &params)
		if params["REGION"] != "east" {
			t.Fatal("UDF content changed")
		}
		return response(200, `{"id":123}`), nil
	})
	base := l.h.Transport
	l.h = client(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/stackscripts/") {
			return response(200, `{"id":7,"label":"trusted","script":"#!/bin/bash\necho ok","images":["linux"],"mine":true,"is_public":false,"user_defined_fields":[{"name":"REGION","label":"Region","oneof":"east,west"}]}`), nil
		}
		return base.RoundTrip(r)
	})
	live, e := l.StackScript(context.Background(), 7)
	if e != nil {
		t.Fatal(e)
	}
	s = Spec{ScriptMode: "stackscript", StackScriptID: 7, StackScriptName: live.Label, StackScriptSnapshot: live.Snapshot, StackScriptData: map[string]string{"REGION": "east"}, Image: "linux", Plan: "p", Region: "r", SSHPublicKey: "ssh-ed25519 fixture"}
	if _, e = l.Create(context.Background(), s, "fixture"); e != nil {
		t.Fatal(e)
	}
	if writes != 1 {
		t.Fatal("duplicate create", writes)
	}
}

func TestStartupLinodeRequiresRegionMetadata(t *testing.T) {
	c := startupCatalog()
	c.Regions[0].Extra["metadata"] = "false"
	s := Spec{ScriptMode: "shell", UserData: "#!/bin/bash\necho ok\n", Image: "linux", Region: "r"}
	if ValidateStartup("linode", s, c) == nil {
		t.Fatal("region capability ignored")
	}
}
