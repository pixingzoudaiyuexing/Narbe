package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func stackFixture(t *testing.T, mutate func(map[string]any)) *Linode {
	t.Helper()
	return &Linode{rest: rest{base: "https://api.linode.com/v4", token: "private-account-fixture", h: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("read validation attempted write")
		}
		if r.Header.Get("Authorization") != "Bearer private-account-fixture" {
			t.Fatal("wrong account token")
		}
		script := map[string]any{"id": 7, "label": "安装工具", "description": "<script>alert(1)</script>", "script": "#!/bin/bash\necho secret-source\n", "images": []string{"linux"}, "user_defined_fields": []any{map[string]any{"name": "REGION", "label": "Region", "oneof": "east,west"}, map[string]any{"name": "PACKAGES", "label": "Packages", "manyof": "a,b,c", "default": "a"}, map[string]any{"name": "OPTIONAL", "label": "Optional", "default": ""}}, "mine": true, "is_public": false, "updated": "2026-10-15"}
		if mutate != nil {
			mutate(script)
		}
		if r.URL.Path == "/v4/linode/stackscripts" {
			var filter map[string]any
			json.Unmarshal([]byte(r.Header.Get("X-Filter")), &filter)
			if filter["mine"] != true || filter["label"].(map[string]any)["+contains"] != "安装" {
				t.Fatal("scope/search not official filter", filter)
			}
			return response(200, string(marshal(map[string]any{"data": []any{script}, "page": 1, "pages": 1, "results": 1}))), nil
		}
		return response(200, string(marshal(script))), nil
	})}}
}
func TestStackScriptReadRedactsSourceAndUsesAccountScope(t *testing.T) {
	l := stackFixture(t, nil)
	p, e := l.StackScripts(context.Background(), "安装", "mine", 1)
	if e != nil || len(p.Items) != 1 {
		t.Fatal(p, e)
	}
	s, e := l.StackScript(context.Background(), 7)
	if e != nil || s.Snapshot == "" {
		t.Fatal(s, e)
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "secret-source") {
		t.Fatal("source leaked")
	}
	if s.Fields[0].OneOf != "east,west" || s.Fields[1].ManyOf != "a,b,c" || s.Fields[2].Default == nil {
		t.Fatal("official UDF parsing", s.Fields)
	}
}
func TestStackScriptLiveDefinitionPermissionAndUDFValidation(t *testing.T) {
	l := stackFixture(t, nil)
	s, e := l.StackScript(context.Background(), 7)
	if e != nil {
		t.Fatal(e)
	}
	spec := Spec{ScriptMode: "stackscript", StackScriptID: 7, StackScriptName: s.Label, StackScriptSnapshot: s.Snapshot, StackScriptData: map[string]string{"REGION": "east"}, Image: "linux"}
	if e = l.ValidateStackScript(context.Background(), spec); e != nil {
		t.Fatal(e)
	}
	for name, data := range map[string]map[string]string{"missing": {}, "unknown": {"REGION": "east", "UNDECLARED": "x"}, "oneof": {"REGION": "evil"}, "manyof": {"REGION": "east", "PACKAGES": "a,evil"}, "repeat": {"REGION": "east", "PACKAGES": "a,a"}} {
		t.Run(name, func(t *testing.T) {
			bad := spec
			bad.StackScriptData = data
			if l.ValidateStackScript(context.Background(), bad) == nil {
				t.Fatal("invalid parameters accepted")
			}
		})
	}
	for _, mutate := range []func(map[string]any){func(s map[string]any) { s["script"] = "#!/bin/sh\nchanged" }, func(s map[string]any) { s["images"] = []string{"other"} }, func(s map[string]any) { s["username"] = "new-owner" }, func(s map[string]any) { s["is_public"] = true }} {
		if stackFixture(t, mutate).ValidateStackScript(context.Background(), spec) == nil {
			t.Fatal("changed script accepted")
		}
	}
	denied := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(*http.Request) (*http.Response, error) {
		return response(403, `{"errors":[{"reason":"private raw secret"}]}`), nil
	})}}
	e = denied.ValidateStackScript(context.Background(), spec)
	if e == nil || strings.Contains(e.Error(), "raw secret") {
		t.Fatal("private script access leaked or bypassed")
	}
	spec.StackScriptID = -1
	if l.ValidateStackScript(context.Background(), spec) == nil {
		t.Fatal("malicious ID accepted")
	}
}
func TestStackScriptPrivateImageOnlyWhenExplicitlySupported(t *testing.T) {
	l := stackFixture(t, func(s map[string]any) { s["images"] = []string{"any/all"} })
	s, e := l.StackScript(context.Background(), 7)
	if e != nil {
		t.Fatal(e)
	}
	v := Spec{ScriptMode: "stackscript", StackScriptID: 7, StackScriptName: s.Label, StackScriptSnapshot: s.Snapshot, StackScriptData: map[string]string{"REGION": "east"}, Image: "private/123"}
	if e = l.ValidateStackScript(context.Background(), v); e != nil {
		t.Fatal(e)
	}
	v.StackScriptSnapshot = "stale"
	if l.ValidateStackScript(context.Background(), v) == nil {
		t.Fatal("snapshot not checked")
	}
}
func TestStackScriptUnknownDefinitionsFailClosed(t *testing.T) {
	for _, fields := range [][]StackField{{{Name: "EVIL;command", Label: "X"}}, {{Name: "X", Label: "x"}, {Name: "X", Label: "duplicate"}}, {{Name: "X", OneOf: "a", ManyOf: "b"}}} {
		if ValidateStackFields(fields, map[string]string{"X": "a"}) == nil {
			t.Fatal(fmt.Sprint(fields))
		}
	}
}
