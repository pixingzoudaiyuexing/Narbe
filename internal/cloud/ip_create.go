package cloud

import "context"

// IncrementalCreateProvider splits a screening VM creation into bounded ticks.
// Each Advance dispatches at most one cloud write; waiting is read-only.
type IncrementalCreateProvider interface {
	BeginScreeningCreate(context.Context, Spec, string) (Operation, error)
	AdvanceScreeningCreate(context.Context, Spec, string, Operation) (Operation, bool, error)
}

// ScreeningIPCleaner is optional and only used for a previously ledgered
// automatic-screening address. Manual IP changes never invoke it.
type ScreeningIPCleaner interface {
	CleanupScreeningIP(context.Context, Operation, string, string) (Operation, bool, error)
}
