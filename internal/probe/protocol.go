// Package probe implements the outbound-only connectivity probe protocol and
// bounded checks. It has no cloud provider credentials or resource operations.
package probe

import "time"

var Version = "dev-cn-probe"

type Target struct {
	IP             string `json:"ip"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Path           string `json:"path,omitempty"`
	ServerName     string `json:"serverName,omitempty"`
	ExpectedStatus int    `json:"expectedStatus,omitempty"`
	Challenge      string `json:"challenge,omitempty"`
}

type Task struct {
	TaskID    string `json:"taskId"`
	LeaseID   string `json:"leaseId"`
	Target    Target `json:"target"`
	TimeoutMS int    `json:"timeoutMs"`
}

type Result struct {
	Success        bool      `json:"success"`
	Classification string    `json:"classification"`
	LatencyMS      int64     `json:"latencyMs"`
	StartedAt      time.Time `json:"startedAt"`
	CompletedAt    time.Time `json:"completedAt"`
	StatusCode     int       `json:"statusCode,omitempty"`
	Error          string    `json:"error,omitempty"`
}

type Egress struct {
	IP         string    `json:"ip"`
	Country    string    `json:"country"`
	ObservedAt time.Time `json:"observedAt"`
	Source     string    `json:"source"`
	Error      string    `json:"error,omitempty"`
}

type EnrollRequest struct {
	RegistrationToken string `json:"registrationToken"`
	Version           string `json:"version"`
}

type Identity struct {
	ProbeID string `json:"probeId"`
	Token   string `json:"token"`
}

type PollRequest struct {
	Version          string    `json:"version"`
	EgressIP         string    `json:"egressIP"`
	EgressCountry    string    `json:"egressCountry"`
	EgressObservedAt time.Time `json:"egressObservedAt"`
	EgressSource     string    `json:"egressSource"`
	EgressError      string    `json:"egressError,omitempty"`
}

type PollResponse struct {
	Task *Task `json:"task"`
}

type ResultRequest struct {
	TaskID  string `json:"taskId"`
	LeaseID string `json:"leaseId"`
	Target  Target `json:"target"`
	Result  Result `json:"result"`
	Egress  Egress `json:"egress"`
}
