package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

type inventoryRoundTrip func(*http.Request) (*http.Response, error)

func (f inventoryRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestInventorySlowCompletedScanRemainsReadable(t *testing.T) {
	h := setup(t)
	defer h.a.stopInventories()
	id := h.add(t)
	gate := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	var reads atomic.Int32
	h.fake.beforeRead = func() { reads.Add(1); once.Do(func() { close(entered) }); <-gate }
	r := h.request("GET", "inventory?accountId="+id, nil, true)
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	<-entered
	h.a.inventoryMu.Lock()
	h.a.inventories[id].started = time.Now().Add(-75 * time.Second)
	h.a.inventoryMu.Unlock()
	close(gate)
	v := inventoryResult(t, h, id)
	if !v.Complete || reads.Load() != 1 {
		t.Fatal("slow completed result was replaced before it could be read", v.Phase, reads.Load())
	}
}

func TestInventoryExpiredPartialLineageForcesFullAWSRescan(t *testing.T) {
	h := setup(t)
	defer h.a.stopInventories()
	var eastReads atomic.Int32
	fixtureTransport := inventoryRoundTrip(func(r *http.Request) (*http.Response, error) {
		_ = r.ParseForm()
		action := r.Form.Get("Action")
		status := 200
		body := ""
		switch action {
		case "DescribeRegions":
			body = `<DescribeRegionsResponse><regionInfo><item><regionName>us-east-1</regionName></item><item><regionName>eu-west-1</regionName></item></regionInfo></DescribeRegionsResponse>`
		case "DescribeInstanceTypes":
			body = `<DescribeInstanceTypesResponse><instanceTypeSet><item><instanceType>t3.medium</instanceType></item></instanceTypeSet></DescribeInstanceTypesResponse>`
		case "DescribeInstances":
			if r.URL.Host == "ec2.eu-west-1.amazonaws.com" {
				status = 403
				body = `<Response><Errors><Error><Code>UnauthorizedOperation</Code></Error></Errors></Response>`
			} else {
				eastReads.Add(1)
				body = `<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>same</instanceId><instanceType>t3.medium</instanceType><instanceState><name>running</name></instanceState></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`
			}
		default:
			t.Errorf("unexpected/cloud-write action: %s", action)
			status = 400
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	h.a.Factory = func(name string, c cloud.Credentials) (cloud.Provider, error) {
		return cloud.NewWithClient(name, c, &http.Client{Transport: fixtureTransport})
	}
	r := h.request("POST", "accounts", map[string]any{"name": "AWS expiry fixture", "provider": "aws", "credentials": map[string]string{"accessKeyId": "fixture", "secretAccessKey": "fixture-not-real"}}, true)
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var acc account
	json.Unmarshal(r.Body.Bytes(), &acc)
	v := inventoryResult(t, h, acc.ID)
	if v.Complete || len(v.Failures) != 1 {
		t.Fatal(v)
	}
	before := eastReads.Load()
	h.a.inventoryMu.Lock()
	anchor := h.a.inventories[acc.ID].fullScanCompletedAt
	h.a.inventories[acc.ID].started = time.Now().Add(-20 * time.Second)
	h.a.inventoryMu.Unlock()
	if anchor.IsZero() {
		t.Fatal("initial full scan lacks lineage timestamp")
	}
	r = h.request("GET", fmt.Sprintf("inventory?accountId=%s&retry=eu-west-1", acc.ID), nil, true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	inventoryResult(t, h, acc.ID)
	h.a.inventoryMu.Lock()
	inherited := h.a.inventories[acc.ID].fullScanCompletedAt
	h.a.inventoryMu.Unlock()
	if !inherited.Equal(anchor) || eastReads.Load() != before {
		t.Fatal("fresh partial retry reset lineage or re-read successful region")
	}
	h.a.inventoryMu.Lock()
	entry := h.a.inventories[acc.ID]
	entry.started = time.Now().Add(-20 * time.Second)
	entry.completedAt = time.Now()
	entry.fullScanCompletedAt = time.Now().Add(-61 * time.Second)
	h.a.inventoryMu.Unlock()
	r = h.request("GET", fmt.Sprintf("inventory?accountId=%s&retry=eu-west-1", acc.ID), nil, true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	inventoryResult(t, h, acc.ID)
	if eastReads.Load() != before+1 {
		t.Fatal("expired partial retry retained old successful region", before, eastReads.Load())
	}
}
