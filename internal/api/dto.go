package api

import (
	"github.com/go-playground/validator/v10"

	"github.com/time/timebooks/agent-memory/internal/core"
)

var validate = validator.New()

// WriteMemoryRequest is the API wrapper validated at transport boundary.
type WriteMemoryRequest struct {
	Content   string            `json:"content" validate:"required,min=1,max=2000"`
	Type      core.MemoryType   `json:"type" validate:"required,oneof=episodic semantic procedural outcome"`
	Workspace string            `json:"workspace" validate:"required,min=1"`
	Entities  []string          `json:"entities,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
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
