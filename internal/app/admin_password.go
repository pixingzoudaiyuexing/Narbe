package app

import "github.com/pixingzoudaiyuexing/Narbe/internal/store"

// Reuse the existing Argon2 concurrency budget for privileged confirmations.
func (a *App) checkAdminPassword(password string) bool {
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
		return store.CheckPassword(password, a.S.Setting("password"))
	default:
		return false
	}
}
