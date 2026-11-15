package cloud

// IP-screening is an optional provider capability. Its writes are deliberately
// separate from Action: they require this-attempt ownership and a durable
// checkpoint before every dispatch, and never delete arbitrary existing VMs.
import (
	"context"
	"errors"
	"net/netip"
	"regexp"
	"time"
)

type ScreeningProvider interface {
	InspectScreening(context.Context, Spec, string, Operation) (ScreeningResource, error)
	ReplaceScreeningIP(context.Context, Spec, string, Operation, string) (Operation, error)
}

type ScreeningResource struct {
	Instance    Instance          `json:"instance"`
	Fingerprint string            `json:"fingerprint"`
	Method      string            `json:"method"`
	Resources   []RelatedResource `json:"resources"`
}

// No credentials, login passwords, user-data or raw API bodies enter this ledger.
type ScreeningOperation struct {
	Method        string           `json:"method"`
	Stage         string           `json:"stage"`
	Fingerprint   string           `json:"fingerprint"`
	OldPublicIPID string           `json:"oldPublicIpId,omitempty"`
	NewPublicIPID string           `json:"newPublicIpId,omitempty"`
	Writes        []ScreeningWrite `json:"writes"`
}
type ScreeningWrite struct {
	Action     string `json:"action"`
	ResourceID string `json:"resourceId"`
	State      string `json:"state"`
	At         int64  `json:"at"`
}

var screeningToken = regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`)

func screeningIntent(s Spec, token string, op Operation) error {
	if !screeningToken.MatchString(token) || op.ResourceID == "" || s.Region == "" || op.Region != s.Region || s.Plan == "" || s.Image == "" {
		return &Error{Code: "自动筛选缺少可信创建身份或地区配置"}
	}
	return nil
}
func screeningStart(op Operation, r ScreeningResource, expected string) (Operation, error) {
	if expected == "" || r.Fingerprint != expected {
		return op, &Error{Code: "实例配置已变化，已暂停自动换 IP，请核实当前资源"}
	}
	if op.Screening != nil && op.Screening.Stage != "completed" {
		return op, &Error{Code: "先前换 IP 步骤已派发，禁止重复执行；请只读核实", Uncertain: true}
	}
	op.Screening = &ScreeningOperation{Method: r.Method, Stage: "validated", Fingerprint: r.Fingerprint, Writes: []ScreeningWrite{}}
	op.Ledger = &ResourceLedger{Status: "verified", Resources: append([]RelatedResource{}, r.Resources...)}
	return op, nil
}
func screeningCheckpoint(ctx context.Context, op *Operation, stage, action, target string) error {
	if op.Screening == nil {
		return errors.New("缺少自动换 IP 操作账本")
	}
	if _, ok := ctx.Value(linodeRecorderKey{}).(func(Operation) error); !ok {
		return &Error{Code: "自动换 IP 必须先配置持久化操作账本，未发送云请求"}
	}
	op.Screening.Stage = stage
	if action != "" {
		op.Screening.Writes = append(op.Screening.Writes, ScreeningWrite{Action: action, ResourceID: target, State: "dispatching", At: time.Now().Unix()})
	}
	return recordOperation(ctx, *op)
}
func screeningConfirmed(ctx context.Context, op *Operation, stage string) error {
	if n := len(op.Screening.Writes); n > 0 {
		op.Screening.Writes[n-1].State = "confirmed"
	}
	return screeningCheckpoint(ctx, op, stage, "", "")
}
func screeningUncertain(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrDispatchExpired) || Uncertain(err) {
		return err
	}
	// Once a write is accepted, inability to observe completion is unknown,
	// even if the particular failing read was a plain GET.
	return &Error{Code: "换 IP 云写已派发但后续状态无法确认，请人工核实；不会自动重放", Uncertain: true}
}
func screeningWait(ctx context.Context, read func() (bool, error)) error {
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	for {
		done, err := read()
		if err != nil || done {
			return err
		}
		t := time.NewTimer(2 * time.Second)
		select {
		case <-deadline.C:
			t.Stop()
			return &Error{Code: "换 IP 状态等待超过五分钟，请只读核实", Uncertain: true}
		case <-ctx.Done():
			t.Stop()
			return &Error{Code: "换 IP 状态等待中断，请只读核实", Uncertain: true}
		case <-t.C:
		}
	}
}
func screeningAbsent(err error) bool {
	var ce *Error
	return errors.As(err, &ce) && ce.Status == 404
}
func screeningIPv4(s string) bool {
	ip, err := netip.ParseAddr(s)
	return err == nil && ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip) && !netip.MustParsePrefix("198.18.0.0/15").Contains(ip)
}
