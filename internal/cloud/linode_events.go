package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

type linodeRecorderKey struct{}

func WithOperationRecorder(ctx context.Context, save func(Operation) error) context.Context {
	return context.WithValue(ctx, linodeRecorderKey{}, save)
}
func recordOperation(ctx context.Context, op Operation) error {
	if save, ok := ctx.Value(linodeRecorderKey{}).(func(Operation) error); ok {
		if save(op) != nil {
			return &Error{Code: "云操作账本保存失败，请人工核实已发送步骤", Uncertain: true}
		}
	}
	return nil
}

var ErrOperationFailed = errors.New("云端事件明确报告操作失败，请查看控制台")

type linEvent struct {
	ID             int
	Action, Status string
	Entity         *struct {
		ID   int
		Type string
	}
	SecondaryEntity *struct {
		ID   json.RawMessage
		Type string
	} `json:"secondary_entity"`
}

func eventAction(action string) string {
	return map[string]string{"rebuild": "linode_rebuild", "resize": "linode_resize", "clone": "linode_clone", "migrate": "linode_migrate_datacenter", "password-reset": "password_reset", "stop": "linode_shutdown", "start": "linode_boot"}[action]
}
func (l *Linode) events(ctx context.Context, id, action string) ([]linEvent, error) {
	n, e := strconv.Atoi(id)
	if e != nil {
		return nil, e
	}
	filters := map[string]any{"entity.id": n, "entity.type": "linode", "+order_by": "id", "+order": "desc"}
	if action != "" {
		filters["action"] = eventAction(action)
	}
	filter, _ := json.Marshal(filters)
	var all []linEvent
	seen := map[int]bool{}
	for page := 1; page <= 20; page++ {
		var res struct {
			Data  []linEvent
			Pages int
		}
		_, e = l.request(ctx, "GET", fmt.Sprintf("/account/events?page_size=500&page=%d", page), nil, &res, string(filter))
		if e != nil {
			return nil, e
		}
		if res.Data == nil || res.Pages < 1 {
			return nil, &Error{Code: "事件列表响应不完整，不能证明操作结果"}
		}
		for _, ev := range res.Data {
			if ev.ID < 1 || seen[ev.ID] {
				return nil, &Error{Code: "事件ID重复或无效，不能确认状态"}
			}
			seen[ev.ID] = true
		}
		all = append(all, res.Data...)
		if len(all) > 10000 {
			return nil, &Error{Code: "事件超出核实上限，未发送操作"}
		}
		if page >= res.Pages {
			return all, nil
		}
	}
	return nil, &Error{Code: "事件分页超过核实上限，未发送操作"}
}
func (l *Linode) eventBaseline(ctx context.Context, id, action string) (int, error) {
	events, e := l.events(ctx, id, "")
	if e != nil {
		return 0, e
	}
	after := 0
	for _, v := range events {
		if v.ID > after {
			after = v.ID
		}
	}
	return after, nil
}
func (l *Linode) pollAdvanced(ctx context.Context, op Operation) (bool, error) {
	check := op.Linode
	if check == nil {
		return false, errors.New("缺少Linode操作核实信息")
	}
	if check.Action == "password-reset" && check.OriginalState != "" {
		return l.pollPasswordReset(ctx, op)
	}
	if check.Action == "create" {
		var i linInstance
		if e := l.call(ctx, "GET", "/linode/instances/"+Escape(op.ResourceID), nil, &i); e != nil {
			return false, e
		}
		if i.Status != op.Expected {
			return false, nil
		}
		return l.verifySelection(ctx, op.ResourceID, 0, check.Firewall)
	}
	if check.Action == "firewall-create" || check.Action == "firewall-rules" || check.Action == "firewall-status" {
		if (check.Action == "firewall-create" || check.Action == "firewall-rules") && check.ExpectedRules == nil {
			return false, errors.New("缺少已确认目标规则，必须人工核实")
		}
		if check.Action == "firewall-status" && check.ExpectedStatus == "" {
			return false, errors.New("缺少目标启用状态，必须人工核实")
		}
		f, e := l.firewall(ctx, op.ResourceID)
		if e != nil {
			return false, e
		}
		// Rules/device/status fingerprint must still be the accepted target configuration.
		if check.ExpectedRules != nil && fingerprint(f.Rules) != fingerprint(*check.ExpectedRules) {
			return false, nil
		}
		if check.ExpectedStatus != "" && f.Status != check.ExpectedStatus {
			return false, nil
		}
		return true, nil
	}
	if check.Action == "firewall-delete" {
		var f LinodeFirewall
		e := l.call(ctx, "GET", "/networking/firewalls/"+Escape(op.ResourceID), nil, &f)
		if ce, ok := e.(*Error); ok && ce.Status == 404 {
			return true, nil
		}
		return false, e
	}
	if check.Action == "firewall-bind" {
		return l.verifySelection(ctx, op.ResourceID, check.InterfaceID, check.Firewall)
	}
	id := op.ResourceID
	if check.Action == "clone" {
		id = check.SourceID
	}
	done, e := l.eventFinished(ctx, id, check.Action, check.EventAfter, func() int {
		if check.Action == "clone" {
			n, _ := strconv.Atoi(op.ResourceID)
			return n
		}
		return check.DiskID
	}())
	if e != nil || !done {
		return false, e
	}
	if check.Action == "password-reset" {
		return true, nil
	}
	var i linInstance
	if e := l.call(ctx, "GET", "/linode/instances/"+Escape(op.ResourceID), nil, &i); e != nil {
		return false, e
	}
	if fmt.Sprint(i.ID) != op.ResourceID {
		return false, &Error{Code: "操作目标身份无法确认"}
	}
	if check.Label != "" && i.Label != check.Label {
		return false, nil
	}
	if check.Plan != "" && i.Type != check.Plan {
		return false, nil
	}
	if check.Image != "" && i.Image != check.Image {
		return false, nil
	}
	if op.Region != "" && i.Region != op.Region {
		return false, nil
	}
	if i.Status != "running" && i.Status != "offline" {
		return false, nil
	}
	if check.Action == "clone" {
		if !check.Configured {
			return false, &Error{Code: "克隆后网络步骤尚未确认，请在控制台人工核实", Uncertain: true}
		}
		return l.verifyInstanceSelection(ctx, op.ResourceID, check.Firewall)
	}
	return true, nil
}

func (l *Linode) eventFinished(ctx context.Context, id, action string, after, diskID int) (bool, error) {
	events, e := l.events(ctx, id, action)
	if e != nil {
		return false, e
	}
	var matching []linEvent
	for _, ev := range events {
		if ev.ID <= after || ev.Action != eventAction(action) {
			continue
		}
		if ev.Entity == nil || ev.Entity.Type != "linode" || fmt.Sprint(ev.Entity.ID) != id {
			continue
		}
		if diskID > 0 {
			if ev.SecondaryEntity == nil {
				continue
			}
			wantType := "disk"
			if action == "clone" {
				wantType = "linode"
			}
			var target int
			if ev.SecondaryEntity.Type != wantType || json.Unmarshal(ev.SecondaryEntity.ID, &target) != nil || target != diskID {
				continue
			}
		}
		matching = append(matching, ev)
	}
	if len(matching) > 1 {
		return false, &Error{Code: "出现多个同类云事件，无法唯一确认本任务结果", Uncertain: true}
	}
	if len(matching) == 0 {
		return false, nil
	}
	ev := matching[0]
	if ev.Status == "failed" {
		return false, ErrOperationFailed
	}
	return ev.Status == "finished", nil
}

// pollPasswordReset reconciles already dispatched steps only. In particular a
// confirmed shutdown/reset never causes Poll to issue reset/boot after restart.
func (l *Linode) pollPasswordReset(ctx context.Context, op Operation) (bool, error) {
	c := op.Linode
	if !c.PasswordResetSent {
		return false, &Error{Code: c.Stage + "；密码重置尚未派发，请人工核实；只读核实不会继续云写", Uncertain: true}
	}
	done, e := l.eventFinished(ctx, op.ResourceID, "password-reset", c.ResetEventAfter, c.DiskID)
	if e != nil || !done {
		return false, e
	}
	s, e := l.passwordTarget(ctx, op)
	if e != nil {
		return false, e
	}
	if c.OriginalState == "running" {
		if !c.BootSent {
			return false, &Error{Code: "密码重置事件已完成，但自动开机未派发；请人工核实运行状态，只读核实不会开机", Uncertain: true}
		}
		boot, e := l.eventFinished(ctx, op.ResourceID, "start", c.BootEventAfter, 0)
		return boot && s.Instance.State == "running", e
	}
	if c.OriginalState == "offline" {
		return s.Instance.State == "offline", nil
	}
	return true, nil
}
