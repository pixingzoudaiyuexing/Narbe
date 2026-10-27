package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"

	"strings"
	"sync"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/lifecycle"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
	"github.com/pixingzoudaiyuexing/Narbe/internal/web"
	"golang.org/x/crypto/ssh"
)

type Factory func(string, cloud.Credentials) (cloud.Provider, error)
type App struct {
	network        networkRuntime
	S              *store.Store
	Origin         string
	Factory        Factory
	mu             sync.Mutex
	attempts       map[string][]time.Time
	loginSlots     chan struct{}
	wake           chan struct{}
	Lifecycle      *lifecycle.Service
	inventoryMu    sync.Mutex
	inventories    map[string]*inventoryEntry
	inventorySlots chan struct{}
	advancedSlots  chan struct{}
}
type account struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Created  int64  `json:"created"`
	Revision int    `json:"revision"`
}
type mutation struct {
	NetworkRevision int                 `json:"networkRevision,omitempty"`
	Advanced        *cloud.LinodeChange `json:"advanced,omitempty"`
	ID              string              `json:"id"`
	AccountID       string              `json:"accountId"`
	Action          string              `json:"action"`
	Region          string              `json:"region"`
	Target          string              `json:"target"`
	Name            string              `json:"name"`
	Spec            cloud.Spec          `json:"spec"`
	Confirmation    string              `json:"confirmation"`
	ConfirmName     string              `json:"confirmName"`
	AccountRevision int                 `json:"accountRevision"`
}
type job struct {
	ID        string           `json:"id"`
	AccountID string           `json:"accountId"`
	Action    string           `json:"action"`
	Region    string           `json:"region"`
	Target    string           `json:"target"`
	Name      string           `json:"name"`
	State     string           `json:"state"`
	Progress  string           `json:"progress"`
	Result    *cloud.Operation `json:"result,omitempty"`
	Error     string           `json:"error,omitempty"`
	Created   int64            `json:"created"`
	Updated   int64            `json:"updated"`
	Provider  string           `json:"provider"`
}

func New(s *store.Store, origin string) *App {
	return &App{S: s, Origin: strings.TrimRight(origin, "/"), Lifecycle: lifecycle.New(), attempts: map[string][]time.Time{}, loginSlots: make(chan struct{}, 2), wake: make(chan struct{}, 4), inventories: map[string]*inventoryEntry{}, inventorySlots: make(chan struct{}, 2), advancedSlots: make(chan struct{}, 2)}
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if e := a.S.DB.PingContext(r.Context()); e != nil {
			reply(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		reply(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/login", a.login)
	mux.Handle("/api/", a.auth(http.HandlerFunc(a.api)))
	mux.Handle("/", web.Handler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' https://api64.ipify.org; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && !a.sameOrigin(r) {
			fail(w, 403, "请求来源无效，请重新打开面板")
			return
		}
		bodyLimit := int64(128 << 10)
		if r.Method == "POST" && (r.URL.Path == "/api/confirm" || r.URL.Path == "/api/jobs") {
			bodyLimit = 1 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		mux.ServeHTTP(w, r)
	})
}
func (a *App) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	expected := a.Origin
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + r.Host
	}
	return origin == expected
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}
func read(r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("需要 JSON 请求")
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return errors.New("请求格式无效")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("请求包含多余内容")
	}
	return nil
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	if !a.allowLogin(host, time.Now()) {
		fail(w, 429, "登录尝试过多，请十分钟后重试")
		return
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	default:
		fail(w, 429, "登录请求繁忙，请稍后重试")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if read(r, &body) != nil || !store.CheckPassword(body.Password, a.S.Setting("password")) {
		fail(w, 401, "密码不正确")
		return
	}
	token := store.ID()
	csrf := store.ID()
	_, e := a.S.DB.Exec("DELETE FROM sessions WHERE expires<?", store.Now())
	if e == nil {
		_, e = a.S.DB.Exec("INSERT INTO sessions(hash,csrf,expires) VALUES(?,?,?)", store.Hash(token), csrf, store.Now()+12*3600)
	}
	if e != nil {
		fail(w, 500, "无法创建登录会话")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "narbe_session", Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.Origin, "https://") || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600})
	reply(w, 200, map[string]string{"csrf": csrf})
}
func (a *App) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, e := r.Cookie("narbe_session")
		if e != nil {
			fail(w, 401, "请先登录")
			return
		}
		var csrf string
		if e = a.S.DB.QueryRow("SELECT csrf FROM sessions WHERE hash=? AND expires>?", store.Hash(cookie.Value), store.Now()).Scan(&csrf); e != nil {
			fail(w, 401, "登录已过期，请重新登录")
			return
		}
		if r.Method != "GET" && r.Header.Get("X-CSRF-Token") != csrf {
			fail(w, 403, "页面已失效，请刷新后重试")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) provider(id string) (account, cloud.Provider, error) {
	return a.providerContext(context.Background(), id)
}
func (a *App) providerContext(ctx context.Context, id string) (account, cloud.Provider, error) {
	if pinned, ok := ctx.Value(providerSnapshotKey{}).(providerSnapshot); ok && pinned.account.ID == id {
		return pinned.account, pinned.provider, nil
	}
	var acc account
	var sealed []byte
	e := a.S.DB.QueryRow("SELECT id,name,provider,credentials,created,revision FROM accounts WHERE id=? AND retired=0", id).Scan(&acc.ID, &acc.Name, &acc.Provider, &sealed, &acc.Created, &acc.Revision)
	if e != nil {
		return acc, nil, errors.New("云账号不存在")
	}
	raw, e := a.S.Unseal(sealed, "account:"+id)
	if e != nil {
		return acc, nil, errors.New("云凭据无法解密，请恢复匹配的 master.key")
	}
	var c cloud.Credentials
	if json.Unmarshal(raw, &c) != nil {
		return acc, nil, errors.New("云凭据损坏")
	}
	p, e := a.factory(ctx, acc.Provider, c)
	return acc, p, e
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case path == "network":
		a.networkAPI(w, r)
	case path == "network/test":
		a.networkTest(w, r)
	case path == "session" && r.Method == "GET":
		cookie, _ := r.Cookie("narbe_session")
		var csrf string
		_ = a.S.DB.QueryRow("SELECT csrf FROM sessions WHERE hash=?", store.Hash(cookie.Value)).Scan(&csrf)
		reply(w, 200, map[string]string{"csrf": csrf})
	case path == "logout" && r.Method == "POST":
		cookie, _ := r.Cookie("narbe_session")
		_, e := a.S.DB.Exec("DELETE FROM sessions WHERE hash=?", store.Hash(cookie.Value))
		if e != nil {
			fail(w, 500, "退出失败")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "narbe_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		reply(w, 200, map[string]bool{"ok": true})
	case path == "password" && r.Method == "POST":
		var b struct {
			Current  string `json:"current"`
			Password string `json:"password"`
		}
		if read(r, &b) != nil || !store.CheckPassword(b.Current, a.S.Setting("password")) {
			fail(w, 400, "当前密码不正确")
			return
		}
		h, e := store.Password(b.Password)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		tx, e := a.S.DB.Begin()
		if e != nil {
			fail(w, 500, "密码保存失败")
			return
		}
		defer tx.Rollback()
		if _, e = tx.Exec("UPDATE settings SET value=? WHERE key='password'", h); e == nil {
			_, e = tx.Exec("DELETE FROM sessions")
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, "密码保存失败")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	case path == "accounts":
		a.accounts(w, r)
	case strings.HasPrefix(path, "accounts/"):
		a.accountEdit(w, r, strings.TrimPrefix(path, "accounts/"))
	case path == "stackscripts" && r.Method == "GET":
		a.stackScripts(w, r)
	case path == "linode" && r.Method == "GET":
		a.linodeRead(w, r)
	case path == "inventory" && r.Method == "GET":
		a.inventory(w, r)
	case path == "catalog" && r.Method == "GET":
		acc, p, e := a.provider(r.URL.Query().Get("accountId"))
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Minute)
		defer cancel()
		c, e := p.Catalog(ctx, r.URL.Query().Get("region"), r.URL.Query().Get("plan"))
		if e != nil {
			cloudFail(w, e)
			return
		}
		if c.Regions == nil {
			c.Regions = []cloud.Choice{}
		}
		if c.Plans == nil {
			c.Plans = []cloud.Choice{}
		}
		if c.Images == nil {
			c.Images = []cloud.Choice{}
		}
		if a.Lifecycle != nil {
			a.Lifecycle.Enrich(ctx, &c)
		}
		cloud.LocalizeRegions(acc.Provider, c.Regions)
		reply(w, 200, c)
	case path == "instances" && r.Method == "GET":
		_, p, e := a.provider(r.URL.Query().Get("accountId"))
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		items, e := p.Instances(r.Context(), r.URL.Query().Get("region"))
		if e != nil {
			cloudFail(w, e)
			return
		}
		reply(w, 200, items)
	case path == "confirm" && r.Method == "POST":
		var b mutation
		if e := read(r, &b); e != nil {
			fail(w, 400, e.Error())
			return
		}
		b.Confirmation = ""
		ns, err := a.networkSnapshot()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		b.NetworkRevision = ns.Revision
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		r = r.WithContext(context.WithValue(ctx, networkContext{}, ns))
		pinned, acc, _, err := a.pinProvider(r.Context(), b.AccountID)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		r = r.WithContext(pinned)
		b.AccountRevision = acc.Revision
		if e := a.validate(r.Context(), b); e != nil {
			cloudFail(w, e)
			return
		}
		a.network.Lock()
		defer a.network.Unlock()
		if current, err := a.networkLocked(); err != nil || current.Revision != ns.Revision {
			fail(w, 409, "网络出口已变化，请重新确认")
			return
		}
		var currentRevision int
		if err := a.S.DB.QueryRow("SELECT revision FROM accounts WHERE id=? AND retired=0", b.AccountID).Scan(&currentRevision); err != nil || currentRevision != acc.Revision {
			fail(w, 409, "账号凭据已变化，请重新确认")
			return
		}
		id := store.ID()
		raw, _ := json.Marshal(b)
		_, e := a.S.DB.Exec("DELETE FROM confirmations WHERE expires<?", store.Now())
		if e == nil {
			_, e = a.S.DB.Exec("INSERT INTO confirmations(id,digest,expires) VALUES(?,?,?)", id, store.Hash(string(raw)), store.Now()+300)
		}
		if e != nil {
			fail(w, 500, "无法准备确认")
			return
		}
		reply(w, 200, map[string]any{"confirmation": id, "accountRevision": b.AccountRevision, "networkRevision": b.NetworkRevision})
	case path == "jobs" && r.Method == "POST":
		a.submit(w, r)
	case path == "jobs" && r.Method == "GET":
		jobs, e := a.jobs()
		if e != nil {
			fail(w, 500, "任务读取失败")
			return
		}
		reply(w, 200, jobs)
	case strings.HasPrefix(path, "jobs/") && r.Method == "POST":
		a.jobAction(w, r, strings.TrimPrefix(path, "jobs/"))
	default:
		fail(w, 404, "接口不存在")
	}
}
func cloudFail(w http.ResponseWriter, e error) {
	msg := e.Error()
	var ce *cloud.Error
	if errors.As(e, &ce) {
		switch ce.Status {
		case 401, 403:
			msg += "。请检查凭据、权限和访问策略"
		case 429:
			msg += "。请求过于频繁，请稍后刷新"
		case 409, 422:
			msg += "。请检查资源状态、配额及配置"
		default:
			msg += "。请检查网络、地区与云账号配置"
		}
	}
	fail(w, 400, msg)
}
func (a *App) accounts(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		rows, e := a.S.DB.Query("SELECT id,name,provider,created,revision FROM accounts WHERE retired=0 ORDER BY created,id")
		if e != nil {
			fail(w, 500, "云账号读取失败")
			return
		}
		defer rows.Close()
		items := []account{}
		for rows.Next() {
			var i account
			if e = rows.Scan(&i.ID, &i.Name, &i.Provider, &i.Created, &i.Revision); e != nil {
				fail(w, 500, "云账号读取失败")
				return
			}
			items = append(items, i)
		}
		reply(w, 200, items)
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "不支持的请求")
		return
	}
	var b struct {
		Name        string            `json:"name"`
		Provider    string            `json:"provider"`
		Credentials cloud.Credentials `json:"credentials"`
	}
	if e := read(r, &b); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if len(strings.TrimSpace(b.Name)) < 1 || len(b.Name) > 100 {
		fail(w, 400, "请填写 1–100 字符的账号备注")
		return
	}
	if e := cloud.ValidateCredentials(b.Provider, b.Credentials); e != nil {
		fail(w, 400, e.Error())
		return
	}
	p, e := a.factory(r.Context(), b.Provider, b.Credentials)
	if e != nil {
		cloudFail(w, e)
		return
	}
	if e = checkProvider(r.Context(), p, b.Provider); e != nil {
		cloudFail(w, e)
		return
	}
	id := store.ID()
	raw, _ := json.Marshal(b.Credentials)
	sealed, e := a.S.Seal(raw, "account:"+id)
	if e == nil {
		_, e = a.S.DB.Exec("INSERT INTO accounts(id,name,provider,credentials,created) VALUES(?,?,?,?,?)", id, b.Name, b.Provider, sealed, store.Now())
	}
	if e != nil {
		fail(w, 500, "云账号保存失败")
		return
	}
	reply(w, 201, account{ID: id, Name: b.Name, Provider: b.Provider, Created: store.Now()})
}
func (a *App) accountEdit(w http.ResponseWriter, r *http.Request, id string) {
	acc, _, e := a.provider(id)
	if e != nil {
		fail(w, 404, e.Error())
		return
	}
	if r.Method != "DELETE" && r.Method != "PUT" {
		fail(w, 405, "不支持的请求")
		return
	}
	var b struct {
		Name        string            `json:"name"`
		ConfirmName string            `json:"confirmName"` // Accepted for older clients; no typed-name gate.
		Password    string            `json:"password"`
		Credentials cloud.Credentials `json:"credentials"`
	}
	if read(r, &b) != nil {
		fail(w, 400, "请求格式无效")
		return
	}
	var sealed []byte
	if r.Method == "PUT" {
		if len(strings.TrimSpace(b.Name)) < 1 || len(b.Name) > 100 {
			fail(w, 400, "账号备注无效")
			return
		}
		if b.Credentials != nil {
			if e := cloud.ValidateCredentials(acc.Provider, b.Credentials); e != nil {
				fail(w, 400, e.Error())
				return
			}
			p, e := a.factory(r.Context(), acc.Provider, b.Credentials)
			if e != nil {
				cloudFail(w, e)
				return
			}
			if e = checkProvider(r.Context(), p, acc.Provider); e != nil {
				cloudFail(w, e)
				return
			}
			raw, _ := json.Marshal(b.Credentials)
			sealed, e = a.S.Seal(raw, "account:"+id)
			if e != nil {
				fail(w, 500, "凭据加密失败")
				return
			}
		}
	} else if !a.checkAdminPassword(b.Password) {
		fail(w, 400, "需要管理员密码确认移除账号")
		return
	}
	tx, e := a.S.DB.Begin()
	if e != nil {
		fail(w, 500, "无法保存")
		return
	}
	defer tx.Rollback()
	var retired, revision int
	if e = tx.QueryRow("SELECT retired,revision FROM accounts WHERE id=?", id).Scan(&retired, &revision); e != nil || retired != 0 || revision != acc.Revision {
		fail(w, 409, "账号已移除或凭据已变化，请刷新")
		return
	}
	var updated sql.Result
	if r.Method == "DELETE" {
		updated, e = tx.Exec("UPDATE accounts SET retired=1,credentials=x'' WHERE id=? AND retired=0 AND revision=?", id, revision)
	} else if sealed != nil {
		updated, e = tx.Exec("UPDATE accounts SET name=?,credentials=?,revision=revision+1 WHERE id=? AND retired=0 AND revision=?", b.Name, sealed, id, revision)
	} else {
		updated, e = tx.Exec("UPDATE accounts SET name=? WHERE id=? AND retired=0 AND revision=?", b.Name, id, revision)
	}
	if e == nil {
		n, err := updated.RowsAffected()
		if err != nil || n != 1 {
			fail(w, 409, "账号已变化，请刷新")
			return
		}
	}

	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		fail(w, 500, "保存失败")
		return
	}
	a.inventoryMu.Lock()
	if entry := a.inventories[id]; entry != nil {
		entry.cancel()
		delete(a.inventories, id)
	}
	a.inventoryMu.Unlock()
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *App) validate(ctx context.Context, b mutation) error {
	if len(b.ID) < 16 || len(b.ID) > 64 {
		return errors.New("请求标识无效")
	}
	for _, ch := range b.ID {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return errors.New("请求标识无效")
		}
	}
	ns, _ := ctx.Value(networkContext{}).(*networkState)
	if ns == nil {
		var err error
		ns, err = a.networkSnapshot()
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, networkContext{}, ns)
	}
	if b.NetworkRevision != ns.Revision {
		return errors.New("网络出口已变化，请重新确认")
	}
	acc, p, e := a.providerContext(ctx, b.AccountID)
	if e != nil {
		return e
	}
	if b.AccountRevision != acc.Revision {
		return errors.New("账号凭据已变化，请重新检查并确认操作")
	}
	if b.Name == "" {
		return errors.New("资源名称缺失，请刷新资源身份")
	}
	if b.Action != "create" && (b.Spec.UserData != "" || b.Spec.ScriptMode != "" && b.Spec.ScriptMode != "none" || b.Spec.StackScriptID != 0 || b.Spec.StackScriptSnapshot != "" || b.Spec.StackScriptName != "" || len(b.Spec.StackScriptData) > 0 || b.Spec.ScriptCompatibilityAck) {
		return errors.New("初始化脚本仅可用于新建服务器")
	}
	if cloud.LinodeAction(b.Action) {
		if acc.Provider != "linode" {
			return errors.New("高级管理仅支持Linode")
		}
		return a.validateLinode(ctx, b, p)
	}
	if b.Advanced != nil {
		return errors.New("普通操作不能附带高级参数")
	}
	if b.Action == "create" {
		if b.Target != "" || b.Name != b.Spec.Name || b.Region != b.Spec.Region {
			return errors.New("创建配置不一致")
		}
		c, e := p.Catalog(ctx, b.Region, b.Spec.Plan)
		if e != nil {
			return e
		}
		if e = cloud.ValidateSpec(b.Spec, c); e != nil {
			return e
		}
		if e = cloud.ValidateStartup(acc.Provider, b.Spec, c); e != nil {
			return e
		}
		if b.Spec.ScriptMode == "stackscript" {
			ss, ok := p.(cloud.StackScriptProvider)
			if !ok {
				return errors.New("该适配器不支持StackScripts")
			}
			if e = ss.ValidateStackScript(ctx, b.Spec); e != nil {
				return e
			}
		}
		s := b.Spec
		if acc.Provider != "linode" && s.Firewall != nil {
			return errors.New("防火墙模式仅适用于Linode")
		}
		im, _ := cloud.Find(c.Images, s.Image)
		plan, _ := cloud.Find(c.Plans, s.Plan)
		if plan.Disk > 0 && plan.Disk < im.Disk {
			return errors.New("规格磁盘小于镜像最低要求")
		}
		switch acc.Provider {
		case "aws":
			if s.Network == "" || s.Group == "" || s.Key == "" {
				return errors.New("AWS 需要选择子网、安全组和已有 SSH 密钥对")
			}
			if im.Family == "windows" {
				key, _ := cloud.Find(c.Keys, s.Key)
				if key.Extra["type"] != "rsa" {
					return errors.New("AWS Windows 需要 RSA 密钥对")
				}
			}
			n, _ := cloud.Find(c.Networks, s.Network)
			g, _ := cloud.Find(c.Groups, s.Group)
			if n.Extra["vpc"] != g.Extra["vpc"] {
				return errors.New("子网和安全组必须属于同一 VPC")
			}
			if s.Backups || s.Password != "" || s.Disk != 0 {
				return errors.New("AWS 不支持这里的自动备份、指定密码或磁盘大小选项")
			}
		case "azure":
			if s.Disk < 0 || s.Disk > 4095 || (s.Disk > 0 && s.Disk < im.Disk) {
				return errors.New("系统盘容量应不小于镜像要求且不超过4095 GB")
			}
			if containsName([]string{"root", "admin", "administrator", "guest"}, strings.ToLower(s.Username)) {
				return errors.New("此管理员用户名被 Azure 保留，请使用其他名称")
			}
			if s.Password != "" {
				maxLength := 72
				if im.Family == "windows" {
					maxLength = 123
				}
				if len(s.Password) < 12 || len(s.Password) > maxLength {
					return errors.New("管理员密码长度不符合云厂商要求")
				}
				var upper, lower, digit, special bool
				for _, ch := range s.Password {
					switch {
					case ch >= 'A' && ch <= 'Z':
						upper = true
					case ch >= 'a' && ch <= 'z':
						lower = true
					case ch >= '0' && ch <= '9':
						digit = true
					default:
						special = true
					}
				}
				n := 0
				for _, v := range []bool{upper, lower, digit, special} {
					if v {
						n++
					}
				}
				if n < 3 {
					return errors.New("密码需包含大小写字母、数字、符号中的至少三类")
				}
			}

			_, cidr, err := net.ParseCIDR(s.ManagementCIDR)
			if err != nil || cidr.IP.To4() == nil {
				return errors.New("请填写有效的 IPv4 管理来源 CIDR，例如你的公网IP/32")
			}
			ones, _ := cidr.Mask.Size()
			if ones < 16 {
				return errors.New("管理来源范围过宽，请使用 /16 或更小的可信地址范围")
			}
			if s.Network == "" || s.Username == "" || len(s.Username) > 20 || s.IPv6 || s.Backups {
				return errors.New("Azure 需要有效子网和管理员用户名；暂不支持此处的 IPv6/自动备份")
			}
			for _, ch := range s.Username {
				if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
					return errors.New("管理员用户名仅支持小写字母、数字、下划线和连字符")
				}
			}
			if im.Family == "windows" && len(s.Password) < 12 {
				return errors.New("Windows Server 需要至少 12 字符的管理员密码")
			}
			if im.Family != "windows" && !validSSH(s.SSHPublicKey) {
				return errors.New("Azure Linux 需要有效的 SSH 公钥")
			}
		case "linode":
			if len(s.Name) < 3 || len(s.Name) > 32 {
				return errors.New("Linode名称长度需要3–32字符")
			}
			if advanced, ok := p.(cloud.LinodeAdvanced); ok {
				selection := cloud.FirewallSelection{Mode: "all"}
				if s.Firewall != nil {
					selection = *s.Firewall
				}
				if e = advanced.ValidateFirewallSelection(ctx, selection); e != nil {
					return e
				}
			} else if s.Firewall != nil {
				return errors.New("该适配器无法核实Linode防火墙模式")
			}
			if s.Password == "" && s.SSHPublicKey == "" && s.Key == "" {
				return errors.New("Linode 需要 root 密码或 SSH 公钥")
			}
			if e := cloud.ValidateLinodePassword(s.Password); e != nil {
				return e
			}
			if s.Disk != 0 {
				return errors.New("Linode 在此处不支持自定义磁盘大小")
			}
			if s.Key != "" {
				k, _ := cloud.Find(c.Keys, s.Key)
				if !validSSH(k.Extra["publicKey"]) {
					return errors.New("所选 SSH 公钥无效")
				}
			}
		case "digitalocean":
			if s.Key == "" {
				return errors.New("DigitalOcean 需要选择已有 SSH 密钥，避免通过邮件传送初始密码")
			}
			if s.Password != "" || s.SSHPublicKey != "" || s.Disk != 0 {
				return errors.New("DigitalOcean 使用账号下的 SSH 密钥，不接受指定密码或自定义磁盘大小")
			}
		}
		if s.SSHPublicKey != "" && !validSSH(s.SSHPublicKey) {
			return errors.New("SSH 公钥格式无效")
		}
		return nil
	}
	items, e := p.Instances(ctx, b.Region)
	if e != nil {
		return e
	}
	for _, i := range items {
		if i.ID == b.Target && i.Name == b.Name && i.Region == b.Region {
			if containsName([]string{"start", "stop", "reboot", "delete"}, b.Action) || b.Action == "poweroff" && acc.Provider == "azure" {
				return nil
			}
			return errors.New("不支持的云操作")
		}
	}
	return errors.New("实例不存在或身份已变化，请刷新列表")
}
func validSSH(s string) bool {
	key, _, _, rest, e := ssh.ParseAuthorizedKey([]byte(s))
	if e != nil || len(rest) != 0 {
		return false
	}
	switch key.Type() {
	case "ssh-ed25519":
		return true
	case "ssh-rsa":
		return len(key.Marshal()) >= 256
	}
	return false
}

func (a *App) submit(w http.ResponseWriter, r *http.Request) {
	var b mutation
	if read(r, &b) != nil {
		fail(w, 400, "请求格式无效")
		return
	}
	ns, err := a.networkSnapshot()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), networkContext{}, ns))
	confirmation := b.Confirmation
	if confirmation == "" {
		fail(w, 409, "需要新的确认令牌")
		return
	}
	b.Confirmation = ""
	if (b.Action == "create" && b.Spec.ScriptMode == "stackscript") || b.Action == "rebuild" {
		var exists int
		e := a.S.DB.QueryRow("SELECT count(*) FROM jobs WHERE id=?", b.ID).Scan(&exists)
		if e != nil {
			fail(w, 500, "任务读取失败")
			return
		}
		if exists == 0 {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if e = a.validate(ctx, b); e != nil {
				cloudFail(w, e)
				return
			}
		}
	}
	a.network.Lock()
	defer a.network.Unlock()
	current, err := a.networkLocked()
	if err != nil {
		fail(w, 500, "网络配置不可用")
		return
	}
	raw, _ := json.Marshal(b)
	sealed, e := a.S.Seal(raw, "job:"+b.ID)
	if e != nil {
		fail(w, 500, "任务加密失败")
		return
	}
	tx, e := a.S.DB.Begin()
	if e != nil {
		fail(w, 500, "任务保存失败")
		return
	}
	defer tx.Rollback()
	var old []byte
	e = tx.QueryRow("SELECT payload FROM jobs WHERE id=?", b.ID).Scan(&old)
	if e == nil {
		plain, e := a.S.Unseal(old, "job:"+b.ID)
		if e != nil || string(plain) != string(raw) {
			fail(w, 409, "请求标识已用于其他配置")
			return
		}
		reply(w, 200, map[string]string{"id": b.ID})
		return
	}
	if e != sql.ErrNoRows {
		fail(w, 500, "任务查询失败")
		return
	}
	if b.NetworkRevision != current.Revision {
		fail(w, 409, "网络出口已变化，请重新确认")
		return
	}
	res, e := tx.Exec("DELETE FROM confirmations WHERE id=? AND digest=? AND expires>?", confirmation, store.Hash(string(raw)), store.Now())
	if e != nil {
		fail(w, 500, "确认读取失败")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		fail(w, 409, "确认已失效，请重新检查配置")
		return
	}
	var revision int
	if e = tx.QueryRow("SELECT revision FROM accounts WHERE id=? AND retired=0", b.AccountID).Scan(&revision); e != nil || revision != b.AccountRevision {
		fail(w, 409, "账号凭据已变化，请重新确认")
		return
	}
	_, e = tx.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,created,updated) VALUES(?,?,?,?,?,?,?,'queued','等待执行',?,?)", b.ID, b.AccountID, b.Action, b.Region, b.Target, b.Name, sealed, store.Now(), store.Now())
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		fail(w, 500, "任务保存失败")
		return
	}
	select {
	case a.wake <- struct{}{}:
	default:
	}
	reply(w, 202, map[string]string{"id": b.ID})
}
func (a *App) jobs() ([]job, error) {
	rows, e := a.S.DB.Query("SELECT j.id,j.account_id,j.action,j.region,j.target,j.name,j.state,j.progress,j.result,j.error,j.created,j.updated,a.provider FROM jobs j JOIN accounts a ON a.id=j.account_id ORDER BY j.created DESC,j.id DESC LIMIT 200")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []job{}
	for rows.Next() {
		var j job
		var result []byte
		if e = rows.Scan(&j.ID, &j.AccountID, &j.Action, &j.Region, &j.Target, &j.Name, &j.State, &j.Progress, &result, &j.Error, &j.Created, &j.Updated, &j.Provider); e != nil {
			return nil, e
		}
		if len(result) > 0 {
			j.Result = &cloud.Operation{}
			if json.Unmarshal(result, j.Result) != nil {
				return nil, errors.New("任务结果损坏")
			}
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (a *App) jobAction(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		fail(w, 404, "任务不存在")
		return
	}
	id, action := parts[0], parts[1]
	if action == "cancel" {
		res, e := a.S.DB.Exec("UPDATE jobs SET state='cancelled',progress='已取消，未发送云操作',updated=? WHERE id=? AND state='queued'", store.Now(), id)
		if e != nil {
			fail(w, 500, "取消失败")
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			fail(w, 409, "仅可取消尚未执行的任务")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if action == "resolve" {
		var b struct {
			Password    string `json:"password"`
			ConfirmName string `json:"confirmName"`
			Checked     bool   `json:"checked"`
		}
		if read(r, &b) != nil || !b.Checked || !a.checkAdminPassword(b.Password) {
			fail(w, 400, "需要管理员密码及明确的云端核实确认")
			return
		}
		res, e := a.S.DB.Exec("UPDATE jobs SET state='resolved',progress='管理员已在云控制台人工核实；未重试或删除任何资源',updated=? WHERE id=? AND state='unknown'", store.Now(), id)
		if e != nil {
			fail(w, 500, "核实记录保存失败")
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			fail(w, 409, "任务状态已变化")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if action != "reconcile" {
		fail(w, 404, "操作不存在")
		return
	}
	var aid, state, act string
	var result, payload []byte
	if e := a.S.DB.QueryRow("SELECT account_id,state,action,result,payload FROM jobs WHERE id=?", id).Scan(&aid, &state, &act, &result, &payload); e != nil {
		fail(w, 404, "任务不存在")
		return
	}
	if state != "unknown" {
		fail(w, 409, "仅需核实结果未知的任务")
		return
	}
	var op cloud.Operation
	if len(result) == 0 || json.Unmarshal(result, &op) != nil || op.ResourceID == "" || act == "reboot" {
		fail(w, 409, "缺少可靠的云端完成证据；请在云控制台按任务标识核实，系统不会重新执行")
		return
	}
	raw, e := a.S.Unseal(payload, "job:"+id)
	var original mutation
	if e != nil || json.Unmarshal(raw, &original) != nil || original.AccountID != aid {
		fail(w, 409, "任务原始账号身份无法核实，请到云控制台检查")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	acc, p, e := a.providerContext(ctx, aid)
	if e != nil {
		cloudFail(w, e)
		return
	}
	if acc.Revision != original.AccountRevision {
		fail(w, 409, "云账号凭据已变更，不能以新账号状态证明原任务；请到原云控制台核实")
		return
	}
	done, e := p.Poll(ctx, op)
	if e != nil {
		cloudFail(w, e)
		return
	}
	if !done {
		fail(w, 409, "云端尚未达到目标状态，请稍后再次核实")
		return
	}
	updated, e := a.S.DB.Exec("UPDATE jobs SET state='succeeded',progress='只读核实云端目标状态已达到',error='',updated=? WHERE id=? AND state='unknown' AND EXISTS(SELECT 1 FROM accounts WHERE id=? AND retired=0 AND revision=?)", store.Now(), id, aid, original.AccountRevision)
	if e != nil {
		fail(w, 500, "核实结果保存失败")
		return
	}
	if n, _ := updated.RowsAffected(); n != 1 {
		fail(w, 409, "任务或账号身份已变化，请刷新")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

func checkProvider(ctx context.Context, p cloud.Provider, kind string) error {
	c, e := p.Catalog(ctx, "", "")
	if e != nil {
		return e
	}
	region := ""
	if kind == "aws" {
		if len(c.Regions) == 0 {
			return errors.New("账号没有可访问地区")
		}
		region = c.Regions[0].ID
	}
	_, e = p.Instances(ctx, region)
	return e
}

func containsName(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}
