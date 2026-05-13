package engine

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
)

// RetrievalMode alters signal weighting depending on caller intent.
type RetrievalMode string

const (
	ModeSearch   RetrievalMode = "search"
	ModeRecall   RetrievalMode = "recall"
	ModeRelate   RetrievalMode = "relate"
	ModeOutcomes RetrievalMode = "outcomes"
)

// RetrievalOptions controls retrieval behavior.
type RetrievalOptions struct {
	Workspace string
	Query     string
	TopK      int
	Mode      RetrievalMode
	Filters   RetrievalFilters
}

type RetrievalFilters struct {
	Types         []core.MemoryType
	Tiers         []core.StorageTier
	OutcomeResult *core.OutcomeResult
	MinConfidence *float64
	MinDecayScore *float64
	Entities      []string
	DateFrom      *time.Time
	DateTo        *time.Time
}

// SignalBreakdown holds explainable component scores.
type SignalBreakdown struct {
	Semantic float64 `json:"semantic"`
	Recency  float64 `json:"recency"`
	Outcome  float64 `json:"outcome"`
	Decay    float64 `json:"decay"`
	TierBias float64 `json:"tier_bias"`
	Total    float64 `json:"total"`
}

// RetrievalHit extends SearchHit with explain details.
type RetrievalHit struct {
	Memory    core.MemoryEntry `json:"memory"`
	Score     float64          `json:"score"`
	Breakdown SignalBreakdown  `json:"breakdown"`
}

// RetrievalResult is a ranked response with explainability.
type RetrievalResult struct {
	Mode    RetrievalMode  `json:"mode"`
	Weights SignalWeights  `json:"weights"`
	Hits    []RetrievalHit `json:"hits"`
}

// SignalWeights configures weighted rerank.
type SignalWeights struct {
	Semantic float64 `json:"semantic"`
	Recency  float64 `json:"recency"`
	Outcome  float64 `json:"outcome"`
	Decay    float64 `json:"decay"`
	TierBias float64 `json:"tier_bias"`
}

// RetrievalEngine combines semantic search and additional ranking signals.
type RetrievalEngine struct {
	vector *VectorSearcher
	clock  func() time.Time
}

// NewRetrievalEngine creates a retrieval engine.
func NewRetrievalEngine(vector *VectorSearcher) *RetrievalEngine {
	return &RetrievalEngine{
		vector: vector,
		clock:  func() time.Time { return time.Now().UTC() },
	}
}

// Retrieve computes mode-aware weighted ranking with explain output.
func (e *RetrievalEngine) Retrieve(ctx context.Context, opt RetrievalOptions) (*RetrievalResult, error) {
	if opt.TopK <= 0 {
		opt.TopK = 10
	}
	if opt.Mode == "" {
		opt.Mode = ModeSearch
	}
	baseHits, err := e.vector.SearchWithOptions(ctx, VectorSearchOptions{
		Workspace: opt.Workspace,
		Query:     opt.Query,
		TopK:      max(opt.TopK*3, 10),
		Types:     opt.Filters.Types,
		Tiers:     opt.Filters.Tiers,
	})
	if err != nil {
		return nil, err
	}
	weights := modeWeights(opt.Mode)
	now := e.clock()

	ranked := make([]RetrievalHit, 0, len(baseHits))
	for _, h := range baseHits {
		if !matchRetrievalFilters(h.Memory, opt.Filters) {
			continue
		}
		recency := recencyScore(now, h.Memory.UpdatedAt)
		outcome := outcomeScore(opt.Mode, h.Memory)
		decay := decayScore(h.Memory)
		tierBias := tierBiasScore(h.Memory.StorageTier)
		total := weights.Semantic*h.Score + weights.Recency*recency + weights.Outcome*outcome + weights.Decay*decay + weights.TierBias*tierBias
		ranked = append(ranked, RetrievalHit{
			Memory: h.Memory,
			Score:  total,
			Breakdown: SignalBreakdown{
				Semantic: h.Score,
				Recency:  recency,
				Outcome:  outcome,
				Decay:    decay,
				TierBias: tierBias,
				Total:    total,
			},
		})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if len(ranked) > opt.TopK {
		ranked = ranked[:opt.TopK]
	}
	ids := make([]string, 0, len(ranked))
	for _, h := range ranked {
		ids = append(ids, h.Memory.ID)
	}
	_ = e.vector.MarkAccessed(ctx, ids)
	return &RetrievalResult{
		Mode:    opt.Mode,
		Weights: weights,
		Hits:    ranked,
	}, nil
}

func matchRetrievalFilters(m core.MemoryEntry, f RetrievalFilters) bool {
	if f.OutcomeResult != nil {
		if m.Outcome == nil {
			return false
		}
		if m.Outcome.Result != *f.OutcomeResult {
			return false
		}
	}
	if f.MinConfidence != nil {
		if m.Confidence < *f.MinConfidence {
			return false
		}
	}
	if f.MinDecayScore != nil {
		if m.DecayScore < *f.MinDecayScore {
			return false
		}
	}
	if len(f.Entities) > 0 {
		has := false
		for _, e := range f.Entities {
			ev := strings.TrimSpace(strings.ToLower(e))
			if ev == "" {
				continue
			}
			for _, me := range m.Entities {
				if strings.ToLower(strings.TrimSpace(me)) == ev {
					has = true
					break
				}
			}
			if has {
				break
			}
		}
		if !has {
			return false
		}
	}
	if f.DateFrom != nil && !m.UpdatedAt.IsZero() {
		if m.UpdatedAt.Before(*f.DateFrom) {
			return false
		}
	}
	if f.DateTo != nil && !m.UpdatedAt.IsZero() {
		if m.UpdatedAt.After(*f.DateTo) {
			return false
		}
	}
	return true
}

func modeWeights(mode RetrievalMode) SignalWeights {
	switch mode {
	case ModeRecall:
		return SignalWeights{Semantic: 0.40, Recency: 0.28, Outcome: 0.10, Decay: 0.12, TierBias: 0.10}
	case ModeRelate:
		return SignalWeights{Semantic: 0.60, Recency: 0.10, Outcome: 0.05, Decay: 0.15, TierBias: 0.10}
	case ModeOutcomes:
		return SignalWeights{Semantic: 0.30, Recency: 0.15, Outcome: 0.40, Decay: 0.05, TierBias: 0.10}
	default:
		return SignalWeights{Semantic: 0.55, Recency: 0.20, Outcome: 0.10, Decay: 0.05, TierBias: 0.10}
	}
}

func tierBiasScore(tier core.StorageTier) float64 {
	switch tier {
	case core.TierMarkdown:
		return 1
	case core.TierVectorGraph:
		return 0.5
	case core.TierVector:
		return 0.35
	case core.TierDocument:
		return 0.2
	default:
		return 0
	}
}

func recencyScore(now, updated time.Time) float64 {
	if updated.IsZero() {
		return 0.2
	}
	h := now.Sub(updated).Hours()
	if h <= 0 {
		return 1
	}
	return 1 / (1 + h/24)
}

func outcomeScore(mode RetrievalMode, m core.MemoryEntry) float64 {
	if m.Outcome == nil {
		return 0
	}
	switch strings.ToLower(string(m.Outcome.Result)) {
	case "success":
		if mode == ModeOutcomes {
			return 1
		}
		return 0.8
	case "partial":
		return 0.55
	case "failure":
		if mode == ModeOutcomes {
			return 0.7
		}
		return 0.3
	default:
		return 0.1
	}
}

func decayScore(m core.MemoryEntry) float64 {
	if m.DecayScore <= 0 {
		return 1
	}
	return math.Max(0, 1-m.DecayScore)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
