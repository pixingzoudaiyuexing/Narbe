package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

type screeningConfig struct {
	ProbeID            string  `json:"probeId"`
	ControlProbeID     string  `json:"controlProbeId"`
	Protocol           string  `json:"protocol"`
	Port               int     `json:"port"`
	ServerName         string  `json:"serverName"`
	Path               string  `json:"path"`
	ExpectedStatus     int     `json:"expectedStatus"`
	Challenge          string  `json:"challenge"`
	TimeoutSeconds     int     `json:"timeoutSeconds"`
	IntervalSeconds    int     `json:"intervalSeconds"`
	Successes          int     `json:"successes"`
	Failures           int     `json:"failures"`
	ReadySeconds       int     `json:"readySeconds"`
	MaxDurationSeconds int     `json:"maxDurationSeconds"`
	MaxAttempts        int     `json:"maxAttempts"`
	AutoReplace        bool    `json:"autoReplace"`
	BudgetUSD          float64 `json:"budgetUSD"`
	CostPerAttemptUSD  float64 `json:"costPerAttemptUSD"`
	RiskAccepted       bool    `json:"riskAccepted"`
}

func (c screeningConfig) target(ip string) probe.Target {
	return probe.Target{IP: ip, Port: c.Port, Protocol: c.Protocol, Path: c.Path, ServerName: c.ServerName, ExpectedStatus: c.ExpectedStatus, Challenge: c.Challenge}
}
func (c screeningConfig) normal() screeningConfig {
	if c.ControlProbeID == "" {
		c.ControlProbeID = "manager"
	}
	if c.ExpectedStatus == 0 && (c.Protocol == "http" || c.Protocol == "https") {
		c.ExpectedStatus = 200
	}
	return c
}
func (a *App) validateReadOnlyScreening(c screeningConfig) error {
	c.AutoReplace = false
	c.RiskAccepted = false
	c.MaxAttempts = 1
	c.CostPerAttemptUSD = 1
	c.BudgetUSD = 1
	return a.validateScreening(c)
}

func (a *App) validateScreening(c screeningConfig) error {
	c = c.normal()
	if e := probe.ValidateTarget(c.target("9.9.9.9"), []int{c.Port}); e != nil {
		return fmt.Errorf("尚无有效检测目标：%w", e)
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 15 || c.IntervalSeconds < 2 || c.IntervalSeconds > 300 || c.Successes < 2 || c.Successes > 10 || c.Failures < 2 || c.Failures > 10 || c.ReadySeconds < 30 || c.ReadySeconds > 1800 || c.MaxDurationSeconds < c.ReadySeconds || c.MaxDurationSeconds > 21600 || c.MaxAttempts < 1 || c.MaxAttempts > 20 {
		return errors.New("检测超时、间隔、连续次数或整轮时间/尝试上限无效")
	}
	if math.IsNaN(c.BudgetUSD) || math.IsInf(c.BudgetUSD, 0) || math.IsNaN(c.CostPerAttemptUSD) || math.IsInf(c.CostPerAttemptUSD, 0) || c.CostPerAttemptUSD < 0.01 || c.CostPerAttemptUSD > 10000 || c.BudgetUSD < c.CostPerAttemptUSD || c.BudgetUSD > 100000 {
		return errors.New("请输入有效的每轮费用预留估算与总预算（USD）；它们不是真实账单上限")
	}
	if c.AutoReplace && !c.RiskAccepted {
		return errors.New("自动换 IP 可能多次计费并删除本轮新建服务器，需要首次明确授权")
	}
	p, e := a.getProbe(c.ProbeID)
	if e != nil || p.Role != "domestic" || p.Status != "online" {
		return errors.New("请选择出口已核实且在线的中国大陆探针；离线或出口不正确时不会创建实例")
	}
	if c.ControlProbeID != "manager" {
		q, e := a.getProbe(c.ControlProbeID)
		if e != nil || q.Role != "control" || q.Status != "online" || q.ID == p.ID {
			return errors.New("请选择在线且出口位于中国大陆以外的对照探针")
		}
	}
	return nil
}

type screeningRound struct {
	ReplacementHistory []cloud.Operation      `json:"replacementHistory,omitempty"`
	Writes             []cloud.ScreeningWrite `json:"writes"`
	Attempt            int                    `json:"attempt"`
	CreateToken        string                 `json:"createToken"`
	ResourceID         string                 `json:"resourceId"`
	IP                 string                 `json:"ip"`
	Fingerprint        string                 `json:"fingerprint"`
	Method             string                 `json:"method"`
	State              string                 `json:"state"`
	Created            int64                  `json:"created"`
	CreateOperation    cloud.Operation        `json:"createOperation"`
	Operation          cloud.Operation        `json:"operation"`
	Domestic           *probe.Result          `json:"domestic,omitempty"`
	Control            *probe.Result          `json:"control,omitempty"`
	EstimatedUSD       float64                `json:"estimatedUSD"`
	RepeatedIP         bool                   `json:"repeatedIP"`
	Evidence           []screeningEvidence    `json:"evidence"`
}
type screeningEvidence struct {
	Target         probe.Target `json:"target"`
	Domestic       probe.Result `json:"domestic"`
	Control        probe.Result `json:"control"`
	DomesticEgress probe.Egress `json:"domesticEgress"`
	ControlEgress  probe.Egress `json:"controlEgress"`
}
type screeningView struct {
	CleanupPlans    []cloud.Operation       `json:"cleanupPlans,omitempty"`
	CleanupIndex    int                     `json:"cleanupIndex,omitempty"`
	Kind            string                  `json:"kind"`
	Phase           string                  `json:"phase"`
	NextRun         int64                   `json:"nextRun"`
	ReadyUntil      int64                   `json:"readyUntil"`
	Good            int                     `json:"good"`
	Bad             int                     `json:"bad"`
	PendingDomestic string                  `json:"pendingDomestic,omitempty"`
	PendingControl  string                  `json:"pendingControl,omitempty"`
	RouteCheck      string                  `json:"routeCheck,omitempty"`
	ReservedNext    bool                    `json:"reservedNext"`
	ID              string                  `json:"id"`
	JobID           string                  `json:"jobId"`
	Provider        string                  `json:"provider"`
	Region          string                  `json:"region"`
	State           string                  `json:"state"`
	Stage           string                  `json:"stage"`
	Attempt         int                     `json:"attempt"`
	MaxAttempts     int                     `json:"maxAttempts"`
	IP              string                  `json:"ip"`
	ResourceID      string                  `json:"resourceId"`
	Reason          string                  `json:"reason"`
	Started         int64                   `json:"started"`
	Updated         int64                   `json:"updated"`
	EstimatedUSD    float64                 `json:"estimatedUSD"`
	BudgetUSD       float64                 `json:"budgetUSD"`
	Method          string                  `json:"method"`
	Rounds          []screeningRound        `json:"rounds"`
	Resources       []cloud.RelatedResource `json:"resources"`
	Summary         map[string]string       `json:"summary"`
}
type screeningPayload struct {
	PrivateProxyAllowlist string            `json:"privateProxyAllowlist"`
	Mutation              mutation          `json:"mutation"`
	Credentials           cloud.Credentials `json:"credentials"`
	Network               cloud.Egress      `json:"network"`
	NetworkRevision       int               `json:"networkRevision"`
	Provider              string            `json:"provider"`
	QueuedAt              int64             `json:"queuedAt"`
}

func (a *App) startScreening(ctx context.Context, id string, b mutation, acc account, ns *networkState) error {
	return a.startWorkflow(ctx, id, b, acc, ns, "screening", false)
}
func (a *App) startWorkflow(ctx context.Context, id string, b mutation, acc account, ns *networkState, kind string, postCheck bool) error {
	if _, ok := ctx.Value(providerSnapshotKey{}).(providerSnapshot); !ok {
		return errors.New("缺少账号凭据快照")
	}
	var raw []byte
	var revision int
	var queuedAt int64
	tx, e := a.S.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = tx.QueryRow("SELECT credentials,revision FROM accounts WHERE id=? AND retired=0", b.AccountID).Scan(&raw, &revision); e != nil || revision != b.AccountRevision {
		return errors.New("账号已变化；未创建资源")
	}
	if e = tx.QueryRow("SELECT created FROM jobs WHERE id=? AND state='running'", id).Scan(&queuedAt); e != nil || queuedAt > store.Now() || queuedAt <= store.Now()-int64(store.MaxQueuedAge/time.Second) {
		return errors.New("创建授权等待过期；未创建资源")
	}
	plain, e := a.S.Unseal(raw, "account:"+b.AccountID)
	if e != nil {
		return e
	}
	var credentials cloud.Credentials
	if json.Unmarshal(plain, &credentials) != nil {
		return errors.New("凭据快照无法读取")
	}
	c := b.Screening.normal()
	b.Screening = &c
	payload, _ := json.Marshal(screeningPayload{Mutation: b, Credentials: credentials, Network: ns.Config, NetworkRevision: ns.Revision, PrivateProxyAllowlist: os.Getenv("NARBE_PROXY_ALLOW_PRIVATE"), Provider: acc.Provider, QueuedAt: queuedAt})
	sealed, e := a.S.Seal(payload, "screening:"+id)
	if e != nil {
		return e
	}
	v := screeningView{Kind: "screening", Phase: "route", ID: id, JobID: id, Provider: acc.Provider, Region: b.Region, State: "queued", Stage: "等待自动筛选", MaxAttempts: c.MaxAttempts, BudgetUSD: c.BudgetUSD, Started: store.Now(), Updated: store.Now(), Rounds: []screeningRound{}, Resources: []cloud.RelatedResource{}, Summary: map[string]string{"name": b.Spec.Name, "plan": b.Spec.Plan, "image": b.Spec.Image, "pricing": "费用为预留估算，不代表真实最终账单；仍存在的实例和关联资源会继续计费"}}
	if kind == "manual-ip" {
		v.Kind, v.Phase, v.State, v.Stage = "manual-ip", "manual-begin", "replacing", "原机换 IP：等待核对当前配置"
		v.ResourceID = b.Target
		v.Attempt = 1
		v.MaxAttempts = 1
		v.BudgetUSD = 0
		v.Rounds = []screeningRound{{Attempt: 1, ResourceID: b.Target, Created: store.Now(), Evidence: []screeningEvidence{}}}
		v.Summary = map[string]string{"name": b.Name, "postCheck": "false", "pricing": "仅执行这次确认的地址变更；已有服务器不会自动删除；公网地址可能单独计费"}
		if postCheck {
			v.Summary["postCheck"] = "true"
		}
	}
	data, _ := json.Marshal(v)
	_, e = tx.Exec("INSERT INTO screenings(id,job_id,state,stage,payload,data,created,updated) VALUES(?,?,?,?,?,?,?,?)", id, id, v.State, v.Stage, sealed, data, v.Started, v.Updated)
	if e == nil {
		_, e = tx.Exec("UPDATE jobs SET state='screening',progress=?,updated=? WHERE id=? AND state='running'", v.Stage, store.Now(), id)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}
func (a *App) screeningLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if a.runScreeningOne(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func (a *App) screenLoad(id string) (screeningView, screeningPayload, error) {
	var v screeningView
	var p screeningPayload
	var data, encrypted []byte
	var state, stage string
	e := a.S.DB.QueryRow("SELECT data,payload,state,stage FROM screenings WHERE id=?", id).Scan(&data, &encrypted, &state, &stage)
	if e != nil {
		return v, p, e
	}
	if json.Unmarshal(data, &v) != nil {
		return v, p, errors.New("筛选账本损坏")
	}
	raw, e := a.S.Unseal(encrypted, "screening:"+id)
	if e != nil || json.Unmarshal(raw, &p) != nil {
		return v, p, errors.New("筛选凭据或参数无法解密")
	}
	v.State, v.Stage = state, stage
	return v, p, nil
}
func (a *App) screenSave(v *screeningView) error {
	v.Updated = store.Now()
	data, e := json.Marshal(v)
	if e != nil {
		return e
	}
	tx, e := a.S.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var stopped int
	if e = tx.QueryRow("SELECT stop FROM screenings WHERE id=?", v.ID).Scan(&stopped); e != nil {
		return e
	}
	if stopped != 0 && v.State != "unknown" {
		v.State = "stopped"
		v.Stage = "用户已停止；当前实例及附属资源可能继续计费"
		v.Reason = v.Stage
		data, e = json.Marshal(v)
		if e != nil {
			return e
		}
	}
	_, e = tx.Exec("UPDATE screenings SET state=?,stage=?,data=?,next_run=?,updated=? WHERE id=?", v.State, v.Stage, data, v.NextRun, v.Updated, v.ID)
	jobState := "screening"
	switch v.State {
	case "accepted":
		jobState = "succeeded"
	case "unknown", "paused":
		jobState = "unknown"
	case "stopped":
		jobState = "cancelled"
	case "failed", "exhausted":
		jobState = "failed"
	}
	var result []byte
	if len(v.Rounds) > 0 {
		result, _ = json.Marshal(v.Rounds[len(v.Rounds)-1].Operation)
	}
	if e == nil {
		_, e = tx.Exec("UPDATE jobs SET state=?,progress=?,result=COALESCE(?,result),updated=? WHERE id=?", jobState, "国内连通性自动筛选："+v.Stage, result, v.Updated, v.JobID)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}

var errScreenStop = errors.New("用户已停止自动筛选；当前仍存资源保留，不会继续云写")

func (a *App) screenGuard(ctx context.Context, v *screeningView, p screeningPayload) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if store.Now()-v.Started >= int64(p.Mutation.Screening.MaxDurationSeconds) {
		return errors.New("整轮最长执行时间已达到")
	}
	var stop, retired, revision int
	e := a.S.DB.QueryRow("SELECT s.stop,a.retired,a.revision FROM screenings s JOIN jobs j ON j.id=s.job_id JOIN accounts a ON a.id=j.account_id WHERE s.id=?", v.ID).Scan(&stop, &retired, &revision)
	if e != nil {
		return errors.New("无法读取停止/账号状态，停止云写")
	}
	if stop != 0 {
		return errScreenStop
	}
	if retired != 0 || revision != p.Mutation.AccountRevision {
		return errors.New("账号已移除或凭据版本改变，停止后续云写")
	}
	return nil
}
func (a *App) screenEnd(v *screeningView, state, reason string) {
	v.State, v.Stage, v.Reason = state, reason, reason
	_ = a.screenSave(v)
}
func (a *App) screenPause(v *screeningView, e error) {
	if e == nil {
		e = errors.New("等待被中断，证据不足；当前资源保留")
	}
	if errors.Is(e, context.DeadlineExceeded) {
		e = errors.New("等待超时，证据不足；当前资源保留且可能继续计费")
	}
	if errors.Is(e, context.Canceled) {
		e = errors.New("等待已中断；当前资源保留，不自动恢复云写")
	}
	state := "paused"
	if errors.Is(e, errScreenStop) {
		state = "stopped"
	}
	a.screenEnd(v, state, e.Error())
}
func (a *App) screenClient(ctx context.Context, p screeningPayload) (context.Context, cloud.Provider, error) {
	h, e := cloud.NewEgressClient(p.Network, p.PrivateProxyAllowlist)
	if e != nil {
		return ctx, nil, e
	}
	ns := &networkState{Config: p.Network, Revision: p.NetworkRevision, client: h}
	ctx = context.WithValue(ctx, networkContext{}, ns)
	provider, e := a.factory(ctx, p.Provider, p.Credentials)
	if e != nil {
		return ctx, nil, e
	}
	return context.WithValue(ctx, providerSnapshotKey{}, providerSnapshot{account: account{ID: p.Mutation.AccountID, Provider: p.Provider, Revision: p.Mutation.AccountRevision}, provider: provider}), provider, nil
}

type screeningIDContext struct{}

func (a *App) controlCheck(ctx context.Context, c screeningConfig, target probe.Target) (probe.Result, probe.Egress, error) {
	if c.ControlProbeID != "manager" {
		return probe.Result{}, probe.Egress{}, errors.New("远程对照必须使用持久化异步租约")
	}
	observe := a.connectivity.observe
	if observe == nil {
		observe = probe.ObserveEgress
	}
	before, e := observe(ctx)
	if e != nil || before.Error != "" || !probe.PublicIPv4(before.IP) || !probe.ValidCountry(before.Country) || before.Country == "CN" || before.Source != probe.EgressSource || time.Since(before.ObservedAt) > time.Minute {
		return probe.Result{}, before, errors.New("海外对照出口不正确或无法核实，停止自动换 IP")
	}
	check := a.connectivity.check
	if check == nil {
		check = probe.Check
	}
	result := check(ctx, target, time.Duration(c.TimeoutSeconds)*time.Second)
	after, e := observe(ctx)
	if e != nil || after.Error != "" || after.IP != before.IP || after.Country != before.Country || after.Source != probe.EgressSource {
		return result, after, errors.New("海外对照期间网络出口变化，证据失效")
	}
	return result, after, nil
}
func (a *App) screeningsAPI(w http.ResponseWriter, r *http.Request, path string) {
	if path == "" && r.Method == "GET" {
		rows, e := a.S.DB.Query("SELECT id,data,state,stage FROM screenings ORDER BY created DESC,id DESC LIMIT 20")
		if e != nil {
			fail(w, 500, "筛选记录读取失败")
			return
		}
		defer rows.Close()
		items := []screeningView{}
		for rows.Next() {
			var id, state, stage string
			var data []byte
			if rows.Scan(&id, &data, &state, &stage) != nil {
				fail(w, 500, "筛选记录损坏")
				return
			}
			var v screeningView
			if json.Unmarshal(data, &v) != nil {
				fail(w, 500, "筛选记录损坏")
				return
			}
			v.State, v.Stage = state, stage
			items = append(items, v)
		}
		if rows.Err() != nil {
			fail(w, 500, "筛选记录读取失败")
			return
		}
		reply(w, 200, map[string]any{"items": items})
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" || len(parts[0]) > 64 {
		fail(w, 404, "筛选任务不存在")
		return
	}
	v, p, e := a.screenLoad(parts[0])
	if e != nil {
		fail(w, 404, "筛选任务不存在或无法解密")
		return
	}
	if len(parts) == 1 && r.Method == "GET" {
		reply(w, 200, v)
		return
	}
	if len(parts) == 2 && parts[1] == "stop" && r.Method == "POST" {

		tx, err := a.S.DB.Begin()
		if err != nil {
			fail(w, 500, "停止请求无法保存")
			return
		}
		defer tx.Rollback()
		_, err = tx.Exec("UPDATE screenings SET stop=1,stage='停止已请求：不再提交新的云写，当前资源可能继续计费',updated=? WHERE id=?", store.Now(), v.ID)
		if err == nil {
			_, err = tx.Exec("UPDATE screenings SET state='stopped',stage='用户已停止；当前仍存资源保留，可能继续计费' WHERE id=? AND busy=0 AND state NOT IN ('unknown','accepted')", v.ID)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE jobs SET state='cancelled',progress='自动筛选已停止，当前资源保留且可能继续计费',updated=? WHERE id=? AND EXISTS(SELECT 1 FROM screenings WHERE id=? AND state='stopped')", store.Now(), v.JobID, v.ID)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(w, 500, "停止请求保存失败")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if len(parts) == 2 && parts[1] == "reconcile" && r.Method == "POST" {

		var retired, revision int
		if a.S.DB.QueryRow("SELECT retired,revision FROM accounts WHERE id=?", p.Mutation.AccountID).Scan(&retired, &revision) != nil || retired != 0 || revision != p.Mutation.AccountRevision {
			fail(w, 409, "账号已移除或凭据版本已变化，不能继续使用历史凭据核实；请到云控制台检查原资源")
			return
		}
		res, e := a.S.DB.Exec("UPDATE screenings SET busy=1 WHERE id=? AND busy=0 AND state NOT IN ('queued','creating','waiting-ready','probing','replacing')", v.ID)
		if e != nil {
			fail(w, 500, "只读核实无法开始")
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			fail(w, 409, "任务仍在执行，请停止后再只读核实")
			return
		}
		defer a.S.DB.Exec("UPDATE screenings SET busy=0 WHERE id=?", v.ID)
		if len(v.Rounds) == 0 {
			reply(w, 200, v)
			return
		}
		idx := len(v.Rounds) - 1
		round := v.Rounds[idx]
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		ctx, provider, e := a.screenClient(ctx, p)
		if e != nil {
			fail(w, 400, "原凭据或网络快照不可用")
			return
		}
		if ns, _ := ctx.Value(networkContext{}).(*networkState); ns != nil && ns.client != nil {
			defer ns.client.CloseIdleConnections()
		}
		if round.Operation.IP != nil {
			pp, ok := provider.(cloud.IPProvider)
			if !ok {
				fail(w, 400, "适配器不支持原机 IP 只读核实")
				return
			}
			reconcileCtx := cloud.WithOperationRecorder(ctx, func(checkpoint cloud.Operation) error {
				v.Rounds[idx].Operation = checkpoint
				v.Resources = mergeScreenResources(v.Resources, checkpoint)
				return a.screenSave(&v)
			})
			op, done, err := pp.ReconcileIPChange(reconcileCtx, round.Operation)
			if v.Kind == "screening" && op.IP != nil && op.IP.Checkpoint["cleanup-step"] == "deleting" {
				// The cleaner can only read in this persisted dispatched stage. Never
				// invoke its initial deletion branch from a read-only API.
				if cleaner, ok := provider.(cloud.ScreeningIPCleaner); ok {
					id, token := screeningCleanupOwner(v, op.IP.OldResourceID)
					if id != "" && id == op.IP.Checkpoint["cleanup-allowed-id"] && token == op.IP.Checkpoint["cleanup-owner-token"] {
						op, done, err = cleaner.CleanupScreeningIP(reconcileCtx, op, id, token)
					}
				}
			}
			v.Rounds[idx].Operation = op
			v.Resources = mergeScreenResources(v.Resources, op)
			if err != nil {
				v.Stage = "只读核实尚不能确认上次云写；不会重发：" + err.Error()
			} else if done && op.IP != nil && op.IP.Failure != "" {
				v.State = "failed"
				v.Stage = op.IP.Failure + "；" + op.IP.Notice
				v.Reason = v.Stage
			} else if done && op.IP != nil {
				v.IP = op.IP.NewIP
				v.Stage = "只读确认本次地址操作已完成；未自动派发其他云写或宣称国内连通性通过"
				if v.Kind == "manual-ip" && !v.SummaryBool("postCheck") {
					v.State = "accepted"
					v.Reason = v.Stage
				}
			} else {
				v.Stage = "已保存只读核实检查点；后续可能仍需云端核实，不自动恢复云写"
			}
			if a.screenSave(&v) != nil {
				fail(w, 500, "核实证据保存失败")
				return
			}
			reply(w, 200, v)
			return
		}
		sp, ok := provider.(cloud.ScreeningProvider)
		if !ok {
			fail(w, 400, "适配器不支持安全只读核实")
			return
		}
		if round.Operation.ResourceID == "" {
			v.Stage = "创建响应缺少可信资源 ID，请按本轮任务标签到云控制台核实；不会自动重发"
		} else if round.Operation.Expected == "deleted" {
			done, err := provider.Poll(ctx, round.Operation)
			if err == nil && done {
				v.Rounds[idx].State = "deleted-confirmed"
				v.Stage = "只读确认原实例已删除；自动流程仍暂停，不会创建下一台"
			} else {
				v.Stage = "尚不能只读确认删除结果；不会创建下一台"
			}
		} else {
			current, err := sp.InspectScreening(ctx, p.Mutation.Spec, round.CreateToken, round.Operation)
			if err == nil {
				v.ResourceID = current.Instance.ID
				v.Resources = mergeRelated(v.Resources, current.Resources)
				v.Stage = "只读核实当前实例仍存在；自动流程未恢复，现有资源可能继续计费"
				for _, ip := range current.Instance.IPs {
					if probe.PublicIPv4(ip) {
						v.IP = ip
						break
					}
				}
			} else {
				v.Stage = "只读核实仍无法确认目标及附属资源；请到云控制台检查"
			}
		}
		if a.screenSave(&v) != nil {
			fail(w, 500, "核实证据保存失败")
			return
		}
		reply(w, 200, v)
		return
	}
	fail(w, 405, "筛选操作不支持")
}

// Only actual connection-level failure can justify replacement. Invalid policy,
// TLS certificate, challenge, HTTP status and cancellation are insufficient.
func networkFailure(r probe.Result) bool {
	return !r.Success && (r.Classification == "timeout" || r.Classification == "connection_error")
}
func validReplacementEvidence(v screeningView, c *screeningConfig, now time.Time) error {
	if len(v.Rounds) == 0 {
		return errors.New("缺少本轮检测证据")
	}
	es := v.Rounds[len(v.Rounds)-1].Evidence
	if len(es) < c.Failures {
		return errors.New("国内连续失败证据不足，不自动换 IP")
	}
	for i, e := range es[len(es)-c.Failures:] {
		if e.Target != c.target(v.IP) || !networkFailure(e.Domestic) || !e.Control.Success || !validProbeResult(e.Domestic) || !validProbeResult(e.Control) {
			return errors.New("检测目标或失败类别无法核实")
		}
		if (i == c.Failures-1 && (now.Sub(e.Domestic.CompletedAt) > 90*time.Second || now.Sub(e.Control.CompletedAt) > 60*time.Second)) || now.Sub(e.Domestic.CompletedAt) > time.Duration(c.ReadySeconds)*time.Second || e.Domestic.CompletedAt.After(now.Add(5*time.Second)) || e.Control.CompletedAt.After(now.Add(5*time.Second)) || e.Control.StartedAt.Before(e.Domestic.CompletedAt.Add(-2*time.Second)) || e.Control.CompletedAt.Sub(e.Domestic.CompletedAt) > 60*time.Second {
			return errors.New("国内/海外检测证据已过期或不同时段，不自动换 IP")
		}
		if e.DomesticEgress.Country != "CN" || !probe.PublicIPv4(e.DomesticEgress.IP) || e.DomesticEgress.Error != "" || e.DomesticEgress.Source != probe.EgressSource || !probe.ValidCountry(e.ControlEgress.Country) || e.ControlEgress.Country == "CN" || e.ControlEgress.Error != "" || !probe.PublicIPv4(e.ControlEgress.IP) || e.ControlEgress.Source != probe.EgressSource {
			return errors.New("检测出口证据不足，不自动换 IP")
		}
	}
	return nil
}

func mergeRelated(old, extra []cloud.RelatedResource) []cloud.RelatedResource {
	out := append([]cloud.RelatedResource{}, old...)
	for _, r := range extra {
		if r.ID == "" {
			continue
		}
		found := false
		for n := range out {
			if out[n].ID == r.ID {
				out[n] = r
				found = true
				break
			}
		}
		if !found {
			out = append(out, r)
		}
	}
	return out
}
func mergeScreenResources(old []cloud.RelatedResource, op cloud.Operation) []cloud.RelatedResource {
	extra := []cloud.RelatedResource{}
	if op.ResourceID != "" {
		extra = append(extra, cloud.RelatedResource{ID: op.ResourceID, Kind: "instance", State: "存在及费用需核实"})
	}
	for _, id := range op.Owned {
		extra = append(extra, cloud.RelatedResource{ID: id, Kind: "associated", State: "存在及费用需核实"})
	}
	out := mergeRelated(old, extra)
	if op.IP != nil {
		out = mergeRelated(out, op.IP.Resources)
	}
	if op.Ledger != nil {
		out = mergeRelated(out, op.Ledger.Resources)
	}
	return out
}

func screeningStage(stage string) string {
	labels := map[string]string{
		"validated":              "正在核对换 IP 配置",
		"deleting":               "正在删除本轮实例",
		"deleted-confirmed":      "已确认本轮实例删除",
		"stopping":               "正在停止本轮实例",
		"stopped":                "已确认关机",
		"starting":               "正在重新开机",
		"creating-public-ip":     "正在创建新的公网 IP",
		"public-ip-created":      "新公网 IP 已创建，等待换绑",
		"updating-nic":           "正在为原网卡绑定新公网 IP",
		"public-ip-associated":   "新公网 IP 已绑定",
		"deleting-old-public-ip": "正在删除已解绑的旧公网 IP",
		"completed":              "本轮换 IP 操作已核实，准备重新检测",
	}
	if label, ok := labels[stage]; ok {
		return label
	}
	return "云端步骤待核实，请查看本轮操作记录"
}

func screeningEvidenceCount(v screeningView) int {
	n := 0
	for _, r := range v.Rounds {
		n += len(r.Evidence)
	}
	return n
}
