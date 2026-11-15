package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	ControlURL        string
	RegistrationToken string
	DataDir           string
	AllowedPorts      []int
	Version           string
}

type savedIdentity struct {
	ControlURL string   `json:"controlURL"`
	Identity   Identity `json:"identity"`
	Seen       []string `json:"seen,omitempty"`
}

// Agent executes at most one bounded read-only check at a time. Failed result
// uploads remain durably pending and are retried before asking for another job.
type Agent struct {
	cfg     Config
	client  *http.Client
	observe func(context.Context) (Egress, error)
	check   func(context.Context, Target, time.Duration) Result
	saved   savedIdentity
	egress  Egress
	pending *ResultRequest
}

func NewAgent(cfg Config) (*Agent, error) {
	return newAgent(cfg, directClient(20*time.Second), ObserveEgress, Check)
}

// Tests inject a trusted transport for loopback HTTP; production has no
// insecure-control-channel flag or proxy inherited from environment variables.
func newAgent(cfg Config, client *http.Client, observe func(context.Context) (Egress, error), checker func(context.Context, Target, time.Duration) Result) (*Agent, error) {
	u, err := url.Parse(cfg.ControlURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, errors.New("NARBE_PROBE_CONTROL_URL must be a verified HTTPS origin")
	}
	cfg.ControlURL = strings.TrimSuffix(cfg.ControlURL, "/")
	if cfg.Version == "" {
		cfg.Version = Version
	}
	if len(cfg.Version) > 128 || len(cfg.AllowedPorts) == 0 || len(cfg.AllowedPorts) > 32 || cfg.DataDir == "" {
		return nil, errors.New("probe version, data directory or allowed ports are invalid")
	}
	for _, p := range cfg.AllowedPorts {
		if p < 1 || p > 65535 {
			return nil, errors.New("probe allowed port out of range")
		}
	}
	if err = os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, errors.New("cannot prepare private probe data directory")
	}
	dir, err := os.Lstat(cfg.DataDir)
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 || dir.Mode().Perm()&0022 != 0 {
		return nil, errors.New("probe data directory must be a real directory")
	}
	a := &Agent{cfg: cfg, client: client, observe: observe, check: checker}
	b, err := readPrivate(filepath.Join(cfg.DataDir, "identity.json"))
	if err == nil {
		if json.Unmarshal(b, &a.saved) != nil || a.saved.ControlURL != cfg.ControlURL || !validID(a.saved.Identity.ProbeID) || !validToken(a.saved.Identity.Token) || len(a.saved.Seen) > 1024 {
			return nil, errors.New("stored probe identity is invalid or belongs to another Narbe")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot safely read probe identity")
	}
	b, err = readPrivate(filepath.Join(cfg.DataDir, "pending.json"))
	if err == nil {
		var p ResultRequest
		if json.Unmarshal(b, &p) != nil || !validID(p.TaskID) || !validID(p.LeaseID) {
			return nil, errors.New("stored probe result is invalid")
		}
		a.pending = &p
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot safely read pending probe result")
	}
	return a, nil
}

func (a *Agent) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.Step(ctx); err != nil {
			// Only sanitized, static errors are returned by Step. No tokens or
			// target response bodies are written to logs by this package.
			slog.Warn("探针周期未完成", "reason", err.Error())
			if !wait(ctx, 10*time.Second) {
				return ctx.Err()
			}
			continue
		}
		if !wait(ctx, 5*time.Second) {
			return ctx.Err()
		}
	}
}

// Step performs one heartbeat/check/result cycle. It is useful for deterministic
// integration tests; callers must serialize Step calls on an Agent.
func (a *Agent) Step(ctx context.Context) error {
	if a.saved.Identity.Token == "" {
		if !validToken(a.cfg.RegistrationToken) {
			return errors.New("a valid single-use registration token is required")
		}
		var id Identity
		if err := a.post(ctx, "/probe/v1/enroll", EnrollRequest{a.cfg.RegistrationToken, a.cfg.Version}, &id, false); err != nil {
			return errors.New("probe enrollment failed; check Narbe registration status")
		}
		if !validID(id.ProbeID) || !validToken(id.Token) {
			return errors.New("Narbe returned an invalid probe identity")
		}
		a.saved = savedIdentity{ControlURL: a.cfg.ControlURL, Identity: id}
		if err := a.saveIdentity(); err != nil {
			return err
		}
		a.cfg.RegistrationToken = ""
	}
	if a.pending != nil {
		if err := a.deliverPending(ctx); err != nil {
			return err
		}
	}
	if a.egress.ObservedAt.IsZero() || time.Since(a.egress.ObservedAt) > time.Minute {
		a.egress, _ = a.observe(ctx)
	}
	var poll PollResponse
	q := PollRequest{a.cfg.Version, a.egress.IP, a.egress.Country, a.egress.ObservedAt, a.egress.Source, a.egress.Error}
	if err := a.post(ctx, "/probe/v1/poll", q, &poll, true); err != nil {
		return errors.New("probe heartbeat failed")
	}
	if err := privateWrite(filepath.Join(a.cfg.DataDir, "heartbeat"), []byte(time.Now().UTC().Format(time.RFC3339))); err != nil {
		return errors.New("cannot persist probe health status")
	}
	if poll.Task == nil {
		return nil
	}
	t := *poll.Task
	if !validID(t.TaskID) || !validID(t.LeaseID) {
		return errors.New("Narbe supplied an invalid probe lease")
	}
	key := t.TaskID + ":" + t.LeaseID
	for _, seen := range a.saved.Seen {
		if seen == key {
			return errors.New("probe lease was already executed; refusing replay")
		}
	}
	a.saved.Seen = append(a.saved.Seen, key)
	if len(a.saved.Seen) > 1024 {
		a.saved.Seen = a.saved.Seen[len(a.saved.Seen)-1024:]
	}
	if err := a.saveIdentity(); err != nil {
		return err
	}
	started := time.Now().UTC()
	r := Result{StartedAt: started, CompletedAt: started, Classification: "invalid_target", Error: "检测目标不满足本地公开地址及端口限制"}
	before, beforeErr := a.observe(ctx)
	if err := ValidateTarget(t.Target, a.cfg.AllowedPorts); err == nil && t.TimeoutMS >= 100 && t.TimeoutMS <= 30000 {
		if beforeErr == nil && PublicIPv4(before.IP) && !before.ObservedAt.IsZero() && time.Since(before.ObservedAt) < time.Minute {
			r = a.check(ctx, t.Target, time.Duration(t.TimeoutMS)*time.Millisecond)
		} else {
			r.Classification, r.Error = "egress_unverified", "无法核实当前实际出口，不能判定国内可用性"
		}
	}
	after, afterErr := a.observe(ctx)
	a.egress = after
	if afterErr != nil || beforeErr != nil || before.IP != after.IP || before.Country != after.Country {
		r.Success, r.Classification, r.Error = false, "egress_unverified", "检测前后出口不一致或无法核实"
	}
	a.pending = &ResultRequest{t.TaskID, t.LeaseID, t.Target, r, after}
	b, err := json.Marshal(a.pending)
	if err != nil || privateWrite(filepath.Join(a.cfg.DataDir, "pending.json"), b) != nil {
		return errors.New("cannot persist probe result; lease will not be replayed")
	}
	return a.deliverPending(ctx)
}

type controlStatusError struct{ status int }

func (e *controlStatusError) Error() string { return "probe control request rejected" }

func (a *Agent) deliverPending(ctx context.Context) error {
	err := a.post(ctx, "/probe/v1/results", a.pending, nil, true)
	var rejected *controlStatusError
	// 410 explicitly means the lease expired or the workflow was stopped.
	// Drop the stale result, retaining the durable seen-lease record forever
	// within its bounded window. Never reexecute the stale check.
	if err != nil && !(errors.As(err, &rejected) && rejected.status == http.StatusGone) {
		return errors.New("probe result pending; no new job requested")
	}
	if err := os.Remove(filepath.Join(a.cfg.DataDir, "pending.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot clear delivered or expired probe result")
	}
	a.pending = nil
	return nil
}

func (a *Agent) post(ctx context.Context, path string, input, output any, auth bool) error {
	b, err := json.Marshal(input)
	if err != nil || len(b) > 64<<10 {
		return errors.New("invalid probe request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.ControlURL+path, bytes.NewReader(b))
	if err != nil {
		return errors.New("invalid probe control request")
	}
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+a.saved.Identity.Token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return errors.New("probe control channel unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &controlStatusError{status: resp.StatusCode}
	}
	b, err = io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(b) > 64<<10 {
		return errors.New("probe control response too large")
	}
	if output != nil && json.Unmarshal(b, output) != nil {
		return errors.New("invalid probe control response")
	}
	return nil
}

func (a *Agent) saveIdentity() error {
	b, err := json.Marshal(a.saved)
	if err != nil || privateWrite(filepath.Join(a.cfg.DataDir, "identity.json"), b) != nil {
		return errors.New("cannot persist private probe identity")
	}
	return nil
}

func validID(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validToken(s string) bool {
	if len(s) < 32 || len(s) > 256 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func readPrivate(path string) ([]byte, error) {
	s, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !s.Mode().IsRegular() || s.Mode().Perm() != 0600 || s.Size() > 128<<10 {
		return nil, errors.New("unsafe probe private file")
	}
	return os.ReadFile(path)
}

func privateWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".probe-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func wait(ctx context.Context, duration time.Duration) bool {
	t := time.NewTimer(duration)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func Healthy(dataDir string) bool {
	b, err := readPrivate(filepath.Join(dataDir, "heartbeat"))
	if err != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, string(b))
	return err == nil && time.Since(t) >= 0 && time.Since(t) < 2*time.Minute
}
