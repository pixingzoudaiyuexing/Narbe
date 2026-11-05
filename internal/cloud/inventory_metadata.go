package cloud

import (
	"context"
	"net/url"
	"strconv"
)

type awsType struct {
	ID        string   `xml:"instanceType"`
	CPU       int      `xml:"vCpuInfo>defaultVCpus"`
	Memory    int      `xml:"memoryInfo>sizeInMiB"`
	Arch      []string `xml:"processorInfo>supportedArchitectures>item"`
	LocalDisk int      `xml:"instanceStorageInfo>totalSizeInGB"`
}

func (a *AWS) enrichInstances(ctx context.Context, region string, items []Instance) error {
	ids := []string{}
	seen := map[string]bool{}
	for _, i := range items {
		if !seen[i.Plan] {
			seen[i.Plan] = true
			ids = append(ids, i.Plan)
		}
	}
	types := map[string]awsType{}
	for offset := 0; offset < len(ids); offset += 100 {
		end := offset + 100
		if end > len(ids) {
			end = len(ids)
		}
		params := url.Values{}
		for n, id := range ids[offset:end] {
			params.Set("InstanceType."+strconv.Itoa(n+1), id)
		}
		var res struct {
			Items []awsType `xml:"instanceTypeSet>item"`
		}
		if e := retryRead(ctx, func() error { return a.call(ctx, region, "DescribeInstanceTypes", params, &res) }); e != nil {
			return e
		}
		for _, t := range res.Items {
			types[t.ID] = t
		}
	}
	for j := range items {
		i := &items[j]
		if t, ok := types[i.Plan]; ok {
			if i.CPU == 0 {
				i.CPU = t.CPU
			}
			i.Memory = t.Memory
			if i.Architecture == "" && len(t.Arch) == 1 {
				i.Architecture = Arch(t.Arch[0])
			}
			i.Disk = t.LocalDisk
			i.DiskKind = "实例存储；EBS 系统盘另计"
		}
	}
	return nil
}

func advertisedPrice(monthly, hourly float64, source string) *Pricing {
	if monthly <= 0 && hourly <= 0 {
		return nil
	}
	if monthly < 0 {
		monthly = 0
	}
	if hourly < 0 {
		hourly = 0
	}
	return &Pricing{Monthly: monthly, Hourly: hourly, Currency: "USD", Source: source}
}
