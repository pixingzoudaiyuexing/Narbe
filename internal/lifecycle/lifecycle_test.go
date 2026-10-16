package lifecycle

import (
	"context"
	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
	"os"
	"testing"
	"time"
)

func TestReleaseDatesDistinguishStandardLTSUnreleasedAndEnded(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	ubuntu, e := ParseCSV("version,codename,series,created,release,eol,eol-server,eol-esm\n24.04 LTS,Noble,noble,2023-10-12,2024-04-25,2029-05-31,2029-05-31,2034-04-25\n20.04 LTS,Focal,focal,2019-10-17,2020-04-23,2025-05-29,2025-05-29,2030-04-23\n26.10,Stonking,stonking,2026-04-24,2026-10-15,2027-07-15,,\n", "ubuntu")
	if e != nil {
		t.Fatal(e)
	}
	for name, want := range map[string]string{"ubuntu-noble-24.04-arm64": "standard", "ubuntu-20_04-lts": "ended", "ubuntu-26_10": "unreleased", "ubuntu-99.99": "unknown"} {
		if got, _ := Classify(name, "ubuntu", ubuntu, now); got != want {
			t.Fatal(name, got, want)
		}
	}
	debian, e := ParseCSV("version,codename,series,created,release,eol,eol-lts,eol-elts\n12,Bookworm,bookworm,2021-08-14,2023-06-10,2026-07-11,2028-06-30,2033-06-30\n11,Bullseye,bullseye,2019-07-06,2021-08-14,2024-08-14,2026-08-31,2031-06-30\n14,Forky,forky,2025-08-09,,,,\n", "debian")
	if e != nil {
		t.Fatal(e)
	}
	for name, want := range map[string]string{"Debian 12": "lts", "debian-11": "ended", "debian-14": "unreleased"} {
		if got, _ := Classify(name, "debian", debian, now); got != want {
			t.Fatal(name, got, want)
		}
	}
}
func TestWindowsOfficialMarkdownHandlesEndedMainstreamAndFutureVersions(t *testing.T) {
	raw := `| Windows Server version | Servicing option | Editions | Availability date | Mainstream support end date | Extended support end date |
| --- | --- | --- | --- | --- | --- |
| Windows Server 2025 | LTSC | Standard | 2024-11-01 | 2029-11-13 | 2034-11-14 |
| Windows Server 2019 (version 1809) | LTSC | Standard | 2018-11-13 | End of updates | 2029-01-09 |
| Windows Server 2028 | LTSC | Standard | 2028-11-01 | 2033-11-13 | 2038-11-14 |`
	releases, e := ParseWindows(raw)
	if e != nil || len(releases) != 3 {
		t.Fatal(releases, e)
	}
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	for name, want := range map[string]string{"Windows_Server-2025-datacenter": "standard", "Windows_Server-2019": "extended", "Windows_Server-2028": "unreleased"} {
		if got, _ := Classify(name, "windows", releases, now); got != want {
			t.Fatal(name, got, want)
		}
	}
}
func TestMalformedFeedsFailAsUnknown(t *testing.T) {
	if _, e := ParseCSV("garbage", "ubuntu"); e == nil {
		t.Fatal("bad CSV accepted")
	}
	if _, e := ParseWindows("<html>error</html>"); e == nil {
		t.Fatal("bad page accepted")
	}
	if got, _ := Classify("Ubuntu 26.04", "ubuntu", nil, time.Now()); got != "unknown" {
		t.Fatal(got)
	}
}
func TestOfficialLifecycleFeeds(t *testing.T) {
	if os.Getenv("NARBE_LIFECYCLE_LIVE") != "1" {
		t.Skip("opt-in public read-only upstream evidence")
	}
	s := New()
	c := cloud.Catalog{Images: []cloud.Choice{{Name: "Ubuntu 24.04 LTS", Family: "ubuntu"}, {Name: "Debian 13", Family: "debian"}, {Name: "Windows Server 2025", Family: "windows"}}}
	s.Enrich(context.Background(), &c)
	for _, im := range c.Images {
		t.Log(im.Name, im.Maintenance, im.MaintenanceEnd, im.Extra["lifecycleSource"])
		if im.Maintenance != "standard" {
			t.Fatalf("official feed not verified: %+v", im)
		}
	}
}

func TestOfficialCSVAllowsMissingOptionalTrailingFields(t *testing.T) {
	rows, e := ParseCSV("version,codename,series,created,release,eol,eol-server,eol-esm,eol-legacy\n4.10,Warty,warty,2004-03-05,2004-10-20,2006-04-30\n24.04 LTS,Noble,noble,2023-10-12,2024-04-25,2029-05-31,2029-05-31,2034-04-25,2039-04-28\n", "ubuntu")
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
}

func TestAmazonLinuxMaintenanceRemainsUnknownWithoutVerifiedMapping(t *testing.T) {
	status, end := Classify("al2023-ami-2026.10.0-kernel-x86_64", "amazonlinux", nil, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	if status != "unknown" || end != "" {
		t.Fatal("unverified Amazon Linux lifecycle presented as maintained", status, end)
	}
}
