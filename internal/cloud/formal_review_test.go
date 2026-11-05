package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestReviewAzureWindowsComputerNameDoesNotEndInHyphen(t *testing.T) {
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture", expires: time.Now().Add(time.Hour)}
	created := map[string]bool{}
	computerName := ""
	a.h = client(func(r *http.Request) (*http.Response, error) {
		path := r.URL.Path
		if r.Method == "GET" {
			if !created[path] {
				return response(404, `{}`), nil
			}
			return response(200, `{"properties":{"provisioningState":"Succeeded"}}`), nil
		}
		created[path] = true
		if strings.Contains(path, "/virtualMachines/") {
			var b struct {
				Properties struct{ OSProfile struct{ ComputerName string } }
			}
			json.NewDecoder(r.Body).Decode(&b)
			computerName = b.Properties.OSProfile.ComputerName
		}
		return response(201, `{}`), nil
	})
	_, e := a.Create(context.Background(), Spec{Name: strings.Repeat("a", 14) + "-server", Region: "r", Image: "MicrosoftWindowsServer|WindowsServer|2025|version", Network: a.group() + "/providers/Microsoft.Network/virtualNetworks/n/subnets/s", Username: "narbe", Password: "Strong-fixture-password1", ManagementCIDR: "192.0.2.1/32"}, "review-task-token")
	if e != nil {
		t.Fatal(e)
	}
	if computerName == "" || len(computerName) > 15 || strings.HasSuffix(computerName, "-") {
		t.Fatalf("invalid Windows computerName sent: %q", computerName)
	}
}
func TestReviewAWSImagesHaveServerSideNameFilterAndAmazonLinux(t *testing.T) {
	imageCalls := 0
	a := &AWS{c: Credentials{}, h: client(func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		switch r.Form.Get("Action") {
		case "DescribeRegions":
			return response(200, `<r><regionInfo><item><regionName>us-east-1</regionName></item></regionInfo></r>`), nil
		case "DescribeInstanceTypeOfferings":
			return response(200, `<r/>`), nil
		case "DescribeInstanceTypes":
			return response(200, `<r/>`), nil
		case "DescribeImages":
			imageCalls++
			hasName := false
			for k, v := range r.Form {
				if strings.HasSuffix(k, ".Name") && len(v) > 0 && v[0] == "name" {
					hasName = true
				}
			}
			if !hasName {
				t.Error("DescribeImages scans all official AMIs instead of supported system name patterns")
			}
			return response(200, `<r><imagesSet><item><imageId>ami-amazon</imageId><name>al2023-ami-2026.10.0-kernel-6.12-x86_64</name><architecture>x86_64</architecture></item><item><imageId>ami-ubuntu</imageId><name>ubuntu/images/hvm-ssd/ubuntu-noble-24.04-amd64-server-20261001</name><architecture>x86_64</architecture></item><item><imageId>ami-debian</imageId><name>debian-13-amd64-20261001</name><architecture>x86_64</architecture></item><item><imageId>ami-windows</imageId><name>Windows_Server-2025-English-Full-Base-2026.10.01</name><architecture>x86_64</architecture></item></imagesSet></r>`), nil
		default:
			return response(200, `<r/>`), nil
		}
	})}
	c, e := a.Catalog(context.Background(), "us-east-1", "")
	if e != nil {
		t.Fatal(e)
	}
	if imageCalls < 1 || len(c.Images) != 4 {
		t.Fatalf("supported system disappeared: images=%+v calls=%d", c.Images, imageCalls)
	}
	if im, ok := Find(c.Images, "ami-amazon"); !ok || im.Family != "amazonlinux" {
		t.Fatalf("Amazon Linux not identified: %+v", im)
	}
}

func TestReviewAzureWindowsNameBoundaryRequests(t *testing.T) {
	for _, name := range []string{strings.Repeat("a", 14), strings.Repeat("b", 14) + "-long-name", "short", "---------------", "123456789012345", "--leading"} {
		t.Run(name, func(t *testing.T) {
			a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture", expires: time.Now().Add(time.Hour)}
			created := map[string]bool{}
			computerName := ""
			a.h = client(func(r *http.Request) (*http.Response, error) {
				path := r.URL.Path
				if r.Method == "GET" {
					if !created[path] {
						return response(404, `{}`), nil
					}
					return response(200, `{"properties":{"provisioningState":"Succeeded"}}`), nil
				}
				created[path] = true
				if strings.Contains(path, "/virtualMachines/") {
					var body struct {
						Properties struct{ OSProfile struct{ ComputerName string } }
					}
					json.NewDecoder(r.Body).Decode(&body)
					computerName = body.Properties.OSProfile.ComputerName
				}
				return response(201, `{}`), nil
			})
			s := Spec{Name: name, Region: "r", Image: "MicrosoftWindowsServer|WindowsServer|2025|v", Username: "narbe", Password: "Strong-fixture-password1", ManagementCIDR: "192.0.2.1/32"}
			op, e := a.Create(context.Background(), s, "fixed-review-token")
			if e != nil {
				t.Fatal(e)
			}
			if op.ResourceID != a.Plan(s, "fixed-review-token").ResourceID {
				t.Fatal("VM resource name changed")
			}
			if len(computerName) < 1 || len(computerName) > 15 || strings.HasPrefix(computerName, "-") || strings.HasSuffix(computerName, "-") {
				t.Fatal("invalid hostname", computerName)
			}
			numeric := true
			for _, ch := range computerName {
				if ch < '0' || ch > '9' {
					numeric = false
				}
			}
			if numeric {
				t.Fatal("all numeric hostname", computerName)
			}
		})
	}
}
func TestReviewAzureLedgerVerifiesScopeVMTagAndNeverDeletesDependencies(t *testing.T) {
	a := &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, access: "fixture", expires: time.Now().Add(time.Hour)}
	s := Spec{Name: "ledger-vm", Region: "r"}
	op := a.Plan(s, "original-task")
	deletes := 0
	tag := "original-task"
	a.h = client(func(r *http.Request) (*http.Response, error) {
		if r.Method == "DELETE" {
			deletes++
			if !strings.EqualFold(r.URL.Path, op.ResourceID) {
				t.Fatal("dependency deleted", r.URL.Path)
			}
			return response(202, ""), nil
		}
		if r.Method != "GET" {
			t.Fatal("unexpected write", r.Method)
		}
		return response(200, `{"id":"`+op.ResourceID+`","location":"r","tags":{"narbe-job":"`+tag+`"}}`), nil
	})
	if !a.MatchesCreation(context.Background(), s, "original-task", op, strings.ToUpper(op.ResourceID)) {
		t.Fatal("valid case-insensitive resource ledger rejected")
	}
	tag = "external-rebuild"
	if a.MatchesCreation(context.Background(), s, "original-task", op, op.ResourceID) {
		t.Fatal("same VM ID recreated by another task inherited ledger")
	}
	tag = "original-task"
	changed := op
	changed.Owned = append([]string{}, op.Owned...)
	changed.Owned[0] = "/subscriptions/other/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/x"
	if a.MatchesCreation(context.Background(), s, "original-task", changed, op.ResourceID) {
		t.Fatal("cross-subscription resource linked")
	}
	a.c["resourceGroup"] = "other"
	if a.MatchesCreation(context.Background(), s, "original-task", op, op.ResourceID) {
		t.Fatal("cross-resource-group ledger linked")
	}
	a.c["resourceGroup"] = "rg"
	if _, e := a.Action(context.Background(), Instance{ID: op.ResourceID, Region: "r"}, "delete", "delete-task"); e != nil {
		t.Fatal(e)
	}
	if deletes != 1 {
		t.Fatal("wrong write count", deletes)
	}
}
func TestReviewAWSFiltersPreserveArchitectureAndStopRepeatedCursor(t *testing.T) {
	pages := 0
	a := &AWS{c: Credentials{}, h: client(func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		switch r.Form.Get("Action") {
		case "DescribeRegions":
			return response(200, `<r><regionInfo><item><regionName>us-east-1</regionName></item></regionInfo></r>`), nil
		case "DescribeInstanceTypeOfferings":
			return response(200, `<r><instanceTypeOfferingSet><item><instanceType>p</instanceType></item></instanceTypeOfferingSet></r>`), nil
		case "DescribeInstanceTypes":
			return response(200, `<r><instanceTypeSet><item><instanceType>p</instanceType><processorInfo><supportedArchitectures><item>arm64</item></supportedArchitectures></processorInfo></item></instanceTypeSet></r>`), nil
		case "DescribeImages":
			pages++
			if r.Form.Get("Filter.4.Name") != "architecture" || r.Form.Get("Filter.4.Value.1") != "arm64" || r.Form.Get("Filter.5.Name") != "name" || r.Form.Get("IncludeDeprecated") != "false" || r.Form.Get("MaxResults") == "" {
				t.Fatal("filter contract changed", r.Form)
			}
			patterns := []string{}
			for key, value := range r.Form {
				if strings.HasPrefix(key, "Filter.5.Value.") {
					patterns = append(patterns, value...)
				}
			}
			for _, name := range []string{"Ubuntu-26.04", "ubuntu/images/noble", "debian-13", "Windows_Server-2025", "Rocky-9", "AlmaLinux-10", "Fedora-42", "CentOS-Stream", "al2023-ami-new", "al2029-ami-future", "amzn2-ami-latest", "amzn-ami-hvm"} {
				matched := false
				for _, pattern := range patterns {
					expression := regexp.QuoteMeta(pattern)
					expression = strings.ReplaceAll(expression, `\*`, ".*")
					expression = strings.ReplaceAll(expression, `\?`, ".")
					ok, _ := regexp.MatchString("^"+expression+"$", name)
					matched = matched || ok
				}
				if !matched {
					t.Errorf("existing or normal future system filtered out: %s", name)
				}
			}
			return response(200, `<r><imagesSet/><nextToken>same-token</nextToken></r>`), nil
		default:
			return response(200, `<r/>`), nil
		}
	})}
	_, e := a.Catalog(context.Background(), "us-east-1", "p")
	if e == nil || pages != 2 {
		t.Fatal("repeating image pages were not stopped", e, pages)
	}
}
func TestReviewLinodePasswordModesAndNoWeakRequestSent(t *testing.T) {
	writes := 0
	l := &Linode{rest: rest{base: "https://api.linode.com/v4", h: client(func(r *http.Request) (*http.Response, error) {
		writes++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["root_pass"]; ok {
			t.Fatal("SSH-only sends empty password")
		}
		if len(body["authorized_keys"].([]any)) != 1 {
			t.Fatal(body)
		}
		return response(200, `{"id":123}`), nil
	})}}
	for _, weak := range []string{"aaaaaaaaaaaa", "123456789012", "Password123!Password123!", "P@ssw0rd123!", "password1password1!"} {
		if _, e := l.Create(context.Background(), Spec{Password: weak}, "task"); e == nil {
			t.Fatal("weak password reaches provider")
		}
		if writes != 0 {
			t.Fatal("weak password generated HTTP write")
		}
	}
	if _, e := l.Create(context.Background(), Spec{Name: "ssh-only", SSHPublicKey: "ssh-ed25519 public-fixture"}, "task"); e != nil {
		t.Fatal(e)
	}
	if writes != 1 {
		t.Fatal(writes)
	}
	for _, password := range []string{"K8!vQ2$mR9#zT4@p", "cobalt orchard river lantern nebula meadow"} {
		if e := ValidateLinodePassword(password); e != nil {
			t.Fatal("strong password rejected by invented complexity rule", e)
		}
	}
}

func TestReviewExpiredDispatchNeverSendsCloudWrites(t *testing.T) {
	ctx := WithDispatchDeadline(context.Background(), time.Now().Add(-time.Minute))
	for _, provider := range []string{"aws", "azure", "linode", "digitalocean"} {
		t.Run(provider, func(t *testing.T) {
			writes := 0
			h := client(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					t.Error("expired intent sent a write", r.Method, r.URL.Path)
					writes++
				}
				return response(404, `{}`), nil
			})
			var p Provider
			switch provider {
			case "aws":
				p = &AWS{c: Credentials{}, h: h}
			case "azure":
				p = &Azure{c: Credentials{"subscriptionId": "sub", "resourceGroup": "rg"}, h: h, access: "fixture", expires: time.Now().Add(time.Hour)}
			case "linode":
				p = &Linode{rest: rest{h: h, base: "https://api.linode.com/v4"}}
			case "digitalocean":
				p = &DigitalOcean{rest: rest{h: h, base: "https://api.digitalocean.com/v2"}}
			}
			_, e := p.Create(ctx, Spec{Name: "freshly-expired", Region: "us-east-1", Image: "i"}, "expired-review-task")
			if !errors.Is(e, ErrDispatchExpired) || writes != 0 {
				t.Fatal("expired preflight reached a cloud write", e, writes)
			}
		})
	}
}
func TestReviewAlreadyDispatchedOperationContinuesPastQueueDeadline(t *testing.T) {
	ctx := WithDispatchDeadline(context.Background(), time.Now().Add(time.Hour))
	guard := ctx.Value(dispatchKey{}).(*dispatchGuard)
	writes := 0
	r := rest{base: "https://api.example.test", h: client(func(*http.Request) (*http.Response, error) {
		writes++
		guard.mu.Lock()
		guard.deadline = time.Now().Add(-time.Minute)
		guard.mu.Unlock()
		return response(200, `{}`), nil
	})}
	if e := r.call(ctx, "PUT", "/first-owned-resource", map[string]string{}, nil); e != nil {
		t.Fatal(e)
	}
	if e := r.call(ctx, "PUT", "/next-owned-resource", map[string]string{}, nil); e != nil {
		t.Fatal("queue deadline interrupted an already-dispatched multi-step operation", e)
	}
	if writes != 2 {
		t.Fatal(writes)
	}
}
