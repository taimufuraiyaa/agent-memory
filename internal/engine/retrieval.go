package engine

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/config"
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
	Policy    RetrievalPolicy
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
	Semantic       float64 `json:"semantic"`
	Recency        float64 `json:"recency"`
	Outcome        float64 `json:"outcome"`
	Decay          float64 `json:"decay"`
	TierBias       float64 `json:"tier_bias"`
	Salience       float64 `json:"salience"`
	Suppression    float64 `json:"suppression"`
	RelativeToBest float64 `json:"relative_to_best"`
	Activation     float64 `json:"activation"`
	Total          float64 `json:"total"`
}

type FamiliarityBand string

const (
	BandStrongRecall    FamiliarityBand = "strong_recall"
	BandWeakFamiliarity FamiliarityBand = "weak_familiarity"
	BandSuppressed      FamiliarityBand = "suppressed"
)

type ExclusionReason string

const (
	ReasonMinSemantic      ExclusionReason = "min_semantic_score"
	ReasonMinTotal         ExclusionReason = "min_total_score"
	ReasonRelativeCutoff   ExclusionReason = "relative_cutoff"
	ReasonSuppression      ExclusionReason = "suppression"
	ReasonSuppressionUntil ExclusionReason = "suppression_until"
)

type RetrievalPolicy struct {
	MinSemanticScore    *float64 `json:"min_semantic_score,omitempty"`
	MinTotalScore       *float64 `json:"min_total_score,omitempty"`
	RelativeScoreCutoff *float64 `json:"relative_score_cutoff,omitempty"`
	WeakSemanticScore   *float64 `json:"weak_semantic_score,omitempty"`
	WeakTotalScore      *float64 `json:"weak_total_score,omitempty"`
	WeakRelativeCutoff  *float64 `json:"weak_relative_cutoff,omitempty"`
}

type RetrievalPolicySnapshot struct {
	MinSemanticScore    float64 `json:"min_semantic_score"`
	MinTotalScore       float64 `json:"min_total_score"`
	RelativeScoreCutoff float64 `json:"relative_score_cutoff"`
	WeakSemanticScore   float64 `json:"weak_semantic_score"`
	WeakTotalScore      float64 `json:"weak_total_score"`
	WeakRelativeCutoff  float64 `json:"weak_relative_cutoff"`
}

// RetrievalHit extends SearchHit with explain details.
type RetrievalHit struct {
	Memory           core.MemoryEntry  `json:"memory"`
	Score            float64           `json:"score"`
	Breakdown        SignalBreakdown   `json:"breakdown"`
	Band             FamiliarityBand   `json:"band"`
	ExclusionReasons []ExclusionReason `json:"exclusion_reasons,omitempty"`
}

// RetrievalResult is a ranked response with explainability.
type RetrievalResult struct {
	Mode           RetrievalMode           `json:"mode"`
	Weights        SignalWeights           `json:"weights"`
	Policy         RetrievalPolicySnapshot `json:"policy"`
	Hits           []RetrievalHit          `json:"hits"`
	StrongHits     []RetrievalHit          `json:"strong_hits,omitempty"`
	WeakHits       []RetrievalHit          `json:"weak_hits,omitempty"`
	SuppressedHits []RetrievalHit          `json:"suppressed_hits,omitempty"`
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
	cache  *QueryCache
	clock  func() time.Time
}

// NewRetrievalEngine creates a retrieval engine.
func NewRetrievalEngine(vector *VectorSearcher) *RetrievalEngine {
	return &RetrievalEngine{
		vector: vector,
		cache:  NewQueryCache(DefaultQueryCacheConfig()),
		clock:  func() time.Time { return time.Now().UTC() },
	}
}

// NewRetrievalEngineWithCache creates a retrieval engine with custom cache config.
func NewRetrievalEngineWithCache(vector *VectorSearcher, cacheConfig QueryCacheConfig) *RetrievalEngine {
	return &RetrievalEngine{
		vector: vector,
		cache:  NewQueryCache(cacheConfig),
		clock:  func() time.Time { return time.Now().UTC() },
	}
}

// Retrieve computes mode-aware weighted ranking with explain output.
// Results are cached to reduce latency for repeated queries.
func (e *RetrievalEngine) Retrieve(ctx context.Context, opt RetrievalOptions) (*RetrievalResult, error) {
	// Check result cache first
	if cachedHits := e.cache.GetResults(ctx, opt); cachedHits != nil {
		weights := modeWeights(opt.Mode)
		policy := policyForMode(opt.Mode, opt.Policy)
		
		return &RetrievalResult{
			Mode:       opt.Mode,
			Weights:    weights,
			Policy:     policy,
			Hits:       cachedHits,
			StrongHits: filterStrongHits(cachedHits),
			WeakHits:   filterWeakHits(cachedHits),
		}, nil
	}
	
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
	policy := policyForMode(opt.Mode, opt.Policy)
	now := e.clock()

	ranked := make([]RetrievalHit, 0, len(baseHits))
	for _, h := range baseHits {
		if !matchRetrievalFilters(h.Memory, opt.Filters) {
			continue
		}
		// Enforce the semantic floor before weighted reranking so recency and
		// other secondary signals cannot rescue low-semantic candidates.
		if h.Score < policy.MinSemanticScore {
			continue
		}
		recency := recencyScore(now, h.Memory.UpdatedAt)
		outcome := outcomeScore(opt.Mode, h.Memory)
		decay := decayScore(h.Memory)
		tierBias := tierBiasScore(h.Memory.StorageTier)
		salience := salienceSignal(now, h.Memory)
		suppression := suppressionSignal(now, h.Memory)
		activation := weights.Semantic*h.Score + weights.Recency*recency + weights.Outcome*outcome + weights.Decay*decay + weights.TierBias*tierBias + salience
		total := activation - suppression
		ranked = append(ranked, RetrievalHit{
			Memory: h.Memory,
			Score:  total,
			Breakdown: SignalBreakdown{
				Semantic:    h.Score,
				Recency:     recency,
				Outcome:     outcome,
				Decay:       decay,
				TierBias:    tierBias,
				Salience:    salience,
				Suppression: suppression,
				Activation:  activation,
				Total:       total,
			},
		})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	bestScore := 0.0
	if len(ranked) > 0 {
		bestScore = ranked[0].Score
	}
	strong := make([]RetrievalHit, 0, len(ranked))
	weak := make([]RetrievalHit, 0, len(ranked))
	suppressed := make([]RetrievalHit, 0, len(ranked))
	for _, hit := range ranked {
		hit.Breakdown.RelativeToBest = relativeToBest(bestScore, hit.Score)
		band, reasons := classifyHit(opt.Mode, policy, now, hit)
		hit.Band = band
		hit.ExclusionReasons = reasons
		switch band {
		case BandStrongRecall:
			strong = append(strong, hit)
		case BandWeakFamiliarity:
			weak = append(weak, hit)
		default:
			suppressed = append(suppressed, hit)
		}
	}
	if len(strong) > opt.TopK {
		strong = strong[:opt.TopK]
	}
	if len(weak) > opt.TopK {
		weak = weak[:opt.TopK]
	}
	if len(suppressed) > opt.TopK {
		suppressed = suppressed[:opt.TopK]
	}
	ids := make([]string, 0, len(strong)+len(weak))
	for _, h := range strong {
		ids = append(ids, h.Memory.ID)
	}
	for _, h := range weak {
		ids = append(ids, h.Memory.ID)
	}
	_ = e.vector.MarkAccessed(ctx, ids)
	visible := strong
	if opt.Mode != ModeRecall {
		visible = append(append([]RetrievalHit{}, strong...), weak...)
	}
	
	// Store results in cache for future queries
	e.cache.SetResults(ctx, opt, visible)
	
	return &RetrievalResult{
		Mode:           opt.Mode,
		Weights:        weights,
		Policy:         policy,
		Hits:           visible,
		StrongHits:     strong,
		WeakHits:       weak,
		SuppressedHits: suppressed,
	}, nil
}

// InvalidateCache clears cached results for a workspace after writes.
func (e *RetrievalEngine) InvalidateCache(workspace string) {
	e.cache.InvalidateWorkspace(workspace)
}

// CacheStats returns cache performance metrics.
func (e *RetrievalEngine) CacheStats() CacheStats {
	return e.cache.Stats()
}

func filterStrongHits(hits []RetrievalHit) []RetrievalHit {
	strong := make([]RetrievalHit, 0, len(hits))
	for _, h := range hits {
		if h.Band == BandStrongRecall {
			strong = append(strong, h)
		}
	}
	return strong
}

func filterWeakHits(hits []RetrievalHit) []RetrievalHit {
	weak := make([]RetrievalHit, 0, len(hits))
	for _, h := range hits {
		if h.Band == BandWeakFamiliarity {
			weak = append(weak, h)
		}
	}
	return weak
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

func policyForMode(mode RetrievalMode, override RetrievalPolicy) RetrievalPolicySnapshot {
	defaults := config.ResolveAdaptivePolicy(string(mode))
	base := RetrievalPolicySnapshot{
		MinSemanticScore:    defaults.MinSemanticScore,
		MinTotalScore:       defaults.MinTotalScore,
		RelativeScoreCutoff: defaults.RelativeScoreCutoff,
		WeakSemanticScore:   defaults.WeakSemanticScore,
		WeakTotalScore:      defaults.WeakTotalScore,
		WeakRelativeCutoff:  defaults.WeakRelativeCutoff,
	}
	if override.MinSemanticScore != nil {
		base.MinSemanticScore = *override.MinSemanticScore
	}
	if override.MinTotalScore != nil {
		base.MinTotalScore = *override.MinTotalScore
	}
	if override.RelativeScoreCutoff != nil {
		base.RelativeScoreCutoff = *override.RelativeScoreCutoff
	}
	if override.WeakSemanticScore != nil {
		base.WeakSemanticScore = *override.WeakSemanticScore
	}
	if override.WeakTotalScore != nil {
		base.WeakTotalScore = *override.WeakTotalScore
	}
	if override.WeakRelativeCutoff != nil {
		base.WeakRelativeCutoff = *override.WeakRelativeCutoff
	}
	return base
}

func salienceSignal(now time.Time, m core.MemoryEntry) float64 {
	tuning := core.DefaultAdaptiveSignalTuning()
	signal := clampFloat(m.SalienceScore, 0, 1) * tuning.SalienceScoreFactor
	if m.UsefulCount > 0 {
		signal += math.Min(float64(m.UsefulCount), tuning.UsefulCountCap) * tuning.UsefulCountStep
	}
	if !m.LastHelpfulAt.IsZero() {
		signal += recencyScore(now, m.LastHelpfulAt) * tuning.LastHelpfulRecencyWeight
	}
	return signal
}

func suppressionSignal(now time.Time, m core.MemoryEntry) float64 {
	tuning := core.DefaultAdaptiveSignalTuning()
	signal := clampFloat(m.SuppressionScore, 0, 1) * tuning.SuppressionScoreFactor
	if m.RejectedCount > 0 {
		signal += math.Min(float64(m.RejectedCount), tuning.RejectedCountCap) * tuning.RejectedCountStep
	}
	if m.HarmfulCount > 0 {
		signal += math.Min(float64(m.HarmfulCount), tuning.HarmfulCountCap) * tuning.HarmfulCountStep
	}
	if !m.LastRejectedAt.IsZero() {
		signal += recencyScore(now, m.LastRejectedAt) * tuning.LastRejectedRecencyWeight
	}
	if m.SuppressionUntil != nil && m.SuppressionUntil.After(now) {
		signal += tuning.ActiveSuppressionBoost
	}
	if m.Pinned {
		signal *= tuning.PinnedSuppressionFactor
	}
	return signal
}

func classifyHit(mode RetrievalMode, policy RetrievalPolicySnapshot, now time.Time, hit RetrievalHit) (FamiliarityBand, []ExclusionReason) {
	tuning := core.DefaultAdaptiveSignalTuning()
	reasons := make([]ExclusionReason, 0, 4)
	relative := hit.Breakdown.RelativeToBest
	if hit.Memory.SuppressionUntil != nil && hit.Memory.SuppressionUntil.After(now) && !hit.Memory.Pinned {
		reasons = append(reasons, ReasonSuppressionUntil)
	}
	if hit.Breakdown.Suppression >= tuning.SuppressionBandThreshold && !hit.Memory.Pinned {
		reasons = append(reasons, ReasonSuppression)
	}
	if hit.Breakdown.Semantic < policy.MinSemanticScore {
		reasons = append(reasons, ReasonMinSemantic)
	}
	if hit.Score < policy.MinTotalScore {
		reasons = append(reasons, ReasonMinTotal)
	}
	if relative < policy.RelativeScoreCutoff {
		reasons = append(reasons, ReasonRelativeCutoff)
	}
	if len(reasons) == 0 {
		return BandStrongRecall, nil
	}

	weakReasons := make([]ExclusionReason, 0, len(reasons))
	if hit.Breakdown.Semantic < policy.WeakSemanticScore {
		weakReasons = append(weakReasons, ReasonMinSemantic)
	}
	if hit.Score < policy.WeakTotalScore {
		weakReasons = append(weakReasons, ReasonMinTotal)
	}
	if relative < policy.WeakRelativeCutoff {
		weakReasons = append(weakReasons, ReasonRelativeCutoff)
	}
	if containsReason(reasons, ReasonSuppression) || containsReason(reasons, ReasonSuppressionUntil) {
		return BandSuppressed, reasons
	}
	if len(weakReasons) == 0 {
		if mode == ModeRecall {
			return BandWeakFamiliarity, reasons
		}
		return BandWeakFamiliarity, reasons
	}
	return BandSuppressed, reasons
}

func relativeToBest(best, score float64) float64 {
	if best <= 0 {
		if score <= 0 {
			return 0
		}
		return 1
	}
	r := score / best
	return clampFloat(r, 0, 1)
}

func containsReason(items []ExclusionReason, target ExclusionReason) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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

