// Package audit owns the hosted content-free security ledger contract.
package audit

import (
	"encoding/json"
	"time"
)

const SchemaVersion = "audit.v1"

type Event struct {
	TenantID      string
	ID            string
	SchemaVersion string
	OccurredAt    time.Time
	ReceivedAt    time.Time
	ActorType     string
	ActorID       string
	CredentialRef string
	SessionRef    string
	Service       string
	Operation     string
	Outcome       string
	RequestID     string
	TraceID       string
	TargetType    string
	TargetID      string
	PolicyVersion string
	ReasonCode    string
	RiskSignals   []string
	SafeMetadata  map[string]any
	PreviousHash  string
	EventHash     string
}

type Filter struct {
	ActorID   string
	RequestID string
	TargetID  string
	Operation string
	Outcome   string
	From      time.Time
	To        time.Time
	Limit     int
}

type ArchiveRecord struct {
	Event      Event
	ClaimToken string
	Attempts   int
}

func (e Event) JSON() ([]byte, error) {
	return json.Marshal(e)
}
