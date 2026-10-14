package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

func (a *App) linodeRead(w http.ResponseWriter, r *http.Request) {
	select {
	case a.advancedSlots <- struct{}{}:
		defer func() { <-a.advancedSlots }()
	default:
		fail(w, 429, "高级查询正在进行，请稍后重试")
		return
	}
	acc, p, e := a.provider(r.URL.Query().Get("accountId"))
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if acc.Provider != "linode" {
		fail(w, 400, "本轮高级功能仅支持Linode")
		return
	}
	advanced, ok := p.(cloud.LinodeAdvanced)
	if !ok {
		fail(w, 400, "该Linode适配器暂不支持高级管理")
		return
	}
	page := 1
	if r.URL.Query().Get("page") != "" {
		page, e = strconv.Atoi(r.URL.Query().Get("page"))
		if e != nil {
			fail(w, 400, "分页参数无效")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	period := r.URL.Query().Get("period")
	if r.URL.Query().Get("view") == "instance-firewalls" {
		period = r.URL.Query().Get("interfaceId")
	}
	data, e := advanced.LinodeRead(ctx, r.URL.Query().Get("view"), r.URL.Query().Get("target"), period, page)
	if e != nil {
		cloudFail(w, e)
		return
	}
	var revision int
	if e = a.S.DB.QueryRow("SELECT revision FROM accounts WHERE id=? AND retired=0", acc.ID).Scan(&revision); e != nil || revision != acc.Revision {
		fail(w, 409, "账号凭据已变化，请刷新后重新查询")
		return
	}
	reply(w, 200, map[string]any{"accountId": acc.ID, "accountRevision": acc.Revision, "data": data})
}
func (a *App) validateLinode(ctx context.Context, b mutation, p cloud.Provider) error {
	advanced, ok := p.(cloud.LinodeAdvanced)
	if !ok || b.Advanced == nil {
		return errors.New("高级操作参数无效")
	}
	c := *b.Advanced
	if strings.HasPrefix(b.Action, "firewall-") && b.Action != "firewall-bind" {
		if b.Region != "" {
			return errors.New("账号防火墙不使用实例地区")
		}
		if b.Action == "firewall-create" {
			if b.Name != c.Label || b.Target != "" {
				return errors.New("防火墙创建名称不一致")
			}
		} else {
			raw, e := advanced.LinodeRead(ctx, "firewall", b.Target, "", 1)
			if e != nil {
				return e
			}
			f, ok := raw.(cloud.LinodeFirewall)
			if !ok || fmt.Sprint(f.ID) != b.Target || f.Label != b.Name {
				return errors.New("防火墙身份已变化")
			}
		}
	} else {
		raw, e := advanced.LinodeRead(ctx, "instance", b.Target, "", 1)
		if e != nil {
			return e
		}
		s, ok := raw.(cloud.LinodeState)
		if !ok || s.Instance.ID != b.Target || s.Instance.Name != b.Name || s.Instance.Region != b.Region {
			return errors.New("服务器账号、地区或资源身份已变化，请重新确认")
		}
	}
	if c.SSHPublicKey != "" && !validSSH(c.SSHPublicKey) {
		return errors.New("SSH公钥格式无效")
	}
	return advanced.ValidateChange(ctx, b.Target, b.Action, c)
}
