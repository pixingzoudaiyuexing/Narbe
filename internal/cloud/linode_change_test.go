package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// This fixture is an HTTP contract boundary, not a substitute provider. The
// production Linode parser, selection checks, request writer and poller all run.
type linodeChangeFixture struct {
	t              *testing.T
	mu             sync.Mutex
	instances      map[string]map[string]any
	disks          map[string][]map[string]any
	configs        map[string][]map[string]any
	volumes        map[string][]map[string]any
	interfaces     map[string][]map[string]any
	before         []map[string]any
	after          []map[string]any
	writes         []linodeChangeWrite
	loseReply      bool
	eventPages     int
	eventPageData  map[int][]map[string]any
	eventPageReads map[int]int
	readPaths      map[string]int
}

type linodeChangeWrite struct {
	Method, Path string
	Body         map[string]any
}

func newLinodeChangeFixture(t *testing.T) (*Linode, *linodeChangeFixture) {
	t.Helper()
	f := &linodeChangeFixture{
		t:          t,
		instances:  map[string]map[string]any{},
		disks:      map[string][]map[string]any{},
		configs:    map[string][]map[string]any{},
		volumes:    map[string][]map[string]any{},
		interfaces: map[string][]map[string]any{},
		before:     []map[string]any{}, after: []map[string]any{},
	}
	for _, id := range []string{"1", "2"} {
		n, _ := strconv.Atoi(id)
		region := "us-east"
		if id == "2" {
			region = "eu-west"
		}
		f.instances[id] = map[string]any{
			"id": n, "label": "fixture-" + id, "region": region,
			"status": "offline", "type": "plan-large", "image": "linode/debian13",
			"interface_generation": "legacy_config", "site_type": "core",
			"ipv4":  []string{"203.0.113." + id},
			"specs": map[string]any{"vcpus": 2, "memory": 4096, "disk": 40960},
		}
		f.disks[id] = []map[string]any{
			{"id": 11, "label": "Debian root", "filesystem": "ext4", "size": 18000, "status": "ready"},
			{"id": 12, "label": "swap", "filesystem": "swap", "size": 512, "status": "ready"},
			{"id": 13, "label": "extra-data", "filesystem": "ext4", "size": 1000, "status": "ready"},
		}
		f.configs[id] = []map[string]any{{"id": 21, "label": "Debian", "devices": map[string]any{
			"sda": map[string]any{"disk_id": 11, "volume_id": 0},
			"sdb": map[string]any{"disk_id": 12, "volume_id": 0},
		}, "interfaces": []map[string]any{{"purpose": "public"}}}}
		f.volumes[id] = []map[string]any{}
		f.interfaces[id] = []map[string]any{{"id": 91, "public": map[string]any{"ipv4": map[string]any{}}}}
	}
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(f.roundTrip)}}
	return l, f
}

func (f *linodeChangeFixture) jsonResponse(v any) *http.Response {
	b, err := json.Marshal(v)
	if err != nil {
		f.t.Fatal(err)
	}
	return response(200, string(b))
}

func (f *linodeChangeFixture) page(v any) *http.Response {
	return f.jsonResponse(map[string]any{"data": v, "page": 1, "pages": 1, "results": reflect.ValueOf(v).Len()})
}

func (f *linodeChangeFixture) roundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Scheme != "https" || r.URL.Host != "api.linode.com" {
		f.t.Fatalf("unexpected cloud URL %s", r.URL)
	}
	path := strings.TrimPrefix(r.URL.Path, "/v4")
	if r.Method == "GET" {
		if f.readPaths == nil {
			f.readPaths = map[string]int{}
		}
		f.readPaths[path]++
	}
	if r.Method != "GET" {
		body := map[string]any{}
		if r.Body != nil {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if len(raw) > 0 && json.Unmarshal(raw, &body) != nil {
				f.t.Fatalf("invalid request JSON: %s", raw)
			}
		}
		f.writes = append(f.writes, linodeChangeWrite{r.Method, path, body})
		if f.loseReply {
			return nil, io.ErrUnexpectedEOF
		}
		switch path {
		case "/linode/instances/1/rebuild", "/linode/instances/1/resize", "/linode/instances/1/disks/11/password", "/linode/instances/1/migrate":
			return f.jsonResponse(map[string]any{}), nil
		case "/linode/instances/1/clone":
			f.instances["2"]["label"] = body["label"]
			return f.jsonResponse(f.instances["2"]), nil
		case "/linode/instances/2/firewalls":
			if r.Method == "PUT" {
				return f.jsonResponse(map[string]any{}), nil
			}
		}
		f.t.Fatalf("unexpected write: %s %s", r.Method, path)
	}
	if path == "/account/events" {
		var filter map[string]any
		if err := json.Unmarshal([]byte(r.Header.Get("X-Filter")), &filter); err != nil {
			f.t.Fatalf("event query must use valid X-Filter: %v", err)
		}
		values := f.before
		if len(f.writes) > 0 {
			values = f.after
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		if f.eventPageReads == nil {
			f.eventPageReads = map[int]int{}
		}
		f.eventPageReads[page]++
		if f.eventPageData != nil {
			values = f.eventPageData[page]
		}
		selected := []map[string]any{}
		for _, ev := range values {
			if linodeFixtureMatches(filter, ev) {
				selected = append(selected, ev)
			}
		}
		pages := f.eventPages
		if pages == 0 {
			pages = 1
		}
		return f.jsonResponse(map[string]any{"data": selected, "page": page, "pages": pages, "results": len(selected)}), nil
	}
	switch path {
	case "/regions":
		return f.page([]map[string]any{
			{"id": "us-east", "label": "Newark", "status": "ok", "site_type": "core", "capabilities": []string{"Linodes", "Linode Interfaces"}},
			{"id": "eu-west", "label": "London", "status": "ok", "site_type": "core", "capabilities": []string{"Linodes", "Linode Interfaces"}},
		}), nil
	case "/linode/types":
		return f.page([]map[string]any{
			{"id": "plan-large", "label": "4 GB", "memory": 4096, "disk": 40960, "vcpus": 2, "price": map[string]any{"monthly": 24, "hourly": .036}},
			{"id": "plan-small", "label": "2 GB", "memory": 2048, "disk": 20480, "vcpus": 1, "price": map[string]any{"monthly": 12, "hourly": .018}},
		}), nil
	case "/regions/us-east/availability", "/regions/eu-west/availability":
		return f.jsonResponse([]map[string]any{{"plan": "plan-large", "available": true}, {"plan": "plan-small", "available": true}}), nil
	case "/images":
		return f.page([]map[string]any{{"id": "linode/debian13", "label": "Debian 13", "architecture": "x86_64", "size": 2048, "status": "available", "is_public": true}}), nil
	case "/profile/sshkeys":
		return f.page([]map[string]any{}), nil
	case "/networking/firewalls":
		return f.page([]map[string]any{}), nil
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "linode" && parts[1] == "instances" {
		id := parts[2]
		if len(parts) == 3 {
			return f.jsonResponse(f.instances[id]), nil
		}
		if len(parts) == 4 {
			switch parts[3] {
			case "disks":
				return f.page(f.disks[id]), nil
			case "configs":
				return f.page(f.configs[id]), nil
			case "volumes":
				return f.page(f.volumes[id]), nil
			case "interfaces":
				return f.jsonResponse(map[string]any{"interfaces": f.interfaces[id]}), nil
			case "firewalls":
				return f.page([]map[string]any{}), nil
			}
		}
	}
	f.t.Fatalf("unexpected read: %s %s", r.Method, path)
	return nil, errors.New("unexpected fixture route")
}

// Honor the documented JSON filtering operators so a same-action-only query
// cannot accidentally pass the other-action pending-event regression.
func linodeFixtureMatches(filter, value map[string]any) bool {
	for key, wanted := range filter {
		if key == "+order" || key == "+order_by" {
			continue
		}
		if key == "+and" || key == "+or" {
			parts, ok := wanted.([]any)
			if !ok {
				return false
			}
			matches := 0
			for _, part := range parts {
				if p, ok := part.(map[string]any); ok && linodeFixtureMatches(p, value) {
					matches++
				}
			}
			if key == "+and" && matches != len(parts) || key == "+or" && matches == 0 {
				return false
			}
			continue
		}
		var got any = value
		for _, field := range strings.Split(key, ".") {
			obj, ok := got.(map[string]any)
			if !ok {
				return false
			}
			got = obj[field]
		}
		if operators, ok := wanted.(map[string]any); ok {
			if g, ok := got.(map[string]any); ok {
				if !linodeFixtureMatches(operators, g) {
					return false
				}
				continue
			}
			for op, expected := range operators {
				g, _ := strconv.ParseFloat(fmt.Sprint(got), 64)
				e, _ := strconv.ParseFloat(fmt.Sprint(expected), 64)
				if op == "+gt" && g <= e || op == "+gte" && g < e || op == "+neq" && fmt.Sprint(got) == fmt.Sprint(expected) {
					return false
				}
			}
		} else if fmt.Sprint(got) != fmt.Sprint(wanted) {
			return false
		}
	}
	return true
}

func linodeFixtureEvent(id int, action, status string, entity, secondary int) map[string]any {
	ev := map[string]any{"id": id, "action": action, "status": status, "entity": map[string]any{"id": entity, "type": "linode"}}
	if secondary > 0 {
		kind := "disk"
		if action == "linode_clone" {
			kind = "linode"
		}
		ev["secondary_entity"] = map[string]any{"id": secondary, "type": kind}
	}
	return ev
}

func linodeFixtureChange(t *testing.T, l *Linode) LinodeChange {
	t.Helper()
	v, err := l.LinodeRead(context.Background(), "instance", "1", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	return LinodeChange{Snapshot: v.(LinodeState).Snapshot, Acknowledge: true}
}

func TestLinodeChangeRequiresFreshDestructiveAcknowledgement(t *testing.T) {
	for _, action := range []string{"rebuild", "resize", "password-reset", "clone", "migrate"} {
		t.Run(action, func(t *testing.T) {
			l, f := newLinodeChangeFixture(t)
			c := linodeFixtureChange(t, l)
			switch action {
			case "rebuild":
				c.Image = "linode/debian13"
				c.SSHPublicKey = "ssh-ed25519 fixture"
			case "resize":
				c.Plan = "plan-small"
			case "password-reset":
				c.Password = "Fixture-Strong-Password-82!"
				c.DiskID = 11
			case "clone":
				c.Region = "eu-west"
				c.Plan = "plan-large"
				c.Label = "new-clone"
				c.Firewall = &FirewallSelection{Mode: "all"}
			case "migrate":
				c.Region = "eu-west"
			}
			if err := l.ValidateChange(context.Background(), "1", action, c); err != nil {
				t.Fatal("valid control must pass before proving guard", err)
			}
			c.Acknowledge = false
			if err := l.ValidateChange(context.Background(), "1", action, c); err == nil {
				t.Fatal("missing consequence acknowledgement accepted")
			}
			c.Acknowledge = true
			c.Snapshot = "previous-configuration"
			if err := l.ValidateChange(context.Background(), "1", action, c); err == nil {
				t.Fatal("stale source identity accepted")
			}
			if len(f.writes) != 0 {
				t.Fatal("confirmation validation sent cloud write")
			}
		})
	}
}

func TestLinodeChangeRebuildSSHOnlyAndNoImplicitResize(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Image = "linode/debian13"
	c.SSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG4ZfwSWioVr3BPWuHucYrSQOgMzuEu3LCUXyJCDP+rZ fixture"
	if err := l.ValidateChange(context.Background(), "1", "rebuild", c); err != nil {
		t.Fatal(err)
	}
	f.before = []map[string]any{linodeFixtureEvent(100, "linode_rebuild", "finished", 1, 0)}
	op, err := l.Change(context.Background(), "1", "rebuild", c, "fixture-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 1 || f.writes[0].Path != "/linode/instances/1/rebuild" {
		t.Fatalf("rebuild writes: %+v", f.writes)
	}
	b := f.writes[0].Body
	if b["image"] != "linode/debian13" || b["booted"] != true {
		t.Fatalf("rebuild image/boot contract: %#v", b)
	}
	if _, exists := b["root_pass"]; exists {
		t.Fatal("SSH-only rebuild included root password")
	}
	if _, exists := b["type"]; exists {
		t.Fatal("rebuild silently resized source")
	}
	if keys, ok := b["authorized_keys"].([]any); !ok || len(keys) != 1 || keys[0] != c.SSHPublicKey {
		t.Fatalf("SSH public key lost: %#v", b)
	}
	if done, err := l.Poll(context.Background(), op); err != nil || done {
		t.Fatalf("old finished event completed a new rebuild: %v %v", done, err)
	}
	f.after = []map[string]any{linodeFixtureEvent(101, "linode_rebuild", "finished", 1, 0)}
	if done, err := l.Poll(context.Background(), op); err != nil || !done {
		t.Fatalf("new event and rebuilt image unconfirmed: %v %v", done, err)
	}
}

func TestLinodeChangeResizeUsesActualAllocatedDiskMB(t *testing.T) {
	for _, total := range []int{20480, 20481} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			l, f := newLinodeChangeFixture(t)
			f.disks["1"] = []map[string]any{{"id": 11, "label": "root", "filesystem": "ext4", "size": total, "status": "ready"}}
			c := linodeFixtureChange(t, l)
			c.Plan = "plan-small"
			err := l.ValidateChange(context.Background(), "1", "resize", c)
			if total > 20480 {
				if err == nil {
					t.Fatal("target capacity exceeded by one MB but downgrade accepted")
				}
				if len(f.writes) != 0 {
					t.Fatal("unsafe downgrade sent write")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = l.Change(context.Background(), "1", "resize", c, "fixture-task"); err != nil {
				t.Fatal(err)
			}
			if len(f.writes) != 1 {
				t.Fatalf("resize write count %d", len(f.writes))
			}
			b := f.writes[0].Body
			if b["type"] != "plan-small" || b["migration_type"] != "cold" || b["allow_auto_disk_resize"] != false {
				t.Fatalf("resize changed disk implicitly: %#v", b)
			}
		})
	}
}

func TestLinodeChangeProtectsVolumesAndCrossRegionNetwork(t *testing.T) {
	for _, scenario := range []string{"volume", "legacy-vpc", "legacy-vlan", "linode-vpc", "linode-vlan", "placement-group", "managed"} {
		t.Run(scenario, func(t *testing.T) {
			l, f := newLinodeChangeFixture(t)
			switch scenario {
			case "volume":
				f.volumes["1"] = []map[string]any{{"id": 51, "label": "retained-billable-volume", "size": 10}}
			case "legacy-vpc", "legacy-vlan":
				f.configs["1"][0]["interfaces"] = []map[string]any{{"purpose": strings.TrimPrefix(scenario, "legacy-"), "vpc_id": 7}}
			case "linode-vpc", "linode-vlan":
				f.instances["1"]["interface_generation"] = "linode"
				f.interfaces["1"] = []map[string]any{{"id": 91, strings.TrimPrefix(scenario, "linode-"): map[string]any{"id": 7}}}
			case "placement-group":
				f.instances["1"]["placement_group"] = map[string]any{"id": 71}
			case "managed":
				f.instances["1"]["lke_cluster_id"] = 81
			}
			c := linodeFixtureChange(t, l)
			c.Region, c.Plan, c.Label = "eu-west", "plan-large", "new-clone"
			c.Firewall = &FirewallSelection{Mode: "all"}
			for _, action := range []string{"clone", "migrate"} {
				change := c
				if action == "migrate" {
					change.Plan = ""
					change.Label = ""
					change.Firewall = nil
				}
				if err := l.ValidateChange(context.Background(), "1", action, change); err == nil {
					t.Errorf("%s %s accepted despite unhandled attached resources", scenario, action)
				}
			}
			if len(f.writes) != 0 {
				t.Fatal("protected resource validation sent write")
			}
		})
	}
}

func TestLinodeChangePasswordRequiresOfflineOwnedSystemDisk(t *testing.T) {
	for _, scenario := range []string{"online", "foreign-disk", "swap", "raw", "weak-password", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			l, f := newLinodeChangeFixture(t)
			c := linodeFixtureChange(t, l)
			c.DiskID, c.Password = 11, "v6*Q9Jn2!wS8eZ4x"
			switch scenario {
			case "online":
				f.instances["1"]["status"] = "running"
			case "foreign-disk":
				c.DiskID = 99
			case "swap":
				c.DiskID = 12
			case "raw":
				f.disks["1"][0]["filesystem"] = "raw"
				c.Snapshot = linodeFixtureChange(t, l).Snapshot
			case "weak-password":
				c.Password = "password123456"
			}
			err := l.ValidateChange(context.Background(), "1", "password-reset", c)
			if scenario != "valid" {
				if err == nil {
					t.Fatal("unsafe password reset accepted")
				}
				if len(f.writes) != 0 {
					t.Fatal("reset validation changed password")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			op, err := l.Change(context.Background(), "1", "password-reset", c, "fixture-task")
			if err != nil {
				t.Fatal(err)
			}
			if len(f.writes) != 1 || f.writes[0].Path != "/linode/instances/1/disks/11/password" || f.writes[0].Body["password"] != c.Password {
				t.Fatalf("wrong disk password endpoint/payload: %+v", f.writes)
			}
			if _, exists := f.writes[0].Body["root_pass"]; exists {
				t.Fatal("disk reset used instance root_pass parameter")
			}
			f.after = []map[string]any{linodeFixtureEvent(101, "password_reset", "finished", 1, 12)}
			if done, err := l.Poll(context.Background(), op); err != nil || done {
				t.Fatalf("another disk's event confirmed password change: %v %v", done, err)
			}
			f.after = []map[string]any{linodeFixtureEvent(101, "password_reset", "finished", 1, 11)}
			if done, err := l.Poll(context.Background(), op); err != nil || !done {
				t.Fatalf("own disk completion not recognized: %v %v", done, err)
			}
		})
	}
}

func TestLinodeChangeCloneCreatesNewBillableIDWithoutOverwritingSource(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Region, c.Plan, c.Label = "eu-west", "plan-large", "new-clone"
	c.Firewall = &FirewallSelection{Mode: "all"}
	if err := l.ValidateChange(context.Background(), "1", "clone", c); err != nil {
		t.Fatal(err)
	}
	f.before = []map[string]any{linodeFixtureEvent(100, "linode_clone", "finished", 1, 2)}
	f.after = []map[string]any{linodeFixtureEvent(101, "linode_clone", "finished", 1, 2)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	op, err := l.Change(ctx, "1", "clone", c, "fixture-task")
	if err != nil {
		t.Fatal(err)
	}
	if op.ResourceID != "2" || op.Linode.SourceID != "1" || !reflect.DeepEqual(op.Owned, []string{"linode:2"}) {
		t.Fatalf("clone ledger lost distinct IDs: %#v", op)
	}
	if len(f.writes) != 2 || f.writes[0].Path != "/linode/instances/1/clone" || f.writes[1].Path != "/linode/instances/2/firewalls" {
		t.Fatalf("clone/firewall mutated source or repeated clone: %+v", f.writes)
	}
	b := f.writes[0].Body
	if b["region"] != "eu-west" || b["type"] != "plan-large" || b["label"] != "new-clone" {
		t.Fatalf("wrong new clone target: %#v", b)
	}
	for _, unsupported := range []string{"linode_id", "tags", "root_pass", "configs"} {
		if _, exists := b[unsupported]; exists {
			t.Errorf("new clone included unsupported/overwriting parameter %q", unsupported)
		}
	}
	if !reflect.DeepEqual(b["disks"], []any{float64(13)}) {
		t.Fatalf("unreferenced local disk omitted from full clone: %#v", b["disks"])
	}
	for i := 0; i < 3; i++ {
		if done, err := l.Poll(context.Background(), op); err != nil || !done {
			t.Fatalf("clone result unavailable: %v %v", done, err)
		}
	}
	if len(f.writes) != 2 {
		t.Fatal("read-only result polling sent another cloud write")
	}
	if f.instances["1"]["status"] != "offline" || f.instances["1"]["region"] != "us-east" {
		t.Fatal("source instance changed")
	}
}

func TestLinodeChangeMigrationUsesColdRegionPayloadAndFreshTarget(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Region = "eu-west"
	if err := l.ValidateChange(context.Background(), "1", "migrate", c); err != nil {
		t.Fatal(err)
	}
	op, err := l.Change(context.Background(), "1", "migrate", c, "fixture-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 1 || f.writes[0].Path != "/linode/instances/1/migrate" {
		t.Fatalf("migration write contract: %+v", f.writes)
	}
	b := f.writes[0].Body
	if b["region"] != "eu-west" || b["type"] != "cold" || b["upgrade"] != false {
		t.Fatalf("migration confused plan and transport type: %#v", b)
	}
	if _, exists := b["migration_type"]; exists {
		t.Fatal("migration used resize's field name")
	}
	f.after = []map[string]any{linodeFixtureEvent(101, "linode_migrate_datacenter", "finished", 1, 0)}
	if done, err := l.Poll(context.Background(), op); err != nil || done {
		t.Fatalf("finished event but old region falsely completed migration: %v %v", done, err)
	}
	f.instances["1"]["region"] = "eu-west"
	f.instances["1"]["ipv4"] = []string{"198.51.100.9"}
	if done, err := l.Poll(context.Background(), op); err != nil || !done {
		t.Fatalf("migrated target unconfirmed: %v %v", done, err)
	}
	v, err := l.LinodeRead(context.Background(), "instance", "1", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.(LinodeState).Instance.IPs, []string{"198.51.100.9"}) {
		t.Fatal("fresh migration detail reused old public IP")
	}
	if len(f.writes) != 1 {
		t.Fatal("IP refresh allocated or reassigned actual cloud IPs")
	}
}

func TestLinodeChangePollNeedsUniqueNewEventAndExactResource(t *testing.T) {
	for _, scenario := range []string{"old", "foreign", "wrong-action", "started", "in-progress", "completed", "ambiguous", "failed", "finished"} {
		t.Run(scenario, func(t *testing.T) {
			l, f := newLinodeChangeFixture(t)
			ev := linodeFixtureEvent(101, "linode_rebuild", "finished", 1, 0)
			switch scenario {
			case "old":
				ev["id"] = 100
			case "foreign":
				ev["entity"] = map[string]any{"id": 2, "type": "linode"}
			case "wrong-action":
				ev["action"] = "linode_resize"
			case "started":
				ev["status"], ev["percent_complete"] = "started", 100
			case "in-progress":
				ev["status"] = "in_progress"
			case "completed":
				ev["status"] = "completed"
			case "failed":
				ev["status"] = "failed"
			}
			f.before = []map[string]any{ev}
			if scenario == "ambiguous" {
				f.before = append(f.before, linodeFixtureEvent(102, "linode_rebuild", "finished", 1, 0))
			}
			op := Operation{ResourceID: "1", Region: "us-east", Linode: &LinodeOperation{Action: "rebuild", Image: "linode/debian13", EventAfter: 100}}
			done, err := l.Poll(context.Background(), op)
			if scenario == "finished" {
				if err != nil || !done {
					t.Fatalf("unique completion rejected: %v %v", done, err)
				}
			} else if scenario == "failed" {
				if done || !errors.Is(err, ErrOperationFailed) {
					t.Fatalf("failure event result: %v %v", done, err)
				}
			} else if scenario == "ambiguous" {
				if done || !Uncertain(err) {
					t.Fatalf("ambiguous events should remain unknown: %v %v", done, err)
				}
			} else if err != nil || done {
				t.Fatalf("unproven event completed job: %v %v", done, err)
			}
			if len(f.writes) != 0 {
				t.Fatal("result polling sent a cloud write")
			}
		})
	}
}

func TestLinodeChangeRejectsAnyPendingCloudActionBeforeDispatch(t *testing.T) {
	for _, status := range []string{"scheduled", "started", "in_progress"} {
		t.Run(status, func(t *testing.T) {
			l, f := newLinodeChangeFixture(t)
			f.before = []map[string]any{linodeFixtureEvent(77, "linode_migrate", status, 1, 0)}
			c := linodeFixtureChange(t, l)
			c.Plan = "plan-small"
			if err := l.ValidateChange(context.Background(), "1", "resize", c); err == nil {
				t.Fatal("resize accepted while another cloud action is pending")
			}
			if len(f.writes) != 0 {
				t.Fatal("pending protection sent a cloud write")
			}
		})
	}
}

func TestLinodeChangeLostWriteReplyRemainsUnknownWithoutRetry(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Plan = "plan-small"
	if err := l.ValidateChange(context.Background(), "1", "resize", c); err != nil {
		t.Fatal(err)
	}
	f.loseReply = true
	op, err := l.Change(context.Background(), "1", "resize", c, "fixture-task")
	if !Uncertain(err) {
		t.Fatalf("lost accepted-or-rejected response must be uncertain: %v", err)
	}
	if len(f.writes) != 1 {
		t.Fatalf("non-idempotent resize was retried %d times", len(f.writes))
	}
	if op.ResourceID != "1" {
		t.Fatal("uncertain operation lost source identity")
	}
	f.after = []map[string]any{linodeFixtureEvent(101, "linode_resize", "started", 1, 0)}
	if done, err := l.Poll(context.Background(), op); err != nil || done {
		t.Fatalf("uncertain running operation falsely completed: %v %v", done, err)
	}
	if len(f.writes) != 1 {
		t.Fatal("uncertain result read repeated cloud mutation")
	}
}

func TestLinodeChangeRejectsRepeatedEventIDsAcrossPages(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Plan = "plan-small"
	// A moving or broken page cursor repeats the same item. Do not treat this as
	// complete history and dispatch a destructive cloud operation.
	f.eventPages = 2
	f.before = []map[string]any{linodeFixtureEvent(99, "linode_rebuild", "finished", 1, 0)}
	if err := l.ValidateChange(context.Background(), "1", "resize", c); err == nil {
		t.Fatal("repeated event IDs proved the server idle")
	}
	if len(f.writes) != 0 {
		t.Fatal("incomplete event validation sent a cloud write")
	}
}

func TestLinodeChangeChecksBoundedLaterEventPages(t *testing.T) {
	for _, status := range []string{"scheduled", "started", "in_progress", "finished"} {
		t.Run(status, func(t *testing.T) {
			l, f := newLinodeChangeFixture(t)
			c := linodeFixtureChange(t, l)
			c.Plan = "plan-small"
			f.eventPages = 2
			f.eventPageData = map[int][]map[string]any{
				1: {linodeFixtureEvent(99, "linode_rebuild", "finished", 1, 0)},
				2: {linodeFixtureEvent(98, "linode_migrate", status, 1, 0)},
			}
			err := l.ValidateChange(context.Background(), "1", "resize", c)
			if status == "finished" {
				if err != nil {
					t.Fatalf("fully read idle history rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("older cloud operation omitted from busy check")
			}
			if f.eventPageReads[1] != 1 || f.eventPageReads[2] != 1 {
				t.Fatalf("event pages not consumed once: %v", f.eventPageReads)
			}
			if len(f.writes) != 0 {
				t.Fatal("history validation emitted a cloud write")
			}
		})
	}
}

func TestLinodeChangeRefusesEventHistoryAboveBound(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Plan = "plan-small"
	f.eventPages = 21
	f.eventPageData = map[int][]map[string]any{}
	for page := 1; page <= 21; page++ {
		f.eventPageData[page] = []map[string]any{linodeFixtureEvent(200-page, "linode_rebuild", "finished", 1, 0)}
	}
	if err := l.ValidateChange(context.Background(), "1", "resize", c); err == nil {
		t.Fatal("unbounded event history proved server idle")
	}
	if f.eventPageReads[21] != 0 {
		t.Fatalf("event history exceeded bounded reads: %v", f.eventPageReads)
	}
	if len(f.writes) != 0 {
		t.Fatal("bounded history refusal emitted cloud write")
	}
}

func TestLinodeChangeCloneCannotAcceptSourceAsNewResource(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Region, c.Plan, c.Label = "eu-west", "plan-large", "new-clone"
	c.Firewall = &FirewallSelection{Mode: "all"}
	f.instances["2"]["id"] = 1
	op, err := l.Change(context.Background(), "1", "clone", c, "fixture-task")
	if !Uncertain(err) {
		t.Fatalf("untrusted clone ID should retain unknown: %v", err)
	}
	if len(f.writes) != 1 || f.writes[0].Path != "/linode/instances/1/clone" {
		t.Fatalf("untrusted clone response caused another resource write: %+v", f.writes)
	}
	if len(op.Owned) != 0 {
		t.Fatal("source instance was falsely claimed as a newly owned billable resource")
	}
}

func TestLinodeChangeClonePersistsNewIDBeforeFollowupWrites(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Region, c.Plan, c.Label = "eu-west", "plan-large", "new-clone"
	c.Firewall = &FirewallSelection{Mode: "all"}
	ctx := WithOperationRecorder(context.Background(), func(op Operation) error {
		if op.ResourceID == "2" {
			return errors.New("fixture database unavailable")
		}
		return nil
	})
	op, err := l.Change(ctx, "1", "clone", c, "fixture-task")
	if !Uncertain(err) || op.ResourceID != "2" || !reflect.DeepEqual(op.Owned, []string{"linode:2"}) {
		t.Fatalf("accepted clone lost ledger identity after save failure: op=%+v err=%v", op, err)
	}
	if len(f.writes) != 1 {
		t.Fatalf("follow-up network write ran without durable clone identity: %+v", f.writes)
	}
}

func TestLinodeChangeBaselineAcceptsOfficialImageSecondaryID(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Plan = "plan-small"
	// Linode's public SDK HTTP recording contains this exact entity shape:
	// create/rebuild event secondary IDs are image strings, not integer IDs.
	f.before = []map[string]any{{"id": 100, "action": "linode_create", "status": "finished",
		"entity":           map[string]any{"id": 1, "type": "linode"},
		"secondary_entity": map[string]any{"id": "linode/debian12", "type": "image"}}}
	if err := l.ValidateChange(context.Background(), "1", "resize", c); err != nil {
		t.Fatalf("normal image creation history blocked resize: %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatal("history compatibility validation sent cloud write")
	}
}

func TestLinodeChangeOtherConcurrentCloneCannotCompleteOrReconfigureTarget(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	c := linodeFixtureChange(t, l)
	c.Region, c.Plan, c.Label = "eu-west", "plan-large", "new-clone"
	c.Firewall = &FirewallSelection{Mode: "all"}
	f.before = []map[string]any{linodeFixtureEvent(100, "linode_clone", "finished", 1, 8)}
	// Cloud Manager may independently clone the same source. Its event appears
	// before this task's event; the returned new Linode is already "offline".
	f.after = []map[string]any{linodeFixtureEvent(101, "linode_clone", "finished", 1, 9)}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	op, err := l.Change(ctx, "1", "clone", c, "fixture-task")
	if !Uncertain(err) || op.ResourceID != "2" || !reflect.DeepEqual(op.Owned, []string{"linode:2"}) {
		t.Fatalf("unproven clone lost unknown ledger: op=%+v err=%v", op, err)
	}
	if len(f.writes) != 1 || f.writes[0].Path != "/linode/instances/1/clone" || f.readPaths["/linode/instances/2"] != 0 {
		t.Fatalf("another clone's event advanced this target network configuration: writes=%+v reads=%v", f.writes, f.readPaths)
	}
	// Even an already configured task must still match its new clone's ID.
	op.Linode.Configured = true
	if done, err := l.Poll(context.Background(), op); err != nil || done {
		t.Fatalf("another target clone event completed this task: %v %v", done, err)
	}
	if len(f.writes) != 1 {
		t.Fatal("polling a competing clone replayed source POST")
	}
}
