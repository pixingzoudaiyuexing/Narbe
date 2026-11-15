package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
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
	Writes       []cloud.ScreeningWrite `json:"writes"`
	Attempt      int                    `json:"attempt"`
	CreateToken  string                 `json:"createToken"`
	ResourceID   string                 `json:"resourceId"`
	IP           string                 `json:"ip"`
	Fingerprint  string                 `json:"fingerprint"`
	Method       string                 `json:"method"`
	State        string                 `json:"state"`
	Created      int64                  `json:"created"`
	Operation    cloud.Operation        `json:"operation"`
	Domestic     *probe.Result          `json:"domestic,omitempty"`
	Control      *probe.Result          `json:"control,omitempty"`
	EstimatedUSD float64                `json:"estimatedUSD"`
	RepeatedIP   bool                   `json:"repeatedIP"`
	Evidence     []screeningEvidence    `json:"evidence"`
}
type screeningEvidence struct {
	Target         probe.Target `json:"target"`
	Domestic       probe.Result `json:"domestic"`
	Control        probe.Result `json:"control"`
	DomesticEgress probe.Egress `json:"domesticEgress"`
	ControlEgress  probe.Egress `json:"controlEgress"`
}
type screeningView struct {
	ReservedNext bool                    `json:"reservedNext"`
	ID           string                  `json:"id"`
	JobID        string                  `json:"jobId"`
	Provider     string                  `json:"provider"`
	Region       string                  `json:"region"`
	State        string                  `json:"state"`
	Stage        string                  `json:"stage"`
	Attempt      int                     `json:"attempt"`
	MaxAttempts  int                     `json:"maxAttempts"`
	IP           string                  `json:"ip"`
	ResourceID   string                  `json:"resourceId"`
	Reason       string                  `json:"reason"`
	Started      int64                   `json:"started"`
	Updated      int64                   `json:"updated"`
	EstimatedUSD float64                 `json:"estimatedUSD"`
	BudgetUSD    float64                 `json:"budgetUSD"`
	Method       string                  `json:"method"`
	Rounds       []screeningRound        `json:"rounds"`
	Resources    []cloud.RelatedResource `json:"resources"`
	Summary      map[string]string       `json:"summary"`
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
	v := screeningView{ID: id, JobID: id, Provider: acc.Provider, Region: b.Region, State: "queued", Stage: "等待自动筛选", MaxAttempts: c.MaxAttempts, BudgetUSD: c.BudgetUSD, Started: store.Now(), Updated: store.Now(), Rounds: []screeningRound{}, Resources: []cloud.RelatedResource{}, Summary: map[string]string{"name": b.Spec.Name, "plan": b.Spec.Plan, "image": b.Spec.Image, "pricing": "费用为预留估算，不代表真实最终账单；仍存在的实例和关联资源会继续计费"}}
	data, _ := json.Marshal(v)
	_, e = tx.Exec("INSERT INTO screenings(id,job_id,state,stage,payload,data,created,updated) VALUES(?,?,'queued',?,?,?,?,?)", id, id, v.Stage, sealed, data, v.Started, v.Updated)
	if e == nil {
		_, e = tx.Exec("UPDATE jobs SET state='screening',progress='国内连通性自动筛选：等待创建',updated=? WHERE id=? AND state='running'", store.Now(), id)
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
	_, e = tx.Exec("UPDATE screenings SET state=?,stage=?,data=?,updated=? WHERE id=?", v.State, v.Stage, data, v.Updated, v.ID)
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
func (a *App) runScreeningOne(parent context.Context) bool {
	var id string
	e := a.S.DB.QueryRow("UPDATE screenings SET busy=1 WHERE id=(SELECT id FROM screenings WHERE state='queued' AND busy=0 AND stop=0 ORDER BY created,id LIMIT 1) AND busy=0 AND state='queued' RETURNING id").Scan(&id)
	if e != nil {
		return false
	}
	defer a.S.DB.Exec("UPDATE screenings SET busy=0 WHERE id=?", id)
	v, p, e := a.screenLoad(id)
	if e != nil {
		_, _ = a.S.DB.Exec("UPDATE screenings SET state='paused',stage='筛选账本或密文损坏，未派发后续云写' WHERE id=?", id)
		return true
	}
	c := p.Mutation.Screening
	if c == nil {
		a.screenEnd(&v, "failed", "筛选参数缺失")
		return true
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(c.MaxDurationSeconds)*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, screeningIDContext{}, id)
	ctx, provider, e := a.screenClient(ctx, p)
	if e != nil {
		a.screenEnd(&v, "paused", "网络或凭据快照不可用")
		return true
	}
	sp, ok := provider.(cloud.ScreeningProvider)
	if !ok {
		a.screenEnd(&v, "failed", "云适配器不支持安全的本轮资源识别，未创建实例")
		return true
	}
	if e = a.validateScreening(*c); e != nil {
		a.screenPause(&v, e)
		return true
	}
	if p.QueuedAt > store.Now() || p.QueuedAt <= store.Now()-int64(store.MaxQueuedAge/time.Second) {
		a.screenEnd(&v, "paused", "创建授权已过期，未创建实例")
		return true
	}
	// Check both routes before incurring the first cloud charge.
	if e = a.screenRouteReady(ctx, *c); e != nil {
		a.screenPause(&v, e)
		return true
	}
	var op cloud.Operation
	var token string
	needCreate := true
	for attempt := 1; attempt <= c.MaxAttempts; attempt++ {
		if e = a.screenGuard(ctx, &v, p); e != nil {
			a.screenPause(&v, e)
			return true
		}
		cents := int64(math.Ceil(c.CostPerAttemptUSD*100 - 1e-7))
		spent := int64(math.Round(v.EstimatedUSD * 100))
		budget := int64(math.Floor(c.BudgetUSD*100 + 1e-7))
		if !v.ReservedNext && spent+cents > budget {
			a.screenEnd(&v, "exhausted", "费用预留估算达到预算上限；仍存资源未自动删除")
			return true
		}
		if !v.ReservedNext {
			v.EstimatedUSD = float64(spent+cents) / 100
		}
		v.ReservedNext = false
		v.Attempt = attempt
		if needCreate {
			token = store.Hash(id + ":" + strconv.Itoa(attempt))[:32]
			op = cloud.Operation{}
			if planner, ok := provider.(interface {
				Plan(cloud.Spec, string) cloud.Operation
			}); ok {
				op = planner.Plan(p.Mutation.Spec, token)
			}
		}
		round := screeningRound{Attempt: attempt, CreateToken: token, Created: store.Now(), Operation: op, State: "creating", EstimatedUSD: float64(cents) / 100, Evidence: []screeningEvidence{}}
		v.Resources = mergeScreenResources(v.Resources, op)
		v.Rounds = append(v.Rounds, round)
		idx := len(v.Rounds) - 1
		saveOp := func(checkpoint cloud.Operation) error {
			v.Rounds[idx].Operation = checkpoint
			v.Rounds[idx].ResourceID = checkpoint.ResourceID
			v.ResourceID = checkpoint.ResourceID
			v.Resources = mergeScreenResources(v.Resources, checkpoint)
			if checkpoint.Screening != nil {
				v.Stage = screeningStage(checkpoint.Screening.Stage)
			}
			if e := a.screenSave(&v); e != nil {
				return e
			}
			if e := a.screenGuard(ctx, &v, p); e != nil {
				return e
			}
			if checkpoint.Screening != nil && len(checkpoint.Screening.Writes) > 0 {
				last := checkpoint.Screening.Writes[len(checkpoint.Screening.Writes)-1]
				if last.State == "dispatching" && (last.Action == "delete-instance" || last.Action == "stop-instance" || last.Action == "create-public-ip" || last.Action == "associate-public-ip") {
					if e := validReplacementEvidence(v, c, time.Now()); e != nil {
						return e
					}
				}
			}
			return nil
		}
		operationCtx := cloud.WithOperationRecorder(ctx, saveOp)
		operationCtx = cloud.WithWriteGuard(operationCtx, func() error {
			if e := a.screenGuard(ctx, &v, p); e != nil {
				return e
			}
			if attempt == 1 && len(v.Rounds[idx].Writes) <= 1 && (p.QueuedAt > store.Now() || p.QueuedAt <= store.Now()-int64(store.MaxQueuedAge/time.Second)) {
				return cloud.ErrDispatchExpired
			}
			checkpoint := v.Rounds[idx].Operation
			if checkpoint.Screening != nil && len(checkpoint.Screening.Writes) > 0 {
				last := checkpoint.Screening.Writes[len(checkpoint.Screening.Writes)-1]
				if last.State == "dispatching" && (last.Action == "delete-instance" || last.Action == "stop-instance" || last.Action == "create-public-ip" || last.Action == "associate-public-ip") {
					if e := validReplacementEvidence(v, c, time.Now()); e != nil {
						return e
					}
				}
			}
			v.Rounds[idx].Writes = append(v.Rounds[idx].Writes, cloud.ScreeningWrite{Action: "cloud-write-dispatch", ResourceID: checkpoint.ResourceID, State: "dispatching", At: store.Now()})
			if e := a.screenSave(&v); e != nil {
				return e
			}
			return a.screenGuard(ctx, &v, p)
		})
		if needCreate {
			v.State, v.Stage = "creating", "正在创建第 "+strconv.Itoa(attempt)+" 轮服务器（写意图已保存）"
			if e = a.screenSave(&v); e != nil {
				return true
			}
			if e = a.screenGuard(ctx, &v, p); e != nil {
				a.screenPause(&v, e)
				return true
			}
			v.Rounds[idx].Writes = append(v.Rounds[idx].Writes, cloud.ScreeningWrite{Action: "create-instance", ResourceID: op.ResourceID, State: "dispatching", At: store.Now()})
			if e = a.screenSave(&v); e != nil {
				return true
			}
			if e = a.screenGuard(ctx, &v, p); e != nil {
				a.screenPause(&v, e)
				return true
			}
			op, e = provider.Create(operationCtx, p.Mutation.Spec, token)
			if e != nil {
				if op.ResourceID != "" || len(op.Owned) > 0 {
					v.Rounds[idx].Operation = op
				}
				v.ResourceID = v.Rounds[idx].Operation.ResourceID
				v.Resources = mergeScreenResources(v.Resources, v.Rounds[idx].Operation)
				a.screenEnd(&v, "unknown", "创建结果无法完整核实；不会重发或创建下一台，请只读核实云端")
				return true
			}
			if e = saveOp(op); e != nil {
				a.screenPause(&v, e)
				return true
			}
			op.Startup = cloud.SummarizeStartup(p.Mutation.Spec)
			if e = a.screenPoll(ctx, &v, p, provider, op); e != nil {
				a.screenEnd(&v, "unknown", "创建已受理但最终状态未知；不会继续云写")
				return true
			}
			if len(v.Rounds[idx].Writes) > 0 {
				v.Rounds[idx].Writes[len(v.Rounds[idx].Writes)-1].State = "confirmed"
			}
		}
		v.State, v.Stage = "waiting-ready", "等待公网 IPv4 和指定端口服务就绪"
		v.Rounds[idx].State = v.State
		if a.screenSave(&v) != nil {
			return true
		}
		var resource cloud.ScreeningResource
		readyDeadline := time.Now().Add(time.Duration(c.ReadySeconds) * time.Second)
		for {
			if e = a.screenGuard(ctx, &v, p); e != nil {
				a.screenPause(&v, e)
				return true
			}
			resource, e = sp.InspectScreening(ctx, p.Mutation.Spec, token, op)
			if e != nil {
				if time.Now().After(readyDeadline) {
					a.screenEnd(&v, "paused", "资源尚未就绪或身份无法核实；未自动换 IP："+e.Error())
					return true
				}
				if !a.screenWait(ctx, time.Duration(c.IntervalSeconds)*time.Second) {
					a.screenPause(&v, ctx.Err())
					return true
				}
				continue
			}
			ip := ""
			for _, s := range resource.Instance.IPs {
				if probe.PublicIPv4(s) {
					ip = s
					break
				}
			}
			if ip != "" {
				v.IP = ip
				v.ResourceID = resource.Instance.ID
				v.Method = resource.Method
				v.Resources = mergeRelated(v.Resources, resource.Resources)
				v.Rounds[idx].IP = ip
				v.Rounds[idx].ResourceID = v.ResourceID
				v.Rounds[idx].Method = v.Method
				v.Rounds[idx].Fingerprint = resource.Fingerprint
				for _, prior := range v.Rounds[:idx] {
					if prior.IP == ip {
						v.Rounds[idx].RepeatedIP = true
					}
				}
				if a.screenSave(&v) != nil {
					return true
				}
				control, _, err := a.controlCheck(ctx, *c, c.target(ip))
				if err != nil {
					a.screenPause(&v, err)
					return true
				}
				v.Rounds[idx].Control = &control
				if control.Success {
					break
				}
			}
			if time.Now().After(readyDeadline) {
				a.screenEnd(&v, "paused", "尚无有效检测目标：海外对照端口未就绪或没有公网 IPv4；不会自动删机")
				return true
			}
			if !a.screenWait(ctx, time.Duration(c.IntervalSeconds)*time.Second) {
				a.screenPause(&v, ctx.Err())
				return true
			}
		}
		v.State, v.Stage = "probing", "正在执行国内与海外同 IP、同端口对照检测"
		v.Rounds[idx].State = v.State
		if a.screenSave(&v) != nil {
			return true
		}
		good, bad := 0, 0
		for {
			if e = a.screenGuard(ctx, &v, p); e != nil {
				a.screenPause(&v, e)
				return true
			}
			target := c.target(v.IP)
			domestic, deg, err := a.remoteCheck(ctx, v.ID, c.ProbeID, target, time.Duration(c.TimeoutSeconds)*time.Second)
			if err != nil {
				a.screenPause(&v, err)
				return true
			}
			control, ceg, err := a.controlCheck(ctx, *c, target)
			if err != nil {
				a.screenPause(&v, err)
				return true
			}
			v.Rounds[idx].Domestic = &domestic
			v.Rounds[idx].Control = &control
			evidence := screeningEvidence{Target: target, Domestic: domestic, Control: control, DomesticEgress: deg, ControlEgress: ceg}
			v.Rounds[idx].Evidence = append(v.Rounds[idx].Evidence, evidence)
			if screeningEvidenceCount(v) >= 600 {
				a.screenEnd(&v, "paused", "达到整轮600次检测的安全上限，全部历史已保留；当前实例保留")
				return true
			}
			if !domestic.Success && !networkFailure(domestic) {
				a.screenEnd(&v, "paused", "国内检测配置、TLS/HTTP应用响应或出口证据异常，不能据此判定IP不可达；不自动换 IP")
				return true
			}
			if !control.Success {
				good, bad = 0, 0
				v.Stage = "海外也不可达：服务器或端口尚未就绪，不自动换 IP"
			} else if domestic.Success {
				good++
				bad = 0
				v.Stage = fmt.Sprintf("国内连续成功 %d/%d", good, c.Successes)
			} else {
				bad++
				good = 0
				v.Stage = fmt.Sprintf("海外正常、国内连续失败 %d/%d（仅为疑似国内不可达）", bad, c.Failures)
			}
			if a.screenSave(&v) != nil {
				return true
			}
			if good >= c.Successes {
				if e = a.screenGuard(ctx, &v, p); e != nil {
					a.screenPause(&v, e)
					return true
				}
				v.Rounds[idx].State = "accepted"
				a.screenEnd(&v, "accepted", "国内与海外对照连续通过；当前实例已保留，仍持续计费")
				return true
			}
			if bad >= c.Failures {
				break
			}
			if time.Now().After(readyDeadline) {
				a.screenEnd(&v, "paused", "检测等待时间已到，证据不足；当前实例保留，不自动删除")
				return true
			}
			if !a.screenWait(ctx, time.Duration(c.IntervalSeconds)*time.Second) {
				a.screenPause(&v, ctx.Err())
				return true
			}
		}
		if !c.AutoReplace {
			a.screenEnd(&v, "paused", "疑似国内不可达；自动更换未启用，实例保留")
			return true
		}
		if attempt >= c.MaxAttempts || int64(math.Round(v.EstimatedUSD*100))+cents > budget {
			a.screenEnd(&v, "exhausted", "尝试次数或费用预留估算上限已达到；当前实例保留")
			return true
		}
		if e = a.screenGuard(ctx, &v, p); e != nil {
			a.screenPause(&v, e)
			return true
		}
		// Refresh the route and exact live identity immediately before destructive work.
		if e = a.screenRouteReady(ctx, *c); e != nil {
			a.screenPause(&v, e)
			return true
		}
		current, e := sp.InspectScreening(ctx, p.Mutation.Spec, token, op)
		if e != nil || current.Fingerprint != resource.Fingerprint || current.Instance.ID != resource.Instance.ID {
			a.screenEnd(&v, "paused", "目标身份或配置已变化；未自动删除或换 IP")
			return true
		}
		liveIP := ""
		for _, ip := range current.Instance.IPs {
			if probe.PublicIPv4(ip) {
				liveIP = ip
				break
			}
		}
		if liveIP != v.IP {
			a.screenEnd(&v, "paused", "公网 IP 在检测期间改变，旧证据失效；未自动换 IP")
			return true
		}
		if e = validReplacementEvidence(v, c, time.Now()); e != nil {
			a.screenPause(&v, e)
			return true
		}
		v.EstimatedUSD += float64(cents) / 100
		v.ReservedNext = true
		v.State, v.Stage = "replacing", "正在按云厂商策略更换本轮公网 IP"
		v.Rounds[idx].State = "replacing"
		if a.screenSave(&v) != nil {
			return true
		}
		op, e = sp.ReplaceScreeningIP(operationCtx, p.Mutation.Spec, token, op, resource.Fingerprint)
		if op.ResourceID != "" || len(op.Owned) > 0 {
			v.Rounds[idx].Operation = op
		}
		if op.Ledger != nil {
			v.Resources = mergeRelated(v.Resources, op.Ledger.Resources)
		}
		if e != nil {
			a.screenEnd(&v, "unknown", "换 IP 或删除步骤结果待核实；不会重放，也不会创建下一台："+e.Error())
			return true
		}
		v.Rounds[idx].State = "replaced"
		if op.Expected == "deleted" {
			v.Rounds[idx].State = "deleted-confirmed"
			needCreate = true
		} else {
			needCreate = false
		}
		if a.screenSave(&v) != nil {
			return true
		}
		if !a.screenWait(ctx, time.Duration(c.IntervalSeconds)*time.Second) {
			a.screenPause(&v, ctx.Err())
			return true
		}
	}
	return true
}
func (a *App) screenWait(ctx context.Context, d time.Duration) bool {
	if a.connectivity.tick > 0 {
		d = a.connectivity.tick
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (a *App) screenPoll(ctx context.Context, v *screeningView, p screeningPayload, provider cloud.Provider, op cloud.Operation) error {
	for {
		if e := a.screenGuard(ctx, v, p); e != nil {
			return e
		}
		done, e := provider.Poll(ctx, op)
		if e != nil {
			return e
		}
		if done {
			return nil
		}
		if !a.screenWait(ctx, 4*time.Second) {
			return ctx.Err()
		}
	}
}

type screeningIDContext struct{}

func (a *App) screenRouteReady(ctx context.Context, c screeningConfig) error {
	p, e := a.getProbe(c.ProbeID)
	if e != nil || p.Role != "domestic" || p.Status != "online" {
		return errors.New("国内探针离线或出口不正确，无法判断；暂停自动筛选")
	}
	if c.ControlProbeID != "manager" {
		q, e := a.getProbe(c.ControlProbeID)
		if e != nil || q.Role != "control" || q.Status != "online" {
			return errors.New("海外对照探针离线或出口无法核实")
		}
		return nil
	}
	observe := a.connectivity.observe
	if observe == nil {
		observe = probe.ObserveEgress
	}
	egr, e := observe(ctx)
	if e != nil || !probe.PublicIPv4(egr.IP) || !probe.ValidCountry(egr.Country) || egr.Country == "CN" || egr.Source != probe.EgressSource || time.Since(egr.ObservedAt) > time.Minute {
		return errors.New("管理端海外对照出口无法核实，请选择海外探针；未据此自动删机")
	}
	return nil
}
func (a *App) controlCheck(ctx context.Context, c screeningConfig, target probe.Target) (probe.Result, probe.Egress, error) {
	if c.ControlProbeID != "manager" {
		id, _ := ctx.Value(screeningIDContext{}).(string)
		return a.remoteCheck(ctx, id, c.ControlProbeID, target, time.Duration(c.TimeoutSeconds)*time.Second)
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
func (a *App) remoteCheck(ctx context.Context, screeningID, probeID string, target probe.Target, timeout time.Duration) (probe.Result, probe.Egress, error) {
	p, e := a.getProbe(probeID)
	if e != nil || p.Status != "online" {
		return probe.Result{}, probe.Egress{}, errors.New("探针离线或出口证据不足；不自动换 IP")
	}
	if e = probe.ValidateTarget(target, []int{target.Port}); e != nil {
		return probe.Result{}, probe.Egress{}, e
	}
	id, lease := store.ID(), store.ID()
	raw, _ := json.Marshal(target)
	deadline := time.Now().Add(90 * time.Second)
	_, e = a.S.DB.Exec("INSERT INTO probe_tasks(id,probe_id,screening_id,lease,target,timeout_ms,state,created,expires) SELECT ?,?,?,?,?,?,'queued',?,? WHERE EXISTS(SELECT 1 FROM screenings WHERE id=? AND stop=0 AND state IN ('waiting-ready','probing'))", id, probeID, screeningID, lease, raw, timeout.Milliseconds(), store.Now(), deadline.Unix(), screeningID)
	if e != nil {
		return probe.Result{}, probe.Egress{}, errors.New("探针任务无法保存")
	}
	defer a.S.DB.Exec("UPDATE probe_tasks SET state='expired' WHERE id=? AND state IN ('queued','leased')", id)
	for {
		var state string
		var rr, eg []byte
		e = a.S.DB.QueryRow("SELECT state,result,egress FROM probe_tasks WHERE id=?", id).Scan(&state, &rr, &eg)
		if e != nil {
			return probe.Result{}, probe.Egress{}, errors.New("探针任务不存在或已停止")
		}
		if state == "done" {
			var result probe.Result
			var egress probe.Egress
			if json.Unmarshal(rr, &result) != nil || json.Unmarshal(eg, &egress) != nil || !validProbeEgress(p, egress) {
				return result, egress, errors.New("探针出口或检测结果无法核实")
			}
			return result, egress, nil
		}
		var stop int
		if a.S.DB.QueryRow("SELECT stop FROM screenings WHERE id=?", screeningID).Scan(&stop) != nil || stop != 0 {
			return probe.Result{}, probe.Egress{}, errScreenStop
		}
		if state == "expired" || time.Now().After(deadline) {
			return probe.Result{}, probe.Egress{}, errors.New("探针离线或没有及时回传证据；不会自动删机")
		}
		if !a.screenWait(ctx, time.Second) {
			return probe.Result{}, probe.Egress{}, ctx.Err()
		}
	}
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
