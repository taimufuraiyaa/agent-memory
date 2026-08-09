package retrieval

import (
	"context"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/search"
)

type Query struct {
	TenantID            string
	AuthorizedSourceIDs []string
	Text                string
	Limit               int
	ContextTokenBudget  int
	Generate            bool
	Provider            string
	Model               string
}

type Breakdown struct {
	Exact       float64 `json:"exact"`
	FullText    float64 `json:"full_text"`
	Vector      float64 `json:"vector"`
	Decay       float64 `json:"decay"`
	Salience    float64 `json:"salience"`
	Feedback    float64 `json:"feedback"`
	Suppression float64 `json:"suppression"`
	Activation  float64 `json:"activation"`
	Total       float64 `json:"total"`
}

type Evidence struct {
	SourceID         string         `json:"source_id"`
	SourceVersion    int64          `json:"source_version"`
	PassageID        string         `json:"passage_id"`
	StructuralNodeID string         `json:"structural_node_id"`
	CitationID       string         `json:"citation_id"`
	Text             string         `json:"text"`
	Locator          map[string]any `json:"locator"`
	Score            float64        `json:"score"`
	Breakdown        Breakdown      `json:"breakdown"`
}

type ContextMetadata struct {
	Budget      int      `json:"budget"`
	UsedTokens  int      `json:"used_tokens"`
	IncludedIDs []string `json:"included_ids"`
	ClippedIDs  []string `json:"clipped_ids"`
}

type Result struct {
	Answerable  bool            `json:"answerable"`
	Evidence    []Evidence      `json:"evidence"`
	Context     ContextMetadata `json:"context"`
	Generated   bool            `json:"generated"`
	Synthesis   string          `json:"synthesis,omitempty"`
	FailureCode string          `json:"failure_code,omitempty"`
}

type Candidate struct {
	Evidence
	DecayScore       float64
	SalienceScore    float64
	SuppressionScore float64
	UsefulCount      int
	RejectedCount    int
	HarmfulCount     int
	LastHelpfulAt    *time.Time
	LastRejectedAt   *time.Time
	SuppressionUntil *time.Time
}

type EvidenceKey struct {
	SourceID  string `json:"source_id"`
	PassageID string `json:"passage_id"`
}

type Repository interface {
	AuthorizedSourceIDs(context.Context, string, []string) ([]string, error)
	LexicalCandidates(context.Context, string, []string, string, int) ([]Candidate, error)
	EvidenceByPassageIDs(context.Context, string, []string, []EvidenceKey) ([]Candidate, error)
}

type VectorSearcher interface {
	SearchVectors(context.Context, string, []string, []float32, int) ([]search.VectorHit, error)
}

type ModelGateway interface {
	Embed(context.Context, modelgateway.EmbedRequest) (modelgateway.EmbedResponse, error)
	Generate(context.Context, modelgateway.GenerateRequest) (modelgateway.GenerateResponse, error)
}
