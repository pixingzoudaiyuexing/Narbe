package app

import (
	"context"
	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"net/http"
	"strconv"
	"time"
)

func (a *App) stackScripts(w http.ResponseWriter, r *http.Request) {
	select {
	case a.advancedSlots <- struct{}{}:
		defer func() { <-a.advancedSlots }()
	default:
		fail(w, 429, "脚本查询正在进行，请稍后重试")
		return
	}
	acc, p, e := a.provider(r.URL.Query().Get("accountId"))
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	ss, ok := p.(cloud.StackScriptProvider)
	if acc.Provider != "linode" || !ok {
		fail(w, 400, "StackScripts仅用于Linode")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var data any
	if id := r.URL.Query().Get("id"); id != "" {
		n, err := strconv.Atoi(id)
		if err != nil || n < 1 {
			fail(w, 400, "StackScript ID无效")
			return
		}
		data, e = ss.StackScript(ctx, n)
	} else {
		page := 1
		if value := r.URL.Query().Get("page"); value != "" {
			page, e = strconv.Atoi(value)
			if e != nil {
				fail(w, 400, "分页无效")
				return
			}
		}
		data, e = ss.StackScripts(ctx, r.URL.Query().Get("search"), r.URL.Query().Get("scope"), page)
	}
	if e != nil {
		cloudFail(w, e)
		return
	}
	var revision int
	if a.S.DB.QueryRow("SELECT revision FROM accounts WHERE id=? AND retired=0", acc.ID).Scan(&revision) != nil || revision != acc.Revision {
		fail(w, 409, "云账号凭据已变化，请重开创建表单")
		return
	}
	reply(w, 200, map[string]any{"accountId": acc.ID, "accountRevision": acc.Revision, "data": data})
}
