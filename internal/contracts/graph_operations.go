package contracts

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var (
	ErrGraphOperationConflict = errors.New("graph operation conflicts with current state")
	ErrGraphOperationNotFound = errors.New("graph operation target was not found")
	ErrGraphOperationDisabled = errors.New("graph indexing is disabled")
	ErrGraphOperationInvalid  = errors.New("invalid graph operation")
)

type GraphOperationAction string

const (
	GraphOperationUpdate   GraphOperationAction = "update"
	GraphOperationRebuild  GraphOperationAction = "rebuild"
	GraphOperationCancel   GraphOperationAction = "cancel"
	GraphOperationRetry    GraphOperationAction = "retry"
	GraphOperationDisable  GraphOperationAction = "disable"
	GraphOperationRollback GraphOperationAction = "rollback"
)

type GraphOperationRequest struct {
	Scope            core.GraphScope      `json:"scope"`
	ConfigurationID  string               `json:"configuration_id"`
	Action           GraphOperationAction `json:"action"`
	IdempotencyKey   string               `json:"idempotency_key,omitempty"`
	ExpectedRevision string               `json:"expected_revision,omitempty"`
	JobID            string               `json:"job_id,omitempty"`
	Actor            string               `json:"-"`
}

type GraphIndexReadiness struct {
	ConfigurationID       string `json:"configuration_id,omitempty"`
	Ready                 bool   `json:"ready"`
	Enabled               bool   `json:"enabled"`
	Compatible            bool   `json:"compatible"`
	State                 string `json:"state"`
	AdapterName           string `json:"adapter_name,omitempty"`
	AdapterVersion        string `json:"adapter_version,omitempty"`
	ArtifactSchemaVersion string `json:"artifact_schema_version,omitempty"`
	ReasonCode            string `json:"reason_code,omitempty"`
	Reason                string `json:"reason,omitempty"`
}
type GraphIndexStatus struct {
	ConfigurationID       string                 `json:"configuration_id"`
	ConfigurationVersion  int64                  `json:"configuration_version"`
	Enabled               bool                   `json:"enabled"`
	State                 string                 `json:"state"`
	AdapterName           string                 `json:"adapter_name,omitempty"`
	AdapterVersion        string                 `json:"adapter_version,omitempty"`
	Compatible            bool                   `json:"compatible"`
	IndexMethod           string                 `json:"index_method,omitempty"`
	ArtifactSchemaVersion string                 `json:"artifact_schema_version,omitempty"`
	ActiveRevisionID      string                 `json:"active_revision_id,omitempty"`
	PreviousRevisionID    string                 `json:"previous_revision_id,omitempty"`
	IndexedWatermark      core.GraphWatermark    `json:"indexed_watermark"`
	PendingChanges        int64                  `json:"pending_changes"`
	PendingRecords        int64                  `json:"pending_records"`
	CurrentJob            *core.GraphJob         `json:"current_job,omitempty"`
	QueueAgeSeconds       int64                  `json:"queue_age_seconds"`
	LastJobState          core.GraphJobState     `json:"last_job_state,omitempty"`
	LastJobID             string                 `json:"last_job_id,omitempty"`
	LastSuccessfulAt      *time.Time             `json:"last_successful_at,omitempty"`
	EstimatedCostUSD      float64                `json:"estimated_cost_usd"`
	CostAvailable         bool                   `json:"cost_available"`
	Fresh                 bool                   `json:"fresh"`
	Degraded              bool                   `json:"degraded"`
	RemediationCode       string                 `json:"remediation_code,omitempty"`
	AuthorizedOperations  []GraphOperationAction `json:"authorized_operations"`
}
type GraphOperationResult struct {
	Action     GraphOperationAction `json:"action"`
	Accepted   bool                 `json:"accepted"`
	Coalesced  bool                 `json:"coalesced"`
	Job        *core.GraphJob       `json:"job,omitempty"`
	RevisionID string               `json:"revision_id,omitempty"`
	Status     GraphIndexStatus     `json:"status"`
}

type GraphOperationController interface {
	Readiness(context.Context, core.GraphScope, string) (GraphIndexReadiness, error)
	Status(context.Context, core.GraphScope, string) (GraphIndexStatus, error)
	Operate(context.Context, GraphOperationRequest) (GraphOperationResult, error)
}
type GraphOperationStore interface {
	GraphIndexReadiness(context.Context, core.GraphScope, string) (GraphIndexReadiness, error)
	GraphIndexStatus(context.Context, core.GraphScope, string) (GraphIndexStatus, error)
	ApplyGraphOperation(context.Context, GraphOperationRequest) (GraphOperationResult, error)
}
