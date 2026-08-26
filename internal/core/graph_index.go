package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidGraphScope      = errors.New("invalid graph scope")
	ErrInvalidGraphTransition = errors.New("invalid graph state transition")
	ErrInvalidGraphRecord     = errors.New("invalid graph record")
)

// GraphScope is the mandatory isolation boundary for every graph-index record.
// TenantID is empty only for standalone installations.
type GraphScope struct {
	TenantID    string `json:"tenant_id,omitempty"`
	WorkspaceID string `json:"workspace_id"`
}

func (s GraphScope) Validate() error {
	if strings.TrimSpace(s.WorkspaceID) == "" {
		return fmt.Errorf("%w: workspace_id is required", ErrInvalidGraphScope)
	}
	return nil
}

type GraphRevisionState string

const (
	GraphRevisionQueued     GraphRevisionState = "queued"
	GraphRevisionProjecting GraphRevisionState = "projecting"
	GraphRevisionIndexing   GraphRevisionState = "indexing"
	GraphRevisionValidating GraphRevisionState = "validating"
	GraphRevisionImporting  GraphRevisionState = "importing"
	GraphRevisionEvaluating GraphRevisionState = "evaluating"
	GraphRevisionReady      GraphRevisionState = "ready"
	GraphRevisionActive     GraphRevisionState = "active"
	GraphRevisionPrevious   GraphRevisionState = "previous"
	GraphRevisionFailed     GraphRevisionState = "failed"
	GraphRevisionCancelled  GraphRevisionState = "cancelled"
)

var graphRevisionTransitions = map[GraphRevisionState]map[GraphRevisionState]struct{}{
	GraphRevisionQueued: {
		GraphRevisionProjecting: {}, GraphRevisionCancelled: {}, GraphRevisionFailed: {},
	},
	GraphRevisionProjecting: {
		GraphRevisionIndexing: {}, GraphRevisionCancelled: {}, GraphRevisionFailed: {},
	},
	GraphRevisionIndexing: {
		GraphRevisionValidating: {}, GraphRevisionCancelled: {}, GraphRevisionFailed: {},
	},
	GraphRevisionValidating: {
		GraphRevisionImporting: {}, GraphRevisionCancelled: {}, GraphRevisionFailed: {},
	},
	GraphRevisionImporting: {
		GraphRevisionEvaluating: {}, GraphRevisionCancelled: {}, GraphRevisionFailed: {},
	},
	GraphRevisionEvaluating: {
		GraphRevisionReady: {}, GraphRevisionCancelled: {}, GraphRevisionFailed: {},
	},
	GraphRevisionReady: {
		GraphRevisionActive: {}, GraphRevisionCancelled: {}, GraphRevisionFailed: {},
	},
	GraphRevisionActive: {
		GraphRevisionPrevious: {}, GraphRevisionFailed: {},
	},
	GraphRevisionPrevious: {
		GraphRevisionActive: {},
	},
}

func ValidateGraphRevisionTransition(from, to GraphRevisionState) error {
	allowed, known := graphRevisionTransitions[from]
	if !known {
		return fmt.Errorf("%w: unknown revision state %q", ErrInvalidGraphTransition, from)
	}
	if _, ok := allowed[to]; !ok {
		return fmt.Errorf("%w: revision %q cannot transition to %q", ErrInvalidGraphTransition, from, to)
	}
	return nil
}

type GraphJobState string

const (
	GraphJobQueued     GraphJobState = "queued"
	GraphJobRunning    GraphJobState = "running"
	GraphJobCompleted  GraphJobState = "completed"
	GraphJobFailed     GraphJobState = "failed"
	GraphJobCancelled  GraphJobState = "cancelled"
	GraphJobDeadLetter GraphJobState = "dead_letter"
)

type GraphTrustState string

const (
	GraphTrustProposed    GraphTrustState = "proposed"
	GraphTrustReviewed    GraphTrustState = "reviewed"
	GraphTrustApproved    GraphTrustState = "approved"
	GraphTrustRejected    GraphTrustState = "rejected"
	GraphTrustSuperseded  GraphTrustState = "superseded"
	GraphTrustQuarantined GraphTrustState = "quarantined"
	GraphTrustStale       GraphTrustState = "stale"
	GraphTrustDeleted     GraphTrustState = "deleted"
)

var graphTrustTransitions = map[GraphTrustState]map[GraphTrustState]struct{}{
	GraphTrustProposed: {
		GraphTrustReviewed: {}, GraphTrustApproved: {}, GraphTrustRejected: {},
		GraphTrustQuarantined: {}, GraphTrustStale: {}, GraphTrustDeleted: {},
	},
	GraphTrustReviewed: {
		GraphTrustApproved: {}, GraphTrustRejected: {}, GraphTrustSuperseded: {},
		GraphTrustQuarantined: {}, GraphTrustStale: {}, GraphTrustDeleted: {},
	},
	GraphTrustApproved: {
		GraphTrustRejected: {}, GraphTrustSuperseded: {}, GraphTrustQuarantined: {},
		GraphTrustStale: {}, GraphTrustDeleted: {},
	},
	GraphTrustRejected: {
		GraphTrustReviewed: {}, GraphTrustSuperseded: {}, GraphTrustDeleted: {},
	},
	GraphTrustQuarantined: {
		GraphTrustReviewed: {}, GraphTrustRejected: {}, GraphTrustDeleted: {},
	},
	GraphTrustStale: {
		GraphTrustReviewed: {}, GraphTrustSuperseded: {}, GraphTrustDeleted: {},
	},
	GraphTrustSuperseded: {GraphTrustDeleted: {}},
}

func ValidateGraphTrustTransition(from, to GraphTrustState) error {
	allowed, known := graphTrustTransitions[from]
	if !known {
		return fmt.Errorf("%w: unknown trust state %q", ErrInvalidGraphTransition, from)
	}
	if _, ok := allowed[to]; !ok {
		return fmt.Errorf("%w: trust state %q cannot transition to %q", ErrInvalidGraphTransition, from, to)
	}
	return nil
}

type GraphIndexMethod string

const (
	GraphIndexStandard GraphIndexMethod = "standard"
	GraphIndexFast     GraphIndexMethod = "fast"
)

type GraphConfiguration struct {
	ID                    string           `json:"id"`
	Scope                 GraphScope       `json:"scope"`
	Version               int64            `json:"version"`
	Enabled               bool             `json:"enabled"`
	AdapterName           string           `json:"adapter_name"`
	AdapterVersion        string           `json:"adapter_version"`
	IndexMethod           GraphIndexMethod `json:"index_method"`
	ProjectionVersion     string           `json:"projection_version"`
	ArtifactSchemaVersion string           `json:"artifact_schema_version"`
	PromptFingerprint     string           `json:"prompt_fingerprint"`
	ModelRoute            string           `json:"model_route"`
	CreatedAt             time.Time        `json:"created_at"`
	UpdatedAt             time.Time        `json:"updated_at"`
}

func (c GraphConfiguration) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.ID) == "" || c.Version < 1 {
		return fmt.Errorf("%w: configuration identity and positive version are required", ErrInvalidGraphRecord)
	}
	if c.IndexMethod != GraphIndexStandard && c.IndexMethod != GraphIndexFast {
		return fmt.Errorf("%w: unsupported index method %q", ErrInvalidGraphRecord, c.IndexMethod)
	}
	return nil
}

type GraphWatermark struct {
	Sequence  int64     `json:"sequence"`
	EventTime time.Time `json:"event_time"`
	Digest    string    `json:"digest"`
}

type GraphJob struct {
	ID              string        `json:"id"`
	Scope           GraphScope    `json:"scope"`
	ConfigurationID string        `json:"configuration_id"`
	RevisionID      string        `json:"revision_id"`
	IdempotencyKey  string        `json:"idempotency_key"`
	State           GraphJobState `json:"state"`
	Attempt         int           `json:"attempt"`
	LeaseOwner      string        `json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time    `json:"lease_expires_at,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type GraphRevision struct {
	ID                 string             `json:"id"`
	Scope              GraphScope         `json:"scope"`
	ConfigurationID    string             `json:"configuration_id"`
	BaseRevisionID     string             `json:"base_revision_id,omitempty"`
	State              GraphRevisionState `json:"state"`
	Cutoff             GraphWatermark     `json:"cutoff"`
	ProjectionHash     string             `json:"projection_hash,omitempty"`
	ArtifactHash       string             `json:"artifact_hash,omitempty"`
	PreviousRevisionID string             `json:"previous_revision_id,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

func (r GraphRevision) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.ConfigurationID) == "" {
		return fmt.Errorf("%w: revision and configuration identities are required", ErrInvalidGraphRecord)
	}
	if _, known := graphRevisionTransitions[r.State]; !known && r.State != GraphRevisionFailed && r.State != GraphRevisionCancelled {
		return fmt.Errorf("%w: unknown revision state %q", ErrInvalidGraphRecord, r.State)
	}
	return nil
}

type GraphEvidence struct {
	ID                   string     `json:"id"`
	Scope                GraphScope `json:"scope"`
	CanonicalKind        string     `json:"canonical_kind"`
	CanonicalID          string     `json:"canonical_id"`
	CanonicalFingerprint string     `json:"canonical_fingerprint"`
	Locator              string     `json:"locator,omitempty"`
	OccurrenceCount      int        `json:"occurrence_count"`
}

type GraphEntity struct {
	ID              string          `json:"id"`
	Scope           GraphScope      `json:"scope"`
	Trust           GraphTrustState `json:"trust"`
	FirstRevisionID string          `json:"first_revision_id"`
	LastRevisionID  string          `json:"last_revision_id"`
	SupersededBy    string          `json:"superseded_by,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type GraphEntityVersion struct {
	EntityID        string   `json:"entity_id"`
	RevisionID      string   `json:"revision_id"`
	ExternalID      string   `json:"external_id"`
	Name            string   `json:"name"`
	EntityType      string   `json:"entity_type"`
	Description     string   `json:"description"`
	Aliases         []string `json:"aliases,omitempty"`
	OccurrenceCount int      `json:"occurrence_count"`
	Degree          int      `json:"degree"`
}

type GraphEdge struct {
	ID              string          `json:"id"`
	Scope           GraphScope      `json:"scope"`
	SourceEntityID  string          `json:"source_entity_id"`
	TargetEntityID  string          `json:"target_entity_id"`
	NormalizedKind  string          `json:"normalized_kind"`
	ExternalKind    string          `json:"external_kind,omitempty"`
	Trust           GraphTrustState `json:"trust"`
	FirstRevisionID string          `json:"first_revision_id"`
	LastRevisionID  string          `json:"last_revision_id"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type GraphEdgeVersion struct {
	EdgeID             string                  `json:"edge_id"`
	RevisionID         string                  `json:"revision_id"`
	ExternalID         string                  `json:"external_id"`
	Description        string                  `json:"description"`
	Weight             float64                 `json:"weight"`
	Origin             GraphRelationshipOrigin `json:"origin"`
	ProvenanceApproved bool                    `json:"provenance_approved"`
}

type GraphCommunity struct {
	ID                    string     `json:"id"`
	Scope                 GraphScope `json:"scope"`
	ConfigurationID       string     `json:"configuration_id,omitempty"`
	RevisionID            string     `json:"revision_id"`
	ExternalID            string     `json:"external_id"`
	ParentID              string     `json:"parent_id,omitempty"`
	Level                 int        `json:"level"`
	EntityCount           int        `json:"entity_count"`
	EdgeCount             int        `json:"edge_count"`
	SourceCount           int        `json:"source_count"`
	UnresolvedCount       int        `json:"unresolved_count"`
	MembershipFingerprint string     `json:"membership_fingerprint,omitempty"`
	EvidenceFingerprint   string     `json:"evidence_fingerprint,omitempty"`
}

type GraphReport struct {
	ID                    string                    `json:"id"`
	Scope                 GraphScope                `json:"scope"`
	CommunityID           string                    `json:"community_id"`
	RevisionID            string                    `json:"revision_id"`
	Title                 string                    `json:"title"`
	Summary               string                    `json:"summary"`
	Findings              []string                  `json:"findings,omitempty"`
	Rank                  float64                   `json:"rank"`
	Trust                 GraphTrustState           `json:"trust"`
	AdmissionState        GraphReportAdmissionState `json:"admission_state,omitempty"`
	Stale                 bool                      `json:"stale"`
	EvidenceCount         int                       `json:"evidence_count"`
	UnresolvedCount       int                       `json:"unresolved_count"`
	ModelRoute            string                    `json:"model_route,omitempty"`
	ModelFingerprint      string                    `json:"model_fingerprint,omitempty"`
	PromptFingerprint     string                    `json:"prompt_fingerprint,omitempty"`
	MembershipFingerprint string                    `json:"membership_fingerprint,omitempty"`
	EvidenceFingerprint   string                    `json:"evidence_fingerprint,omitempty"`
	ReviewVersion         int64                     `json:"review_version,omitempty"`
}

type GraphReview struct {
	ID              string            `json:"id"`
	Scope           GraphScope        `json:"scope"`
	Action          GraphReviewAction `json:"action,omitempty"`
	TargetKind      string            `json:"target_kind"`
	TargetID        string            `json:"target_id"`
	From            GraphTrustState   `json:"from"`
	To              GraphTrustState   `json:"to"`
	ExpectedVersion int64             `json:"expected_version"`
	Reason          string            `json:"reason,omitempty"`
	ReviewerID      string            `json:"reviewer_id"`
	CreatedAt       time.Time         `json:"created_at"`
}

type GraphFeedback struct {
	ID         string     `json:"id"`
	Scope      GraphScope `json:"scope"`
	RequestID  string     `json:"request_id"`
	TargetKind string     `json:"target_kind"`
	TargetID   string     `json:"target_id"`
	Outcome    string     `json:"outcome"`
	Reason     string     `json:"reason,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (f GraphFeedback) Validate() error {
	if err := f.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.RequestID) == "" || strings.TrimSpace(f.TargetID) == "" {
		return fmt.Errorf("%w: graph feedback identity is required", ErrInvalidGraphRecord)
	}
	allowedTargets := map[string]struct{}{"request": {}, "route": {}, "path": {}, "entity": {}, "edge": {}, "report": {}, "memory": {}}
	if _, ok := allowedTargets[strings.TrimSpace(f.TargetKind)]; !ok {
		return fmt.Errorf("%w: unsupported graph feedback target", ErrInvalidGraphRecord)
	}
	allowedOutcomes := map[string]struct{}{"helpful": {}, "ignored": {}, "rejected": {}, "harmful": {}}
	if _, ok := allowedOutcomes[strings.TrimSpace(f.Outcome)]; !ok {
		return fmt.Errorf("%w: unsupported graph feedback outcome", ErrInvalidGraphRecord)
	}
	return nil
}

type GraphActivation struct {
	Scope             GraphScope `json:"scope"`
	ConfigurationID   string     `json:"configuration_id"`
	ExpectedRevision  string     `json:"expected_revision,omitempty"`
	CandidateRevision string     `json:"candidate_revision"`
}

func (a GraphActivation) Validate() error {
	if err := a.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(a.ConfigurationID) == "" || strings.TrimSpace(a.CandidateRevision) == "" {
		return fmt.Errorf("%w: configuration and candidate revision are required", ErrInvalidGraphRecord)
	}
	if a.ExpectedRevision != "" && a.ExpectedRevision == a.CandidateRevision {
		return fmt.Errorf("%w: candidate revision must differ from expected revision", ErrInvalidGraphRecord)
	}
	return nil
}
