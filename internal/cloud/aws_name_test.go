package cloud

import (
	"context"
	"net/http"
	"testing"
)

func TestAWSBlankNameTagFallsBackToStableInstanceID(t *testing.T) {
	a := &AWS{c: Credentials{"accessKeyId": "fixture", "secretAccessKey": "fixture"}, h: client(func(r *http.Request) (*http.Response, error) {
		_ = r.ParseForm()
		if r.Form.Get("Action") != "DescribeInstances" {
			t.Fatal("unexpected write")
		}
		return response(200, `<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>i-stable</instanceId><instanceState><name>running</name></instanceState><tagSet><item><key>Name</key><value> </value></item></tagSet></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`), nil
	})}
	items, e := a.Instances(context.Background(), "us-east-1")
	if e != nil || len(items) != 1 || items[0].Name != "i-stable" || items[0].ID != "i-stable" || items[0].Region != "us-east-1" {
		t.Fatal(items, e)
	}
}
