package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/probe"
	"github.com/pixingzoudaiyuexing/Narbe/internal/store"
)

type connectivityRuntime struct {
	mu       sync.Mutex
	geoCache map[string]probe.Egress
	// Test seams never exposed in a configuration, request or environment variable.
	lookup  func(context.Context, string) (probe.Egress, error)
	observe func(context.Context) (probe.Egress, error)
	check   func(context.Context, probe.Target, time.Duration) probe.Result
	tick    time.Duration
}
type probeView struct {
	RecentChecks    int          `json:"recentChecks"`
	RecentSuccesses int          `json:"recentSuccesses"`
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Role            string       `json:"role"`
	ExpectedIP      string       `json:"expectedIP"`
	Enabled         bool         `json:"enabled"`
	LastSeen        int64        `json:"lastSeen"`
	Version         string       `json:"version"`
	Egress          probe.Egress `json:"egress"`
	Checks          int          `json:"checks"`
	Successes       int          `json:"successes"`
	Status          string       `json:"status"`
	LastCheckAt     string       `json:"lastCheckAt"`
}

func (a *App) probeViews() ([]probeView, error) {

	rows, e := a.S.DB.Query(`SELECT id,name,role,expected_ip,enabled,last_seen,version,egress,checks,successes,
      COALESCE((SELECT json_extract(t.result,'$.completedAt') FROM probe_tasks t WHERE t.probe_id=p.id AND t.state='done' ORDER BY t.created DESC,t.id DESC LIMIT 1),''),
      (SELECT count(*) FROM (SELECT id FROM probe_tasks t WHERE t.probe_id=p.id AND t.state='done' ORDER BY t.created DESC,t.id DESC LIMIT 100)),
      COALESCE((SELECT sum(CASE WHEN json_extract(result,'$.success')=1 AND COALESCE(json_extract(egress,'$.error'),'')='' THEN 1 ELSE 0 END) FROM (SELECT result,egress FROM probe_tasks t WHERE t.probe_id=p.id AND t.state='done' ORDER BY t.created DESC,t.id DESC LIMIT 100)),0)
      FROM connectivity_probes p ORDER BY name,id`)

	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []probeView{}
	for rows.Next() {
		var p probeView
		var raw []byte
		if e = rows.Scan(&p.ID, &p.Name, &p.Role, &p.ExpectedIP, &p.Enabled, &p.LastSeen, &p.Version, &raw, &p.Checks, &p.Successes, &p.LastCheckAt, &p.RecentChecks, &p.RecentSuccesses); e != nil {
			return nil, e
		}
		_ = json.Unmarshal(raw, &p.Egress)
		p.Status = "offline"
		if !p.Enabled {
			p.Status = "disabled"
		} else if p.LastSeen > store.Now()-45 {
			p.Status = "unverified"
			if validProbeEgress(p, p.Egress) {
				p.Status = "online"
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func validProbeEgress(p probeView, e probe.Egress) bool {
	age := time.Since(e.ObservedAt)
	return e.Error == "" && e.Source == probe.EgressSource && probe.PublicIPv4(e.IP) && e.IP == p.ExpectedIP && age >= -5*time.Second && age <= 60*time.Second && ((p.Role == "domestic" && e.Country == "CN") || (p.Role == "control" && probe.ValidCountry(e.Country) && e.Country != "CN"))
}
func (a *App) getProbe(id string) (probeView, error) {
	ps, e := a.probeViews()
	if e != nil {
		return probeView{}, e
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return probeView{}, errors.New("探针不存在")
}
func (a *App) probesAPI(w http.ResponseWriter, r *http.Request, path string) {
	if path == "" && r.Method == "GET" {
		items, e := a.probeViews()
		if e != nil {
			fail(w, 500, "探针读取失败")
			return
		}
		reply(w, 200, map[string]any{"items": items})
		return
	}
	if path == "" && r.Method == "POST" {
		var b struct {
			Name       string `json:"name"`
			Role       string `json:"role"`
			ExpectedIP string `json:"expectedIP"`
		}
		if read(r, &b) != nil || len(strings.TrimSpace(b.Name)) < 1 || len(b.Name) > 80 || (b.Role != "domestic" && b.Role != "control") || !probe.PublicIPv4(b.ExpectedIP) {
			fail(w, 400, "请填写探针名称、用途和实际公网 IPv4 出口")
			return
		}
		id, token := store.ID(), store.ID()+store.ID()
		tx, e := a.S.DB.Begin()
		if e != nil {
			fail(w, 500, "探针保存失败")
			return
		}
		defer tx.Rollback()
		var count int
		_ = tx.QueryRow("SELECT count(*) FROM connectivity_probes WHERE enabled=1").Scan(&count)
		if count >= 64 {
			fail(w, 409, "最多启用64个探针，请先停用不再使用的探针")
			return
		}
		_, e = tx.Exec("INSERT INTO connectivity_probes(id,name,role,expected_ip) VALUES(?,?,?,?)", id, strings.TrimSpace(b.Name), b.Role, b.ExpectedIP)
		if e == nil {
			_, e = tx.Exec("INSERT INTO probe_enrollments(token_hash,probe_id,expires) VALUES(?,?,?)", store.Hash(token), id, store.Now()+300)
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, "探针保存失败")
			return
		}
		reply(w, 201, map[string]string{"id": id, "registrationToken": token})
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == "DELETE" || len(parts) == 2 && parts[1] == "disable" && r.Method == "POST" {
		tx, e := a.S.DB.Begin()
		if e != nil {
			fail(w, 500, "探针停用失败")
			return
		}
		defer tx.Rollback()
		_, e = tx.Exec("UPDATE connectivity_probes SET enabled=0,token_hash='' WHERE id=?", parts[0])
		if e == nil {
			_, e = tx.Exec("DELETE FROM probe_enrollments WHERE probe_id=?", parts[0])
		}
		if e == nil {
			_, e = tx.Exec("UPDATE probe_tasks SET state='expired' WHERE probe_id=? AND state IN ('queued','leased')", parts[0])
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, "探针停用失败")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	fail(w, 405, "探针操作不支持")
}
func (a *App) verifyProbeEgress(ctx context.Context, p probeView, e probe.Egress) probe.Egress {
	if !validProbeEgress(p, e) {
		e.Error = "探针出口不匹配、离线或地理证据不足"
		return e
	}
	a.connectivity.mu.Lock()
	cached, ok := a.connectivity.geoCache[e.IP]
	a.connectivity.mu.Unlock()
	if !ok || time.Since(cached.ObservedAt) > time.Minute {
		lookup := a.connectivity.lookup
		if lookup == nil {
			lookup = probe.LookupEgress
		}
		var err error
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cached, err = lookup(ctx, e.IP)
		if err != nil || cached.Error != "" || time.Since(cached.ObservedAt) > time.Minute || time.Since(cached.ObservedAt) < -5*time.Second || cached.IP != e.IP || cached.Country != e.Country || cached.Source != probe.EgressSource {
			e.Error = "管理端无法独立核实探针出口，不能据此自动换 IP"
			return e
		}
		a.connectivity.mu.Lock()
		if a.connectivity.geoCache == nil {
			a.connectivity.geoCache = map[string]probe.Egress{}
		}
		if len(a.connectivity.geoCache) >= 64 {
			a.connectivity.geoCache = map[string]probe.Egress{}
		}
		a.connectivity.geoCache[e.IP] = cached
		a.connectivity.mu.Unlock()
	}
	if cached.Country != e.Country {
		e.Error = "出口地理证据冲突"
	}
	return e
}
func (a *App) probeAPI(w http.ResponseWriter, r *http.Request) {
	// a.Origin is the explicitly configured external HTTPS origin (TLS may terminate
	// at the existing reverse proxy). Never trust arbitrary X-Forwarded-* headers.
	if r.Method != "POST" || !strings.HasPrefix(a.Origin, "https://") {
		fail(w, 403, "探针控制通道需要配置 HTTPS Origin")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/probe/v1/")
	if path == "enroll" {
		var b probe.EnrollRequest
		if read(r, &b) != nil || len(b.RegistrationToken) < 32 || len(b.RegistrationToken) > 128 || len(b.Version) < 1 || len(b.Version) > 80 {
			fail(w, 400, "注册信息无效")
			return
		}
		token := store.ID() + store.ID()
		tx, e := a.S.DB.Begin()
		if e != nil {
			fail(w, 500, "探针注册失败")
			return
		}
		defer tx.Rollback()
		var id string
		e = tx.QueryRow("DELETE FROM probe_enrollments WHERE token_hash=? AND expires>? RETURNING probe_id", store.Hash(b.RegistrationToken), store.Now()).Scan(&id)
		if e != nil {
			fail(w, 401, "注册凭据已失效或已使用")
			return
		}
		res, e := tx.Exec("UPDATE connectivity_probes SET token_hash=?,version=? WHERE id=? AND enabled=1 AND token_hash=''", store.Hash(token), b.Version, id)
		if e == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				e = errors.New("invalid")
			}
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 409, "探针已停用或已注册")
			return
		}
		reply(w, 200, probe.Identity{ProbeID: id, Token: token})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(token) < 32 || len(token) > 128 || r.Header.Get("Authorization") == token {
		fail(w, 401, "探针认证失败")
		return
	}
	var id string
	if a.S.DB.QueryRow("SELECT id FROM connectivity_probes WHERE token_hash=? AND enabled=1", store.Hash(token)).Scan(&id) != nil {
		fail(w, 401, "探针认证失败")
		return
	}
	p, e := a.getProbe(id)
	if e != nil {
		fail(w, 401, "探针身份无效")
		return
	}
	switch path {
	case "poll":
		var b probe.PollRequest
		if read(r, &b) != nil || len(b.Version) < 1 || len(b.Version) > 80 {
			fail(w, 400, "心跳格式无效")
			return
		}
		eg := probe.Egress{IP: b.EgressIP, Country: b.EgressCountry, ObservedAt: b.EgressObservedAt, Source: b.EgressSource, Error: b.EgressError}
		eg = a.verifyProbeEgress(r.Context(), p, eg)
		raw, _ := json.Marshal(eg)
		if _, e = a.S.DB.Exec("UPDATE connectivity_probes SET last_seen=?,version=?,egress=? WHERE id=? AND enabled=1", store.Now(), b.Version, raw, id); e != nil {
			fail(w, 500, "心跳保存失败")
			return
		}
		if eg.Error != "" {
			reply(w, 200, probe.PollResponse{})
			return
		}
		var task probe.Task
		var target []byte
		e = a.S.DB.QueryRow("UPDATE probe_tasks SET state='leased' WHERE id=(SELECT t.id FROM probe_tasks t JOIN screenings s ON s.id=t.screening_id WHERE t.probe_id=? AND t.state='queued' AND t.expires>? AND s.stop=0 AND s.state IN ('probing','waiting-ready') ORDER BY t.created,t.id LIMIT 1) AND state='queued' AND NOT EXISTS(SELECT 1 FROM probe_tasks WHERE probe_id=? AND state='leased' AND expires>?) RETURNING id,lease,target,timeout_ms", id, store.Now(), id, store.Now()).Scan(&task.TaskID, &task.LeaseID, &target, &task.TimeoutMS)
		if e == sql.ErrNoRows {
			reply(w, 200, probe.PollResponse{})
			return
		}
		if e != nil || json.Unmarshal(target, &task.Target) != nil {
			fail(w, 500, "检测任务读取失败")
			return
		}
		reply(w, 200, probe.PollResponse{Task: &task})
	case "results":
		var b probe.ResultRequest
		if read(r, &b) != nil || len(b.TaskID) > 64 || len(b.LeaseID) > 64 || !validProbeResult(b.Result) {
			fail(w, 400, "检测结果格式无效")
			return
		}
		var target []byte
		var created, expires int64
		var state, reportHash string
		requestRaw, _ := json.Marshal(b)
		requestHash := store.Hash(string(requestRaw))
		e = a.S.DB.QueryRow("SELECT target,created,expires,state,report_hash FROM probe_tasks WHERE id=? AND probe_id=? AND lease=?", b.TaskID, id, b.LeaseID).Scan(&target, &created, &expires, &state, &reportHash)
		if e == nil && state == "done" && reportHash == requestHash {
			w.WriteHeader(204)
			return
		}
		if e == nil && (state == "expired" || expires < store.Now()) {
			fail(w, 410, "检测租约已过期，丢弃结果且不得重放")
			return
		}
		var expected probe.Target
		if e != nil || json.Unmarshal(target, &expected) != nil || expected != b.Target || state != "leased" || expires < store.Now() || b.Result.StartedAt.Unix() < created-2 || b.Result.CompletedAt.Unix() > store.Now()+5 {
			fail(w, 409, "检测任务、目标、时间或租约不匹配；不能重放")
			return
		}
		eg := a.verifyProbeEgress(r.Context(), p, b.Egress)
		raw, _ := json.Marshal(b.Result)
		eraw, _ := json.Marshal(eg)
		tx, e := a.S.DB.Begin()
		if e != nil {
			fail(w, 500, "检测结果保存失败")
			return
		}
		defer tx.Rollback()
		res, e := tx.Exec("UPDATE probe_tasks SET state='done',result=?,egress=?,report_hash=? WHERE id=? AND probe_id=? AND lease=? AND state='leased' AND expires>=? AND EXISTS(SELECT 1 FROM connectivity_probes WHERE id=? AND enabled=1)", raw, eraw, requestHash, b.TaskID, id, b.LeaseID, store.Now(), id)
		if e == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				_ = tx.Rollback()
				var acceptedHash string
				if a.S.DB.QueryRow("SELECT report_hash FROM probe_tasks WHERE id=? AND probe_id=? AND lease=? AND state='done'", b.TaskID, id, b.LeaseID).Scan(&acceptedHash) == nil && acceptedHash == requestHash {
					w.WriteHeader(204)
					return
				}
				fail(w, 409, "检测结果已提交或租约失效")
				return
			}
		}
		if e == nil {
			_, e = tx.Exec("UPDATE connectivity_probes SET checks=checks+1,successes=successes+?,last_seen=?,egress=? WHERE id=?", boolInt(b.Result.Success && eg.Error == ""), store.Now(), eraw, id)
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, "检测结果保存失败")
			return
		}
		w.WriteHeader(204)
	default:
		fail(w, 404, "探针端点不存在")
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func validProbeResult(r probe.Result) bool {
	classes := map[string]bool{"ok": true, "success": true, "timeout": true, "connection_refused": true, "network_error": true, "connection_error": true, "canceled": true, "redirect_rejected": true, "tls_error": true, "http_status": true, "challenge_mismatch": true, "invalid_target": true, "egress_unverified": true, "response_too_large": true, "http_error": true}
	return (r.StatusCode == 0 || r.StatusCode >= 100 && r.StatusCode <= 599) && classes[r.Classification] && len(r.Error) <= 256 && r.LatencyMS >= 0 && r.LatencyMS <= 30000 && !r.StartedAt.IsZero() && !r.CompletedAt.Before(r.StartedAt) && r.CompletedAt.Sub(r.StartedAt) <= 35*time.Second && (!r.Success || r.Classification == "ok" || r.Classification == "success")
}
