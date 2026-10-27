package app

import (
	"context"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

type providerSnapshotKey struct{}
type providerSnapshot struct {
	account  account
	provider cloud.Provider
}

// One request keeps the same credential revision, provider and network client.
// This is immutable request data, not an account/instance lock. Later requests
// independently read the latest credentials and egress configuration.
func (a *App) pinProvider(ctx context.Context, id string) (context.Context, account, cloud.Provider, error) {
	acc, p, err := a.providerContext(ctx, id)
	if err != nil {
		return ctx, acc, p, err
	}
	return context.WithValue(ctx, providerSnapshotKey{}, providerSnapshot{acc, p}), acc, p, nil
}
