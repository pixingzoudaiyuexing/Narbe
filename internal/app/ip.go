package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

func exactIPInstance(ctx context.Context, p cloud.Provider, b mutation) (cloud.Instance, error) {
	if b.Target == "" || b.Region == "" {
		return cloud.Instance{}, errors.New("服务器身份缺失")
	}
	items, e := p.Instances(ctx, b.Region)
	if e != nil {
		return cloud.Instance{}, e
	}
	for _, i := range items {
		if i.ID == b.Target && i.Region == b.Region && (b.Name == "" || b.Name == i.Name) {
			return i, nil
		}
	}
	return cloud.Instance{}, errors.New("服务器不存在或身份已变化，请刷新")
}
func (a *App) validateIPChange(ctx context.Context, b mutation, p cloud.Provider) error {
	if b.Advanced != nil || b.IPChange == nil {
		return errors.New("公网 IP 变更参数缺失或混有其他操作")
	}
	i, e := exactIPInstance(ctx, p, b)
	if e != nil {
		return e
	}
	pp, ok := p.(cloud.IPProvider)
	if !ok {
		return errors.New("此适配器不支持安全的原机换 IP")
	}
	info, e := pp.IPInfo(ctx, i)
	if e != nil {
		return e
	}
	_, e = cloud.NewIPOperation(i, *b.IPChange, info, b.ID)
	return e
}
func defaultIPCheckConfig() screeningConfig {
	return screeningConfig{ControlProbeID: "manager", Protocol: "tcp", Port: 443, TimeoutSeconds: 5, IntervalSeconds: 5, Successes: 2, Failures: 2, ReadySeconds: 300, MaxDurationSeconds: 1800, MaxAttempts: 1}
}
func (a *App) startIPWorkflow(ctx context.Context, id string, b mutation, acc account, ns *networkState) error {
	post := b.Screening != nil
	if b.Screening == nil {
		c := defaultIPCheckConfig()
		b.Screening = &c
	}
	return a.startWorkflow(ctx, id, b, acc, ns, "manual-ip", post)
}
func validIPRequestID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (a *App) ipAPI(w http.ResponseWriter, r *http.Request, path string) {
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	ns, e := a.networkSnapshot()
	if e != nil {
		fail(w, 500, "无法读取网络出口")
		return
	}
	ctx = context.WithValue(ctx, networkContext{}, ns)
	if path == "ip" && r.Method == "GET" {
		b := mutation{AccountID: r.URL.Query().Get("accountId"), Region: r.URL.Query().Get("region"), Target: r.URL.Query().Get("target")}
		ctx, acc, p, e := a.pinProvider(ctx, b.AccountID)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		i, e := exactIPInstance(ctx, p, b)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		pp, ok := p.(cloud.IPProvider)
		if !ok {
			fail(w, 400, "此适配器不支持公网 IP 管理")
			return
		}
		info, e := pp.IPInfo(ctx, i)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		out := map[string]any{"accountRevision": acc.Revision, "networkRevision": ns.Revision, "provider": acc.Provider, "instance": i, "data": info}
		var raw []byte
		var state, stage string
		if a.S.DB.QueryRow(`SELECT s.data,s.state,s.stage FROM screenings s JOIN jobs j ON j.id=s.job_id WHERE j.account_id=? AND j.region=? AND (j.target=? OR json_extract(s.data,'$.resourceId')=?) ORDER BY s.created DESC,s.id DESC LIMIT 1`, b.AccountID, b.Region, b.Target, b.Target).Scan(&raw, &state, &stage) == nil {
			var last screeningView
			if json.Unmarshal(raw, &last) == nil {
				last.State, last.Stage = state, stage
				out["lastCheck"] = last
			}
		}
		reply(w, 200, out)
		return
	}
	if path != "ip/check" || r.Method != "POST" {
		fail(w, 405, "IP 操作不支持")
		return
	}
	var q struct {
		ID, AccountID, Region, Target, IP string
		Config                            screeningConfig `json:"config"`
	}
	if read(r, &q) != nil || !validIPRequestID(q.ID) || !probe.PublicIPv4(q.IP) {
		fail(w, 400, "请求标识或公网 IPv4 无效")
		return
	}
	c := q.Config.normal()
	if c.AutoReplace {
		fail(w, 400, "已有服务器检测不会自动换 IP 或删除实例")
		return
	}
	c.AutoReplace = false
	c.MaxAttempts = 1
	c.CostPerAttemptUSD = 0
	c.BudgetUSD = 0
	c.RiskAccepted = false
	if e = a.validateReadOnlyScreening(c); e != nil {
		fail(w, 400, e.Error())
		return
	}
	ctx, acc, p, e := a.pinProvider(ctx, q.AccountID)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	b := mutation{ID: q.ID, Action: "ip-check", AccountID: q.AccountID, Region: q.Region, Target: q.Target, AccountRevision: acc.Revision, NetworkRevision: ns.Revision, Screening: &c}
	i, e := exactIPInstance(ctx, p, b)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	b.Name = i.Name
	pp, ok := p.(cloud.IPProvider)
	if !ok {
		fail(w, 400, "云适配器不支持 IP 核实")
		return
	}
	info, e := pp.IPInfo(ctx, i)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	found := false
	for _, x := range info.Addresses {
		if x.Address == q.IP {
			found = true
		}
	}
	for _, x := range info.ServiceIPv4 {
		if x == q.IP {
			found = true
		}
	}
	if !found {
		fail(w, 400, "检测目标不属于所选实例；不接受任意 IP、内网或 Metadata 扫描")
		return
	}
	canonical, _ := json.Marshal(struct {
		Mutation mutation
		IP       string
	}{b, q.IP})

	tx, e := a.S.DB.Begin()
	if e != nil {
		fail(w, 500, "任务保存失败")
		return
	}
	defer tx.Rollback()
	var oldraw []byte
	e = tx.QueryRow("SELECT payload FROM jobs WHERE id=?", q.ID).Scan(&oldraw)
	if e == nil {
		oldplain, err := a.S.Unseal(oldraw, "job:"+q.ID)
		if err != nil || string(oldplain) != string(canonical) {
			fail(w, 409, "请求 ID 已被不同参数使用")
		} else {
			reply(w, 202, map[string]string{"id": q.ID})
		}
		return
	}
	if e != sql.ErrNoRows {
		fail(w, 500, "任务读取失败")
		return
	}
	var raw []byte
	var rev int
	if e = tx.QueryRow("SELECT credentials,revision FROM accounts WHERE id=? AND retired=0", q.AccountID).Scan(&raw, &rev); e != nil || rev != acc.Revision {
		fail(w, 409, "账号已变化")
		return
	}
	plain, e := a.S.Unseal(raw, "account:"+q.AccountID)
	var credentials cloud.Credentials
	if e != nil || json.Unmarshal(plain, &credentials) != nil {
		fail(w, 500, "凭据快照读取失败")
		return
	}
	payload, _ := json.Marshal(screeningPayload{Mutation: b, Credentials: credentials, Network: ns.Config, NetworkRevision: ns.Revision, PrivateProxyAllowlist: proxyAllowlist(), Provider: acc.Provider, QueuedAt: store.Now()})
	sealed, e := a.S.Seal(payload, "screening:"+q.ID)
	if e != nil {
		fail(w, 500, "任务加密失败")
		return
	}
	jobraw := canonical
	jobsealed, e := a.S.Seal(jobraw, "job:"+q.ID)
	if e != nil {
		fail(w, 500, "任务加密失败")
		return
	}
	v := screeningView{Kind: "check", Phase: "route", ID: q.ID, JobID: q.ID, Provider: acc.Provider, Region: q.Region, ResourceID: i.ID, IP: q.IP, State: "queued", Stage: "等待只读 IP 检测", MaxAttempts: 1, Attempt: 1, Started: store.Now(), Updated: store.Now(), ReadyUntil: store.Now() + int64(c.ReadySeconds), Rounds: []screeningRound{{Attempt: 1, ResourceID: i.ID, IP: q.IP, Created: store.Now(), Evidence: []screeningEvidence{}}}, Resources: []cloud.RelatedResource{}, Summary: map[string]string{"name": i.Name, "pricing": "仅只读检测，不更改或删除已有服务器"}}
	data, _ := json.Marshal(v)
	_, e = tx.Exec(`INSERT INTO jobs(id,account_id,action,region,target,name,state,progress,payload,created,updated) VALUES(?,?,'ip-check',?,?,?,'screening',?,?,?,?)`, q.ID, q.AccountID, q.Region, q.Target, i.Name, v.Stage, jobsealed, v.Started, v.Updated)
	if e == nil {
		_, e = tx.Exec("INSERT INTO screenings(id,job_id,state,stage,payload,data,created,updated) VALUES(?,?,'queued',?,?,?,?,?)", q.ID, q.ID, v.Stage, sealed, data, v.Started, v.Updated)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		fail(w, 500, "检测任务保存失败")
		return
	}
	reply(w, 202, map[string]string{"id": q.ID})
}

// Kept in a single helper so encrypted snapshots use exactly the same explicit
// proxy allowlist as ordinary create and screening workflows.
func proxyAllowlist() string { return os.Getenv("NARBE_PROXY_ALLOW_PRIVATE") }
