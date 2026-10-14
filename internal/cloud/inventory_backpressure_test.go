package cloud

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestAWSInventoryBackpressureBoundsFinishedRegionResults(t *testing.T) {
	var reads atomic.Int32
	fourth := make(chan struct{})
	a := &AWS{c: Credentials{"accessKeyId": "fixture", "secretAccessKey": "fixture"}, h: client(func(r *http.Request) (*http.Response, error) {
		_ = r.ParseForm()
		switch r.Form.Get("Action") {
		case "DescribeRegions":
			body := "<DescribeRegionsResponse><regionInfo>"
			for n := 1; n <= 12; n++ {
				body += fmt.Sprintf("<item><regionName>us-east-%d</regionName></item>", n)
			}
			return response(200, body+"</regionInfo></DescribeRegionsResponse>"), nil
		case "DescribeInstances":
			if reads.Add(1) == 4 {
				close(fourth)
			}
			return response(200, `<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>i-same</instanceId><instanceState><name>running</name></instanceState></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`), nil
		case "DescribeInstanceTypes":
			return response(200, `<DescribeInstanceTypesResponse><instanceTypeSet/></DescribeInstanceTypesResponse>`), nil
		default:
			t.Error("unexpected or mutating request", r.Form.Get("Action"))
			return response(400, ""), nil
		}
	})}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan Inventory, 1)
	go func() {
		first := true
		done <- ScanInventory(context.Background(), a, "aws", "", Inventory{}, func(Inventory) {
			if first {
				first = false
				close(entered)
				<-release
			}
		})
	}()
	<-entered
	accumulated := false
	select {
	case <-fourth:
		accumulated = true
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	v := <-done
	if accumulated {
		t.Fatal("paused result consumer allowed more than three finished-region results to accumulate")
	}
	if !v.Complete || v.Stats.Total != 12 {
		t.Fatal(v.Stats, v.Complete)
	}
}
