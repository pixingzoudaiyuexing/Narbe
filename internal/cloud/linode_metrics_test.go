package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// All tests use an in-process HTTP transport. Any attempted cloud write fails.
func linodeMetricsFixture(t *testing.T, f transport) *Linode {
	t.Helper()
	return &Linode{rest: rest{base: "https://api.linode.com/v4", token: "fixture-metrics-token", h: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("read-only metric request attempted %s %s", r.Method, r.URL.Path)
		}
		return f(r)
	})}}
}

func metricsJSON(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]json.RawMessage
	if e = json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	return out
}

func TestLinodeTransferInstanceBytesAndSharedPoolGB(t *testing.T) {
	var paths []string
	l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v4/linode/instances/42/transfer":
			return response(200, `{"used":1073741824,"quota":2000,"billable":0}`), nil
		case "/v4/account/transfer":
			return response(200, `{"used":2,"quota":5000,"billable":0,"region_transfers":[{"id":"id-cgk","used":3,"quota":200,"billable":1}],"credit_card":{"last_four":"9876"}}`), nil
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
			return nil, nil
		}
	})
	v, e := l.transfer(context.Background(), "42", "")
	if e != nil {
		t.Fatal(e)
	}
	if v.UsedBytes == nil || *v.UsedBytes != 1<<30 || v.QuotaGB == nil || *v.QuotaGB != 2000 || v.BillableGB == nil || *v.BillableGB != 0 {
		t.Fatalf("instance byte/GB units mixed: %+v", v)
	}
	pool := metricsJSON(t, v.AccountPool)
	if string(pool["usedGB"]) != "2" || string(pool["quotaGB"]) != "5000" || !strings.Contains(string(pool["regions"]), `"id":"id-cgk"`) {
		t.Fatalf("global/region shared pool units or regions lost: %s", pool)
	}
	if len(paths) != 2 || v.Period != time.Now().UTC().Format("2006-01") || v.QueriedAt <= 0 || v.Source != "Linode Transfer API" {
		t.Fatalf("missing source/time/period: %+v; %v", v, paths)
	}
	if v.InBytes != nil || v.OutBytes != nil || v.TotalBytes != nil {
		t.Fatal("unreported inbound or total traffic was invented")
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "credit_card") || strings.Contains(string(raw), "9876") {
		t.Fatal("pool response exposed unrelated billing fields")
	}
}

func TestLinodeTransferMissingAndNullRemainUnknown(t *testing.T) {
	for _, body := range []string{`{}`, `{"used":null,"quota":null,"billable":null}`, `{"used":-1,"quota":"unknown","billable":null}`} {
		t.Run(body, func(t *testing.T) {
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v4/account/transfer" {
					return response(200, `{"used":null,"quota":null,"billable":null,"region_transfers":[]}`), nil
				}
				return response(200, body), nil
			})
			v, e := l.transfer(context.Background(), "42", "")
			if e != nil || v.UsedBytes != nil || v.QuotaGB != nil || v.BillableGB != nil || v.InBytes != nil || v.OutBytes != nil {
				t.Fatalf("unknown values became zero: %+v %v", v, e)
			}
			pool := metricsJSON(t, v.AccountPool)
			if string(pool["usedGB"]) != "null" || string(pool["quotaGB"]) != "null" {
				t.Fatalf("missing shared pool quantities became zero: %s", pool)
			}
		})
	}
}

func TestLinodeTransferHistoryAndAccountPermissionIsolation(t *testing.T) {
	t.Run("explicit current month retains billing-month endpoint", func(t *testing.T) {
		l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/v4/linode/instances/42/transfer":
				return response(200, `{"used":1,"quota":2000,"billable":0}`), nil
			case "/v4/account/transfer":
				return response(200, `{"used":1,"quota":2000,"billable":0,"region_transfers":[]}`), nil
			default:
				t.Fatalf("explicit current month switched to historical transfer period: %s", r.URL.Path)
				return nil, nil
			}
		})
		v, e := l.transfer(context.Background(), "42", time.Now().UTC().Format("2006-01"))
		if e != nil || v.UsedBytes == nil || v.AccountPool == nil {
			t.Fatalf("current calendar month reading lost billing pool: %+v %v", v, e)
		}
	})
	t.Run("historical bytes are not current shared pool", func(t *testing.T) {
		calls := 0
		l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path != "/v4/linode/instances/42/transfer/2025/1" {
				t.Fatalf("historical transfer requested current data: %s", r.URL.Path)
			}
			return response(200, `{"bytes_in":1073741824,"bytes_out":2147483648,"bytes_total":3221225472}`), nil
		})
		v, e := l.transfer(context.Background(), "42", "2025-01")
		if e != nil || calls != 1 || v.Period != "2025-01" || v.InBytes == nil || *v.InBytes != 1<<30 || v.OutBytes == nil || *v.OutBytes != 2<<30 || v.TotalBytes == nil || *v.TotalBytes != 3<<30 {
			t.Fatalf("monthly transfer contract: %+v %v calls=%d", v, e, calls)
		}
		if v.QuotaGB != nil || v.AccountPool != nil {
			t.Fatal("historical instance data was given a current quota/pool")
		}
	})
	t.Run("account forbidden keeps instance reading", func(t *testing.T) {
		l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/v4/account/transfer" {
				return response(403, `{"errors":[{"reason":"account scope denied fixture-metrics-token"}]}`), nil
			}
			return response(200, `{"used":0,"quota":1000,"billable":0}`), nil
		})
		v, e := l.transfer(context.Background(), "42", "")
		if e != nil || v.UsedBytes == nil || *v.UsedBytes != 0 || v.AccountPool != nil || v.AccountError == "" {
			t.Fatalf("optional account permission disabled instance view: %+v %v", v, e)
		}
		if strings.Contains(v.AccountError, "fixture-metrics-token") {
			t.Fatal("permission feedback leaked raw provider error")
		}
	})
}

func TestLinodeStatsUnitsMillisecondsAndSampling(t *testing.T) {
	l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v4/linode/instances/42/stats" {
			t.Fatalf("unexpected stats path %s", r.URL.Path)
		}
		return response(200, `{"title":"fixture (5 min avg)","data":{"cpu":[[1760000000000,150],[1760000300000,42]],"io":{"io":[[1760000000000,12],[1760000300000,18]],"swap":[]},"netv4":{"in":[[1760000000000,8000],[1760000300000,16000]],"out":[]},"netv6":{"in":[],"out":[[1760000000000,32000],[1760000300000,64000]]}}}`), nil
	})
	v, e := l.stats(context.Background(), "42", "")
	if e != nil || v.Period != "过去24小时" || v.QueriedAt <= 0 || v.Source != "Linode Stats API" {
		t.Fatalf("daily stats contract: %+v %v", v, e)
	}
	series := map[string]Series{}
	for _, s := range v.Series {
		series[s.Key] = s
	}
	for key, unit := range map[string]string{"cpu": "%", "io.io": "Block/s", "io.swap": "Block/s", "netv4.in": "bits/s", "netv4.out": "bits/s", "netv6.in": "bits/s", "netv6.out": "bits/s"} {
		if series[key].Unit != unit {
			t.Errorf("%s unit=%q want %q", key, series[key].Unit, unit)
		}
	}
	cpu := series["cpu"]
	if len(cpu.Points) != 2 || cpu.Points[0][0] != 1760000000000 || cpu.Points[0][1] != 150 || cpu.SampleSeconds == nil || *cpu.SampleSeconds != 300 {
		t.Fatalf("CPU clamped or milliseconds/sample lost: %+v", cpu)
	}
	if series["netv4.in"].Points[0][1] != 8000 || series["netv6.out"].Points[1][1] != 64000 {
		t.Fatal("network bit rates changed without changing units")
	}
	if series["io.swap"].SampleSeconds != nil || len(series["io.swap"].Points) != 0 {
		t.Fatal("empty series fabricated data/sampling")
	}
}

func TestLinodeStatsEmptyAndHistoricalPeriods(t *testing.T) {
	for _, body := range []string{`{"data":null}`, `{"data":{"cpu":null,"io":null,"netv4":null,"netv6":null}}`, `{"data":{"cpu":[],"io":{},"netv4":{},"netv6":{}}}`} {
		t.Run(body, func(t *testing.T) {
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) { return response(200, body), nil })
			v, e := l.stats(context.Background(), "42", "")
			if e != nil {
				t.Fatal(e)
			}
			for _, s := range v.Series {
				if s.Points == nil || len(s.Points) != 0 || s.SampleSeconds != nil {
					t.Fatalf("empty stats fabricated a 0 percent sample: %+v", s)
				}
			}
		})
	}
	for _, period := range []string{"2000-01", "2025-01", time.Now().UTC().Format("2006-01")} {
		t.Run(period, func(t *testing.T) {
			parsed, _ := time.Parse("2006-01", period)
			wantPath := fmt.Sprintf("/v4/linode/instances/42/stats/%d/%d", parsed.Year(), parsed.Month())
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != wantPath {
					t.Fatalf("monthly stats path=%s want %s", r.URL.Path, wantPath)
				}
				return response(200, `{"data":{"cpu":[]}}`), nil
			})
			v, e := l.stats(context.Background(), "42", period)
			if e != nil {
				t.Fatal(e)
			}
			if period == time.Now().UTC().Format("2006-01") {
				if !strings.Contains(v.Period, "30天") {
					t.Fatal("current month mislabeled as calendar-month monitoring", v.Period)
				}
			} else if v.Period != period {
				t.Fatal("historical monitoring lost requested month", v.Period)
			}
		})
	}
}

func TestLinodeStatsIrregularSamplingAndInvalidSeries(t *testing.T) {
	for _, tc := range []struct {
		name, points string
		wantError    bool
	}{
		{"irregular", `[[1760000000000,1],[1760000300000,2],[1760000601000,3]]`, false},
		{"reversed", `[[1760000300000,1],[1760000000000,2]]`, true},
		{"negative reading", `[[1760000000000,-1]]`, true},
		{"missing reading", `[[1760000000000]]`, true},
		{"null reading", `[[1760000000000,null]]`, true},
		{"invalid timestamp", `[[0,1]]`, true},
		{"seconds timestamp is not official milliseconds", `[[1760000000,1]]`, true},
		{"duplicate timestamp", `[[1760000000000,1],[1760000000000,2]]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
				return response(200, `{"data":{"cpu":`+tc.points+`}}`), nil
			})
			v, e := l.stats(context.Background(), "42", "")
			if (e != nil) != tc.wantError {
				t.Fatalf("invalid series handling mismatch: %+v %v", v, e)
			}
			if !tc.wantError && v.Series[0].SampleSeconds != nil {
				t.Fatal("irregular sampling advertised a fixed interval")
			}
		})
	}
}

func TestLinodeBillingBalanceSignsAndPrivateFields(t *testing.T) {
	for _, balance := range []string{"25.5", "-25.5", "0", "null"} {
		t.Run(balance, func(t *testing.T) {
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
				return response(200, `{"balance":`+balance+`,"balance_uninvoiced":7.25,"email":"fixture-secret@example.invalid","address_1":"private-address-marker","tax_id":"private-tax-marker","credit_card":{"expiry":"01/2099","last_four":"9876"},"payment_card_number":"never-return-card"}`), nil
			})
			v, e := l.billing(context.Background(), "billing", "", 1)
			if e != nil {
				t.Fatal(e)
			}
			fields := metricsJSON(t, v)
			if string(fields["balance"]) != balance || string(fields["uninvoiced"]) != "7.25" || string(fields["currency"]) != `"USD"` {
				t.Fatalf("official balance sign/estimate/currency changed: %s", fields)
			}
			wantMeaning := map[string]string{"25.5": "待付款", "-25.5": "抵扣", "0": "无余额", "null": "未知"}[balance]
			if !strings.Contains(string(fields["balanceMeaning"]), wantMeaning) {
				t.Fatalf("official balance sign labeled incorrectly: %s", fields["balanceMeaning"])
			}
			if !strings.Contains(string(fields["estimateNote"]), "不含流量") || !strings.Contains(string(fields["estimateNote"]), "估算") {
				t.Fatal("estimated invoice represented as final all-inclusive bill")
			}
			b, _ := json.Marshal(v)
			for _, forbidden := range []string{"fixture-secret", "private-address-marker", "private-tax-marker", "credit_card", "9876", "never-return-card"} {
				if strings.Contains(string(b), forbidden) {
					t.Fatalf("account view leaked %q", forbidden)
				}
			}
		})
	}
}

func TestLinodeInvoicesBoundedPaginationAndUnknownPaymentState(t *testing.T) {
	calls := 0
	l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v4/account/invoices" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "25" {
			t.Fatalf("invoice paging contract changed: %s", r.URL.String())
		}
		return response(200, `{"data":[{"id":9,"date":"2025-02-01T00:00:00","label":"Invoice","subtotal":10,"tax":1,"total":11,"billing_source":"linode","paid":true,"credit_card":{"last_four":"9876"}}],"page":2,"pages":3,"results":51}`), nil
	})
	v, e := l.billing(context.Background(), "invoices", "", 2)
	if e != nil || calls != 1 {
		t.Fatalf("list enumerated history rather than requested page: %v calls=%d", e, calls)
	}
	fields := metricsJSON(t, v)
	var page map[string]json.RawMessage
	_ = json.Unmarshal(fields["invoices"], &page)
	if string(page["page"]) != "2" || string(page["pages"]) != "3" || string(page["results"]) != "51" || string(fields["currency"]) != `"USD"` {
		t.Fatalf("page boundary information missing: %s", fields)
	}
	if !strings.Contains(string(fields["statusNote"]), "未提供") {
		t.Fatal("invoice payment state was invented")
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), `"paid"`) || strings.Contains(string(b), "credit_card") || strings.Contains(string(b), "9876") {
		t.Fatal("untrusted payment state/private fields escaped invoice whitelist")
	}
}

func TestLinodeInvoiceItemsNumericAndStringUnitPrices(t *testing.T) {
	for _, unitPrice := range []string{`"5.05"`, `5.05`} {
		t.Run(unitPrice, func(t *testing.T) {
			calls := 0
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path == "/v4/account/invoices/9" {
					return response(200, `{"id":9,"total":21.45,"tax":1.25,"subtotal":20.2,"date":"2025-02-01T00:00:00"}`), nil
				}
				if r.URL.Path != "/v4/account/invoices/9/items" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "25" {
					t.Fatalf("invoice items scope changed: %s", r.URL.String())
				}
				return response(200, `{"data":[{"label":"Linode 42","amount":20.2,"quantity":4,"unit_price":`+unitPrice+`,"tax":1.25,"total":21.45,"region":null,"from":"2025-01-01T00:00:00","to":"2025-01-31T23:59:59","type":"hourly"}],"page":2,"pages":2,"results":26}`), nil
			})
			v, e := l.billing(context.Background(), "invoice", "9", 2)
			if e != nil || calls != 2 {
				t.Fatalf("official numeric/string unit_price not accepted: %v calls=%d", e, calls)
			}
			b, _ := json.Marshal(v)
			if !strings.Contains(string(b), "5.05") || !strings.Contains(string(b), "Linode 42") {
				t.Fatalf("invoice item detail lost: %s", b)
			}
			var envelope struct {
				Items struct {
					Data []map[string]json.RawMessage `json:"data"`
				} `json:"items"`
			}
			if e := json.Unmarshal(b, &envelope); e != nil || len(envelope.Items.Data) != 1 {
				t.Fatalf("item envelope malformed: %s %v", b, e)
			}
			if string(envelope.Items.Data[0]["tax"]) != "1.25" || string(envelope.Items.Data[0]["total"]) != "21.45" {
				t.Fatalf("invoice details omitted tax/total: %s", b)
			}
		})
	}
}

func TestLinodeInvoiceMissingAmountsRemainUnknown(t *testing.T) {
	for _, amounts := range []string{"", `,"subtotal":null,"tax":null,"total":null`} {
		t.Run(amounts, func(t *testing.T) {
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
				return response(200, `{"data":[{"id":9,"date":"2025-02-01T00:00:00","label":"Invoice"`+amounts+`}],"page":1,"pages":1,"results":1}`), nil
			})
			v, e := l.billing(context.Background(), "invoices", "", 1)
			if e != nil {
				t.Fatal(e)
			}
			b, _ := json.Marshal(v)
			var envelope struct {
				Invoices struct {
					Data []map[string]json.RawMessage `json:"data"`
				} `json:"invoices"`
			}
			if e := json.Unmarshal(b, &envelope); e != nil || len(envelope.Invoices.Data) != 1 {
				t.Fatalf("invoice envelope malformed: %s %v", b, e)
			}
			for _, key := range []string{"subtotal", "tax", "total"} {
				raw := envelope.Invoices.Data[0][key]
				if len(raw) > 0 && string(raw) != "null" {
					t.Errorf("missing %s was presented as amount %s", key, raw)
				}
			}
		})
	}
}

func TestLinodeInvoiceRejectsObjectUnitPrice(t *testing.T) {
	l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v4/account/invoices/9" {
			return response(200, `{"id":9,"total":1}`), nil
		}
		return response(200, `{"data":[{"label":"fixture","unit_price":{"credit_card":"never-forward-object"}}],"page":1,"pages":1,"results":1}`), nil
	})
	if v, e := l.billing(context.Background(), "invoice", "9", 1); e == nil {
		b, _ := json.Marshal(v)
		t.Fatalf("unexpected provider object escaped scalar price whitelist: %s", b)
	}
}

func TestLinodeInvoicePaginationRejectsInvalidEnvelope(t *testing.T) {
	for _, body := range []string{
		`{"data":null,"page":1,"pages":1,"results":0}`,
		`{"data":[],"page":1,"pages":0,"results":0}`,
		`{"data":[],"page":2,"pages":2,"results":0}`,
		`{"data":[],"page":1,"pages":1,"results":-1}`,
	} {
		t.Run(body, func(t *testing.T) {
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) { return response(200, body), nil })
			if _, e := l.billing(context.Background(), "invoices", "", 1); e == nil {
				t.Fatal("invalid envelope falsely reported readable/complete invoices")
			}
		})
	}
}

func TestLinodeReadRejectsInvalidScopeBeforeHTTPRequest(t *testing.T) {
	l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("invalid read request reached provider: %s", r.URL.String())
		return nil, nil
	})
	for _, tc := range []struct {
		view, id, period string
		page             int
	}{
		{"invoices", "", "", 0}, {"invoices", "", "", 101},
		{"invoice", "../payment", "", 1}, {"stats", "42", "2099-12", 1},
		{"transfer", "42", "2025-13", 1}, {"stats", "0", "", 1},
		{"payment", "42", "", 1},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			if _, e := l.LinodeRead(context.Background(), tc.view, tc.id, tc.period, tc.page); e == nil {
				t.Fatal("invalid read scope accepted")
			}
		})
	}
}

func TestLinodeReadPermissionsAreIndependentAndNeverProveWrite(t *testing.T) {
	calls := 0
	l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path == "/v4/account" {
			return response(403, `{"errors":[{"reason":"private billing scope fixture-metrics-token"}]}`), nil
		}
		if size := r.URL.Query().Get("page_size"); size != "" {
			n, e := strconv.Atoi(size)
			if e != nil || n < 25 || n > 500 {
				t.Errorf("permission probe requests unsupported page_size=%q", size)
			}
		}
		rsp := response(200, `{"data":[],"page":1,"pages":1,"results":0}`)
		rsp.Header.Set("X-OAuth-Scopes", "linodes:read_only,firewall:read_only")
		return rsp, nil
	})
	v, e := l.LinodeRead(context.Background(), "permissions", "", "", 1)
	if e != nil || calls != 3 {
		t.Fatalf("one unavailable optional permission failed whole view: %v calls=%d", e, calls)
	}
	b, _ := json.Marshal(v)
	s := string(b)
	if !strings.Contains(s, "服务器") || !strings.Contains(s, "防火墙") || !strings.Contains(s, "账单") || !strings.Contains(s, "不可用") {
		t.Fatalf("permission read status missing: %s", s)
	}
	if strings.Contains(s, "fixture-metrics-token") || strings.Contains(s, "private billing scope") {
		t.Fatal("permission response leaked provider raw errors")
	}
}

func TestLinodePermissionScopeHeaderDoesNotInventIdentityGrants(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"", "未知"},
		{"linodes:read_only,firewall:read_only,account:read_only", "缺少"},
		{"linodes:read_write firewall:read_write account:read_only", "仍需"},
		{"*", "仍需"},
	} {
		t.Run(tc.header, func(t *testing.T) {
			l := linodeMetricsFixture(t, func(r *http.Request) (*http.Response, error) {
				rsp := response(200, `{"data":[],"page":1,"pages":1,"results":0}`)
				if tc.header != "" {
					rsp.Header.Set("X-OAuth-Scopes", tc.header)
				}
				return rsp, nil
			})
			v, e := l.LinodeRead(context.Background(), "permissions", "", "", 1)
			if e != nil {
				t.Fatal(e)
			}
			b, _ := json.Marshal(v)
			var probes []map[string]string
			if e := json.Unmarshal(b, &probes); e != nil || len(probes) != 3 {
				t.Fatalf("permission result shape: %s %v", b, e)
			}
			for _, probe := range probes {
				if probe["name"] == "账单" {
					continue
				}
				if !strings.Contains(probe["write"], tc.want) {
					t.Errorf("scope %q incorrectly proved resource identity write grants: %v", tc.header, probe)
				}
			}
		})
	}
}

func TestRegionContinentKnownCoverageAndRawIdentity(t *testing.T) {
	allowed := map[string]bool{"亚洲": true, "欧洲": true, "北美洲": true, "南美洲": true, "大洋洲": true, "非洲": true}
	for provider, names := range regionNames {
		seen := map[string]string{}
		for group, ids := range continentIDs[provider] {
			if !allowed[group] {
				t.Errorf("unexpected continent %s/%s", provider, group)
			}
			for _, id := range strings.Fields(ids) {
				if previous := seen[id]; previous != "" {
					t.Errorf("ambiguous region group %s/%s: %s and %s", provider, id, previous, group)
				}
				if names[id] == "" {
					t.Errorf("unverified region was assigned geography: %s/%s", provider, id)
				}
				seen[id] = group
			}
		}
		for id := range names {
			if continent := RegionContinent(provider, id); !allowed[continent] {
				t.Errorf("known geography left ungrouped: %s/%s=%s", provider, id, continent)
			}
			choices := []Choice{{ID: id, Name: "official fixture"}}
			LocalizeRegions(provider, choices)
			if choices[0].ID != id || choices[0].Continent != seen[id] || !strings.Contains(choices[0].Name, id) {
				t.Errorf("display localization changed raw API identity: %+v", choices[0])
			}
		}
	}
	for _, tc := range []struct{ provider, id, want string }{
		{"aws", "ap-southeast-2", "大洋洲"}, {"aws", "ap-southeast-4", "大洋洲"}, {"aws", "ap-southeast-6", "大洋洲"},
		{"aws", "il-central-1", "亚洲"}, {"aws", "me-central-1", "亚洲"}, {"aws", "mx-central-1", "北美洲"},
		{"azure", "qatarcentral", "亚洲"}, {"azure", "israelcentral", "亚洲"}, {"azure", "southafricanorth", "非洲"},
		{"azure", "chilecentral", "南美洲"}, {"linode", "ap-southeast", "大洋洲"}, {"linode", "ap-south", "亚洲"},
		{"linode", "br-gru", "南美洲"}, {"digitalocean", "syd1", "大洋洲"},
		{"aws", "ap-southeast-unverified", "其他／位置待确认"}, {"linode", "eu-unverified", "其他／位置待确认"},
		{"azure", "newfuture", "其他／位置待确认"}, {"digitalocean", "syd999", "其他／位置待确认"},
	} {
		if got := RegionContinent(tc.provider, tc.id); got != tc.want {
			t.Errorf("%s/%s=%s want %s", tc.provider, tc.id, got, tc.want)
		}
	}
	unknown := []Choice{{ID: "ap-southeast-unverified", Name: "Tokyo Official Future"}}
	LocalizeRegions("aws", unknown)
	if unknown[0].ID != "ap-southeast-unverified" || unknown[0].Continent != "其他／位置待确认" || !strings.Contains(unknown[0].Name, "Tokyo Official Future") {
		t.Fatalf("unknown geography guessed or hidden: %+v", unknown[0])
	}
}
