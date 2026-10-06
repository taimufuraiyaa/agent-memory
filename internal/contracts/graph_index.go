package contracts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var ErrInvalidGraphIndexRequest = errors.New("invalid graph index request")

type GraphProviderState string

const (
	GraphProviderDisabled     GraphProviderState = "disabled"
	GraphProviderUnavailable  GraphProviderState = "unavailable"
	GraphProviderIncompatible GraphProviderState = "incompatible"
	GraphProviderReady        GraphProviderState = "ready"
)

type GraphReadinessRequest struct {
	Scope           core.GraphScope `json:"scope"`
	ConfigurationID string          `json:"configuration_id,omitempty"`
	ExpectedAdapter string          `json:"expected_adapter,omitempty"`
	ExpectedVersion string          `json:"expected_version,omitempty"`
	ExpectedSchema  string          `json:"expected_schema,omitempty"`
}

func (r GraphReadinessRequest) Validate() error {
	return r.Scope.Validate()
}

type GraphReadiness struct {
	State           GraphProviderState `json:"state"`
	AdapterName     string             `json:"adapter_name,omitempty"`
	AdapterVersion  string             `json:"adapter_version,omitempty"`
	ContractVersion string             `json:"contract_version,omitempty"`
	ArtifactSchema  string             `json:"artifact_schema,omitempty"`
	EnvironmentHash string             `json:"environment_hash,omitempty"`
	ReasonCode      string             `json:"reason_code,omitempty"`
	Guidance        string             `json:"guidance,omitempty"`
}

type GraphIndexRequest struct {
	Scope           core.GraphScope `json:"scope"`
	JobID           string          `json:"job_id"`
	RevisionID      string          `json:"revision_id"`
	ConfigurationID string          `json:"configuration_id"`
	ManifestPath    string          `json:"manifest_path"`
	ManifestHash    string          `json:"manifest_hash,omitempty"`
}

func (r GraphIndexRequest) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.JobID) == "" || strings.TrimSpace(r.RevisionID) == "" || strings.TrimSpace(r.ManifestPath) == "" {
		return fmt.Errorf("%w: job_id, revision_id, and manifest_path are required", ErrInvalidGraphIndexRequest)
	}
	return nil
}

type GraphUpdateRequest struct {
	GraphIndexRequest
	BaseRevisionID string `json:"base_revision_id"`
}

func (r GraphUpdateRequest) Validate() error {
	if err := r.GraphIndexRequest.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.BaseRevisionID) == "" || r.BaseRevisionID == r.RevisionID {
		return fmt.Errorf("%w: a distinct base_revision_id is required", ErrInvalidGraphIndexRequest)
	}
	return nil
}

type GraphIndexOperation struct {
	JobID      string `json:"job_id"`
	RevisionID string `json:"revision_id"`
	Accepted   bool   `json:"accepted"`
	StatusCode string `json:"status_code,omitempty"`
}

type GraphCancelRequest struct {
	Scope      core.GraphScope `json:"scope"`
	JobID      string          `json:"job_id"`
	RevisionID string          `json:"revision_id"`
	ReasonCode string          `json:"reason_code,omitempty"`
}

func (r GraphCancelRequest) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.JobID) == "" || strings.TrimSpace(r.RevisionID) == "" {
		return fmt.Errorf("%w: job_id and revision_id are required", ErrInvalidGraphIndexRequest)
	}
	return nil
}

type GraphArtifactRequest struct {
	Scope        core.GraphScope `json:"scope"`
	JobID        string          `json:"job_id"`
	RevisionID   string          `json:"revision_id"`
	ManifestPath string          `json:"manifest_path"`
	ManifestHash string          `json:"manifest_hash,omitempty"`
}

func (r GraphArtifactRequest) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.RevisionID) == "" || strings.TrimSpace(r.ManifestPath) == "" {
		return fmt.Errorf("%w: revision_id and manifest_path are required", ErrInvalidGraphIndexRequest)
	}
	return nil
}

type GraphArtifactInspection struct {
	RevisionID   string `json:"revision_id"`
	ManifestHash string `json:"manifest_hash,omitempty"`
	Complete     bool   `json:"complete"`
	Compatible   bool   `json:"compatible"`
	FileCount    int    `json:"file_count"`
	TotalBytes   int64  `json:"total_bytes"`
	FailureCode  string `json:"failure_code,omitempty"`
}

// GraphIndexProvider is the replaceable indexing boundary. Implementations may
// run out of process; canonical storage and online retrieval never implement or
// depend on provider-specific output schemas.
type GraphIndexProvider interface {
	Readiness(context.Context, GraphReadinessRequest) (GraphReadiness, error)
	FullIndex(context.Context, GraphIndexRequest) (GraphIndexOperation, error)
	IncrementalUpdate(context.Context, GraphUpdateRequest) (GraphIndexOperation, error)
	Cancel(context.Context, GraphCancelRequest) error
	InspectArtifacts(context.Context, GraphArtifactRequest) (GraphArtifactInspection, error)
}

// ReadGraphProviderReadiness makes a missing optional provider an explicit,
// non-error disabled state. Callers can keep canonical write and basic retrieval
// paths independent from graph installation.
func ReadGraphProviderReadiness(ctx context.Context, provider GraphIndexProvider, request GraphReadinessRequest) (GraphReadiness, error) {
	if err := request.Validate(); err != nil {
		return GraphReadiness{}, err
	}
	if provider == nil {
		return GraphReadiness{
			State:      GraphProviderDisabled,
			ReasonCode: "provider_not_configured",
			Guidance:   "Graph indexing is optional; configure a compatible provider to enable it.",
		}, nil
	}
	return provider.Readiness(ctx, request)
}
