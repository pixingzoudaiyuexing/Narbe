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

// These contracts use the current official Linode Firewall API schema. In
// particular, ACCEPT policies with empty lists allow every protocol without
// relying on the unsupported "ALL" rule protocol.
func TestLinodeFirewallRulesOfficialBoundaries(t *testing.T) {
	tcp := FirewallRule{Action: "ACCEPT", Protocol: "TCP", Ports: "22-24, 80, 443", Label: "ssh-web", Addresses: FirewallAddresses{IPv4: []string{"0.0.0.0/0"}, IPv6: []string{"::/0"}}}
	rules := func(rule FirewallRule) FirewallRules {
		r := OpenFirewallRules()
		r.InboundPolicy = "DROP"
		r.Inbound = []FirewallRule{rule}
		return r
	}
	valid := map[string]FirewallRules{
		"all protocols and addresses":  OpenFirewallRules(),
		"TCP spaces and both families": rules(tcp),
		"TCP all ports":                rules(FirewallRule{Action: "ACCEPT", Protocol: "TCP", Addresses: tcp.Addresses}),
		"UDP single boundary port":     rules(FirewallRule{Action: "DROP", Protocol: "UDP", Ports: "65535", Addresses: FirewallAddresses{IPv4: []string{"192.0.2.1/32"}}}),
		"ICMP IPv6 only":               rules(FirewallRule{Action: "ACCEPT", Protocol: "ICMP", Addresses: FirewallAddresses{IPv6: []string{"2001:db8::1/128"}}}),
		"IPENCAP no ports":             rules(FirewallRule{Action: "ACCEPT", Protocol: "IPENCAP", Addresses: FirewallAddresses{IPv4: []string{"192.0.2.0/24"}}}),
	}
	for name, r := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := ValidateFirewallRules(r); err != nil {
				t.Fatalf("officially valid rules rejected: %v", err)
			}
		})
	}

	invalid := map[string]FirewallRules{}
	change := func(name string, edit func(*FirewallRule)) {
		rule := tcp
		rule.Ports = "22,80,443"
		edit(&rule)
		invalid[name] = rules(rule)
	}
	for _, protocol := range []string{"ALL", "SCTP", "tcp", ""} {
		change("protocol "+protocol, func(r *FirewallRule) { r.Protocol = protocol })
	}
	for _, ports := range []string{"0", "65536", "080", "1-1", "24-22", "22-", "-22", "22,,80", "22,  80", "22,\t80", "22 ,80", " 22", strings.Repeat("1,", 15) + "1"} {
		change("ports "+ports, func(r *FirewallRule) { r.Ports = ports })
	}
	for _, protocol := range []string{"ICMP", "IPENCAP"} {
		change(protocol+" with ports", func(r *FirewallRule) { r.Protocol = protocol })
	}
	for _, label := range []string{"a", "ab", strings.Repeat("a", 33)} {
		change("label length "+fmt.Sprint(len(label)), func(r *FirewallRule) { r.Label = label })
	}
	change("invalid action", func(r *FirewallRule) { r.Action = "ALLOW" })
	change("no addresses", func(r *FirewallRule) { r.Addresses = FirewallAddresses{} })
	change("bare address", func(r *FirewallRule) { r.Addresses = FirewallAddresses{IPv4: []string{"192.0.2.1"}} })
	change("wrong IPv4 family", func(r *FirewallRule) { r.Addresses = FirewallAddresses{IPv4: []string{"::/0"}} })
	change("wrong IPv6 family", func(r *FirewallRule) { r.Addresses = FirewallAddresses{IPv6: []string{"0.0.0.0/0"}} })
	change("IPv6 zone", func(r *FirewallRule) { r.Addresses = FirewallAddresses{IPv6: []string{"fe80::1%eth0/128"}} })
	change("IPv4 mapped IPv6", func(r *FirewallRule) { r.Addresses = FirewallAddresses{IPv6: []string{"::ffff:192.0.2.1/128"}} })
	change("address count 256", func(r *FirewallRule) {
		r.Addresses = FirewallAddresses{IPv4: make([]string, 256)}
		for i := range r.Addresses.IPv4 {
			r.Addresses.IPv4[i] = "192.0.2.1/32"
		}
	})
	r := OpenFirewallRules()
	r.Inbound = nil
	invalid["missing inbound list"] = r
	r = OpenFirewallRules()
	r.OutboundPolicy = ""
	invalid["missing outbound policy"] = r
	r = OpenFirewallRules()
	r.Inbound = make([]FirewallRule, 26)
	invalid["26 rules"] = r
	for name, r := range invalid {
		t.Run("invalid/"+name, func(t *testing.T) {
			if err := ValidateFirewallRules(r); err == nil {
				t.Fatalf("invalid rules accepted: %+v", r)
			}
		})
	}
}

func TestLinodeFirewallCreateExplicitLegacySelection(t *testing.T) {
	for _, mode := range []string{"all", "existing", "custom"} {
		t.Run(mode, func(t *testing.T) {
			selected := &FirewallSelection{Mode: mode}
			wantID := -1
			if mode == "existing" {
				selected.ID, wantID = 42, 42
			}
			if mode == "custom" {
				r := OpenFirewallRules()
				selected.Rules, wantID = &r, 43
			}
			var writes []string
			l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					writes = append(writes, req.Method+" "+req.URL.Path)
				}
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls":
					return response(200, `{"data":[],"pages":1}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/42":
					return response(200, `{"id":42,"label":"existing-fw","status":"enabled","rules":{"inbound_policy":"DROP","outbound_policy":"ACCEPT","inbound":[],"outbound":[]}}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/42/devices":
					return response(200, `{"data":[],"pages":1}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/43":
					return response(200, `{"id":43,"label":"custom-fw","status":"enabled","rules":{"inbound_policy":"ACCEPT","outbound_policy":"ACCEPT","inbound":[],"outbound":[]}}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/43/devices":
					return response(200, `{"data":[],"pages":1}`), nil
				case req.Method == http.MethodPost && req.URL.Path == "/v4/networking/firewalls":
					var b struct {
						Rules FirewallRules `json:"rules"`
					}
					if e := json.NewDecoder(req.Body).Decode(&b); e != nil {
						t.Fatal(e)
					}
					if !reflect.DeepEqual(b.Rules, OpenFirewallRules()) {
						t.Fatalf("custom full rules not sent: %+v", b)
					}
					return response(200, `{"id":43}`), nil
				case req.Method == http.MethodPost && req.URL.Path == "/v4/linode/instances":
					var body map[string]json.RawMessage
					if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
						t.Fatal(e)
					}
					if string(body["firewall_id"]) != fmt.Sprint(wantID) || string(body["interface_generation"]) != `"legacy_config"` {
						t.Fatalf("create relied on null/default or wrong interface generation: %+v", body)
					}
					return response(200, `{"id":123}`), nil
				default:
					t.Fatalf("unexpected request (including account-default writes): %s %s", req.Method, req.URL.Path)
					return nil, nil
				}
			})}}
			op, err := l.Create(context.Background(), Spec{Name: "fixture", Region: "gb-lon", Plan: "g6-standard-1", Image: "linode/debian13", SSHPublicKey: "ssh-ed25519 fixture", Firewall: selected}, "firewall-contract")
			if err != nil || op.ResourceID != "123" {
				t.Fatal(op, err)
			}
			if mode == "custom" {
				if !reflect.DeepEqual(op.Owned, []string{"firewall:43", "linode:123"}) || len(writes) != 2 {
					t.Fatalf("missing compound ownership ledger: %+v, writes=%v", op, writes)
				}
			} else if len(writes) != 1 {
				t.Fatalf("selection changed shared/default rules: %v", writes)
			}
		})
	}
}

func linodeFirewallDeviceJSON(device, iface, parent int) string {
	return fmt.Sprintf(`{"id":%d,"entity":{"id":%d,"type":"linode_interface","parent_entity":{"id":%d,"type":"linode"}}}`, device, iface, parent)
}

func TestLinodeFirewallNewInterfaceExactParentAndDevice(t *testing.T) {
	for _, parentID := range []int{123, 999} {
		t.Run(fmt.Sprint(parentID), func(t *testing.T) {
			var writes []string
			l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					writes = append(writes, req.Method+" "+req.URL.Path)
				}
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/v4/linode/instances/123/interfaces/7/firewalls":
					return response(200, `{"data":[{"id":42}],"pages":1}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/42":
					return response(200, `{"id":42,"label":"shared","status":"enabled"}`), nil
				case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/42/devices":
					return response(200, `{"data":[`+linodeFirewallDeviceJSON(51, 7, parentID)+`,`+linodeFirewallDeviceJSON(52, 8, 999)+`],"pages":1}`), nil
				case req.Method == http.MethodDelete && req.URL.Path == "/v4/networking/firewalls/42/devices/51":
					return response(200, `{}`), nil
				case req.Method == http.MethodPost && req.URL.Path == "/v4/networking/firewalls/43/devices":
					var body map[string]any
					json.NewDecoder(req.Body).Decode(&body)
					if body["type"] != "linode_interface" || body["id"] != float64(7) || len(body) != 2 {
						t.Fatalf("wrong interface device payload: %+v", body)
					}
					return response(200, linodeFirewallDeviceJSON(53, 7, 123)), nil
				default:
					t.Fatalf("wrong scope/shared rule request: %s %s", req.Method, req.URL.Path)
					return nil, nil
				}
			})}}
			op := Operation{ResourceID: "123", Linode: &LinodeOperation{Action: "firewall-bind"}}
			err := l.bindFirewall(context.Background(), LinodeState{Instance: Instance{ID: "123"}, Generation: "linode"}, 7, 43, &op)
			if parentID != 123 {
				if err == nil || len(writes) != 0 {
					t.Fatalf("cross-instance interface modified: err=%v writes=%v", err, writes)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(writes, []string{"DELETE /v4/networking/firewalls/42/devices/51", "POST /v4/networking/firewalls/43/devices"}) {
				t.Fatal(err, writes)
			}
		})
	}
}

func TestLinodeFirewallSharedSnapshotChangeRejectsWrites(t *testing.T) {
	devices := `[ {"id":1,"entity":{"id":123,"type":"linode"}} ]`
	writes := 0
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			writes++
			t.Fatalf("write after shared impact scope changed: %s %s", req.Method, req.URL.Path)
		}
		switch req.URL.Path {
		case "/v4/networking/firewalls/42":
			return response(200, `{"id":42,"label":"shared-fw","status":"enabled","rules":{"inbound_policy":"DROP","outbound_policy":"ACCEPT","inbound":[],"outbound":[]}}`), nil
		case "/v4/networking/firewalls/42/devices":
			return response(200, `{"data":`+devices+`,"pages":1}`), nil
		default:
			t.Fatal(req.URL.Path)
			return nil, nil
		}
	})}}
	f, err := l.firewall(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	devices = `[ {"id":1,"entity":{"id":123,"type":"linode"}}, {"id":2,"entity":{"id":999,"type":"linode"}} ]`
	r := OpenFirewallRules()
	for _, action := range []string{"firewall-rules", "firewall-status", "firewall-delete"} {
		c := LinodeChange{Snapshot: f.Snapshot, Acknowledge: true, Rules: &r, Status: "disabled"}
		if err := l.ValidateChange(context.Background(), "42", action, c); err == nil {
			t.Fatalf("changed shared device set accepted for %s", action)
		}
	}
	if writes != 0 {
		t.Fatal(writes)
	}
}

func TestLinodeFirewallSelectionsFailClosedWithoutReadPermission(t *testing.T) {
	for _, allowUnverified := range []bool{false, true} {
		t.Run(fmt.Sprint(allowUnverified), func(t *testing.T) {
			calls := 0
			l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.Path != "/v4/networking/firewalls" {
					t.Fatalf("permission check wrote or changed defaults: %s %s", req.Method, req.URL.Path)
				}
				return response(403, `{"errors":[{"reason":"fixture denied"}]}`), nil
			})}}
			err := l.ValidateFirewallSelection(context.Background(), FirewallSelection{Mode: "all", AllowUnverified: allowUnverified})
			if (err == nil) != allowUnverified || calls != 1 {
				t.Fatalf("no implicit unverified fallback: err=%v allow=%v calls=%d", err, allowUnverified, calls)
			}
		})
	}

	rules := OpenFirewallRules()
	invalid := []FirewallSelection{
		{Mode: "all", ID: 42},
		{Mode: "all", Rules: &rules},
		{Mode: "existing", ID: 0},
		{Mode: "existing", ID: 42, Rules: &rules},
		{Mode: "existing", ID: 42, AllowUnverified: true},
		{Mode: "custom", Rules: nil},
		{Mode: "custom", ID: 42, Rules: &rules},
		{Mode: "custom", Rules: &rules, AllowUnverified: true},
		{Mode: "default"},
	}
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("ambiguous selection reached cloud: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})}}
	for _, s := range invalid {
		if err := l.ValidateFirewallSelection(context.Background(), s); err == nil {
			t.Fatalf("invalid or implicit-default selection accepted: %+v", s)
		}
	}
}

func TestLinodeFirewallBindingPartialFailureIsUnknown(t *testing.T) {
	var writes []string
	var recorded []Operation
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			writes = append(writes, req.Method+" "+req.URL.Path)
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v4/linode/instances/123":
			return response(200, `{"id":123,"label":"fixture","region":"gb-lon","status":"running","type":"g6-standard-1","image":"linode/debian13","interface_generation":"linode"}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v4/linode/instances/123/interfaces":
			return response(200, `{"interfaces":[{"id":7,"public":{}}]}`), nil
		case req.Method == http.MethodGet && (strings.HasSuffix(req.URL.Path, "/disks") || strings.HasSuffix(req.URL.Path, "/configs") || strings.HasSuffix(req.URL.Path, "/volumes")):
			return response(200, `{"data":[],"pages":1}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v4/linode/instances/123/interfaces/7/firewalls":
			return response(200, `{"data":[{"id":42}],"pages":1}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/42":
			return response(200, `{"id":42,"label":"shared","status":"enabled"}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/42/devices":
			return response(200, `{"data":[`+linodeFirewallDeviceJSON(51, 7, 123)+`],"pages":1}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/43":
			return response(200, `{"id":43,"label":"target-fw","status":"enabled","rules":{"inbound_policy":"ACCEPT","outbound_policy":"ACCEPT","inbound":[],"outbound":[]}}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/43/devices":
			return response(200, `{"data":[],"pages":1}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/v4/networking/firewalls/42/devices/51":
			return response(200, `{}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v4/networking/firewalls/43/devices":
			return response(403, `{"errors":[{"reason":"fixture denied token=must-not-leak"}]}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})}}
	ctx := WithOperationRecorder(context.Background(), func(op Operation) error {
		var copy Operation
		if e := json.Unmarshal(marshal(op), &copy); e != nil {
			return e
		}
		recorded = append(recorded, copy)
		return nil
	})
	op, err := l.Change(ctx, "123", "firewall-bind", LinodeChange{InterfaceID: 7, Firewall: &FirewallSelection{Mode: "existing", ID: 43}}, "bind-contract")
	if err == nil || !Uncertain(err) || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("accepted detach then failed attach must be unknown without secrets: op=%+v err=%v", op, err)
	}
	if len(writes) != 2 || op.ResourceID != "123" || op.Linode == nil || op.Linode.InterfaceID != 7 {
		t.Fatalf("missing partial target ledger or request repeated: op=%+v writes=%v", op, writes)
	}
	if len(recorded) == 0 || len(recorded[len(recorded)-1].Linode.Steps) == 0 || !strings.Contains(recorded[len(recorded)-1].Linode.Steps[0], "device:51") {
		t.Fatalf("accepted detach not durably recorded before next write: %+v", recorded)
	}
}

func TestLinodeFirewallCustomCreateThenInstanceFailureIsUnknown(t *testing.T) {
	var records []Operation
	writes := 0
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			writes++
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls":
			return response(200, `{"data":[],"pages":1}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/43":
			return response(200, `{"id":43,"label":"custom-fw","status":"enabled","rules":{"inbound_policy":"ACCEPT","outbound_policy":"ACCEPT","inbound":[],"outbound":[]}}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v4/networking/firewalls/43/devices":
			return response(200, `{"data":[],"pages":1}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v4/networking/firewalls":
			return response(200, `{"id":43}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v4/linode/instances":
			return response(400, `{"errors":[{"reason":"fixture insufficient quota"}]}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})}}
	ctx := WithOperationRecorder(context.Background(), func(op Operation) error {
		var copy Operation
		if e := json.Unmarshal(marshal(op), &copy); e != nil {
			return e
		}
		records = append(records, copy)
		return nil
	})
	r := OpenFirewallRules()
	op, err := l.Create(ctx, Spec{Name: "fixture", Region: "gb-lon", SSHPublicKey: "ssh-ed25519 fixture", Firewall: &FirewallSelection{Mode: "custom", Rules: &r}}, "compound-contract")
	if err == nil || !Uncertain(err) || !reflect.DeepEqual(op.Owned, []string{"firewall:43"}) || len(records) == 0 || writes != 2 {
		t.Fatalf("created firewall must survive failed VM in unknown ledger: op=%+v err=%v records=%+v writes=%d", op, err, records, writes)
	}
}

func TestLinodeFirewallInterruptedReadbackKeepsExpectedTarget(t *testing.T) {
	for _, action := range []string{"firewall-create", "firewall-rules", "firewall-status"} {
		t.Run(action, func(t *testing.T) {
			goal := OpenFirewallRules()
			old := OpenFirewallRules()
			old.InboundPolicy = "DROP"
			currentRules := old
			currentStatus := "enabled"
			readbackAvailable := false
			writes := 0
			var records []Operation
			l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					writes++
					if (action == "firewall-create" && req.Method == "POST" && req.URL.Path == "/v4/networking/firewalls") || (action == "firewall-rules" && req.Method == "PUT" && req.URL.Path == "/v4/networking/firewalls/42/rules") || (action == "firewall-status" && req.Method == "PUT" && req.URL.Path == "/v4/networking/firewalls/42") {
						return response(200, `{"id":42}`), nil
					}
					t.Fatalf("unexpected write: %s %s", req.Method, req.URL.Path)
				}
				switch req.URL.Path {
				case "/v4/networking/firewalls/42":
					if writes > 0 && !readbackAvailable {
						return response(403, `{"errors":[{"reason":"fixture read denied"}]}`), nil
					}
					return response(200, string(marshal(map[string]any{"id": 42, "label": "fixture-fw", "status": currentStatus, "rules": currentRules}))), nil
				case "/v4/networking/firewalls/42/devices":
					return response(200, `{"data":[],"pages":1}`), nil
				default:
					t.Fatalf("unexpected read: %s", req.URL.Path)
					return nil, nil
				}
			})}}
			ctx := WithOperationRecorder(context.Background(), func(op Operation) error {
				var copy Operation
				if err := json.Unmarshal(marshal(op), &copy); err != nil {
					return err
				}
				records = append(records, copy)
				return nil
			})
			id := "42"
			c := LinodeChange{Label: "fixture-fw", Acknowledge: true}
			if action == "firewall-create" || action == "firewall-rules" {
				c.Rules = &goal
			} else {
				c.Status = "disabled"
			}
			if action == "firewall-create" {
				id = ""
			}
			op, err := l.Change(ctx, id, action, c, "readback-contract")
			if err == nil || !Uncertain(err) || writes != 1 || op.ResourceID != "42" {
				t.Fatalf("accepted write followed by denied read must be unknown: op=%+v err=%v writes=%d", op, err, writes)
			}
			readbackAvailable = true
			if done, err := l.Poll(context.Background(), op); done {
				t.Fatalf("old configuration falsely reconciled as success: err=%v op=%+v", err, op)
			}
			if len(records) == 0 {
				t.Fatal("expected target was not durably recorded")
			}
			last := records[len(records)-1].Linode
			if last == nil || (action != "firewall-status" && !reflect.DeepEqual(last.ExpectedRules, &goal)) || (action == "firewall-status" && last.ExpectedStatus != "disabled") {
				t.Fatalf("recorded operation omitted confirmed target: %+v", last)
			}
			if action == "firewall-status" {
				currentStatus = "disabled"
			} else {
				currentRules = goal
			}
			if done, err := l.Poll(context.Background(), op); err != nil || !done {
				t.Fatalf("matching target could not reconcile: done=%v err=%v", done, err)
			}
			if writes != 1 {
				t.Fatal("read-only reconciliation replayed a cloud write")
			}
		})
	}
}

func TestLinodeFirewallMissingExpectedTargetCannotReconcile(t *testing.T) {
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatal("poll wrote cloud")
		}
		if req.URL.Path == "/v4/networking/firewalls/42" {
			return response(200, `{"id":42,"label":"fixture-fw","status":"enabled","rules":{"inbound_policy":"ACCEPT","outbound_policy":"ACCEPT","inbound":[],"outbound":[]}}`), nil
		}
		if req.URL.Path == "/v4/networking/firewalls/42/devices" {
			return response(200, `{"data":[],"pages":1}`), nil
		}
		t.Fatal(req.URL.Path)
		return nil, nil
	})}}
	for _, action := range []string{"firewall-create", "firewall-rules", "firewall-status"} {
		op := Operation{ResourceID: "42", Linode: &LinodeOperation{Action: action}}
		if done, err := l.Poll(context.Background(), op); done {
			t.Fatalf("missing expected target accepted for %s: %v", action, err)
		}
	}
}

func TestLinodeFirewallExistingSelectionChangedBeforeBindingIsRejected(t *testing.T) {
	current := OpenFirewallRules()
	status := "enabled"
	writes := 0
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			writes++
			t.Fatalf("changed selected firewall reached write: %s %s", req.Method, req.URL.Path)
		}
		switch req.URL.Path {
		case "/v4/networking/firewalls/42":
			return response(200, string(marshal(map[string]any{"id": 42, "label": "fixture-fw", "status": status, "rules": current}))), nil
		case "/v4/networking/firewalls/42/devices":
			return response(200, `{"data":[],"pages":1}`), nil
		default:
			t.Fatal(req.URL.Path)
			return nil, nil
		}
	})}}
	f, err := l.firewall(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	selection := &FirewallSelection{Mode: "existing", ID: 42, Snapshot: f.Snapshot}
	if err := l.ValidateFirewallSelection(context.Background(), *selection); err != nil {
		t.Fatal(err)
	}
	current.InboundPolicy = "DROP"
	op := Operation{ResourceID: "123", Linode: &LinodeOperation{Action: "clone"}}
	if _, err := l.selectionID(context.Background(), selection, "later-binding", &op); err == nil {
		t.Fatal("rule change after confirmation was accepted for delayed binding")
	}
	current = OpenFirewallRules()
	status = "disabled"
	if _, err := l.selectionID(context.Background(), selection, "later-binding", &op); err == nil {
		t.Fatal("disabled firewall was accepted for delayed binding")
	}
	if writes != 0 {
		t.Fatal(writes)
	}
}
