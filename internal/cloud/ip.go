package cloud

import (
	"context"
	"errors"
	"time"
)

// IPProvider handles an explicitly authorized existing instance without deleting
// that instance. Each advance performs bounded reads and at most one cloud write.
// ReconcileIPChange is strictly read-only, including after a lost write response.
type IPProvider interface {
	IPInfo(context.Context, Instance) (IPInfo, error)
	BeginIPChange(context.Context, Instance, IPChange, string) (Operation, error)
	AdvanceIPChange(context.Context, Operation) (Operation, bool, error)
	ReconcileIPChange(context.Context, Operation) (Operation, bool, error)
}
type IPAddress struct {
	Address     string `json:"address"`
	Kind        string `json:"kind"`
	ResourceID  string `json:"resourceId,omitempty"`
	InterfaceID string `json:"interfaceId,omitempty"`
	InstanceID  string `json:"instanceId,omitempty"`
	Region      string `json:"region,omitempty"`
	Primary     bool   `json:"primary"`
}
type IPMethod struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Available    bool   `json:"available"`
	Reason       string `json:"reason,omitempty"`
	RequiresStop bool   `json:"requiresStop"`
	ExtraCharge  bool   `json:"extraCharge"`
	Description  string `json:"description"`
}
type IPInfo struct {
	InstanceID        string      `json:"instanceId"`
	Region            string      `json:"region"`
	State             string      `json:"state"`
	PrimaryIPv4       string      `json:"primaryIPv4"`
	Addresses         []IPAddress `json:"addresses"`
	AvailableIPs      []IPAddress `json:"availableIPs"`
	ServiceIPv4       []string    `json:"serviceIPv4"`
	Methods           []IPMethod  `json:"methods"`
	Snapshot          string      `json:"snapshot"`
	NetworkGeneration string      `json:"networkGeneration,omitempty"`
	Warnings          []string    `json:"warnings"`
}
type IPChange struct {
	Method           string `json:"method"`
	Snapshot         string `json:"snapshot"`
	Address          string `json:"address,omitempty"`
	AllocationID     string `json:"allocationId,omitempty"`
	InterfaceID      string `json:"interfaceId,omitempty"`
	SourceInstanceID string `json:"sourceInstanceId,omitempty"`
	RiskAccepted     bool   `json:"riskAccepted"`
}
type IPOperation struct {
	Method                  string            `json:"method"`
	Step                    string            `json:"step"`
	Stage                   string            `json:"stage"`
	RequestID               string            `json:"requestId"`
	TargetID                string            `json:"targetId"`
	Region                  string            `json:"region"`
	Snapshot                string            `json:"snapshot"`
	OriginalState           string            `json:"originalState"`
	Before                  IPInfo            `json:"before"`
	After                   *IPInfo           `json:"after,omitempty"`
	NewIP                   string            `json:"newIP,omitempty"`
	OldIP                   string            `json:"oldIP,omitempty"`
	NewResourceID           string            `json:"newResourceId,omitempty"`
	OldResourceID           string            `json:"oldResourceId,omitempty"`
	InterfaceID             string            `json:"interfaceId,omitempty"`
	SourceInstanceID        string            `json:"sourceInstanceId,omitempty"`
	Selection               IPChange          `json:"selection"`
	Writes                  []ScreeningWrite  `json:"writes"`
	Resources               []RelatedResource `json:"resources"`
	GuestNetworkingRequired bool              `json:"guestNetworkingRequired"`
	PrimaryChanged          bool              `json:"primaryChanged"`
	AddressRepeated         bool              `json:"addressRepeated"`
	Failure                 string            `json:"failure,omitempty"`
	Notice                  string            `json:"notice,omitempty"`
	// Provider checkpoint contains only bounded identifiers/ETags. Never store
	// credentials, scripts, passwords or raw provider bodies here.
	Checkpoint map[string]string `json:"checkpoint,omitempty"`
}

func NewIPOperation(i Instance, request IPChange, info IPInfo, token string) (Operation, error) {
	if token == "" || i.ID == "" || i.Region == "" || info.InstanceID != i.ID || info.Region != i.Region || info.Snapshot == "" || request.Snapshot != info.Snapshot || !request.RiskAccepted {
		return Operation{}, &Error{Code: "实例身份、IP配置或一次性风险确认不一致，未发送云写"}
	}
	methodOK := false
	for _, m := range info.Methods {
		if m.ID == request.Method && m.Available {
			methodOK = true
		}
	}
	if !methodOK {
		return Operation{}, &Error{Code: "当前实例不支持所选原机换 IP 方式，请查看原因或创建独立替代实例"}
	}
	anchor := info.PrimaryIPv4
	if anchor == "" && len(info.Addresses) > 0 {
		anchor = info.Addresses[0].Address
	}
	return Operation{ResourceID: i.ID, Region: i.Region, Expected: "ip-change", IP: &IPOperation{Method: request.Method, Step: "validated", Stage: "正在核对公网 IP 配置", RequestID: token, TargetID: i.ID, Region: i.Region, Snapshot: info.Snapshot, OriginalState: info.State, Before: info, OldIP: anchor, Selection: request, Writes: []ScreeningWrite{}, Resources: []RelatedResource{}, Checkpoint: map[string]string{}}}, nil
}
func ipCheckpoint(ctx context.Context, op *Operation, step, stage, action, resource string) error {
	if op.IP == nil {
		return errors.New("公网 IP 操作账本缺失")
	}
	if _, ok := ctx.Value(linodeRecorderKey{}).(func(Operation) error); !ok {
		return &Error{Code: "必须先保存公网 IP 操作账本，未发送云写"}
	}
	op.IP.Step, op.IP.Stage = step, stage
	if action != "" {
		op.IP.Writes = append(op.IP.Writes, ScreeningWrite{Action: action, ResourceID: resource, State: "dispatching", At: time.Now().Unix()})
	}
	op.Owned = []string{}
	for _, r := range op.IP.Resources {
		if r.State != "existing-retained" {
			op.Owned = append(op.Owned, r.ID)
		}
	}
	return recordOperation(ctx, *op)
}
func ipConfirmed(ctx context.Context, op *Operation, step, stage string) error {
	if op.IP == nil {
		return errors.New("公网 IP 操作账本缺失")
	}
	if n := len(op.IP.Writes); n > 0 {
		op.IP.Writes[n-1].State = "confirmed"
	}
	return ipCheckpoint(ctx, op, step, stage, "", "")
}
