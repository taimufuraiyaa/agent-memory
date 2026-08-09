// Package security provides deterministic hosted anomaly detection and policy-gated containment.
package security

import "time"

type Severity string

const (
	Low      Severity = "low"
	Medium   Severity = "medium"
	High     Severity = "high"
	Critical Severity = "critical"
)

type EvidenceRef struct {
	EventID    string    `json:"event_id"`
	ReasonCode string    `json:"reason_code"`
	OccurredAt time.Time `json:"occurred_at"`
}

type Finding struct {
	TenantID        string
	ID              string
	RuleID          string
	Severity        Severity
	SummaryCode     string
	State           string
	Evidence        []EvidenceRef
	FirstObservedAt time.Time
	LastObservedAt  time.Time
}

type Action string

const (
	RateLimit        Action = "rate_limit"
	CredentialRevoke Action = "credential_revoke"
	UploadQuarantine Action = "upload_quarantine"
	SourceDisable    Action = "source_disable"
	TenantSuspend    Action = "tenant_suspend"
)

type Policy struct {
	Action           Action
	Enabled          bool
	MinimumSeverity  Severity
	ApprovalRequired bool
	Version          string
}

type ContainmentRequest struct {
	TenantID    string
	FindingID   string
	Action      Action
	TargetType  string
	TargetID    string
	RequestedBy string
	ApprovedBy  string
	ReasonCode  string
	Duration    time.Duration
}
