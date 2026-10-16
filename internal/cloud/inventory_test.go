package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegionLabelsPreserveIDsAndUnknownOfficialNames(t *testing.T) {
	for _, x := range []struct{ provider, id, want string }{{"aws", "ap-northeast-1", "日本东京"}, {"azure", "japaneast", "日本东京／埼玉"}, {"linode", "ap-northeast", "日本东京 2"}, {"digitalocean", "nyc3", "美国纽约 3"}} {
		r := []Choice{{ID: x.id, Name: "official"}}
		LocalizeRegions(x.provider, r)
		if r[0].ID != x.id || r[0].Name != x.want+" · "+x.id {
			t.Fatal(r)
		}
		if RegionLabel(x.provider, "future-location", "Official Future") != "Official Future · future-location" {
			t.Fatal("invented unknown geography")
		}
	}
	if strings.Contains(RegionLabel("aws", "us-east-100", ""), "弗吉尼亚") {
		t.Fatal("guessed geography from prefix")
	}
}

func TestInventoryFailureScopesAreNotCountedTwice(t *testing.T) {
	v := EmptyInventory("fixture")
	v.Phase = "ready"
	v.Failures = []RegionFailure{{Region: "eu-west-1", Message: "previous attempt"}, {Region: "eu-west-1", Message: "region no longer enabled"}, {Region: "*", Message: "bad row"}, {Region: "*", Message: "another bad row"}}
	v.summarize("aws")
	if v.Complete || len(v.Failures) != 2 || v.Failures[0].Message != "region no longer enabled" {
		t.Fatal(v.Failures)
	}
}
func TestInventoryLimitErrorsAreActionableWithoutRawDetails(t *testing.T) {
	for _, code := range []string{"AWS 实例数量超限，不能确认总数", "分页超过安全上限 fixture-secret"} {
		msg := ReadError(&Error{Code: code})
		if !strings.Contains(msg, "安全查询上限") || strings.Contains(msg, "fixture-secret") {
			t.Fatal(msg)
		}
	}
	if !strings.Contains(ReadError(&Error{Code: "列表响应缺少数据 fixture-secret"}), "分页响应异常") {
		t.Fatal("malformed response not explained")
	}
}

func TestAWSInventoryPaginationPartialFailureRetryAndBoundedConcurrency(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	blocked := true
	var active, maxActive atomic.Int32
	a := &AWS{c: Credentials{"accessKeyId": "fixture", "secretAccessKey": "fixture-secret"}, h: client(func(r *http.Request) (*http.Response, error) {
		_ = r.ParseForm()
		action := r.Form.Get("Action")
		if !strings.HasPrefix(action, "Describe") {
			t.Error("cloud write in inventory", action)
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); n > old; old = maxActive.Load() {
			if maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		if action == "DescribeRegions" {
			return response(200, `<DescribeRegionsResponse><regionInfo><item><regionName>us-east-1</regionName></item><item><regionName>eu-west-1</regionName></item><item><regionName>ap-northeast-1</regionName></item><item><regionName>af-south-1</regionName><optInStatus>not-opted-in</optInStatus></item></regionInfo></DescribeRegionsResponse>`), nil
		}
		region := strings.TrimSuffix(strings.TrimPrefix(r.URL.Host, "ec2."), ".amazonaws.com")
		if action == "DescribeInstanceTypes" {
			return response(200, `<DescribeInstanceTypesResponse><instanceTypeSet><item><instanceType>t3.medium</instanceType><vCpuInfo><defaultVCpus>2</defaultVCpus></vCpuInfo><memoryInfo><sizeInMiB>4096</sizeInMiB></memoryInfo><processorInfo><supportedArchitectures><item>x86_64</item></supportedArchitectures></processorInfo></item></instanceTypeSet></DescribeInstanceTypesResponse>`), nil
		}
		if action != "DescribeInstances" {
			t.Error("unexpected", action)
			return response(400, ""), nil
		}
		mu.Lock()
		counts[region]++
		count := counts[region]
		isBlocked := blocked
		mu.Unlock()
		if region == "eu-west-1" && isBlocked {
			return response(403, `<Response><Errors><Error><Code>UnauthorizedOperation</Code></Error></Errors></Response>`), nil
		}
		if region == "ap-northeast-1" && count == 1 {
			return response(429, `<Response><Errors><Error><Code>RequestLimitExceeded</Code></Error></Errors></Response>`), nil
		}
		id, state, next := "i-same", "running", ""
		if region == "us-east-1" {
			if r.Form.Get("NextToken") == "" {
				next = "page2"
			} else {
				id = "i-second"
				state = "stopped"
			}
		}
		return response(200, fmt.Sprintf(`<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>%s</instanceId><instanceType>t3.medium</instanceType><instanceState><name>%s</name></instanceState></item></instancesSet></item></reservationSet><nextToken>%s</nextToken></DescribeInstancesResponse>`, id, state, next)), nil
	})}
	v := ScanInventory(context.Background(), a, "aws", "", Inventory{}, func(Inventory) {})
	if v.Complete || v.Stats.Total != 3 || v.Stats.Running != 2 || v.Stats.Stopped != 1 || len(v.Failures) != 1 || v.Failures[0].Region != "eu-west-1" {
		t.Fatalf("partial result: %+v", v)
	}
	if maxActive.Load() > 3 || counts["af-south-1"] != 0 || counts["ap-northeast-1"] != 2 {
		t.Fatal("concurrency/disabled region/throttle", maxActive.Load(), counts)
	}
	for _, i := range v.Items {
		if i.CPU != 2 || i.Memory != 4096 || i.Pricing != nil {
			t.Fatal("unverified metadata", i)
		}
	}
	beforeUS, beforeJP := counts["us-east-1"], counts["ap-northeast-1"]
	blocked = false
	v = ScanInventory(context.Background(), a, "aws", "eu-west-1", v, func(Inventory) {})
	if !v.Complete || v.Stats.Total != 4 || counts["us-east-1"] != beforeUS || counts["ap-northeast-1"] != beforeJP {
		t.Fatal("retry did not retain other regions", v, counts)
	}
}

func TestAzureInventoryIsResourceGroupScopedAndRetainsPartialRegions(t *testing.T) {
	prefix := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/"
	blocked := true
	foreign := false
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture", expires: time.Now().Add(time.Hour), h: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Error("cloud write", r.Method)
		}
		if strings.Contains(r.URL.Path, "/other/") {
			t.Error("cross-resource-group query")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/locations"):
			return response(200, `{"value":[{"name":"eastus","displayName":"East US"},{"name":"westus","displayName":"West US"}]}`), nil
		case strings.HasSuffix(r.URL.Path, "/virtualMachines"):
			extra := ""
			if foreign {
				extra = `,{"id":"/subscriptions/sub/resourceGroups/other/providers/Microsoft.Compute/virtualMachines/foreign","name":"foreign","location":"eastus"}`
			}
			return response(200, `{"value":[{"id":"`+prefix+`one","name":"same","location":"eastus","properties":{"hardwareProfile":{"vmSize":"Standard_B2s"}}},{"id":"`+prefix+`two","name":"same","location":"westus","properties":{"hardwareProfile":{"vmSize":"Standard_B2s"}}}`+extra+`]}`), nil
		case strings.HasSuffix(r.URL.Path, "/skus"):
			return response(200, `{"value":[{"name":"Standard_B2s","resourceType":"virtualMachines","locations":["eastus","westus"],"capabilities":[{"name":"vCPUs","value":"2"},{"name":"MemoryGB","value":"4"},{"name":"CpuArchitectureType","value":"x64"}]}]}`), nil
		default:
			if strings.HasSuffix(r.URL.Path, "/two") && blocked {
				return response(403, `{"error":"fixture-secret"}`), nil
			}
			return response(200, `{"properties":{"provisioningState":"Succeeded","instanceView":{"statuses":[{"code":"PowerState/running"}]}}}`), nil
		}
	})}
	v := ScanInventory(context.Background(), a, "azure", "", Inventory{}, func(Inventory) {})
	if v.Complete || v.Stats.Total != 1 || len(v.Failures) != 1 || v.Failures[0].Region != "westus" || !strings.Contains(v.Scope, "非整个订阅") {
		t.Fatal(v)
	}
	if v.Items[0].CPU != 2 || v.Items[0].Memory != 4096 {
		t.Fatal("missing official SKU metadata", v.Items)
	}
	blocked = false
	v = ScanInventory(context.Background(), a, "azure", "westus", v, func(Inventory) {})
	if !v.Complete || v.Stats.Total != 2 {
		t.Fatal(v)
	}
	foreign = true
	v = ScanInventory(context.Background(), a, "azure", "", Inventory{}, func(Inventory) {})
	if v.Complete || v.Stats.Total != 2 {
		t.Fatal("foreign VM counted", v)
	}
}

func TestLinodeInventoryAllPagesSpecsAndPublicAddresses(t *testing.T) {
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Error("cloud write")
		}
		if r.URL.Path == "/v4/regions" {
			return response(200, `{"data":[],"pages":1}`), nil
		}
		if r.URL.Query().Get("page") == "1" {
			return response(200, `{"data":[],"pages":2}`), nil
		}
		return response(200, `{"data":[{"id":9,"label":"late","region":"ap-northeast","status":"offline","type":"small","specs":{"vcpus":2,"memory":4096,"disk":51200},"ipv4":["8.8.8.8","10.1.2.3","127.0.0.1"],"ipv6":"2001:4860::1/128"}],"pages":2}`), nil
	})}}
	v := ScanInventory(context.Background(), l, "linode", "", Inventory{}, func(Inventory) {})
	if !v.Complete || v.Stats.Total != 1 || v.Stats.Stopped != 1 || v.Items[0].CPU != 2 || v.Items[0].Disk != 50 || len(v.Items[0].IPs) != 2 {
		t.Fatal(v)
	}
}

func TestDigitalOceanInventoryAccountPaginationAndFailureIsNotEmpty(t *testing.T) {
	fail := false
	calls := 0
	d := &DigitalOcean{rest: rest{base: "https://api.digitalocean.com/v2", h: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Error("cloud write")
		}
		if r.URL.Path == "/v2/regions" {
			return response(200, `{"regions":[]}`), nil
		}
		calls++
		if fail && r.URL.Query().Get("page") == "2" {
			return response(403, `{"message":"fixture-secret"}`), nil
		}
		batch := []map[string]any{}
		count, start := 200, 1
		if r.URL.Query().Get("page") == "2" {
			count = 1
			start = 201
		}
		for n := 0; n < count; n++ {
			batch = append(batch, map[string]any{"id": start + n, "name": "same", "status": "active", "region": map[string]string{"slug": "nyc3"}, "size": map[string]any{"slug": "s-small", "vcpus": 2, "memory": 2048, "disk": 50, "price_monthly": 12, "price_hourly": 0.02}})
		}
		raw, _ := json.Marshal(map[string]any{"droplets": batch})
		return response(200, string(raw)), nil
	})}}
	v := ScanInventory(context.Background(), d, "digitalocean", "", Inventory{}, func(Inventory) {})
	if !v.Complete || v.Stats.Total != 201 || calls != 2 || v.Items[0].CPU != 2 || v.Items[0].Pricing.Currency != "USD" {
		t.Fatal(v.Stats, calls)
	}
	fail = true
	v = ScanInventory(context.Background(), d, "digitalocean", "", Inventory{}, func(Inventory) {})
	raw, _ := json.Marshal(v)
	if v.Complete || len(v.Failures) != 1 || strings.Contains(string(raw), "fixture-secret") {
		t.Fatal("failed pagination became empty success or exposed secret", string(raw))
	}
}

func TestLinodeRegionalAndHourlyOnlyPrice(t *testing.T) {
	p := linType{ID: "small"}
	p.Price.Monthly = 10
	p.Price.Hourly = .02
	p.Region_prices = append(p.Region_prices, struct {
		ID              string
		Monthly, Hourly float64
	}{ID: "in-maa", Monthly: 12, Hourly: .03})
	c := p.choice("in-maa")
	if c.Pricing.Monthly != 12 || c.Pricing.Hourly != .03 || c.Pricing.Currency != "USD" {
		t.Fatal(c)
	}
	p.Price.Monthly = 0
	c = p.choice("other")
	if c.Pricing.Monthly != 0 || c.Pricing.Hourly != .02 {
		t.Fatal(c)
	}
	if advertisedPrice(0, 0, "unknown") != nil {
		t.Fatal("unknown price represented as free")
	}
}
