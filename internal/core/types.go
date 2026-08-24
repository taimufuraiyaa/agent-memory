package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MemoryType is a string enum describing the kind of memory.
type MemoryType string

const (
	EpisodicMemory   MemoryType = "episodic"
	SemanticMemory   MemoryType = "semantic"
	ProceduralMemory MemoryType = "procedural"
	OutcomeMemory    MemoryType = "outcome"
)

// StorageTier describes where a memory is routed.
type StorageTier string

const (
	TierMarkdown    StorageTier = "markdown"
	TierVector      StorageTier = "vector"
	TierVectorGraph StorageTier = "vector+graph"
	TierDocument    StorageTier = "document"
	TierCold        StorageTier = "cold"
)

// SourceType indicates where a memory came from.
type SourceType string

const (
	SourceAgentObservation SourceType = "agent_observation"
	SourceUserInput        SourceType = "user_input"
	SourceCodeAnalysis     SourceType = "code_analysis"
	SourceConsolidation    SourceType = "consolidation"
	SourceReflection       SourceType = "reflection"
	SourceReconstruction   SourceType = "reconstruction"
	SourceImport           SourceType = "import"
)

// RelationType indicates relationship edges between memories.
type RelationType string

const (
	RelCalls       RelationType = "calls"
	RelDependsOn   RelationType = "depends_on"
	RelContains    RelationType = "contains"
	RelContradicts RelationType = "contradicts"
	RelSupersedes  RelationType = "supersedes"
	RelLedTo       RelationType = "led_to"
	RelDerivedFrom RelationType = "derived_from"
)

// OutcomeResult records the result of an approach.
type OutcomeResult string

const (
	OutcomeSuccess OutcomeResult = "success"
	OutcomeFailure OutcomeResult = "failure"
	OutcomePartial OutcomeResult = "partial"
)

type RetrievalFeedback string

const (
	FeedbackHelpful  RetrievalFeedback = "helpful"
	FeedbackIgnored  RetrievalFeedback = "ignored"
	FeedbackRejected RetrievalFeedback = "rejected"
	FeedbackHarmful  RetrievalFeedback = "harmful"
)

type ReconsolidationAction string

const (
	ReconsolidateConfirmed    ReconsolidationAction = "confirmed"
	ReconsolidateClarified    ReconsolidationAction = "clarified"
	ReconsolidateContradicted ReconsolidationAction = "contradicted"
	ReconsolidateSuperseded   ReconsolidationAction = "superseded"
)

// MemoryEntry is the canonical memory record.
type MemoryEntry struct {
	ID        string     `json:"id" db:"id"`
	Type      MemoryType `json:"type" db:"type"`
	Content   string     `json:"content" db:"content"`
	Embedding []float32  `json:"-" db:"embedding"`
	Diagram   *Diagram   `json:"diagram,omitempty"`

	Workspace string  `json:"workspace" db:"workspace"`
	SessionID *string `json:"session_id,omitempty" db:"session_id"`
	AgentID   *string `json:"agent_id,omitempty" db:"agent_id"`
	UserID    *string `json:"user_id,omitempty" db:"user_id"`

	Source     MemorySource `json:"source"`
	Entities   []string     `json:"entities" db:"entities"`
	Tags       []string     `json:"tags" db:"tags"`
	Keywords   []MemoryTerm `json:"keywords,omitempty"`
	Confidence float64      `json:"confidence" db:"confidence"`

	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
	LastAccessedAt      time.Time  `json:"last_accessed_at" db:"last_accessed"`
	AccessCount         int        `json:"access_count" db:"access_count"`
	DecayScore          float64    `json:"decay_score" db:"decay_score"`
	SalienceScore       float64    `json:"salience_score" db:"salience_score"`
	SuppressionScore    float64    `json:"suppression_score" db:"suppression_score"`
	UsefulCount         int        `json:"useful_count" db:"useful_count"`
	IgnoredCount        int        `json:"ignored_count" db:"ignored_count"`
	RejectedCount       int        `json:"rejected_count" db:"rejected_count"`
	HarmfulCount        int        `json:"harmful_count" db:"harmful_count"`
	LastHelpfulAt       time.Time  `json:"last_helpful_at" db:"last_helpful_at"`
	LastRejectedAt      time.Time  `json:"last_rejected_at" db:"last_rejected_at"`
	SuppressionUntil    *time.Time `json:"suppression_until,omitempty" db:"suppression_until"`
	FamiliarityBandLast string     `json:"familiarity_band_last,omitempty" db:"familiarity_band_last"`
	SupersededBy        *string    `json:"superseded_by,omitempty" db:"superseded_by"`

	StorageTier StorageTier `json:"storage_tier" db:"storage_tier"`
	Importance  float64     `json:"importance" db:"importance"`
	Pinned      bool        `json:"pinned" db:"pinned"`
	PromotedAt  *time.Time  `json:"promoted_at,omitempty" db:"promoted_at"`
	DemotedAt   *time.Time  `json:"demoted_at,omitempty" db:"demoted_at"`

	Outcome   *Outcome   `json:"outcome,omitempty"`
	Relations []Relation `json:"relations,omitempty"`
}

// TermSource records how a memory locator term was selected.
type TermSource string

const (
	TermSourceExplicit   TermSource = "explicit"
	TermSourceHashtag    TermSource = "hashtag"
	TermSourceEntity     TermSource = "entity"
	TermSourceTag        TermSource = "tag"
	TermSourceIdentifier TermSource = "identifier"
)

// MemoryTerm is a short, normalized locator used by exact term search.
type MemoryTerm struct {
	Term                 string     `json:"term"`
	Display              string     `json:"display,omitempty"`
	Source               TermSource `json:"source"`
	Ordinal              int        `json:"ordinal"`
	NormalizationVersion string     `json:"normalization_version"`
	ExtractorVersion     string     `json:"extractor_version"`
}

type Diagram struct {
	Lang string `json:"lang"`
	Code string `json:"code"`
}

// MemoryPatch supports partial updates.
type MemoryPatch struct {
	Content      *string      `json:"content,omitempty"`
	Confidence   *float64     `json:"confidence,omitempty"`
	Tags         *[]string    `json:"tags,omitempty"`
	SupersededBy *string      `json:"superseded_by,omitempty"`
	StorageTier  *StorageTier `json:"storage_tier,omitempty"`
	Pinned       *bool        `json:"pinned,omitempty"`
}

// MemorySource describes where memory data came from.
type MemorySource struct {
	Type         SourceType `json:"type"`
	SessionID    string     `json:"session_id,omitempty"`
	FilePath     string     `json:"file_path,omitempty"`
	LineRange    []int      `json:"line_range,omitempty"`
	NoteID       string     `json:"note_id,omitempty"`
	NoteRevision int        `json:"note_revision,omitempty"`
	NotePath     string     `json:"note_path,omitempty"`
	Heading      string     `json:"heading,omitempty"`
}

// Relation is a graph edge from this memory to another memory.
type Relation struct {
	TargetID string            `json:"target_id"`
	Type     RelationType      `json:"type"`
	Weight   float64           `json:"weight"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// RelationEdge is a full graph edge linking two memories.
type RelationEdge struct {
	SourceID string            `json:"source_id"`
	TargetID string            `json:"target_id"`
	Type     RelationType      `json:"type"`
	Weight   float64           `json:"weight"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Outcome captures why an attempt succeeded or failed.
type Outcome struct {
	Result         OutcomeResult `json:"result"`
	Approach       string        `json:"approach"`
	Reason         string        `json:"reason"`
	LinkedMemories []string      `json:"linked_memories"`
}

// SearchFilters controls retrieval filtering.
type SearchFilters struct {
	Types         []MemoryType
	Tiers         []StorageTier
	Workspace     string
	MinConfidence *float64
	OutcomeResult *OutcomeResult
	Entities      []string
}

// RecallOptions controls session-start recall behavior.
type RecallOptions struct {
	Workspace       string
	TaskDescription string
	TokenBudget     int
}

// StoreStats describes high-level store health.
type StoreStats struct {
	Workspace      string `json:"workspace"`
	MemoryCount    int    `json:"memory_count"`
	TombstoneCount int    `json:"tombstone_count"`
}

type Observation struct {
	ID              string    `json:"id"`
	Workspace       string    `json:"workspace"`
	SessionID       string    `json:"session_id"`
	OccurredAt      time.Time `json:"occurred_at"`
	Kind            string    `json:"kind"`
	ToolName        *string   `json:"tool_name,omitempty"`
	Summary         string    `json:"summary"`
	SourceAgent     string    `json:"source_agent,omitempty"`
	SourceAdapter   string    `json:"source_adapter,omitempty"`
	HookEvent       string    `json:"hook_event,omitempty"`
	ExternalEventID string    `json:"external_event_id,omitempty"`
	SchemaVersion   string    `json:"schema_version,omitempty"`
	CaptureMode     string    `json:"capture_mode,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Session struct {
	Workspace        string     `json:"workspace"`
	SessionID        string     `json:"session_id"`
	ProjectRoot      string     `json:"project_root,omitempty"`
	CWD              string     `json:"cwd,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
	ObservationCount int        `json:"observation_count"`
	LastSeenAt       time.Time  `json:"last_seen_at"`
}

// MemoryTombstone is a compact breadcrumb for evicted/superseded memory.
type MemoryTombstone struct {
	ID              string     `json:"id"`
	MemoryID        string     `json:"memory_id"`
	Workspace       string     `json:"workspace"`
	Type            MemoryType `json:"type"`
	EntityHash      string     `json:"entity_hash"`
	FragmentSummary string     `json:"fragment_summary,omitempty"`
	EvictionReason  string     `json:"eviction_reason"`
	LineageMemoryID string     `json:"lineage_memory_id,omitempty"`
	EvictedAt       time.Time  `json:"evicted_at"`
	CooldownUntil   time.Time  `json:"cooldown_until"`
}

// ConsolidationResult summarizes one lifecycle run.
type ConsolidationResult struct {
	Scored    int `json:"scored"`
	Merged    int `json:"merged"`
	Evicted   int `json:"evicted"`
	Promoted  int `json:"promoted"`
	DurationM int `json:"duration_ms"`
}

// Validate enforces basic invariants for in-memory domain objects.
func (m *MemoryEntry) Validate() error {
	if m == nil {
		return errors.New("memory entry is nil")
	}
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(m.Content) == "" {
		return errors.New("content is required")
	}
	if strings.TrimSpace(m.Workspace) == "" {
		return errors.New("workspace is required")
	}
	if !isMemoryType(m.Type) {
		return errors.New("invalid memory type")
	}
	if m.StorageTier != "" && !isStorageTier(m.StorageTier) {
		return errors.New("invalid storage tier")
	}
	if m.Confidence < 0 || m.Confidence > 1 {
		return errors.New("confidence must be between 0 and 1")
	}
	if len(m.Keywords) > 3 {
		return fmt.Errorf("%w: keywords must contain at most 3 terms", ErrInvalidInput)
	}
	return nil
}

// FeedbackStats aggregates retrieval feedback scoring statistics.
type FeedbackStats struct {
	Workspace          string         `json:"workspace"`
	AverageWeek        float64        `json:"average_week"`
	AverageMonth       float64        `json:"average_month"`
	AverageYear        float64        `json:"average_year"`
	TotalFeedbackCount int            `json:"total_feedback_count"`
	ScoreDistribution  map[string]int `json:"score_distribution"`
	AverageUsefulCount float64        `json:"average_useful_count"`
	AverageTotalCount  float64        `json:"average_total_count"`
	AverageUsefulRatio float64        `json:"average_useful_ratio"`
}

// RetrievalRequestLog represents a logged search or recall request with optional feedback.
type RetrievalRequestLog struct {
	ID          string `json:"id"`
	Workspace   string `json:"workspace"`
	RequestType string `json:"request_type"`
	Query       string `json:"query"`
	Score       int    `json:"score"`
	Reason      string `json:"reason"`
	UsefulCount int    `json:"useful_count"`
	TotalCount  int    `json:"total_count"`
	CreatedAt   string `json:"created_at"`
}

func IsMemoryType(v MemoryType) bool {
	return isMemoryType(v)
}

func IsStorageTier(v StorageTier) bool {
	return isStorageTier(v)
}

func isMemoryType(v MemoryType) bool {
	switch v {
	case EpisodicMemory, SemanticMemory, ProceduralMemory, OutcomeMemory:
		return true
	default:
		return false
	}
}

func isStorageTier(v StorageTier) bool {
	switch v {
	case TierMarkdown, TierVector, TierVectorGraph, TierDocument, TierCold:
		return true
	default:
		return false
	}
}
