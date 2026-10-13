package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

func (a *App) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if a.runOne(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-a.wake:
		case <-time.After(3 * time.Second):
		}
	}
}
func (a *App) finish(id, state, progress string, op cloud.Operation, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	var result []byte
	if op.ResourceID != "" {
		result, _ = json.Marshal(op)
	}
	_, _ = a.S.DB.Exec("UPDATE jobs SET state=?,progress=?,result=COALESCE(?,result),error=?,updated=? WHERE id=? AND state='running'", state, progress, result, msg, store.Now(), id)
}
func (a *App) runOne(parent context.Context) bool {
	var id string
	var payload []byte
	if e := a.S.DB.QueryRow("SELECT id,payload FROM jobs WHERE state='queued' ORDER BY created,id LIMIT 1").Scan(&id, &payload); e != nil {
		return false
	}
	res, e := a.S.DB.Exec("UPDATE jobs SET state='running',progress='正在核实配置',updated=? WHERE id=? AND state='queued'", store.Now(), id)
	if e != nil {
		return false
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return true
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Minute)
	defer cancel()
	raw, e := a.S.Unseal(payload, "job:"+id)
	if e != nil {
		a.finish(id, "failed", "任务数据无法解密", cloud.Operation{}, nil)
		return true
	}
	var b mutation
	if e = json.Unmarshal(raw, &b); e != nil {
		a.finish(id, "failed", "任务数据损坏", cloud.Operation{}, nil)
		return true
	}
	if e = a.validate(ctx, b); e != nil {
		a.finish(id, "failed", "配置检查未通过；未发送云操作", cloud.Operation{}, e)
		return true
	}
	acc, p, e := a.provider(b.AccountID)
	if e != nil {
		a.finish(id, "failed", "云账号无法读取", cloud.Operation{}, e)
		return true
	}
	if b.Action == "create" && acc.Provider == "linode" && b.Spec.Key != "" {
		c, e := p.Catalog(ctx, b.Region, b.Spec.Plan)
		if e != nil {
			a.finish(id, "failed", "SSH 密钥检查失败", cloud.Operation{}, e)
			return true
		}
		k, _ := cloud.Find(c.Keys, b.Spec.Key)
		b.Spec.SSHPublicKey = k.Extra["publicKey"]
	}
	_, e = a.S.DB.Exec("UPDATE jobs SET progress='正在向云服务商提交操作',updated=? WHERE id=?", store.Now(), id)
	if e != nil {
		a.finish(id, "failed", "任务记录保存失败，未发送操作", cloud.Operation{}, nil)
		return true
	}
	var op cloud.Operation
	if planner, ok := p.(interface {
		Plan(cloud.Spec, string) cloud.Operation
	}); ok && b.Action == "create" {
		op = planner.Plan(b.Spec, id)
		result, _ := json.Marshal(op)
		if _, e := a.S.DB.Exec("UPDATE jobs SET result=? WHERE id=? AND state='running'", result, id); e != nil {
			a.finish(id, "failed", "资源计划保存失败，未发送操作", cloud.Operation{}, nil)
			return true
		}
	}
	if b.Action == "create" {
		op, e = p.Create(ctx, b.Spec, id)
	} else {
		op, e = p.Action(ctx, cloud.Instance{ID: b.Target, Name: b.Name, Region: b.Region}, b.Action, id)
	}
	if e != nil {
		state := "failed"
		progress := "云端拒绝操作"
		if cloud.Uncertain(e) || len(op.Owned) > 0 {
			state = "unknown"
			progress = "操作结果待核实；请勿重复提交"
		}
		a.finish(id, state, progress, op, e)
		return true
	}
	result, _ := json.Marshal(op)
	if _, e = a.S.DB.Exec("UPDATE jobs SET result=?,progress='云端已受理，等待目标状态',updated=? WHERE id=? AND state='running'", result, store.Now(), id); e != nil {
		a.finish(id, "unknown", "云端已受理但结果记录失败；请核实", op, nil)
		return true
	}
	if b.Action == "reboot" && (acc.Provider == "aws" || acc.Provider == "linode" || op.Expected == "accepted") {
		a.finish(id, "succeeded", "重启请求已受理；不代表来宾系统已完成重启", op, nil)
		return true
	}
	for {
		done, e := p.Poll(ctx, op)
		if e != nil {
			a.finish(id, "unknown", "无法核实最终状态，请检查云控制台", op, e)
			return true
		}
		if done {
			a.finish(id, "succeeded", "云端目标状态已达到", op, nil)
			return true
		}
		select {
		case <-ctx.Done():
			a.finish(id, "unknown", "等待已中断或超时；系统不会自动重复操作", op, nil)
			return true
		case <-time.After(4 * time.Second):
		}
	}
}
