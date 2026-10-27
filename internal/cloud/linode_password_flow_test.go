package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPasswordResetAcceptsRunningInstanceForAutomaticFlow(t *testing.T) {
	l, f := newLinodeChangeFixture(t)
	f.instances["1"]["status"] = "running"
	c := linodeFixtureChange(t, l)
	c.Password = "v6*Q9Jn2!wS8eZ4x"
	c.DiskID = 11
	if err := l.ValidateChange(context.Background(), "1", "password-reset", c); err != nil {
		t.Fatalf("running instance must enter automatic shutdown/reset/restore flow: %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatal("validation performed a cloud write")
	}
}

type passwordFlowFixture struct {
	base          *linodeChangeFixture
	failWrite     string
	loseWrite     string
	eventMode     map[string]string
	readFailure   string
	changedDiskAt string
}

func newPasswordFlow(t *testing.T, initial string) (*Linode, *passwordFlowFixture, LinodeChange) {
	l, base := newLinodeChangeFixture(t)
	base.instances["1"]["status"] = initial
	c := linodeFixtureChange(t, l)
	c.Password = "v6*Q9Jn2!wS8eZ4x"
	c.DiskID = 11
	flow := &passwordFlowFixture{base: base, eventMode: map[string]string{}}
	original := l.h.Transport
	l.h = client(func(r *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(r.URL.Path, "/v4")
		if r.Method == "GET" {
			base.mu.Lock()
			if flow.readFailure != "" && len(base.writes) > 0 && strings.HasSuffix(base.writes[len(base.writes)-1].Path, flow.readFailure) {
				base.mu.Unlock()
				return nil, io.ErrUnexpectedEOF
			}
			base.mu.Unlock()
			return original.RoundTrip(r)
		}
		step := path[strings.LastIndex(path, "/")+1:]
		if step != "shutdown" && step != "boot" && step != "password" {
			return original.RoundTrip(r)
		}
		body := map[string]any{}
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
		}
		base.mu.Lock()
		defer base.mu.Unlock()
		base.writes = append(base.writes, linodeChangeWrite{r.Method, path, body})
		if flow.failWrite == step {
			return response(409, `{"errors":[{"reason":"cloud fixture rejects concurrent action"}]}`), nil
		}
		if flow.loseWrite == step {
			return nil, io.ErrUnexpectedEOF
		}
		switch step {
		case "shutdown":
			base.instances["1"]["status"] = "offline"
		case "boot":
			base.instances["1"]["status"] = "running"
		}
		action := map[string]string{"shutdown": "linode_shutdown", "password": "password_reset", "boot": "linode_boot"}[step]
		disk := 0
		if step == "password" {
			disk = 11
		}
		status := "finished"
		mode := flow.eventMode[step]
		if mode == "pending" {
			status = "started"
		}
		if mode == "failed" {
			status = "failed"
		}
		if mode == "wrong-disk" {
			disk = 12
		}
		event := linodeFixtureEvent(100+len(base.writes), action, status, 1, disk)
		if mode != "missing" {
			base.after = append(base.after, event)
		}
		if mode == "duplicate" {
			base.after = append(base.after, linodeFixtureEvent(200+len(base.writes), action, status, 1, disk))
		}
		if mode == "wrong-entity" {
			event["entity"].(map[string]any)["id"] = 2
		}
		if mode == "wrong-entity-type" {
			event["entity"].(map[string]any)["type"] = "volume"
		}
		if flow.changedDiskAt == step {
			base.disks["1"][0]["label"] = "changed-target"
		}
		return base.jsonResponse(map[string]any{}), nil
	})
	return l, flow, c
}

func TestPasswordResetAutomaticRunningAndOfflineFlow(t *testing.T) {
	for _, initial := range []string{"running", "offline"} {
		t.Run(initial, func(t *testing.T) {
			l, f, c := newPasswordFlow(t, initial)
			var checkpoints []Operation
			ctx := WithOperationRecorder(context.Background(), func(op Operation) error {
				raw, _ := json.Marshal(op)
				if bytes.Contains(raw, []byte(c.Password)) {
					t.Fatal("password leaked into public checkpoint")
				}
				var saved Operation
				_ = json.Unmarshal(raw, &saved)
				checkpoints = append(checkpoints, saved)
				return nil
			})
			op, err := l.Change(ctx, "1", "password-reset", c, "fixed-request")
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"/linode/instances/1/disks/11/password"}
			wantStages := []string{"正在核实服务器和目标磁盘", "正在重置密码", "密码已重置", "已完成"}
			if initial == "running" {
				want = []string{"/linode/instances/1/shutdown", "/linode/instances/1/disks/11/password", "/linode/instances/1/boot"}
				wantStages = []string{"正在核实服务器和目标磁盘", "正在关机", "已关机", "正在重置密码", "密码已重置", "正在开机", "已完成"}
			}
			var actual []string
			for _, w := range f.base.writes {
				actual = append(actual, w.Path)
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("wrong writes: %v", actual)
			}
			var stages []string
			for _, v := range checkpoints {
				stages = append(stages, v.Linode.Stage)
			}
			if !reflect.DeepEqual(stages, wantStages) {
				t.Fatalf("stages %v", stages)
			}
			if op.Linode.Stage != "已完成" || !op.Linode.PasswordResetConfirmed || op.Linode.OriginalState != initial {
				t.Fatalf("bad ledger: %+v", op.Linode)
			}
			if f.base.instances["1"]["status"] != initial {
				t.Fatal("original runtime state not restored")
			}
			before := len(f.base.writes)
			for i := 0; i < 3; i++ {
				done, e := l.Poll(context.Background(), op)
				if e != nil || !done {
					t.Fatalf("completed flow not reconciled: %v %v", done, e)
				}
			}
			if len(f.base.writes) != before {
				t.Fatal("read-only Poll performed cloud writes")
			}
		})
	}
}

func TestPasswordResetStopsAtEveryFailedOrUnknownStage(t *testing.T) {
	for _, step := range []string{"shutdown", "password", "boot"} {
		for _, mode := range []string{"reject", "lost", "read-failure", "pending", "failed", "missing", "duplicate", "wrong-entity", "wrong-entity-type", "changed-disk"} {
			t.Run(step+"/"+mode, func(t *testing.T) {
				l, f, c := newPasswordFlow(t, "running")
				switch mode {
				case "reject":
					f.failWrite = step
				case "lost":
					f.loseWrite = step
				case "read-failure":
					f.readFailure = step
				case "changed-disk":
					f.changedDiskAt = step
				default:
					f.eventMode[step] = mode
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				defer cancel()
				var checkpoints []Operation
				ctx = WithOperationRecorder(ctx, func(op Operation) error {
					raw, _ := json.Marshal(op)
					var saved Operation
					_ = json.Unmarshal(raw, &saved)
					checkpoints = append(checkpoints, saved)
					return nil
				})
				op, e := l.Change(ctx, "1", "password-reset", c, "same-request")
				if e == nil {
					t.Fatal("unverified stage accepted")
				}
				expected := map[string]int{"shutdown": 1, "password": 2, "boot": 3}[step]
				if len(f.base.writes) != expected {
					t.Fatalf("continued/repeated write after uncertainty: %v", f.base.writes)
				}
				if len(checkpoints) == 0 || op.Linode.Stage == "已完成" {
					t.Fatal("lost last step")
				}
				// Rehydrate exactly the public durable checkpoint, simulating restart.
				recovered := checkpoints[len(checkpoints)-1]
				for i := 0; i < 2; i++ {
					_, _ = l.Poll(context.Background(), recovered)
				}
				if len(f.base.writes) != expected {
					t.Fatal("recovery polling replayed a cloud write")
				}
				if step == "shutdown" && mode == "reject" {
					var ce *Error
					if !errors.As(e, &ce) || ce.Status != 409 || !strings.Contains(e.Error(), "cloud fixture rejects concurrent action") {
						t.Fatalf("lost provider error: %v", e)
					}
				}
			})
		}
	}
}

func TestPasswordResetWrongDiskEventAndRecorderFailureNeverContinue(t *testing.T) {
	t.Run("wrong-disk", func(t *testing.T) {
		l, f, c := newPasswordFlow(t, "running")
		f.eventMode["password"] = "wrong-disk"
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		op, e := l.Change(ctx, "1", "password-reset", c, "fixed")
		if e == nil || len(f.base.writes) != 2 || op.Linode.PasswordResetConfirmed {
			t.Fatalf("wrong disk completion permitted boot: %v %+v", e, f.base.writes)
		}
	})
	for _, failStage := range []string{"正在关机", "正在重置密码", "正在开机"} {
		t.Run(failStage, func(t *testing.T) {
			l, f, c := newPasswordFlow(t, "running")
			ctx := WithOperationRecorder(context.Background(), func(op Operation) error {
				if op.Linode.Stage == failStage {
					return errors.New("fixture db error")
				}
				return nil
			})
			_, e := l.Change(ctx, "1", "password-reset", c, "fixed")
			want := map[string]int{"正在关机": 0, "正在重置密码": 1, "正在开机": 2}[failStage]
			if e == nil || len(f.base.writes) != want {
				t.Fatalf("write sent without durable stage: %v %v", e, f.base.writes)
			}
		})
	}
}

func TestPasswordResetConcurrentCloudEventsNeverBecomeLocalLock(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(fmt.Sprint(rejected), func(t *testing.T) {
			l, f, c := newPasswordFlow(t, "running")
			f.base.before = []map[string]any{linodeFixtureEvent(77, "linode_migrate", "in_progress", 1, 0)}
			if rejected {
				f.failWrite = "shutdown"
			}
			if e := l.ValidateChange(context.Background(), "1", "password-reset", c); e != nil {
				t.Fatal("local guard suppressed provider decision", e)
			}
			_, e := l.Change(context.Background(), "1", "password-reset", c, "cloud-decides")
			if rejected {
				var ce *Error
				if !errors.As(e, &ce) || ce.Status != 409 || len(f.base.writes) != 1 {
					t.Fatalf("expected real provider rejection: %v %+v", e, f.base.writes)
				}
			} else if e != nil || len(f.base.writes) != 3 {
				t.Fatalf("provider-accepted flow was suppressed: %v %+v", e, f.base.writes)
			}
		})
	}
}

func TestPasswordResetTransitionalStateDefersToDiskAPI(t *testing.T) {
	l, f, c := newPasswordFlow(t, "booting")
	f.failWrite = "password"
	if e := l.ValidateChange(context.Background(), "1", "password-reset", c); e != nil {
		t.Fatal("transient state was used as a local mutex", e)
	}
	_, e := l.Change(context.Background(), "1", "password-reset", c, "transitional")
	var ce *Error
	if !errors.As(e, &ce) || ce.Status != 409 || len(f.base.writes) != 1 || !strings.HasSuffix(f.base.writes[0].Path, "/disks/11/password") {
		t.Fatalf("cloud did not decide transient operation: %v %+v", e, f.base.writes)
	}
}

func TestPasswordResetFreshIdentityRequiredBeforeAnyCloudWrite(t *testing.T) {
	for _, change := range []string{"instance-id", "disk-id", "disk-filesystem", "disk-allocation", "confirmation-snapshot"} {
		t.Run(change, func(t *testing.T) {
			l, f, c := newPasswordFlow(t, "running")
			switch change {
			case "instance-id":
				f.base.instances["1"]["id"] = 2
			case "disk-id":
				f.base.disks["1"][0]["id"] = 99
			case "disk-filesystem":
				f.base.disks["1"][0]["filesystem"] = "raw"
			case "disk-allocation":
				f.base.disks["1"][0]["size"] = 19000
			case "confirmation-snapshot":
				c.Snapshot = "different-confirmed-resource"
			}
			_, e := l.Change(context.Background(), "1", "password-reset", c, "fixed")
			if e == nil || len(f.base.writes) != 0 {
				t.Fatalf("stale target mutated cloud: %v %+v", e, f.base.writes)
			}
		})
	}
}

func TestPasswordResetRecoveryCanVerifyBootButNeverResumeMissingBoot(t *testing.T) {
	l, f, c := newPasswordFlow(t, "running")
	f.loseWrite = "boot"
	var last Operation
	ctx := WithOperationRecorder(context.Background(), func(op Operation) error { raw, _ := json.Marshal(op); _ = json.Unmarshal(raw, &last); return nil })
	_, e := l.Change(ctx, "1", "password-reset", c, "fixed")
	if e == nil || !Uncertain(e) || len(f.base.writes) != 3 {
		t.Fatalf("lost boot reply was not uncertain: %v %+v", e, f.base.writes)
	}
	// A later official event/read can prove an already-sent boot completed.
	f.base.instances["1"]["status"] = "running"
	f.base.after = append(f.base.after, linodeFixtureEvent(103, "linode_boot", "finished", 1, 0))
	done, e := l.Poll(context.Background(), last)
	if e != nil || !done || len(f.base.writes) != 3 {
		t.Fatalf("read-only completion not recognized: %v %v %+v", done, e, f.base.writes)
	}
	// The same cloud state cannot authorize an unrecorded boot write, and it
	// cannot claim the whole automatic workflow completed without attribution.
	last.Linode.BootSent = false
	done, e = l.Poll(context.Background(), last)
	if e == nil || done || len(f.base.writes) != 3 {
		t.Fatalf("read-only check resumed/claimed missing boot: %v %v", done, e)
	}
}
