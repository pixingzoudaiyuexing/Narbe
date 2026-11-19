package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
	"time"
)

func (a *App) enqueueRemote(workflow, id string, target probe.Target, timeout time.Duration) (string, error) {
	p, e := a.getProbe(id)
	if e != nil || p.Status != "online" {
		return "", errors.New("探针离线或出口证据不足，不自动换 IP")
	}
	if e = probe.ValidateTarget(target, []int{target.Port}); e != nil {
		return "", e
	}
	task, lease := store.ID(), store.ID()
	raw, _ := json.Marshal(target)
	res, e := a.S.DB.Exec(`INSERT INTO probe_tasks(id,probe_id,screening_id,lease,target,timeout_ms,state,created,expires) SELECT ?,?,?,?,?,?,'queued',?,? WHERE EXISTS(SELECT 1 FROM screenings WHERE id=? AND stop=0 AND state IN ('waiting-ready','probing'))`, task, id, workflow, lease, raw, timeout.Milliseconds(), store.Now(), store.Now()+90, workflow)
	if e != nil {
		return "", e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return "", errScreenStop
	}
	return task, nil
}
func (a *App) remoteResult(id, pid string) (probe.Result, probe.Egress, bool, error) {
	var r probe.Result
	var eg probe.Egress
	var state string
	var raw, eraw []byte
	var expiry int64
	e := a.S.DB.QueryRow("SELECT state,result,egress,expires FROM probe_tasks WHERE id=? AND probe_id=?", id, pid).Scan(&state, &raw, &eraw, &expiry)
	if e != nil {
		return r, eg, false, errors.New("探针任务不存在")
	}
	if state == "done" {
		p, e := a.getProbe(pid)
		if e != nil || json.Unmarshal(raw, &r) != nil || json.Unmarshal(eraw, &eg) != nil || !validProbeEgress(p, eg) || !validProbeResult(r) {
			return r, eg, false, errors.New("探针回传、出口或身份无法核实")
		}
		return r, eg, true, nil
	}
	if state == "expired" || expiry < store.Now() {
		return r, eg, false, errors.New("探针离线或未及时回传，无法判断，不自动删机")
	}
	return r, eg, false, nil
}
func (a *App) enqueueControl(workflow string, c screeningConfig, target probe.Target, route bool) (string, error) {
	if c.ControlProbeID != "manager" && !route {
		return a.enqueueRemote(workflow, c.ControlProbeID, target, time.Duration(c.TimeoutSeconds)*time.Second)
	}
	id := store.ID()
	raw, _ := json.Marshal(target)
	config, _ := json.Marshal(c)
	_, e := a.S.DB.Exec("INSERT INTO screening_controls(id,screening_id,state,target,config,route_only,expires) VALUES(?,?,'queued',?,?,?,?)", id, workflow, raw, config, boolInt(route), store.Now()+90)
	return id, e
}
func (a *App) controlResult(id string) (probe.Result, probe.Egress, bool, error) {
	var r probe.Result
	var eg probe.Egress
	var state, msg string
	var raw, eraw []byte
	var expiry int64
	e := a.S.DB.QueryRow("SELECT state,result,egress,expires,error FROM screening_controls WHERE id=?", id).Scan(&state, &raw, &eraw, &expiry, &msg)
	if e != nil {
		var pid string
		if a.S.DB.QueryRow("SELECT probe_id FROM probe_tasks WHERE id=?", id).Scan(&pid) == nil {
			return a.remoteResult(id, pid)
		}
		return r, eg, false, errors.New("海外对照任务不存在")
	}
	if state == "done" {
		if msg != "" {
			return r, eg, false, errors.New(msg)
		}
		if json.Unmarshal(eraw, &eg) != nil {
			return r, eg, false, errors.New("海外出口证据损坏")
		}
		_ = json.Unmarshal(raw, &r)
		return r, eg, true, nil
	}
	if state == "expired" || expiry < store.Now() {
		return r, eg, false, errors.New("海外对照未及时完成，未据此自动换 IP")
	}
	return r, eg, false, nil
}

// Separate bounded network executors own no cloud-write slot. An offline probe
// never reserves an executor; its lease is simply checked by the next DB tick.
func (a *App) controlLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if a.runControlOne(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (a *App) runControlOne(parent context.Context) bool {
	if parent.Err() != nil {
		return false
	}
	var id string
	var targetRaw, configRaw []byte
	var route bool
	e := a.S.DB.QueryRow(`UPDATE screening_controls SET state='running' WHERE id=(SELECT c.id FROM screening_controls c JOIN screenings s ON s.id=c.screening_id WHERE c.state='queued' AND c.expires>? AND s.stop=0 AND s.state IN ('queued','waiting-ready','probing') ORDER BY c.expires,c.id LIMIT 1) AND state='queued' RETURNING id,target,config,route_only`, store.Now()).Scan(&id, &targetRaw, &configRaw, &route)
	if e != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	var target probe.Target
	var c screeningConfig
	var result probe.Result
	var eg probe.Egress
	if json.Unmarshal(targetRaw, &target) != nil || json.Unmarshal(configRaw, &c) != nil {
		e = errors.New("海外任务参数损坏")
	} else if route {
		observe := a.connectivity.observe
		if observe == nil {
			observe = probe.ObserveEgress
		}
		eg, e = observe(ctx)
		if e != nil || eg.Error != "" || !probe.PublicIPv4(eg.IP) || !probe.ValidCountry(eg.Country) || eg.Country == "CN" || eg.Source != probe.EgressSource || time.Since(eg.ObservedAt) > time.Minute || time.Since(eg.ObservedAt) < -5*time.Second {
			e = errors.New("管理端海外出口无法核实，请选择海外对照探针")
		}
	} else {
		result, eg, e = a.controlCheck(ctx, c, target)
	}
	msg := ""
	if e != nil {
		msg = e.Error()
	}
	raw, _ := json.Marshal(result)
	eraw, _ := json.Marshal(eg)
	_, _ = a.S.DB.Exec("UPDATE screening_controls SET state='done',result=?,egress=?,error=? WHERE id=? AND state='running'", raw, eraw, msg, id)
	return true
}
