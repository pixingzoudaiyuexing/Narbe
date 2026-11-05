// Package lifecycle enriches cloud images with advisory upstream support dates.
// It never replaces provider-side availability or compatibility checks.
package lifecycle

import (
	"context"
	"encoding/csv"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"

	"strings"
	"sync"
	"time"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

const UbuntuURL = "https://debian.pages.debian.net/distro-info-data/ubuntu.csv"
const DebianURL = "https://debian.pages.debian.net/distro-info-data/debian.csv"
const WindowsURL = "https://learn.microsoft.com/en-us/windows/release-health/windows-server-release-info?accept=text/markdown"

type Release struct{ Version, Name, Start, StandardEnd, SecurityEnd string }
type entry struct {
	releases []Release
	checked  time.Time
	retry    time.Time
}
type Service struct {
	HTTP  *http.Client
	Now   func() time.Time
	mu    sync.Mutex
	cache map[string]entry
}

func New() *Service {
	return &Service{HTTP: &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Now: time.Now, cache: map[string]entry{}}
}
func (s *Service) get(ctx context.Context, family string) ([]Release, time.Time) {
	s.mu.Lock()
	e := s.cache[family]
	now := s.Now()
	if now.Before(e.retry) {
		s.mu.Unlock()
		return e.releases, e.checked
	}
	s.mu.Unlock()
	source := map[string]string{"ubuntu": UbuntuURL, "debian": DebianURL, "windows": WindowsURL}[family]
	if source == "" {
		return nil, time.Time{}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
	var releases []Release
	if err == nil {
		var res *http.Response
		res, err = s.HTTP.Do(req)
		if err == nil {
			defer res.Body.Close()
			if res.StatusCode != 200 {
				err = fmt.Errorf("upstream status %d", res.StatusCode)
			} else {
				var raw []byte
				raw, err = io.ReadAll(io.LimitReader(res.Body, 2<<20))
				if err == nil {
					if family == "windows" {
						releases, err = ParseWindows(string(raw))
					} else {
						releases, err = ParseCSV(string(raw), family)
					}
				}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && len(releases) > 0 {
		e = entry{releases: releases, checked: now, retry: now.Add(24 * time.Hour)}
	} else {
		e.retry = now.Add(time.Hour)
	}
	s.cache[family] = e
	return e.releases, e.checked
}
func (s *Service) Enrich(ctx context.Context, c *cloud.Catalog) {
	families := map[string]bool{}
	for _, im := range c.Images {
		families[im.Family] = true
	}
	type result struct {
		family   string
		releases []Release
		checked  time.Time
	}
	ch := make(chan result, len(families))
	for family := range families {
		go func(f string) { r, t := s.get(ctx, f); ch <- result{f, r, t} }(family)
	}
	data := map[string]result{}
	for range families {
		r := <-ch
		data[r.family] = r
	}
	for i := range c.Images {
		im := &c.Images[i]
		r := data[im.Family]
		im.Maintenance, im.MaintenanceEnd = Classify(im.Name, im.Family, r.releases, s.Now())
		if im.Extra == nil {
			im.Extra = map[string]string{}
		}
		im.Extra["lifecycleSource"] = map[string]string{"ubuntu": UbuntuURL, "debian": DebianURL, "windows": WindowsURL}[im.Family]
		if !r.checked.IsZero() {
			im.Extra["lifecycleCheckedAt"] = r.checked.UTC().Format(time.RFC3339)
		}
	}
}
func ParseCSV(raw, family string) ([]Release, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	rows, e := reader.ReadAll()
	if e != nil || len(rows) < 2 {
		return nil, fmt.Errorf("invalid lifecycle CSV")
	}
	cols := map[string]int{}
	for i, k := range rows[0] {
		cols[k] = i
	}
	for _, key := range []string{"version", "series", "release", "eol"} {
		if _, ok := cols[key]; !ok {
			return nil, fmt.Errorf("missing column %s", key)
		}
	}
	value := func(row []string, k string) string {
		i, ok := cols[k]
		if !ok || i >= len(row) {
			return ""
		}
		return row[i]
	}
	out := []Release{}
	for _, row := range rows[1:] {
		version := strings.Fields(value(row, "version"))
		if len(version) == 0 {
			continue
		}
		r := Release{Version: version[0], Name: value(row, "series"), Start: value(row, "release"), StandardEnd: value(row, "eol")}
		r.SecurityEnd = r.StandardEnd
		if family == "ubuntu" {
			if v := value(row, "eol-server"); v != "" {
				r.StandardEnd = v
				r.SecurityEnd = v
			}
		} else if family == "debian" {
			if v := value(row, "eol-lts"); v != "" {
				r.SecurityEnd = v
			}
		}
		out = append(out, r)
	}
	return out, nil
}

var tablePattern = regexp.MustCompile(`(?is)<table\b[^>]*>.*?</table>`)
var rowPattern = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
var cellPattern = regexp.MustCompile(`(?is)<t[hd]\b[^>]*>(.*?)</t[hd]>`)
var tagPattern = regexp.MustCompile(`<[^>]*>`)
var yearPattern = regexp.MustCompile(`\b20[0-9]{2}\b`)
var datePattern = regexp.MustCompile(`\b20[0-9]{2}-[0-9]{2}-[0-9]{2}\b`)

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagPattern.ReplaceAllString(s, " "))), " ")
}
func ParseWindows(raw string) ([]Release, error) {
	if strings.Contains(raw, "|") {
		if releases := parseWindowsMarkdown(raw); len(releases) > 0 {
			return releases, nil
		}
	}

	out := []Release{}
	for _, table := range tablePattern.FindAllString(raw, -1) {
		header := []string{}
		std, ext, version, start := -1, -1, -1, -1
		for _, row := range rowPattern.FindAllStringSubmatch(table, -1) {
			cells := cellPattern.FindAllStringSubmatch(row[1], -1)
			texts := []string{}
			for _, c := range cells {
				texts = append(texts, clean(c[1]))
			}
			if len(header) == 0 {
				header = texts
				for i, v := range header {
					v = strings.ToLower(v)
					switch {
					case strings.Contains(v, "mainstream") && strings.Contains(v, "end"):
						std = i
					case strings.Contains(v, "extended") && strings.Contains(v, "end"):
						ext = i
					case strings.Contains(v, "version") || strings.Contains(v, "windows server release"):
						version = i
					case strings.Contains(v, "availability") || strings.Contains(v, "start date"):
						start = i
					}
				}
				continue
			}
			if std < 0 || ext < 0 || version < 0 || len(texts) <= max(std, ext, version) {
				continue
			}
			v := yearPattern.FindString(texts[version])
			if v == "" {
				continue
			}
			r := Release{Version: v, StandardEnd: datePattern.FindString(texts[std]), SecurityEnd: datePattern.FindString(texts[ext])}
			if start >= 0 && start < len(texts) {
				r.Start = datePattern.FindString(texts[start])
			}
			if r.StandardEnd != "" && r.SecurityEnd != "" {
				out = append(out, r)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Windows lifecycle table unavailable")
	}
	return out, nil
}
func Classify(name, family string, releases []Release, now time.Time) (string, string) {
	lower := strings.ToLower(name)
	var version string
	switch family {
	case "ubuntu":
		m := regexp.MustCompile(`(?:^|[^0-9])([0-9]{2})[._-]([0-9]{2})(?:[^0-9]|$)`).FindStringSubmatch(lower)
		if len(m) > 0 {
			version = m[1] + "." + m[2]
		}
	case "debian":
		m := regexp.MustCompile(`debian[^0-9]*([0-9]{1,2})(?:[^0-9]|$)`).FindStringSubmatch(lower)
		if len(m) > 0 {
			version = m[1]
		}
	case "windows":
		version = yearPattern.FindString(lower)
	}
	for _, r := range releases {
		matched := version != "" && r.Version == version
		if !matched && family != "windows" && r.Name != "" && strings.Contains(lower, strings.ToLower(r.Name)) {
			matched = true
		}
		if !matched {
			continue
		}
		start, e := time.Parse("2006-01-02", r.Start)
		if e != nil || start.After(now) {
			return "unreleased", ""
		}
		standard, e1 := time.Parse("2006-01-02", r.StandardEnd)
		security, e2 := time.Parse("2006-01-02", r.SecurityEnd)
		if e1 != nil || e2 != nil {
			return "unknown", ""
		}
		if now.Before(standard.AddDate(0, 0, 1)) {
			return "standard", r.StandardEnd
		}
		if now.Before(security.AddDate(0, 0, 1)) {
			if family == "debian" {
				return "lts", r.SecurityEnd
			}
			return "extended", r.SecurityEnd
		}
		return "ended", r.SecurityEnd
	}
	return "unknown", ""
}

func parseWindowsMarkdown(raw string) []Release {
	out := []Release{}
	version, start, std, ext := -1, -1, -1, -1
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = clean(cells[i])
		}
		if strings.Contains(strings.ToLower(line), "mainstream support end date") && strings.Contains(strings.ToLower(line), "extended support end date") {
			for i, v := range cells {
				v = strings.ToLower(v)
				switch {
				case strings.Contains(v, "windows server version"):
					version = i
				case strings.Contains(v, "availability date"):
					start = i
				case strings.Contains(v, "mainstream support end date"):
					std = i
				case strings.Contains(v, "extended support end date"):
					ext = i
				}
			}
			continue
		}
		if min(version, start, std, ext) < 0 || len(cells) <= max(version, start, std, ext) {
			continue
		}
		v := yearPattern.FindString(cells[version])
		if v == "" {
			continue
		}
		standard := datePattern.FindString(cells[std])
		if strings.Contains(strings.ToLower(cells[std]), "end of updates") {
			standard = "1970-01-01"
		}
		r := Release{Version: v, Start: datePattern.FindString(cells[start]), StandardEnd: standard, SecurityEnd: datePattern.FindString(cells[ext])}
		if r.Start != "" && r.SecurityEnd != "" {
			out = append(out, r)
		}
	}
	return out
}
