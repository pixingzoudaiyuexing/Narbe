package cloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

type transport func(*http.Request) (*http.Response, error)

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func client(f transport) *http.Client {
	return &http.Client{Transport: f, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func TestAWSSigningMatchesOfficialSignerAndCreationContract(t *testing.T) {
	c := Credentials{"accessKeyId": "fixture-id", "secretAccessKey": "fixture-only-secret", "sessionToken": "fixture-session"}
	a := &AWS{c: c}
	a.h = client(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		stamp, e := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
		if e != nil {
			t.Fatal(e)
		}
		copy := r.Clone(r.Context())
		copy.Header.Del("Authorization")
		if e = v4.NewSigner().SignHTTP(context.Background(), sdk.Credentials{AccessKeyID: c["accessKeyId"], SecretAccessKey: c["secretAccessKey"], SessionToken: c["sessionToken"]}, copy, digest(raw), "ec2", "us-east-1", stamp); e != nil {
			t.Fatal(e)
		}
		if copy.Header.Get("Authorization") != r.Header.Get("Authorization") {
			t.Fatalf("signature differs from official signer\n%s\n%s", r.Header.Get("Authorization"), copy.Header.Get("Authorization"))
		}
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		r.ParseForm()
		for key, want := range map[string]string{"Action": "RunInstances", "ClientToken": "immutable-task", "MinCount": "1", "MaxCount": "1", "NetworkInterface.1.SecurityGroupId.1": "sg", "MetadataOptions.HttpTokens": "required"} {
			if r.Form.Get(key) != want {
				t.Errorf("%s=%q", key, r.Form.Get(key))
			}
		}
		return response(200, `<RunInstancesResponse><instancesSet><item><instanceId>i-1</instanceId></item></instancesSet></RunInstancesResponse>`), nil
	})
	op, e := a.Create(context.Background(), Spec{Name: "x", Region: "us-east-1", Plan: "p", Image: "ami", Network: "subnet", Group: "sg"}, "immutable-task")
	if e != nil || op.ResourceID != "i-1" {
		t.Fatal(op, e)
	}
}
func TestAWSImagesFollowPagination(t *testing.T) {
	pages := 0
	a := &AWS{c: Credentials{}, h: client(func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		switch r.Form.Get("Action") {
		case "DescribeRegions":
			return response(200, `<r><regionInfo><item><regionName>us-east-1</regionName></item></regionInfo></r>`), nil
		case "DescribeInstanceTypeOfferings":
			return response(200, `<r><instanceTypeOfferingSet><item><instanceType>x</instanceType></item></instanceTypeOfferingSet></r>`), nil
		case "DescribeInstanceTypes":
			return response(200, `<r><instanceTypeSet><item><instanceType>x</instanceType><vCpuInfo><defaultVCpus>2</defaultVCpus></vCpuInfo><processorInfo><supportedArchitectures><item>arm64</item></supportedArchitectures></processorInfo></item></instanceTypeSet></r>`), nil
		case "DescribeImages":
			pages++
			if pages == 1 {
				return response(200, `<r><imagesSet></imagesSet><nextToken>second</nextToken></r>`), nil
			}
			if r.Form.Get("NextToken") != "second" {
				t.Fatal("missing image cursor")
			}
			return response(200, `<r><imagesSet><item><imageId>ami-new</imageId><name>debian-13-arm64</name><architecture>arm64</architecture></item></imagesSet></r>`), nil
		default:
			return response(200, `<r/>`), nil
		}
	})}
	c, e := a.Catalog(context.Background(), "us-east-1", "x")
	if e != nil || pages != 2 || len(c.Images) != 1 || c.Images[0].ID != "ami-new" {
		t.Fatal(c, e, pages)
	}
}
func TestLinodeCatalogRealDateAndAvailabilityFormat(t *testing.T) {
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v4/regions":
			return response(200, `{"data":[{"id":"r","label":"Tokyo","status":"ok","capabilities":["Linodes"]}],"pages":1}`), nil
		case "/v4/linode/types":
			return response(200, `{"data":[{"id":"p","label":"small","memory":2048,"disk":25600,"vcpus":2}],"pages":1}`), nil
		case "/v4/regions/r/availability":
			return response(200, `[{"available":true,"plan":"p","region":"r"}]`), nil
		case "/v4/images":
			return response(200, `{"data":[{"id":"im","label":"Debian 13","status":"available","eol":"2035-05-31T04:00:00"},{"id":"old","status":"available","eol":"2020-01-01T00:00:00"}],"pages":1}`), nil
		default:
			return response(200, `{"data":[],"pages":1}`), nil
		}
	})}}
	c, e := l.Catalog(context.Background(), "r", "")
	if e != nil || len(c.Images) != 1 || len(c.Plans) != 1 {
		t.Fatal(c, e)
	}
	if expired("") || expired("2035-05-31T04:00:00Z") || !expired("nonsense") {
		t.Fatal("date parser")
	}
}
func TestLinodeSSHOnlyOmitsPasswordAndTracksTask(t *testing.T) {
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(r *http.Request) (*http.Response, error) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if _, ok := b["root_pass"]; ok {
			t.Fatal("empty password field sent")
		}
		if len(b["authorized_keys"].([]any)) != 1 || b["tags"].([]any)[0] != "narbe-job-id" {
			t.Fatal(b)
		}
		return response(200, `{"id":123}`), nil
	})}}
	op, e := l.Create(context.Background(), Spec{Name: "test", SSHPublicKey: "ssh-ed25519 fixture", Region: "r"}, "job-id")
	if e != nil || op.ResourceID != "123" {
		t.Fatal(op, e)
	}
}
func TestPaginationContinuesThroughEmptyPage(t *testing.T) {
	pages := 0
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(r *http.Request) (*http.Response, error) {
		pages++
		if pages == 1 {
			return response(200, `{"data":[],"pages":2}`), nil
		}
		return response(200, `{"data":[{"id":7,"label":"last","region":"r","status":"running"}],"pages":2}`), nil
	})}}
	items, e := l.Instances(context.Background(), "")
	if e != nil || pages != 2 || len(items) != 1 {
		t.Fatal(items, e, pages)
	}
}
func TestDigitalOceanRequiresActionIDAndChecksActionBeforeState(t *testing.T) {
	calls := 0
	d := &DigitalOcean{rest: rest{base: "https://api.digitalocean.com/v2", h: client(func(r *http.Request) (*http.Response, error) { calls++; return response(201, `{}`), nil })}}
	_, e := d.Action(context.Background(), Instance{ID: "1", Region: "r"}, "reboot", "task")
	if !Uncertain(e) || calls != 1 {
		t.Fatal("missing action ID not uncertain", e)
	}
	d.h = client(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/actions/42" {
			t.Fatal("state read before action complete")
		}
		return response(200, `{"action":{"status":"in-progress"}}`), nil
	})
	done, e := d.Poll(context.Background(), Operation{ResourceID: "1", ActionID: "42", Expected: "active"})
	if e != nil || done {
		t.Fatal("unfinished action marked done", e)
	}
}
func TestDigitalOceanCatalogChecksRegionalDiskCompatibility(t *testing.T) {
	d := &DigitalOcean{rest: rest{base: "https://api.digitalocean.com/v2", h: client(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v2/regions":
			return response(200, `{"regions":[{"slug":"r","name":"Tokyo","available":true,"sizes":["p"]}]}`), nil
		case "/v2/sizes":
			return response(200, `{"sizes":[{"slug":"p","available":true,"regions":["r"],"disk":25}]}`), nil
		case "/v2/images":
			return response(200, `{"images":[{"id":1,"name":"Debian 13","status":"available","regions":["r"],"min_disk_size":10},{"id":2,"name":"elsewhere","status":"available","regions":["other"]}]}`), nil
		default:
			return response(200, `{"ssh_keys":[]}`), nil
		}
	})}}
	c, e := d.Catalog(context.Background(), "r", "")
	if e != nil || len(c.Images) != 1 || len(c.Plans) != 1 {
		t.Fatal(c, e)
	}
	if e = ValidateSpec(Spec{Name: "x", Region: "r", Plan: "p", Image: "1", Architecture: "arm64"}, c); e == nil {
		t.Fatal("incompatible requested ARM accepted")
	}
}
func TestMutatingNetworkFailuresAreUncertainAndErrorsNeverLeak(t *testing.T) {
	r := rest{base: "https://api.digitalocean.com/v2", h: client(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fixture secret in raw transport error")
	})}
	if e := r.call(context.Background(), "POST", "/droplets", nil, nil); !Uncertain(e) || strings.Contains(e.Error(), "fixture secret") {
		t.Fatal(e)
	}
	for _, status := range []int{403, 422, 429, 500} {
		r.h = client(func(*http.Request) (*http.Response, error) {
			return response(status, `{"message":"quota limit exceeded token=fixture-secret"}`), nil
		})
		e := r.call(context.Background(), "POST", "/droplets", nil, nil)
		if e == nil || strings.Contains(e.Error(), "fixture-secret") || !strings.Contains(e.Error(), "配额") {
			t.Fatal(e)
		}
	}
}
func TestAzureUniquePlanRefusesExistingResourcesAndPreservesScope(t *testing.T) {
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture-token", expires: time.Now().Add(time.Hour)}
	s := Spec{Name: strings.Repeat("x", 63), Region: "r"}
	x, y := a.Plan(s, "abcdefghijklmnop1"), a.Plan(s, "abcdefghijklmnop2")
	if x.ResourceID == y.ResourceID || len(x.Owned) != 4 {
		t.Fatal("name collision", x, y)
	}
	puts := 0
	a.h = client(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			puts++
		}
		return response(200, `{"tags":{"narbe-job":"other"}}`), nil
	})
	_, e := a.Create(context.Background(), s, "abcdefghijklmnop1")
	if e == nil || puts != 0 {
		t.Fatal("existing resource overwritten", e)
	}
	for _, path := range []string{"https://evil.test/a", "/subscriptions/other/resourceGroups/rg/x", "//evil.test/x", "/subscriptions/sub/../other"} {
		if a.pathOK(path) {
			t.Fatal("path scope accepted", path)
		}
	}
}
func TestAzureLongRunningOperationAndMaliciousPollURL(t *testing.T) {
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture", expires: time.Now().Add(time.Hour)}
	id := a.group() + "/providers/Microsoft.Compute/virtualMachines/x"
	phase := 0
	a.h = client(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			res := response(202, "")
			res.Header.Set("Azure-AsyncOperation", "https://management.azure.com/subscriptions/sub/providers/Microsoft.Compute/locations/r/operations/op")
			return res, nil
		}
		if strings.Contains(r.URL.Path, "operations") {
			phase++
			if phase == 1 {
				return response(200, `{"status":"InProgress"}`), nil
			}
			return response(200, `{"status":"Succeeded"}`), nil
		}
		return response(200, `{"properties":{"provisioningState":"Succeeded","instanceView":{"statuses":[{"code":"PowerState/running"}]}}}`), nil
	})
	op, e := a.Action(context.Background(), Instance{ID: id, Region: "r"}, "reboot", "task")
	if e != nil || op.PollURL == "" {
		t.Fatal(op, e)
	}
	if done, e := a.Poll(context.Background(), op); e != nil || done {
		t.Fatal("old running state accepted during LRO", e)
	}
	if done, e := a.Poll(context.Background(), op); e != nil || !done {
		t.Fatal("LRO success not recognized", e)
	}
	a.h = client(func(*http.Request) (*http.Response, error) {
		res := response(202, "")
		res.Header.Set("Azure-AsyncOperation", "https://evil.test/steal")
		return res, nil
	})
	_, e = a.Action(context.Background(), Instance{ID: id}, "start", "task")
	if !Uncertain(e) {
		t.Fatal("malicious LRO URL accepted", e)
	}
}
func FuzzAzureResourceBoundary(f *testing.F) {
	a := &Azure{c: Credentials{"subscriptionId": "sub"}}
	for _, p := range []string{"/subscriptions/sub/resourceGroups/rg", "https://evil.test", "/subscriptions/other/a", "/subscriptions/sub/../other"} {
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if len(p) > 4096 {
			t.Skip()
		}
		if a.pathOK(p) && (!strings.HasPrefix(strings.ToLower(p), "/subscriptions/sub/") || strings.Contains(p, "/../")) {
			t.Fatal(fmt.Sprintf("scope escape %q", p))
		}
	})
}

func TestAzureCreateUsesDiskAPIVersionAndRecordsNetworkLedger(t *testing.T) {
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture", expires: time.Now().Add(time.Hour)}
	created := map[string]bool{}
	var vmBody map[string]any
	gets := 0
	a.h = client(func(r *http.Request) (*http.Response, error) {
		path := r.URL.Path
		if strings.Contains(path, "/disks/") {
			gets++
			if r.URL.Query().Get("api-version") != "2024-03-02" {
				return response(400, `{"error":{"code":"InvalidApiVersionParameter"}}`), nil
			}
		}
		if r.Method == "GET" {
			if !created[path] {
				return response(404, `{}`), nil
			}
			return response(200, `{"properties":{"provisioningState":"Succeeded"}}`), nil
		}
		if r.Method == "PUT" {
			created[path] = true
			if strings.Contains(path, "/virtualMachines/") {
				json.NewDecoder(r.Body).Decode(&vmBody)
			}
			return response(201, `{}`), nil
		}
		t.Fatal(r.Method)
		return nil, nil
	})
	op, e := a.Create(context.Background(), Spec{ScriptMode: "cloud-config", UserData: "#cloud-config\nruncmd:\n - echo '中文 $HOME'\n", Name: "test", Region: "r", Image: "Canonical|ubuntu|sku|version", Network: a.group() + "/providers/Microsoft.Network/virtualNetworks/net/subnets/default", Username: "narbe", SSHPublicKey: "ssh-ed25519 fixture", ManagementCIDR: "192.0.2.1/32"}, "abcdefghijklmnop")
	if e != nil || gets != 1 || len(op.Owned) != 4 || len(created) != 4 || vmBody == nil {
		t.Fatal(op, e, gets, created)
	}
	props := vmBody["properties"].(map[string]any)
	profile := props["osProfile"].(map[string]any)
	raw, err := base64.StdEncoding.DecodeString(profile["customData"].(string))
	if err != nil || string(raw) != "#cloud-config\nruncmd:\n - echo '中文 $HOME'\n" {
		t.Fatal("Azure customData encoding changed", err)
	}
	if _, ok := props["userData"]; ok {
		t.Fatal("Azure userData misused")
	}
	disk := props["storageProfile"].(map[string]any)["osDisk"].(map[string]any)
	if !strings.HasSuffix(op.Owned[3], "/"+disk["name"].(string)) {
		t.Fatal("disk ledger mismatch")
	}
}

func TestAzureLocationPollAcceptsEmptyCompletionAndRetryAfter(t *testing.T) {
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture", expires: time.Now().Add(time.Hour)}
	op := Operation{ResourceID: a.group() + "/providers/Microsoft.Compute/virtualMachines/x", PollURL: "/subscriptions/sub/providers/Microsoft.Compute/locations/r/operations/op", PollKind: "location", Expected: "deleted"}
	a.h = client(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "operations") {
			return response(204, ""), nil
		}
		return response(404, `{}`), nil
	})
	if done, e := a.Poll(context.Background(), op); e != nil || !done {
		t.Fatal("Location 204 did not finish", e)
	}
	op.NextPoll = time.Now().Add(time.Hour).Unix()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.notBefore = nil
	a.h = client(func(*http.Request) (*http.Response, error) {
		t.Fatal("polled before Retry-After deadline")
		return nil, nil
	})
	if _, e := a.Poll(ctx, op); e == nil {
		t.Fatal("cancelled wait ignored")
	}
	h := http.Header{}
	h.Set("Retry-After", "60")
	if retryDelay(h) != time.Minute {
		t.Fatal("Retry-After ignored")
	}
}

func TestAWSAccountDiscoveryUsesConfiguredRegion(t *testing.T) {
	a := &AWS{c: Credentials{"defaultRegion": "ap-northeast-1"}, h: client(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "ec2.ap-northeast-1.amazonaws.com" {
			t.Fatal("wrong validation region", r.URL.Host)
		}
		return response(200, `<r><regionInfo><item><regionName>us-east-1</regionName></item><item><regionName>ap-northeast-1</regionName></item></regionInfo></r>`), nil
	})}
	c, e := a.Catalog(context.Background(), "", "")
	if e != nil || c.Regions[0].ID != "ap-northeast-1" {
		t.Fatal(c, e)
	}
	if e := ValidateCredentials("aws", Credentials{"accessKeyId": "fixture", "secretAccessKey": "fixture", "defaultRegion": "evil.test"}); e == nil {
		t.Fatal("invalid region accepted")
	}
}
