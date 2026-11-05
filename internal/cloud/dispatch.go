package cloud

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrDispatchExpired = errors.New("任务等待或校验已过期，未发送云操作")

type dispatchKey struct{}
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
