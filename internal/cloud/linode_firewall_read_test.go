package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestLinodeFirewallReadEmptyCollectionsPreserveSnapshot(t *testing.T) {
	for _, devices := range []string{"[]", `[{"id":51,"entity":{"id":123,"type":"linode"}},{"id":52,"entity":{"id":8,"type":"linode_interface","parent_entity":{"id":456,"type":"linode"}}}]`} {
		for _, rules := range []string{`{"inbound":null,"outbound":null,"inbound_policy":"DROP","outbound_policy":"ACCEPT"}`, `{"inbound":null,"outbound":[],"inbound_policy":"DROP","outbound_policy":"ACCEPT"}`, `{"inbound":[],"outbound":null,"inbound_policy":"DROP","outbound_policy":"ACCEPT"}`, `{"inbound":[],"outbound":[],"inbound_policy":"DROP","outbound_policy":"ACCEPT"}`} {
			l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					t.Fatal("unexpected write")
				}
				switch r.URL.Path {
				case "/v4/networking/firewalls/42":
					return response(200, `{"id":42,"label":"empty-fw","status":"enabled","rules":`+rules+`}`), nil
				case "/v4/networking/firewalls/42/devices":
					return response(200, `{"data":`+devices+`,"pages":1}`), nil
				case "/v4/networking/firewalls":
					return response(200, `{"data":[{"id":42}],"pages":1}`), nil
				}
				t.Fatal(r.URL.Path)
				return nil, nil
			})}}
			raw, err := l.firewall(context.Background(), "42")
			if err != nil {
				t.Fatal(err)
			}
			if devices == "[]" && strings.Contains(rules, `"inbound":null`) && strings.Contains(rules, `"outbound":null`) && raw.Snapshot != "81cd5fbcf81e85fe10da0caded6d0a90d6a1bdce01c19b778255054976d1b8f3" {
				t.Fatal("reviewed pre-fix snapshot contract changed")
			}
			before, _ := json.Marshal(raw)
			data, err := l.LinodeRead(context.Background(), "firewalls", "", "", 1)
			if err != nil {
				t.Fatal(err)
			}
			v := data.(LinodeFirewallView)
			b, _ := json.Marshal(v)
			if strings.Contains(string(b), `"devices":null`) || strings.Contains(string(b), `"inbound":null`) || strings.Contains(string(b), `"outbound":null`) {
				t.Fatalf("nil leaked: %s", b)
			}
			if v.Items[0].Snapshot != raw.Snapshot || v.Snapshot != fingerprint([]LinodeFirewall{raw}) {
				t.Fatal("presentation changed trusted snapshot")
			}
			after, _ := json.Marshal(raw)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("display mutated raw state")
			}
			// A read-only choice still accepts the exact pre-normalization snapshot.
			if err = l.ValidateFirewallSelection(context.Background(), FirewallSelection{Mode: "existing", ID: 42, Snapshot: raw.Snapshot}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestLinodeFirewallReadEmptyListsAndBindingSnapshots(t *testing.T) {
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("write")
		}
		return response(200, `{"data":[],"pages":1}`), nil
	})}}
	for _, view := range []string{"firewalls", "instance-firewalls"} {
		data, e := l.LinodeRead(context.Background(), view, "123", "", 1)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(data)
		if !strings.Contains(string(b), `"items":[]`) {
			t.Fatalf("empty %s did not encode []: %s", view, b)
		}
		var record map[string]any
		json.Unmarshal(b, &record)
		if record["snapshot"] != fingerprint(nil) {
			t.Fatal("empty binding fingerprint changed")
		}
	}
}
func TestLinodeFirewallMalformedReadCannotMasqueradeAsEmpty(t *testing.T) {
	for _, payload := range []string{
		`{"id":42,"label":"bad","status":"enabled","rules":{"outbound":null,"inbound_policy":"DROP","outbound_policy":"ACCEPT"}}`,
		`{"id":42,"label":"bad","status":"enabled","rules":{"inbound":null,"inbound_policy":"DROP","outbound_policy":"ACCEPT"}}`,
		`{"id":42,"label":"bad","status":"mystery","rules":{"inbound":null,"outbound":null,"inbound_policy":"DROP","outbound_policy":"ACCEPT"}}`,
		`{"id":42,"label":"bad","status":"enabled","rules":{"inbound":null,"outbound":null,"inbound_policy":"UNKNOWN","outbound_policy":"ACCEPT"}}`,
		`{"id":42,"label":"bad","status":"enabled","rules":{"inbound":null,"outbound":null,"inbound_policy":"ACCEPT"}}`,
	} {
		l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/devices") {
				return response(200, `{"data":[],"pages":1}`), nil
			}
			return response(200, payload), nil
		})}}
		if _, e := l.LinodeRead(context.Background(), "firewall", "42", "", 1); e == nil {
			t.Fatal("malformed security fields accepted", fmt.Sprint(payload))
		}
	}
}
