package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

// Every claim advances one persisted step. Timers, probe leases and cloud state
// polling are data, never a goroutine sleeping while it owns a cloud worker.
func (a *App) runScreeningOne(parent context.Context) bool {
	if parent.Err() != nil {
		return false
	}
	var id string
	e := a.S.DB.QueryRow(`UPDATE screenings SET busy=1 WHERE id=(SELECT id FROM screenings WHERE busy=0 AND stop=0 AND state IN ('queued','creating','waiting-ready','probing','replacing') AND next_run<=? ORDER BY next_run,updated,id LIMIT 1) AND busy=0 RETURNING id`, time.Now().UnixMilli()).Scan(&id)
	if e != nil {
		return false
	}
	defer a.S.DB.Exec("UPDATE screenings SET busy=0 WHERE id=?", id)
	v, p, e := a.screenLoad(id)
	if e != nil {
		_, _ = a.S.DB.Exec("UPDATE screenings SET state='paused',stage='账本或密文损坏，未发送后续云写' WHERE id=?", id)
		return true
	}
	if p.Mutation.Screening == nil {
		a.screenEnd(&v, "failed", "检测参数缺失")
		return true
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, screeningIDContext{}, id)
	if e = a.screenGuard(ctx, &v, p); e != nil {
		a.screenPause(&v, e)
		return true
	}
	ctx, provider, e := a.screenClient(ctx, p)
	if e != nil {
		a.screenPause(&v, errors.New("原凭据或出口快照不可用"))
		return true
	}
	if ns, _ := ctx.Value(networkContext{}).(*networkState); ns != nil && ns.client != nil {
		defer ns.client.CloseIdleConnections()
	}
	if v.Kind == "" {
		v.Kind = "screening"
	}
	if v.Phase == "" {
		v.Phase = "route"
	}
	// Default next eligibility is immediate; a waiting step explicitly schedules.
	v.NextRun = time.Now().UnixMilli()
	if e = a.screenStep(ctx, &v, p, provider); e != nil {
		a.screenPause(&v, e)
	}
	return true
}
func (a *App) screenLater(v *screeningView, c *screeningConfig) error {
	d := time.Duration(c.IntervalSeconds) * time.Second
	if a.connectivity.tick > 0 {
		d = a.connectivity.tick
	}
	v.NextRun = time.Now().Add(d).UnixMilli()
	return a.screenSave(v)
}
func (a *App) screenPhase(v *screeningView, phase, state, stage string) error {
	v.Phase, v.State, v.Stage = phase, state, stage
	return a.screenSave(v)
}
func (a *App) reserveScreen(v *screeningView, c *screeningConfig) error {
	if v.ReservedNext {
		v.ReservedNext = false
		return nil
	}
	cost := int64(math.Ceil(c.CostPerAttemptUSD*100 - 1e-7))
	spent := int64(math.Round(v.EstimatedUSD * 100))
	budget := int64(math.Floor(c.BudgetUSD*100 + 1e-7))
	if v.Attempt >= c.MaxAttempts || spent+cost > budget {
		a.screenEnd(v, "exhausted", "尝试次数或费用预留估算达到上限；当前资源保留且可能计费")
		return errors.New("费用或次数上限")
	}
	v.Attempt++
	v.EstimatedUSD = float64(spent+cost) / 100
	return nil
}
func (a *App) screenOperationContext(ctx context.Context, v *screeningView, p screeningPayload) context.Context {
	save := func(op cloud.Operation) error {
		if len(v.Rounds) == 0 {
			return errors.New("操作账本缺失")
		}
		r := &v.Rounds[len(v.Rounds)-1]
		r.Operation = op
		if v.Phase == "cleanup" && v.CleanupIndex < len(v.CleanupPlans) {
			v.CleanupPlans[v.CleanupIndex] = op
		}
		if op.ResourceID != "" {
			r.ResourceID = op.ResourceID
			v.ResourceID = op.ResourceID
		}
		v.Resources = mergeScreenResources(v.Resources, op)
		if op.IP != nil {
			v.Stage = op.IP.Stage
		}
		if e := a.screenSave(v); e != nil {
			return e
		}
		return a.screenGuard(ctx, v, p)
	}
	ctx = cloud.WithOperationRecorder(ctx, save)
	return cloud.WithWriteGuard(ctx, func() error { return a.screenBeforeWrite(ctx, v, p) })
}
func (a *App) screenBeforeWrite(ctx context.Context, v *screeningView, p screeningPayload) error {
	if e := a.screenGuard(ctx, v, p); e != nil {
		return e
	}
	r := &v.Rounds[len(v.Rounds)-1]
	if v.Kind == "manual-ip" && len(r.Writes) == 0 && (p.QueuedAt > store.Now() || store.Now()-p.QueuedAt >= int64(store.MaxQueuedAge/time.Second)) {
		return cloud.ErrDispatchExpired
	}
	if (v.Phase == "create" || v.Phase == "create-advance" && r.Operation.CreateStep == "pip-put") && v.Attempt == 1 && (p.QueuedAt > store.Now() || store.Now()-p.QueuedAt >= int64(store.MaxQueuedAge/time.Second)) {
		return cloud.ErrDispatchExpired
	}
	// Evidence authorizes the first destructive replacement write. Subsequent
	// steps finish that exact operation; they never start another replacement.
	firstIPWrite := v.Phase == "ip-advance" && r.Operation.IP != nil && len(r.Operation.IP.Writes) == 1 && r.Operation.IP.Writes[0].State == "dispatching"
	if v.Kind == "screening" && (v.Phase == "replace-begin" || v.Phase == "delete" || firstIPWrite) {
		if e := validReplacementEvidence(*v, p.Mutation.Screening, time.Now()); e != nil {
			return e
		}
	}
	r.Writes = append(r.Writes, cloud.ScreeningWrite{Action: "cloud-write-dispatch", ResourceID: r.ResourceID, State: "dispatching", At: store.Now()})
	return a.screenSave(v)
}

func (a *App) screenWriteError(v *screeningView, op cloud.Operation, e error) error {
	r := &v.Rounds[len(v.Rounds)-1]
	if op.ResourceID != "" || op.IP != nil || len(op.Owned) > 0 {
		r.Operation = op
		v.ResourceID = op.ResourceID
		v.Resources = mergeScreenResources(v.Resources, op)
	}
	state, reason := "unknown", "云写结果无法核实；不会自动重发或创建下一台："+e.Error()
	if !cloud.Uncertain(e) && op.ResourceID == "" && len(op.Owned) == 0 && op.IP == nil {
		state = "failed"
		reason = "云 API 拒绝；不会重试：" + e.Error()
	}
	a.screenEnd(v, state, reason)
	return nil
}
func (a *App) screenStep(ctx context.Context, v *screeningView, p screeningPayload, provider cloud.Provider) error {
	c := p.Mutation.Screening
	switch v.Phase {
	case "route":
		if v.Kind == "screening" {
			if e := a.validateScreening(*c); e != nil {
				return e
			}
		} else if v.Kind == "check" {
			if e := a.validateReadOnlyScreening(*c); e != nil {
				return e
			}
		}
		if c.ControlProbeID == "manager" {
			if v.RouteCheck == "" {
				id, e := a.enqueueControl(v.ID, *c, probe.Target{}, true)
				if e != nil {
					return e
				}
				v.RouteCheck = id
				return a.screenLater(v, c)
			}
			_, _, done, e := a.controlResult(v.RouteCheck)
			if e != nil {
				return e
			}
			if !done {
				return a.screenLater(v, c)
			}
		}
		if v.Kind == "check" {
			return a.screenPhase(v, "inspect", "waiting-ready", "核对已有实例的当前公网 IP")
		}
		if v.Kind == "manual-ip" {
			return a.screenPhase(v, "manual-begin", "replacing", "已保存原机换 IP 授权")
		}
		if p.QueuedAt > store.Now() || store.Now()-p.QueuedAt >= int64(store.MaxQueuedAge/time.Second) {
			return errors.New("首次创建授权已过期；未创建实例")
		}
		return a.screenPhase(v, "new-round", "queued", "国内与海外出口就绪，等待创建")
	case "new-round":
		if e := a.reserveScreen(v, c); e != nil {
			return nil
		}
		token := store.Hash(v.ID + ":" + strconv.Itoa(v.Attempt))[:32]
		op := cloud.Operation{}
		if planner, ok := provider.(interface {
			Plan(cloud.Spec, string) cloud.Operation
		}); ok {
			op = planner.Plan(p.Mutation.Spec, token)
		}
		v.Rounds = append(v.Rounds, screeningRound{Attempt: v.Attempt, CreateToken: token, Created: store.Now(), Operation: op, State: "creating", EstimatedUSD: c.CostPerAttemptUSD, Evidence: []screeningEvidence{}})
		return a.screenPhase(v, "create", "creating", fmt.Sprintf("第 %d/%d 次创建：写意图已保存", v.Attempt, c.MaxAttempts))
	case "create":
		r := &v.Rounds[len(v.Rounds)-1]
		if e := a.screenGuard(ctx, v, p); e != nil {
			return e
		}
		r.Writes = append(r.Writes, cloud.ScreeningWrite{Action: "create-instance", State: "dispatching", At: store.Now()})
		if e := a.screenSave(v); e != nil {
			return e
		}
		if incremental, ok := provider.(cloud.IncrementalCreateProvider); ok {
			op, e := incremental.BeginScreeningCreate(ctx, p.Mutation.Spec, r.CreateToken)
			if e != nil {
				return a.screenWriteError(v, op, e)
			}
			r.Operation = op
			r.CreateOperation = op
			v.ResourceID = op.ResourceID
			v.Resources = mergeScreenResources(v.Resources, op)
			return a.screenPhase(v, "create-advance", "creating", "Azure 创建计划已保存，按资源逐步派发")
		}
		op, e := provider.Create(a.screenOperationContext(ctx, v, p), p.Mutation.Spec, r.CreateToken)
		if e != nil {
			return a.screenWriteError(v, op, e)
		}
		op.Startup = cloud.SummarizeStartup(p.Mutation.Spec)
		r.Operation, r.CreateOperation = op, op
		r.ResourceID = op.ResourceID
		v.ResourceID = op.ResourceID
		v.Resources = mergeScreenResources(v.Resources, op)
		v.ReadyUntil = store.Now() + int64(c.ReadySeconds)
		return a.screenPhase(v, "create-wait", "creating", "创建已受理，等待云端就绪")
	case "create-advance":
		r := &v.Rounds[len(v.Rounds)-1]
		incremental, ok := provider.(cloud.IncrementalCreateProvider)
		if !ok {
			return errors.New("增量创建适配器缺失，未重发创建")
		}
		op, done, e := incremental.AdvanceScreeningCreate(a.screenOperationContext(ctx, v, p), p.Mutation.Spec, r.CreateToken, r.Operation)
		if e != nil {
			return a.screenWriteError(v, op, e)
		}
		r.Operation = op
		r.CreateOperation = op
		v.ResourceID = op.ResourceID
		v.Resources = mergeScreenResources(v.Resources, op)
		if !done {
			v.Stage = "正在逐步创建云资源：" + map[string]string{"planned": "计划已保存", "pip-put": "等待公网 IP", "pip-read": "公网 IP 已确认", "nsg-put": "等待安全组", "nsg-read": "安全组已确认", "nic-put": "等待网卡", "nic-read": "网卡已确认", "vm-put": "等待虚拟机", "vm-poll": "核实虚拟机运行", "complete": "创建已核实"}[op.CreateStep]
			return a.screenLater(v, c)
		}
		op.Startup = cloud.SummarizeStartup(p.Mutation.Spec)
		r.Operation = op
		r.CreateOperation = op
		v.ReadyUntil = store.Now() + int64(c.ReadySeconds)
		return a.screenPhase(v, "inspect", "waiting-ready", "云资源已就绪，准备核实目标端口")

	case "create-wait":
		r := &v.Rounds[len(v.Rounds)-1]
		done, e := provider.Poll(ctx, r.Operation)
		if e != nil {
			a.screenEnd(v, "unknown", "创建已受理但状态无法核实；不重复创建")
			return nil
		}
		if !done {
			return a.screenLater(v, c)
		}
		if len(r.Writes) > 0 {
			r.Writes[len(r.Writes)-1].State = "confirmed"
		}
		return a.screenPhase(v, "inspect", "waiting-ready", "等待公网 IPv4 和指定端口就绪")
	case "inspect":
		r := &v.Rounds[len(v.Rounds)-1]
		var ip string
		if v.Kind == "screening" {
			sp, ok := provider.(cloud.ScreeningProvider)
			if !ok {
				return errors.New("云适配器缺少本轮资源身份核验")
			}
			creation := r.CreateOperation
			if creation.ResourceID == "" {
				creation = r.Operation
			}
			resource, e := a.screenOwnedResource(ctx, provider, p, *r, sp, creation)
			if e != nil {
				if store.Now() >= v.ReadyUntil {
					return fmt.Errorf("资源或归属无法核实，未自动换 IP：%w", e)
				}
				return a.screenLater(v, c)
			}
			r.Fingerprint = resource.Fingerprint
			v.Method = resource.Method
			r.Method = resource.Method
			v.Resources = mergeRelated(v.Resources, resource.Resources)
			for _, s := range resource.Instance.IPs {
				if probe.PublicIPv4(s) {
					ip = s
					break
				}
			}
			if r.Operation.IP != nil && probe.PublicIPv4(r.Operation.IP.NewIP) {
				ip = r.Operation.IP.NewIP
			}
			v.ResourceID = resource.Instance.ID
		} else {
			pp, ok := provider.(cloud.IPProvider)
			if !ok {
				return errors.New("不支持安全的已有实例 IP 查询")
			}
			i, e := exactIPInstance(ctx, provider, p.Mutation)
			if e != nil {
				return e
			}
			info, e := pp.IPInfo(ctx, i)
			if e != nil {
				return e
			}
			ip = v.IP
			found := false
			for _, x := range info.Addresses {
				if x.Address == ip {
					found = true
				}
			}
			for _, x := range info.ServiceIPv4 {
				if x == ip {
					found = true
				}
			}
			if !found {
				return errors.New("检测目标不再属于所选实例，已停止，不接受任意 IP 扫描")
			}
		}
		if ip == "" {
			if store.Now() >= v.ReadyUntil {
				return errors.New("尚无有效检测目标：实例没有可核实的公网 IPv4")
			}
			return a.screenLater(v, c)
		}
		v.IP = ip
		r.IP = ip
		r.ResourceID = v.ResourceID
		for _, old := range v.Rounds[:len(v.Rounds)-1] {
			if old.IP == ip {
				r.RepeatedIP = true
			}
		}
		v.PendingDomestic = ""
		v.PendingControl = ""
		v.Good = 0
		v.Bad = 0
		return a.screenPhase(v, "ready-dispatch", "waiting-ready", "正在从海外核实相同 IP、端口是否监听")
	case "ready-dispatch":
		id, e := a.enqueueControl(v.ID, *c, c.target(v.IP), false)
		if e != nil {
			return e
		}
		v.PendingControl = id
		return a.screenPhase(v, "ready-await", "waiting-ready", "等待海外就绪检测；云 Worker 已释放")
	case "ready-await":
		result, _, done, e := a.controlResult(v.PendingControl)
		if e != nil {
			return e
		}
		if !done {
			return a.screenLater(v, c)
		}
		r := &v.Rounds[len(v.Rounds)-1]
		r.Control = &result
		v.PendingControl = ""
		if !result.Success {
			if store.Now() >= v.ReadyUntil {
				return errors.New("尚无有效检测目标：海外端口未就绪，不自动删机")
			}
			v.Phase = "ready-dispatch"
			v.Stage = "海外也不可达，等待服务就绪，不自动换 IP"
			return a.screenLater(v, c)
		}
		return a.screenPhase(v, "domestic-dispatch", "probing", "海外端口已就绪，开始国内对照")
	case "domestic-dispatch":
		if store.Now() >= v.ReadyUntil {
			return errors.New("检测等待时间已到，证据不足，当前资源保留")
		}
		id, e := a.enqueueRemote(v.ID, c.ProbeID, c.target(v.IP), time.Duration(c.TimeoutSeconds)*time.Second)
		if e != nil {
			return e
		}
		v.PendingDomestic = id
		return a.screenPhase(v, "domestic-await", "probing", "等待国内探针回传；云 Worker 已释放")
	case "domestic-await":
		result, _, done, e := a.remoteResult(v.PendingDomestic, c.ProbeID)
		if e != nil {
			return e
		}
		if !done {
			return a.screenLater(v, c)
		}
		v.Rounds[len(v.Rounds)-1].Domestic = &result
		id, e := a.enqueueControl(v.ID, *c, c.target(v.IP), false)
		if e != nil {
			return e
		}
		v.PendingControl = id
		return a.screenPhase(v, "pair-await", "probing", "国内检测已返回，核实同期海外对照")
	case "pair-await":
		control, ceg, done, e := a.controlResult(v.PendingControl)
		if e != nil {
			return e
		}
		if !done {
			return a.screenLater(v, c)
		}
		domestic, deg, done, e := a.remoteResult(v.PendingDomestic, c.ProbeID)
		if e != nil || !done {
			return errors.New("国内检测证据失效")
		}
		r := &v.Rounds[len(v.Rounds)-1]
		r.Domestic = &domestic
		r.Control = &control
		r.Evidence = append(r.Evidence, screeningEvidence{Target: c.target(v.IP), Domestic: domestic, Control: control, DomesticEgress: deg, ControlEgress: ceg})
		v.PendingDomestic = ""
		v.PendingControl = ""
		if screeningEvidenceCount(*v) >= 600 {
			return errors.New("达到600次检测安全上限；全部历史保留，当前实例保留")
		}
		if !domestic.Success && !networkFailure(domestic) {
			return errors.New("国内检测配置、TLS/HTTP响应或出口异常，不能据此自动换 IP")
		}
		if !control.Success {
			v.Good = 0
			v.Bad = 0
			v.Stage = "海外也不可达，未判定国内被阻断"
		} else if domestic.Success {
			v.Good++
			v.Bad = 0
			v.Stage = fmt.Sprintf("国内连续成功 %d/%d", v.Good, c.Successes)
		} else {
			v.Bad++
			v.Good = 0
			v.Stage = fmt.Sprintf("海外正常、国内连续失败 %d/%d（疑似不可达）", v.Bad, c.Failures)
		}
		if v.Good >= c.Successes {
			r.State = "accepted"
			if v.Kind == "screening" && p.Provider == "azure" && r.Operation.IP != nil {
				v.CleanupPlans = screeningCleanupPlans(*v, r.Operation)
				v.CleanupIndex = 0
				if len(v.CleanupPlans) > 0 {
					return a.screenPhase(v, "cleanup", "replacing", "新 IP 国内外检测已通过，逐项核实清理本轮旧 Public IP")
				}
			}
			a.screenEnd(v, "accepted", "国内与海外对照连续通过；仅证明本次指定目标，不保证所有端口或运营商")
			return nil
		}
		if v.Bad >= c.Failures {
			if v.Kind != "screening" || !c.AutoReplace {
				a.screenEnd(v, "paused", "疑似国内不可达；当前实例保留，未授权自动更换")
				return nil
			}
			if e := validReplacementEvidence(*v, c, time.Now()); e != nil {
				return e
			}
			return a.screenPhase(v, "replace-begin", "replacing", "失败证据已保存，正在核对本轮资源与原机换 IP 能力")
		}
		v.Phase = "domestic-dispatch"
		return a.screenLater(v, c)
	case "manual-begin", "replace-begin":
		if v.Kind == "screening" {
			return a.beginAutoReplacement(ctx, v, p, provider)
		}
		pp, ok := provider.(cloud.IPProvider)
		if !ok {
			return errors.New("云适配器不支持原机换 IP")
		}
		i, e := exactIPInstance(ctx, provider, p.Mutation)
		if e != nil {
			return e
		}
		if len(v.Rounds) == 0 {
			v.Rounds = append(v.Rounds, screeningRound{Attempt: 1, ResourceID: i.ID, Created: store.Now(), Evidence: []screeningEvidence{}})
		}
		op, e := pp.BeginIPChange(a.screenOperationContext(ctx, v, p), i, *p.Mutation.IPChange, v.ID)
		if e != nil {
			return a.screenWriteError(v, op, e)
		}
		v.Rounds[len(v.Rounds)-1].Operation = op
		v.Resources = mergeScreenResources(v.Resources, op)
		return a.screenPhase(v, "ip-advance", "replacing", op.IP.Stage)
	case "ip-advance":
		pp, ok := provider.(cloud.IPProvider)
		if !ok {
			return errors.New("云适配器不支持原机换 IP")
		}
		r := &v.Rounds[len(v.Rounds)-1]
		op, done, e := pp.AdvanceIPChange(a.screenOperationContext(ctx, v, p), r.Operation)
		if e != nil {
			if v.Kind == "screening" && p.Provider == "linode" && errors.Is(e, cloud.ErrIPQuota) && !cloud.Uncertain(e) && op.IP != nil && op.IP.NewIP == "" && op.IP.NewResourceID == "" && len(op.IP.Resources) == 0 && len(op.IP.Writes) == 1 && op.IP.Writes[0].Action == "allocate-ip" && r.CreateOperation.ResourceID == v.ResourceID {
				op.IP.Writes[0].State = "rejected-quota"
				r.ReplacementHistory = append(r.ReplacementHistory, op)
				r.Operation = r.CreateOperation
				return a.screenPhase(v, "delete", "replacing", "云 API 明确拒绝额外 IPv4 配额，重新严格核验本轮新实例后删除重建")
			}
			return a.screenWriteError(v, op, e)
		}
		r.Operation = op
		v.Resources = mergeScreenResources(v.Resources, op)
		if !done {
			if op.IP != nil {
				v.Stage = op.IP.Stage
			}
			return a.screenLater(v, c)
		}
		if op.IP != nil && op.IP.Failure != "" {
			a.screenEnd(v, "failed", op.IP.Failure+"；"+op.IP.Notice)
			return nil
		}
		if op.IP == nil || !probe.PublicIPv4(op.IP.NewIP) {
			return errors.New("云端步骤完成但新的公网 IPv4 尚不能核实")
		}
		v.IP = op.IP.NewIP
		v.Method = op.IP.Method
		r.State = "replaced"
		r.RepeatedIP = op.IP.NewIP == op.IP.OldIP
		if v.Kind == "manual-ip" && !v.SummaryBool("postCheck") {
			a.screenEnd(v, "accepted", op.IP.Notice+"；原实例保留，新增或保留的地址可能持续计费")
			return nil
		}
		v.ReadyUntil = store.Now() + int64(c.ReadySeconds)
		if v.Kind == "screening" {
			v.Rounds = append(v.Rounds, screeningRound{Attempt: v.Attempt, Created: store.Now(), CreateToken: r.CreateToken, CreateOperation: r.CreateOperation, Operation: op, ResourceID: op.ResourceID, IP: v.IP, State: "waiting-ready", Evidence: []screeningEvidence{}})
		}
		return a.screenPhase(v, "inspect", "waiting-ready", "云端已核实地址变更，重新检测实际新地址")
	case "cleanup":
		r := &v.Rounds[len(v.Rounds)-1]
		cleaner, ok := provider.(cloud.ScreeningIPCleaner)
		if !ok {
			return errors.New("旧公网 IP 清理适配器缺失，保留资源")
		}
		if v.CleanupIndex >= len(v.CleanupPlans) {
			a.screenEnd(v, "accepted", "国内与海外对照通过；本轮已解绑旧 Public IP 删除已核实，其余资源继续计费")
			return nil
		}
		cleanup := v.CleanupPlans[v.CleanupIndex]
		if cleanup.IP == nil {
			return errors.New("旧公网 IP 清理账本缺失")
		}
		id, token := screeningCleanupOwner(*v, cleanup.IP.OldResourceID)
		if id == "" || id != cleanup.IP.Checkpoint["cleanup-allowed-id"] || token != cleanup.IP.Checkpoint["cleanup-owner-token"] {
			return errors.New("旧 IP 不属于本轮创建账本，保留资源")
		}
		op, done, e := cleaner.CleanupScreeningIP(a.screenOperationContext(ctx, v, p), cleanup, id, token)
		if e != nil {
			return a.screenWriteError(v, op, e)
		}
		v.CleanupPlans[v.CleanupIndex] = op
		r.Operation = op
		v.Resources = mergeScreenResources(v.Resources, op)
		if !done {
			return a.screenLater(v, c)
		}
		v.CleanupIndex++
		if v.CleanupIndex < len(v.CleanupPlans) {
			v.Stage = "已确认一个旧 Public IP 删除，继续核实下一项"
			return a.screenSave(v)
		}
		a.screenEnd(v, "accepted", "国内与海外对照通过；本轮已解绑旧 Public IP 全部删除已核实，其余资源继续计费")
		return nil

	case "delete":
		r := &v.Rounds[len(v.Rounds)-1]
		sp, ok := provider.(cloud.ScreeningProvider)
		if !ok {
			return errors.New("无法核实本轮创建归属")
		}
		creation := r.CreateOperation
		if creation.ResourceID == "" {
			creation = r.Operation
		}
		current, e := a.screenOwnedResource(ctx, provider, p, *r, sp, creation)
		if e != nil || current.Instance.ID != v.ResourceID || current.Fingerprint != r.Fingerprint {
			return errors.New("资源身份或配置改变；未删除本轮实例")
		}
		r.Operation = cloud.Operation{ResourceID: v.ResourceID, Region: v.Region, Expected: "deleted"}
		r.Writes = append(r.Writes, cloud.ScreeningWrite{Action: "delete-instance", ResourceID: v.ResourceID, State: "dispatching", At: store.Now()})
		if e = a.screenSave(v); e != nil {
			return e
		}
		op, e := provider.Action(a.screenOperationContext(ctx, v, p), current.Instance, "delete", v.ID+"-delete-"+strconv.Itoa(v.Attempt))
		if e != nil {
			return a.screenWriteError(v, op, e)
		}
		r.Operation = op
		return a.screenPhase(v, "delete-wait", "replacing", "删除已受理，必须只读确认后才创建下一台")
	case "delete-wait":
		r := &v.Rounds[len(v.Rounds)-1]
		done, e := provider.Poll(ctx, r.Operation)
		if e != nil {
			a.screenEnd(v, "unknown", "删除结果未知，不重复删除，也不创建下一台")
			return nil
		}
		if !done {
			return a.screenLater(v, c)
		}
		r.State = "deleted-confirmed"
		for n := range v.Resources {
			if v.Resources[n].ID == r.ResourceID || v.Resources[n].ID == "linode:"+r.ResourceID || v.Resources[n].ID == "droplet:"+r.ResourceID {
				v.Resources[n].State = "deleted-confirmed"
			}
		}
		v.ReservedNext = true
		// Reservation was made before deletion. new-round consumes it once.

		return a.screenPhase(v, "new-round", "queued", "已只读确认旧实例删除，等待下一次创建")
	}
	return errors.New("无法识别持久化阶段；未派发任何后续云写")
}
func (v *screeningView) SummaryBool(key string) bool { return v.Summary[key] == "true" }
func (a *App) beginAutoReplacement(ctx context.Context, v *screeningView, p screeningPayload, provider cloud.Provider) error {
	c := p.Mutation.Screening
	r := &v.Rounds[len(v.Rounds)-1]
	if e := validReplacementEvidence(*v, c, time.Now()); e != nil {
		return e
	}
	sp, ok := provider.(cloud.ScreeningProvider)
	if !ok {
		return errors.New("无法核实本轮创建归属")
	}
	creation := r.CreateOperation
	if creation.ResourceID == "" {
		creation = r.Operation
	}
	current, e := a.screenOwnedResource(ctx, provider, p, *r, sp, creation)
	if e != nil || current.Instance.ID != v.ResourceID || current.Fingerprint != r.Fingerprint {
		return errors.New("目标身份或配置已变化，未自动换 IP")
	}
	found := false
	for _, ip := range current.Instance.IPs {
		if ip == v.IP {
			found = true
		}
	}
	if !found {
		return errors.New("当前公网 IP 已变化，失败证据失效")
	}
	if e = a.reserveScreen(v, c); e != nil {
		return nil
	}
	pp, ok := provider.(cloud.IPProvider)
	if !ok {
		return errors.New("云适配器不支持增量换 IP 工作流")
	}
	info, e := pp.IPInfo(ctx, current.Instance)
	if e != nil {
		return e
	}
	preferred := map[string]string{"aws": "aws-stop-start", "azure": "azure-public-ip", "linode": "linode-allocate", "digitalocean": ""}[p.Provider]
	for _, m := range info.Methods {
		if m.ID == preferred && m.Available {
			selection := cloud.IPChange{Method: preferred, Snapshot: info.Snapshot, RiskAccepted: true}
			for _, ip := range info.Addresses {
				if ip.Primary {
					selection.InterfaceID = ip.InterfaceID
					break
				}
			}
			op, err := pp.BeginIPChange(a.screenOperationContext(ctx, v, p), current.Instance, selection, store.Hash(v.ID + ":ip:" + strconv.Itoa(v.Attempt))[:32])
			if err != nil {
				return a.screenWriteError(v, op, err)
			}
			r.Operation = op
			return a.screenPhase(v, "ip-advance", "replacing", op.IP.Stage)
		}
	}
	// Only the VM created by this workflow may be replaced. Existing-server
	// workflows never enter this branch. Provider inspection includes attachments.
	if (p.Provider == "linode" || p.Provider == "digitalocean") && r.CreateToken != "" && creation.ResourceID == v.ResourceID && r.Operation.IP == nil {
		return a.screenPhase(v, "delete", "replacing", "原机地址能力不可用；仅删除已核实的本轮新建实例")
	}
	return errors.New("当前配置没有可靠的自动换 IP 方式，实例与附属资源保留")
}

// After an in-place change the original conservative deletion inspector is no
// longer applicable (there may now be a billed additional IP). Bind further
// probes/changes to the adapter's complete, confirmed after-snapshot. Such an
// operation can never fall through to deleting a VM.
func (a *App) screenOwnedResource(ctx context.Context, provider cloud.Provider, p screeningPayload, r screeningRound, sp cloud.ScreeningProvider, creation cloud.Operation) (cloud.ScreeningResource, error) {
	if r.Operation.IP == nil {
		return sp.InspectScreening(ctx, p.Mutation.Spec, r.CreateToken, creation)
	}
	op := r.Operation.IP
	if op.After == nil || op.TargetID != creation.ResourceID || op.Region != p.Mutation.Region || op.After.InstanceID != creation.ResourceID || op.After.Snapshot == "" {
		return cloud.ScreeningResource{}, errors.New("本轮原机换 IP 的确认快照缺失；不会删除实例")
	}
	pp, ok := provider.(cloud.IPProvider)
	if !ok {
		return cloud.ScreeningResource{}, errors.New("缺少原机地址核实接口")
	}
	b := p.Mutation
	b.Target = op.TargetID
	b.Name = p.Mutation.Spec.Name
	i, e := exactIPInstance(ctx, provider, b)
	if e != nil {
		return cloud.ScreeningResource{}, e
	}
	info, e := pp.IPInfo(ctx, i)
	if e != nil {
		return cloud.ScreeningResource{}, e
	}
	if info.Snapshot != op.After.Snapshot || info.InstanceID != op.TargetID || info.Region != op.Region {
		return cloud.ScreeningResource{}, errors.New("原机配置或绑定在检测期间改变；不自动换 IP")
	}
	ips := []string{}
	for _, ip := range info.Addresses {
		ips = append(ips, ip.Address)
	}
	ips = append(ips, info.ServiceIPv4...)
	i.IPs = ips
	return cloud.ScreeningResource{Instance: i, Fingerprint: info.Snapshot, Method: op.Method, Resources: op.Resources}, nil
}

// IDs and owner tokens come only from this workflow's encrypted creation / IP
// ledger. The cleaner independently proves live tags, reverse links and no use.
func screeningCleanupOwner(v screeningView, oldID string) (string, string) {
	if v.Kind != "screening" || oldID == "" {
		return "", ""
	}
	for _, r := range v.Rounds {
		if r.CreateToken != "" {
			for _, id := range r.CreateOperation.Owned {
				if id == oldID {
					return id, r.CreateToken
				}
			}
		}
		if ip := r.Operation.IP; ip != nil && ip.NewResourceID == oldID && ip.RequestID != "" {
			return oldID, ip.RequestID
		}
	}
	return "", ""
}

func screeningCleanupPlans(v screeningView, current cloud.Operation) []cloud.Operation {
	plans := []cloud.Operation{}
	seen := map[string]bool{}
	if current.IP == nil {
		return plans
	}
	for _, r := range v.Rounds {
		ip := r.Operation.IP
		if ip == nil || ip.OldResourceID == "" || ip.OldResourceID == current.IP.NewResourceID || !probe.PublicIPv4(ip.OldIP) || seen[ip.OldResourceID] {
			continue
		}
		id, token := screeningCleanupOwner(v, ip.OldResourceID)
		if id == "" {
			continue
		}
		// Each cleanup retains the latest verified VM/NIC/new-PIP binding. Its old
		// target is derived from an earlier immutable round, never from user input.
		raw, _ := json.Marshal(current)
		var plan cloud.Operation
		if json.Unmarshal(raw, &plan) != nil {
			continue
		}
		plan.IP.OldResourceID = id
		plan.IP.OldIP = ip.OldIP
		if plan.IP.Checkpoint == nil {
			plan.IP.Checkpoint = map[string]string{}
		}
		delete(plan.IP.Checkpoint, "cleanup-step")
		plan.IP.Checkpoint["cleanup-allowed-id"] = id
		plan.IP.Checkpoint["cleanup-owner-token"] = token
		plan.IP.Resources = mergeRelated(plan.IP.Resources, []cloud.RelatedResource{{ID: id, Kind: "public-ip", State: "detached-pending-verification"}})
		plans = append(plans, plan)
		seen[id] = true
	}
	return plans
}
