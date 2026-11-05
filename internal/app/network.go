package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

const networkSetting = "cloud_egress"
const ipCheckURL = "https://api64.ipify.org?format=json"

type networkState struct {
	Revision int          `json:"revision"`
	Config   cloud.Egress `json:"config"`
	client   *http.Client
}
type networkContext struct{}
type networkRuntime struct {
	sync.Mutex
	state        *networkState
	diagnosticAt time.Time
	slots        chan struct{}
}

func (a *App) networkLocked() (*networkState, error) {
	if a.network.state != nil {
		return a.network.state, nil
	}
	s := &networkState{Config: cloud.Egress{Mode: "direct"}}
	var sealed string
	e := a.S.DB.QueryRow("SELECT value FROM settings WHERE key=?", networkSetting).Scan(&sealed)
	if e != nil && e != sql.ErrNoRows {
		return nil, errors.New("网络配置读取失败")
	}
	if e == nil {
		raw, err := base64.RawStdEncoding.DecodeString(sealed)
		if err != nil {
			return nil, errors.New("网络配置损坏")
		}
		raw, err = a.S.Unseal(raw, "settings:"+networkSetting)
		if err != nil || json.Unmarshal(raw, s) != nil || s.Revision < 1 {
			return nil, errors.New("网络配置无法解密，请恢复正确密钥")
		}
	}
	h, err := cloud.NewEgressClient(s.Config, os.Getenv("NARBE_PROXY_ALLOW_PRIVATE"))
	if err != nil {
		return nil, err
	}
	s.client = h
	a.network.state = s
	return s, nil
}
func (a *App) networkSnapshot() (*networkState, error) {
	a.network.Lock()
	defer a.network.Unlock()
	return a.networkLocked()
}
func (a *App) factory(ctx context.Context, name string, c cloud.Credentials) (cloud.Provider, error) {
	if a.Factory != nil {
		return a.Factory(name, c)
	}
	s, _ := ctx.Value(networkContext{}).(*networkState)
	if s == nil {
		var err error
		s, err = a.networkSnapshot()
		if err != nil {
			return nil, err
		}
	}
	return cloud.NewWithClient(name, c, s.client)
}
func networkView(s *networkState) any {
	return map[string]any{"revision": s.Revision, "mode": s.Config.Mode, "protocol": s.Config.Protocol, "host": s.Config.Host, "port": s.Config.Port, "username": s.Config.Username, "passwordSet": s.Config.Password != ""}
}

type networkUpdate struct {
	Revision       int    `json:"revision"`
	Mode           string `json:"mode"`
	Protocol       string `json:"protocol"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	PasswordAction string `json:"passwordAction"`
}

func (b networkUpdate) config(old *networkState) (cloud.Egress, error) {
	c := cloud.Egress{Mode: b.Mode, Protocol: b.Protocol, Host: b.Host, Port: b.Port, Username: b.Username}
	switch b.PasswordAction {
	case "keep":
		if b.Password != "" {
			return c, errors.New("保留密码不能附带新密码")
		}
		if b.Mode == "proxy" {
			if old.Config.Mode != "proxy" || c.Protocol != old.Config.Protocol || c.Host != old.Config.Host || c.Port != old.Config.Port || c.Username != old.Config.Username {
				if old.Config.Password != "" {
					return c, errors.New("代理身份变化时必须重新输入或明确清除密码")
				}
			} else {
				c.Password = old.Config.Password
			}
		}
	case "clear":
		if b.Password != "" {
			return c, errors.New("清除密码不能附带新密码")
		}
	case "replace":
		c.Password = b.Password
	default:
		return c, errors.New("请选择保留、替换或清除代理密码")
	}
	return c, c.Validate()
}
func (a *App) networkAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		s, e := a.networkSnapshot()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		reply(w, 200, networkView(s))
		return
	}
	if r.Method != "PUT" {
		fail(w, 405, "不支持的请求")
		return
	}
	var b networkUpdate
	if read(r, &b) != nil {
		fail(w, 400, "网络配置格式无效")
		return
	}
	a.network.Lock()
	defer a.network.Unlock()
	s, e := a.networkLocked()
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	if b.Revision != s.Revision {
		fail(w, 409, "网络配置已变化，请重新读取")
		return
	}
	c, e := b.config(s)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	h, e := cloud.NewEgressClient(c, os.Getenv("NARBE_PROXY_ALLOW_PRIVATE"))
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	next := &networkState{Revision: s.Revision + 1, Config: c, client: h}
	raw, _ := json.Marshal(next)
	sealed, e := a.S.Seal(raw, "settings:"+networkSetting)
	if e != nil {
		fail(w, 500, "网络配置加密失败")
		return
	}
	tx, e := a.S.DB.Begin()
	if e != nil {
		fail(w, 500, "网络配置保存失败")
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", networkSetting, base64.RawStdEncoding.EncodeToString(sealed)); e == nil {
		_, e = tx.Exec("DELETE FROM confirmations")
	}
	if e == nil {
		_, e = tx.Exec("UPDATE jobs SET state='cancelled',progress='网络出口已变化，未派发任务已取消；请重新确认',updated=? WHERE state='queued'", store.Now())
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.CloseIdleConnections()
		fail(w, 500, "网络配置保存失败")
		return
	}
	a.network.state = next
	s.client.CloseIdleConnections()
	// In-flight reads retain their immutable client. Their cache is discarded.
	a.inventoryMu.Lock()
	for k, entry := range a.inventories {
		entry.cancel()
		delete(a.inventories, k)
	}
	a.inventoryMu.Unlock()
	reply(w, 200, networkView(next))
}

// Diagnostic admissions have a process-wide bounded concurrency and interval.
func (a *App) networkTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "需要POST请求")
		return
	}
	var b struct {
		Kind      string         `json:"kind"`
		AccountID string         `json:"accountId"`
		Config    *networkUpdate `json:"config,omitempty"`
	}
	if read(r, &b) != nil {
		fail(w, 400, "检测参数无效")
		return
	}
	a.network.Lock()
	if a.network.slots == nil {
		a.network.slots = make(chan struct{}, 2)
	}
	slots := a.network.slots
	if time.Since(a.network.diagnosticAt) < time.Second {
		a.network.Unlock()
		fail(w, 429, "检测过于频繁，请稍后重试")
		return
	}
	a.network.diagnosticAt = time.Now()
	a.network.Unlock()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		fail(w, 429, "网络检测繁忙")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	s, e := a.networkSnapshot()
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	h := s.client
	mode := s.Config.Mode
	if b.Kind == "direct" {
		h, e = cloud.NewEgressClient(cloud.Egress{Mode: "direct"}, "")
		mode = "direct"
		if e != nil {
			fail(w, 400, "直连检测初始化失败")
			return
		}
		defer h.CloseIdleConnections()
	} else if b.Kind == "proxy" {
		if b.Config != nil {
			if b.Config.Revision != s.Revision {
				fail(w, 409, "网络配置已变化")
				return
			}
			c, err := b.Config.config(s)
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
			if c.Mode != "proxy" {
				fail(w, 400, "代理检测需要代理模式")
				return
			}
			h, e = cloud.NewEgressClient(c, os.Getenv("NARBE_PROXY_ALLOW_PRIVATE"))
			if e != nil {
				fail(w, 400, e.Error())
				return
			}
			defer h.CloseIdleConnections()
			mode = "proxy"
		}
		if mode != "proxy" {
			fail(w, 400, "请配置代理后检测")
			return
		}
	} else if b.Kind != "cloud" {
		fail(w, 400, "检测类型无效")
		return
	}
	if e != nil {
		fail(w, 400, "检测客户端无效")
		return
	}
	start := time.Now()
	out := map[string]any{"kind": b.Kind, "mode": mode, "revision": s.Revision, "checkedAt": start.Unix(), "status": "failed", "scope": "仅证明本次检测请求，不保证所有云目标使用相同公网NAT"}
	if b.Kind == "cloud" {
		acc, p, err := a.providerContext(context.WithValue(ctx, networkContext{}, s), b.AccountID)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		e = checkProvider(ctx, p, acc.Provider)
		if e == nil {
			out["status"] = "authorized"
			out["message"] = "官方只读API连接及授权查询通过"
		} else {
			var ce *cloud.Error
			switch {
			case errors.As(e, &ce) && ce.Status == 401:
				out["message"] = "云API认证失败"
			case errors.As(e, &ce) && ce.Status == 403:
				out["message"] = "云账号权限不足"
			default:
				out["message"] = e.Error()
			}
		}
	} else {
		req, _ := http.NewRequestWithContext(ctx, "GET", ipCheckURL, nil)
		res, err := h.Do(req)
		if err != nil {
			out["message"] = cloud.NetworkError(err)
		} else {
			defer res.Body.Close()
			raw, err := io.ReadAll(io.LimitReader(res.Body, 1025))
			var ip struct {
				IP string `json:"ip"`
			}
			addr, pe := netip.ParseAddr("")
			if err == nil && len(raw) <= 1024 {
				err = json.Unmarshal(raw, &ip)
				addr, pe = netip.ParseAddr(ip.IP)
			}
			if res.StatusCode == 200 && err == nil && pe == nil && addr.IsGlobalUnicast() && !addr.IsPrivate() {
				out["status"] = "success"
				out["ip"] = addr.String()
				if mode == "proxy" {
					out["proxyAuthentication"] = "本次请求已通过代理（如配置认证）"
				}
			} else if res.StatusCode == 407 {
				out["message"] = "代理认证失败"
			} else {
				out["message"] = "IP检测服务响应无效（HTTP " + strconv.Itoa(res.StatusCode) + "）"
			}
		}
	}
	out["latencyMs"] = time.Since(start).Milliseconds()
	reply(w, 200, out)
}
