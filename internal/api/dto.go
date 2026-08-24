package api

import (
	"github.com/go-playground/validator/v10"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type LibraryPrincipalRequest struct {
	PrincipalID     string   `json:"principal_id"`
	OrganizationIDs []string `json:"organization_ids,omitempty"`
}

type LibraryImportRequest struct {
	Workspace      string `json:"workspace"`
	LibraryID      string `json:"library_id"`
	LibraryKind    string `json:"library_kind,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	PrincipalID    string `json:"principal_id"`
	Title          string `json:"title"`
	EditionLabel   string `json:"edition_label"`
	Language       string `json:"language"`
	Format         string `json:"format,omitempty"`
	Markdown       string `json:"markdown"`
	RightsBasis    string `json:"rights_basis"`
}

type LibraryQueryRequest struct {
	Workspace       string   `json:"workspace"`
	PrincipalID     string   `json:"principal_id"`
	OrganizationIDs []string `json:"organization_ids,omitempty"`
	Question        string   `json:"question"`
	Limit           int      `json:"limit,omitempty"`
	ProposeMemory   bool     `json:"propose_memory,omitempty"`
	MemoryContent   string   `json:"memory_content,omitempty"`
}

type LibraryMemoryReviewRequest struct {
	Workspace   string `json:"workspace"`
	ProposalID  string `json:"proposal_id"`
	PrincipalID string `json:"principal_id"`
	Decision    string `json:"decision"`
}

var validate = validator.New()

// WriteMemoryRequest is the API wrapper validated at transport boundary.
type WriteMemoryRequest struct {
	Content   string            `json:"content" validate:"required,min=1,max=2000"`
	Type      core.MemoryType   `json:"type" validate:"required,oneof=episodic semantic procedural outcome"`
	Workspace string            `json:"workspace" validate:"required,min=1"`
	Entities  []string          `json:"entities,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Keywords  []string          `json:"keywords,omitempty" validate:"omitempty,max=3,dive,min=1,max=128"`
	Outcome   *core.Outcome     `json:"outcome,omitempty"`
	Source    core.MemorySource `json:"source" validate:"required"`
}

// Validate validates write request data.
func (r *WriteMemoryRequest) Validate() error {
	return validate.Struct(r)
}

// SearchRequest is a transport-level search request.
type SearchRequest struct {
	Query       string            `json:"query" validate:"required,min=1"`
	Workspace   string            `json:"workspace" validate:"required,min=1"`
	TopK        int               `json:"top_k,omitempty" validate:"omitempty,gte=1,lte=200"`
	TokenBudget int               `json:"token_budget,omitempty" validate:"omitempty,gte=1,lte=32000"`
	Types       []core.MemoryType `json:"types,omitempty" validate:"omitempty,dive,oneof=episodic semantic procedural outcome"`
}

// Validate validates search request data.
func (r *SearchRequest) Validate() error {
	return validate.Struct(r)
}

type ObserveRequest struct {
	Workspace       string `json:"workspace,omitempty" validate:"omitempty,min=1"`
	SessionID       string `json:"session_id" validate:"required,min=1,max=128"`
	OccurredAt      string `json:"occurred_at" validate:"required,min=1,max=64"`
	Kind            string `json:"kind" validate:"required,min=1,max=64"`
	ToolName        string `json:"tool_name,omitempty" validate:"omitempty,max=128"`
	Prompt          string `json:"prompt,omitempty" validate:"omitempty,max=4000"`
	ToolInput       any    `json:"tool_input,omitempty"`
	ProjectRoot     string `json:"project_root,omitempty" validate:"omitempty,max=512"`
	CWD             string `json:"cwd,omitempty" validate:"omitempty,max=512"`
	Metadata        any    `json:"metadata,omitempty"`
	SourceAgent     string `json:"source_agent,omitempty" validate:"omitempty,max=64"`
	SourceAdapter   string `json:"source_adapter,omitempty" validate:"omitempty,max=128"`
	HookEvent       string `json:"hook_event,omitempty" validate:"omitempty,max=64"`
	ExternalEventID string `json:"external_event_id,omitempty" validate:"omitempty,max=256"`
	SchemaVersion   string `json:"schema_version,omitempty" validate:"omitempty,max=32"`
	CaptureMode     string `json:"capture_mode,omitempty" validate:"omitempty,max=32"`
}

func (r *ObserveRequest) Validate() error {
	return validate.Struct(r)
}

type FeedbackRequest struct {
	Workspace             string                     `json:"workspace,omitempty" validate:"omitempty,min=1"`
	MemoryID              string                     `json:"memory_id" validate:"required,min=1"`
	Outcome               core.RetrievalFeedback     `json:"outcome" validate:"required,oneof=helpful ignored rejected harmful"`
	Validator             string                     `json:"validator,omitempty" validate:"omitempty,max=128"`
	ReasonCategory        string                     `json:"reason_category,omitempty" validate:"omitempty,max=128"`
	OccurredAt            string                     `json:"occurred_at,omitempty" validate:"omitempty,max=64"`
	ReconsolidationAction core.ReconsolidationAction `json:"reconsolidation_action,omitempty" validate:"omitempty,oneof=confirmed clarified contradicted superseded"`
	SuccessorMemoryID     string                     `json:"successor_memory_id,omitempty" validate:"omitempty,min=1"`
}

func (r *FeedbackRequest) Validate() error {
	return validate.Struct(r)
}
