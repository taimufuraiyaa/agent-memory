// Package platformprobe produces content-free staging edge-to-telemetry receipts.
package platformprobe

import (
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ReceiptSchemaV1 = "agent-memory-staging-edge-telemetry-receipt-v1"
	maximumWindow   = 2 * time.Minute
)

type CheckID string
type Outcome string

const (
	CheckEdgeResponse         CheckID = "edge_response"
	CheckAPICorrelation       CheckID = "api_correlation"
	CheckTelemetryObservation CheckID = "telemetry_observation"

	OutcomePassed Outcome = "passed"
	OutcomeFailed Outcome = "failed"
)

var (
	releaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	digestPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	traceIDPattern   = regexp.MustCompile(`^[a-f0-9]{32}$`)
	requiredChecks   = []CheckID{CheckEdgeResponse, CheckAPICorrelation, CheckTelemetryObservation}
)

type Challenge struct {
	ReleaseID            string
	ReleaseReceiptSHA256 string
	RequestID            string
	TraceID              string
	StartedAt            time.Time
	CompletedAt          time.Time
	EdgeStatus           int
	EchoRequestID        string
	EchoTraceID          string
}

type Observation struct {
	RequestID  string    `json:"request_id"`
	TraceID    string    `json:"trace_id"`
	Service    string    `json:"service"`
	Operation  string    `json:"operation"`
	Status     int       `json:"status"`
	Outcome    string    `json:"outcome"`
	ObservedAt time.Time `json:"observed_at"`
}

type Check struct {
	ID      CheckID `json:"id"`
	Outcome Outcome `json:"outcome"`
}

type Receipt struct {
	Schema               string    `json:"schema"`
	Ready                bool      `json:"ready"`
	Environment          string    `json:"environment"`
	ReleaseID            string    `json:"release_id"`
	ReleaseReceiptSHA256 string    `json:"release_receipt_sha256"`
	RequestID            string    `json:"request_id"`
	TraceID              string    `json:"trace_id"`
	StartedAt            time.Time `json:"started_at"`
	CompletedAt          time.Time `json:"completed_at"`
	Checks               []Check   `json:"checks"`
}

type Assessment struct {
	Ready       bool
	CheckCount  int
	PassedCount int
	FailedCount int
}

func Evaluate(challenge Challenge, observation Observation) (Receipt, error) {
	if !releaseIDPattern.MatchString(challenge.ReleaseID) || !digestPattern.MatchString(challenge.ReleaseReceiptSHA256) || !validRequestID(challenge.RequestID) || !traceIDPattern.MatchString(challenge.TraceID) {
		return Receipt{}, errors.New("staging telemetry challenge identity is invalid")
	}
	if challenge.StartedAt.IsZero() || challenge.CompletedAt.IsZero() || challenge.CompletedAt.Before(challenge.StartedAt) || challenge.CompletedAt.Sub(challenge.StartedAt) > maximumWindow {
		return Receipt{}, errors.New("staging telemetry challenge window is invalid")
	}
	if challenge.EdgeStatus != 0 && (challenge.EdgeStatus < 100 || challenge.EdgeStatus > 599) {
		return Receipt{}, errors.New("staging telemetry edge status is invalid")
	}
	results := map[CheckID]bool{
		CheckEdgeResponse:   challenge.EdgeStatus == 200,
		CheckAPICorrelation: challenge.EchoRequestID == challenge.RequestID && challenge.EchoTraceID == challenge.TraceID,
		CheckTelemetryObservation: observation.RequestID == challenge.RequestID &&
			observation.TraceID == challenge.TraceID && observation.Service == "api" &&
			observation.Operation == "GET:/health/ready" && observation.Status == 200 && observation.Outcome == "success" &&
			!observation.ObservedAt.Before(challenge.StartedAt) && !observation.ObservedAt.After(challenge.CompletedAt),
	}
	receipt := Receipt{
		Schema: ReceiptSchemaV1, Ready: true, Environment: "staging",
		ReleaseID: challenge.ReleaseID, ReleaseReceiptSHA256: challenge.ReleaseReceiptSHA256,
		RequestID: challenge.RequestID, TraceID: challenge.TraceID,
		StartedAt: challenge.StartedAt.UTC(), CompletedAt: challenge.CompletedAt.UTC(),
		Checks: make([]Check, 0, len(requiredChecks)),
	}
	for _, id := range requiredChecks {
		outcome := OutcomePassed
		if !results[id] {
			outcome = OutcomeFailed
			receipt.Ready = false
		}
		receipt.Checks = append(receipt.Checks, Check{ID: id, Outcome: outcome})
	}
	return receipt, nil
}

func Assess(receipt Receipt) Assessment {
	assessment := Assessment{Ready: receipt.Ready, CheckCount: len(receipt.Checks)}
	for _, check := range receipt.Checks {
		if check.Outcome == OutcomePassed {
			assessment.PassedCount++
		} else {
			assessment.FailedCount++
		}
	}
	return assessment
}

func validRequestID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value && parsed.Version() == 4
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("staging telemetry receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("staging telemetry receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect staging telemetry receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-staging-telemetry-*")
}
