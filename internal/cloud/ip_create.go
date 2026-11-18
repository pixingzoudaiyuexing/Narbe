package cloud

import "context"

// IncrementalCreateProvider splits a screening VM creation into bounded ticks.
// Each Advance dispatches at most one cloud write; waiting is read-only.
type IncrementalCreateProvider interface {
	BeginScreeningCreate(context.Context, Spec, string) (Operation, error)
	AdvanceScreeningCreate(context.Context, Spec, string, Operation) (Operation, bool, error)
}
