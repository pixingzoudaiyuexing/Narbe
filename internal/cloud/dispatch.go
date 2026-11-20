package cloud

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrDispatchExpired = errors.New("任务等待或校验已过期，未发送云操作")

type dispatchKey struct{}
type writeGuardKey struct{}

// WithWriteGuard adds an optional durable per-request check immediately before
// every cloud mutation. It is used only by the explicitly authorized automated
// workflow, and does not serialize other requests for an account or instance.
func WithWriteGuard(ctx context.Context, check func() error) context.Context {
	return context.WithValue(ctx, writeGuardKey{}, check)
}

type dispatchGuard struct {
	mu       sync.Mutex
	deadline time.Time
	started  bool
}

// The first cloud write must start while the queued intent is fresh. Once it
// starts, multi-step provider operations finish under the operation context;
// expiring a queue deadline must not interrupt already-created Azure dependencies.
func WithDispatchDeadline(ctx context.Context, deadline time.Time) context.Context {
	now := time.Now()
	return context.WithValue(ctx, dispatchKey{}, &dispatchGuard{deadline: now.Add(deadline.Sub(now))})
}
func beforeCloudWrite(ctx context.Context) error {
	if check, ok := ctx.Value(writeGuardKey{}).(func() error); ok {
		if err := check(); err != nil {
			return err
		}
	}
	guard, ok := ctx.Value(dispatchKey{}).(*dispatchGuard)
	if !ok {
		return nil
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.started {
		return nil
	}
	if ctx.Err() != nil || !time.Now().Before(guard.deadline) {
		return ErrDispatchExpired
	}
	guard.started = true
	return nil
}
