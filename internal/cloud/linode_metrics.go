package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"time"
)

func periodPath(period string) (string, error) {
	if period == "" {
		return "", nil
	}
	t, e := time.Parse("2006-01", period)
	now := time.Now().UTC()
	if e != nil || t.Year() < 2000 || t.After(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)) {
		return "", errors.New("月份应为有效的过去或当前年月")
	}
	return fmt.Sprintf("/%d/%d", t.Year(), t.Month()), nil
}

type TransferView struct {
	UsedBytes    *float64       `json:"usedBytes,omitempty"`
	QuotaGB      *float64       `json:"quotaGB,omitempty"`
	BillableGB   *float64       `json:"billableGB,omitempty"`
	InBytes      *float64       `json:"inBytes,omitempty"`
	OutBytes     *float64       `json:"outBytes,omitempty"`
	TotalBytes   *float64       `json:"totalBytes,omitempty"`
	AccountPool  map[string]any `json:"accountPool,omitempty"`
	AccountError string         `json:"accountError,omitempty"`
	Period       string         `json:"period"`
	QueriedAt    int64          `json:"queriedAt"`
	Source       string         `json:"source"`
}

func number(m map[string]json.RawMessage, k string) *float64 {
	var v float64
	if b := m[k]; len(b) > 0 && string(b) != "null" && json.Unmarshal(b, &v) == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
		return &v
	}
	return nil
}
func (l *Linode) transfer(ctx context.Context, id, period string) (TransferView, error) {
	v := TransferView{Period: period, QueriedAt: time.Now().Unix(), Source: "Linode Transfer API"}
	if e := linodeID(id); e != nil {
		return v, e
	}
	suffix, e := periodPath(period)
	if e != nil {
		return v, e
	}
	if period == time.Now().UTC().Format("2006-01") {
		suffix = ""
	}
	if period == "" {
		v.Period = time.Now().UTC().Format("2006-01")
	}
	var raw map[string]json.RawMessage
	if e = l.call(ctx, "GET", "/linode/instances/"+id+"/transfer"+suffix, nil, &raw); e != nil {
		return v, e
	}
	v.UsedBytes = number(raw, "used")
	v.QuotaGB = number(raw, "quota")
	v.BillableGB = number(raw, "billable")
	v.InBytes = number(raw, "bytes_in")
	v.OutBytes = number(raw, "bytes_out")
	v.TotalBytes = number(raw, "bytes_total")
	// Current account pools use GB, never the instance's byte unit or an exclusive remainder.
	if suffix == "" {
		var pool struct {
			Used     *float64 `json:"used"`
			Quota    *float64 `json:"quota"`
			Billable *float64 `json:"billable"`
			Regions  []struct {
				ID       string   `json:"id"`
				Used     *float64 `json:"used"`
				Quota    *float64 `json:"quota"`
				Billable *float64 `json:"billable"`
			} `json:"region_transfers"`
		}
		if e = l.call(ctx, "GET", "/account/transfer", nil, &pool); e != nil {
			v.AccountError = "账户共享流量池无法读取，请检查account读取权限；单机数据不代表独享剩余额度"
		} else {
			v.AccountPool = map[string]any{"usedGB": pool.Used, "quotaGB": pool.Quota, "billableGB": pool.Billable, "regions": pool.Regions}
		}
	}
	return v, nil
}

type Series struct {
	Key           string      `json:"key"`
	Label         string      `json:"label"`
	Unit          string      `json:"unit"`
	Points        [][]float64 `json:"points"`
	SampleSeconds *float64    `json:"sampleSeconds,omitempty"`
}
type StatsView struct {
	Series    []Series `json:"series"`
	Title     string   `json:"title"`
	QueriedAt int64    `json:"queriedAt"`
	Source    string   `json:"source"`
	Period    string   `json:"period"`
}

func (l *Linode) stats(ctx context.Context, id, period string) (StatsView, error) {
	v := StatsView{Series: []Series{}, QueriedAt: time.Now().Unix(), Source: "Linode Stats API", Period: "过去24小时"}
	if e := linodeID(id); e != nil {
		return v, e
	}
	suffix, e := periodPath(period)
	if e != nil {
		return v, e
	}
	if suffix != "" {
		v.Period = period
		if period == time.Now().UTC().Format("2006-01") {
			v.Period = "过去30天（当前年月接口）"
		}
	}
	var raw map[string]json.RawMessage
	if e = l.call(ctx, "GET", "/linode/instances/"+id+"/stats"+suffix, nil, &raw); e != nil {
		return v, e
	}
	_ = json.Unmarshal(raw["title"], &v.Title)
	if b := raw["data"]; len(b) > 0 && string(b) != "null" {
		if json.Unmarshal(b, &raw) != nil {
			return v, &Error{Code: "监控响应格式无效"}
		}
	}
	var cpu metricPoints
	if b := raw["cpu"]; len(b) > 0 && string(b) != "null" {
		if json.Unmarshal(b, &cpu) != nil {
			return v, &Error{Code: "监控CPU时序格式无效"}
		}
	}
	add := func(key, label, unit string, points [][]float64) error {
		if len(points) > 20000 {
			return &Error{Code: "监控采样超过安全上限，请缩小时间范围"}
		}
		var last float64
		for _, p := range points {
			if len(p) != 2 || p[0] < 946684800000 || p[1] < 0 || math.IsNaN(p[0]) || math.IsInf(p[0], 0) || math.IsNaN(p[1]) || math.IsInf(p[1], 0) || (last > 0 && p[0] <= last) {
				return &Error{Code: "监控时序数据无效"}
			}
			last = p[0]
		}
		if points == nil {
			points = [][]float64{}
		}
		s := Series{Key: key, Label: label, Unit: unit, Points: points}
		if len(points) > 1 {
			step := (points[1][0] - points[0][0]) / 1000
			regular := step > 0
			for i := 2; i < len(points); i++ {
				if (points[i][0]-points[i-1][0])/1000 != step {
					regular = false
				}
			}
			if regular {
				s.SampleSeconds = &step
			}
		}
		v.Series = append(v.Series, s)
		return nil
	}
	if e = add("cpu", "CPU使用率", "%", cpu); e != nil {
		return v, e
	}
	for _, group := range []string{"io", "netv4", "netv6"} {
		var fields map[string]metricPoints
		if b := raw[group]; len(b) > 0 && string(b) != "null" {
			if json.Unmarshal(b, &fields) != nil {
				return v, &Error{Code: "监控时序格式无效"}
			}
		}
		keys := []string{"in", "out", "private_in", "private_out"}
		unit := "bits/s"
		prefix := map[string]string{"netv4": "IPv4", "netv6": "IPv6"}[group]
		if group == "io" {
			keys = []string{"io", "swap"}
			unit = "Block/s"
			prefix = "磁盘"
		}
		for _, k := range keys {
			label := map[string]string{"io": "I/O", "swap": "Swap", "in": "入站", "out": "出站", "private_in": "内网入站", "private_out": "内网出站"}[k]
			if e = add(group+"."+k, prefix+" "+label, unit, fields[k]); e != nil {
				return v, e
			}
		}
	}
	return v, nil
}
func (l *Linode) permissions(ctx context.Context) []map[string]string {
	out := []map[string]string{}
	for _, v := range []struct{ name, path, scope string }{{"服务器", "/linode/instances?page_size=25", "linodes:read_only / read_write"}, {"防火墙", "/networking/firewalls?page_size=25", "firewall:read_only / read_write"}, {"账单", "/account", "account:read_only"}} {
		status := "可读取"
		headers, e := l.callHeaders(ctx, "GET", v.path, nil, nil)
		if e != nil {
			status = "读取不可用，请检查Token权限及用户授权"
		}
		write := "写权限未知；只读检查不能证明用户资源授权"
		scopes := headers.Get("X-OAuth-Scopes")
		if scopes != "" {
			allowed := false
			required := map[string]string{"服务器": "linodes:read_write", "防火墙": "firewall:read_write"}[v.name]
			for _, scope := range strings.Fields(strings.ReplaceAll(scopes, ",", " ")) {
				if scope == "*" || (required != "" && scope == required) {
					allowed = true
				}
			}
			if required != "" {
				if allowed {
					write = "Token包含写scope；仍需云端校验资源授权"
				} else {
					write = "Token缺少此功能写scope"
				}
			} else {
				write = "账单仅查询，不需要支付写权限"
			}
		}
		out = append(out, map[string]string{"name": v.name, "status": status, "scope": v.scope, "write": write})
	}
	return out
}
func (l *Linode) billing(ctx context.Context, view, id string, page int) (any, error) {
	if view == "billing" {
		var account struct {
			Balance    *float64 `json:"balance"`
			Uninvoiced *float64 `json:"balance_uninvoiced"`
		}
		if e := l.call(ctx, "GET", "/account", nil, &account); e != nil {
			return nil, e
		}
		meaning := "未知"
		if account.Balance != nil {
			if *account.Balance > 0 {
				meaning = "待付款余额（不代表逾期）"
			} else if *account.Balance < 0 {
				meaning = "账户抵扣额度"
			} else {
				meaning = "无余额"
			}
		}
		return map[string]any{"balanceMeaning": meaning, "balance": account.Balance, "uninvoiced": account.Uninvoiced, "currency": "USD", "queriedAt": time.Now().Unix(), "source": "Linode Account API", "estimateNote": "未出账费用为估算，不含流量费用，最终以账单为准"}, nil
	}
	type invoice struct {
		ID            int      `json:"id"`
		Date          string   `json:"date"`
		Label         string   `json:"label"`
		Subtotal      *float64 `json:"subtotal"`
		Tax           *float64 `json:"tax"`
		Total         *float64 `json:"total"`
		BillingSource string   `json:"billing_source"`
	}
	if view == "invoices" {
		var result struct {
			Data    []invoice `json:"data"`
			Page    int       `json:"page"`
			Pages   int       `json:"pages"`
			Results int       `json:"results"`
		}
		if e := l.call(ctx, "GET", "/account/invoices?page_size=25&page="+strconv.Itoa(page), nil, &result); e != nil {
			return nil, e
		}
		if result.Data == nil || result.Page != page || result.Pages < 1 || result.Page > result.Pages || result.Results < 0 {
			return nil, &Error{Code: "账单分页响应不完整，请重试"}
		}
		return map[string]any{"invoices": result, "currency": "USD", "statusNote": "已生成账单；官方接口未提供付款状态", "queriedAt": time.Now().Unix()}, nil
	}
	if e := linodeID(id); e != nil {
		return nil, e
	}
	var head invoice
	if e := l.call(ctx, "GET", "/account/invoices/"+id, nil, &head); e != nil {
		return nil, e
	}
	var items struct {
		Data []struct {
			Label     string          `json:"label"`
			Amount    *float64        `json:"amount"`
			Quantity  *float64        `json:"quantity"`
			UnitPrice json.RawMessage `json:"unit_price"`
			Type      string          `json:"type"`
			From      string          `json:"from"`
			To        string          `json:"to"`
			Region    string          `json:"region"`
			Tax       *float64        `json:"tax"`
			Total     *float64        `json:"total"`
		} `json:"data"`
		Page  int `json:"page"`
		Pages int `json:"pages"`
	}
	if e := l.call(ctx, "GET", "/account/invoices/"+id+"/items?page_size=25&page="+strconv.Itoa(page), nil, &items); e != nil {
		return nil, e
	}
	if fmt.Sprint(head.ID) != id || items.Data == nil || items.Page != page || items.Pages < 1 || items.Page > items.Pages {
		return nil, &Error{Code: "账单身份或明细分页响应不完整"}
	}
	for _, item := range items.Data {
		raw := item.UnitPrice
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var n float64
		if json.Unmarshal(raw, &n) == nil {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, &Error{Code: "账单单价格式无效"}
		}
		if _, e := strconv.ParseFloat(value, 64); e != nil {
			return nil, &Error{Code: "账单单价格式无效"}
		}
	}
	return map[string]any{"invoice": head, "items": items, "currency": "USD", "statusNote": "付款状态官方未提供"}, nil
}

// Decode nullable provider samples without silently coercing a missing value to 0.
type metricPoints [][]float64

func (p *metricPoints) UnmarshalJSON(raw []byte) error {
	var points [][]*float64
	if e := json.Unmarshal(raw, &points); e != nil {
		return e
	}
	out := metricPoints{}
	for _, point := range points {
		if len(point) != 2 || point[0] == nil || point[1] == nil {
			return errors.New("监控采样缺失，不使用0替代未知")
		}
		out = append(out, []float64{*point[0], *point[1]})
	}
	*p = out
	return nil
}
