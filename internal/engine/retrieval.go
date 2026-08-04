package engine

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
)

// RetrievalMode alters signal weighting depending on caller intent.
type RetrievalMode string

const (
	ModeSearch      RetrievalMode = "search"
	ModeRecall      RetrievalMode = "recall"
	ModeRelate      RetrievalMode = "relate"
	ModeOutcomes    RetrievalMode = "outcomes"
	ModeGraphExpand RetrievalMode = "graph-expand"
	ModeTerms       RetrievalMode = "terms"
)

// RetrievalOptions controls retrieval behavior.
type RetrievalOptions struct {
	Workspace string
	Query     string
	TopK      int
	Mode      RetrievalMode
	Depth     int // graph expansion traversal depth limit
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
	// MinSemanticScore is the absolute floor for cosine similarity before mixer blending.
	// Hits below this threshold are excluded from ranking.
	// Default per-mode (search: 0.30, recall: 0.25, relate: 0.35, outcomes: 0.15).
	MinSemanticScore    *float64 `json:"min_semantic_score,omitempty"`
	MinTotalScore       *float64 `json:"min_total_score,omitempty"`
	RelativeScoreCutoff *float64 `json:"relative_score_cutoff,omitempty"`
	WeakSemanticScore   *float64 `json:"weak_semantic_score,omitempty"`
	WeakTotalScore      *float64 `json:"weak_total_score,omitempty"`
	WeakRelativeCutoff  *float64 `json:"weak_relative_cutoff,omitempty"`
	// SemanticScoreBand is the margin around MinSemanticScore within which a
	// quantized-path score is re-checked on the float path. Default 0.03.
	SemanticScoreBand *float64 `json:"semantic_score_band,omitempty"`
}

type RetrievalPolicySnapshot struct {
	MinSemanticScore    float64 `json:"min_semantic_score"`
	MinTotalScore       float64 `json:"min_total_score"`
	RelativeScoreCutoff float64 `json:"relative_score_cutoff"`
	WeakSemanticScore   float64 `json:"weak_semantic_score"`
	WeakTotalScore      float64 `json:"weak_total_score"`
	WeakRelativeCutoff  float64 `json:"weak_relative_cutoff"`
	SemanticScoreBand   float64 `json:"semantic_score_band"`
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
	RequestID      string                  `json:"request_id,omitempty"`
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

// NewRetrievalEngineWithSharedCache creates a retrieval engine with a shared query cache.
func NewRetrievalEngineWithSharedCache(vector *VectorSearcher, cache *QueryCache) *RetrievalEngine {
	return &RetrievalEngine{
		vector: vector,
		cache:  cache,
		clock:  func() time.Time { return time.Now().UTC() },
	}
}

// Retrieve computes mode-aware weighted ranking with explain output.
// Results are cached to reduce latency for repeated queries.
func (e *RetrievalEngine) Retrieve(ctx context.Context, opt RetrievalOptions) (res *RetrievalResult, retrieveErr error) {
	spanName := "agent-memory.search"
	if opt.Mode == ModeRecall {
		spanName = "agent-memory.recall"
	}
	ctx, span := observability.StartSpan(ctx, spanName)
	defer span.End()

	observability.SetSpanAttributes(ctx,
		observability.WorkspaceAttr(opt.Workspace),
		observability.QueryAttr(opt.Query),
		observability.TopKAttr(opt.TopK),
		observability.ModeAttr(string(opt.Mode)),
	)

	timer := observability.NewTimer()
	defer func() {
		metrics := observability.GetRegistry()
		status := "success"
		if retrieveErr != nil {
			status = "error"
			errType := "runtime_error"
			metrics.RetrievalErrors.WithLabelValues(opt.Workspace, string(opt.Mode), errType).Inc()
			observability.RecordSpanError(ctx, retrieveErr)
		} else if res != nil {
			observability.SetSpanAttributes(ctx, observability.HitCountAttr(len(res.Hits)))
			metrics.RetrievalHits.WithLabelValues(opt.Workspace, string(opt.Mode)).Observe(float64(len(res.Hits)))
		}
		// RetrievalTotal is aggregate-only (no workspace label) to bound cardinality.
		metrics.RetrievalTotal.WithLabelValues(string(opt.Mode), status).Inc()
		timer.ObserveDuration(metrics.RetrievalDuration.WithLabelValues(opt.Workspace, string(opt.Mode)))
	}()

	// Check result cache first
	if cachedHits := e.cache.GetResults(ctx, opt); cachedHits != nil {
		AddCacheHitCount(1)
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

	if opt.Mode == ModeGraphExpand {
		weights := modeWeights(opt.Mode)
		policy := policyForMode(opt.Mode, opt.Policy)
		hits, err := e.retrieveGraphExpand(ctx, opt, policy)
		if err != nil {
			return nil, err
		}
		bestScore := 0.0
		if len(hits) > 0 {
			bestScore = hits[0].Score
		}
		strong := make([]RetrievalHit, 0, len(hits))
		weak := make([]RetrievalHit, 0, len(hits))
		suppressed := make([]RetrievalHit, 0, len(hits))
		now := e.clock()
		for i, hit := range hits {
			hits[i].Breakdown.RelativeToBest = relativeToBest(bestScore, hit.Score)
			band, reasons := classifyHit(opt.Mode, policy, now, hits[i])
			hits[i].Band = band
			hits[i].ExclusionReasons = reasons
			switch band {
			case BandStrongRecall:
				strong = append(strong, hits[i])
			case BandWeakFamiliarity:
				weak = append(weak, hits[i])
			default:
				suppressed = append(suppressed, hits[i])
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
		visible := append(append([]RetrievalHit{}, strong...), weak...)
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
	AddCandidateCount(int64(len(baseHits)))
	AddVectorSearchCount(1)
	weights := modeWeights(opt.Mode)
	policy := policyForMode(opt.Mode, opt.Policy)
	now := e.clock()

	ranked := make([]RetrievalHit, 0, len(baseHits))
	for _, h := range baseHits {
		if !matchRetrievalFilters(h.Memory, opt.Filters) {
			continue
		}
		semanticScore := e.rescoreInBand(ctx, h.Score, h.Memory, opt.Query, policy)
		if semanticScore < policy.MinSemanticScore {
			continue
		}
		recency := recencyScore(now, h.Memory.UpdatedAt)
		outcome := outcomeScore(opt.Mode, h.Memory)
		decay := decayScore(h.Memory)
		tierBias := tierBiasScore(h.Memory.StorageTier)
		salience := salienceSignal(now, h.Memory)
		suppression := suppressionSignal(now, h.Memory)
		activation := weights.Semantic*math.Max(0, semanticScore) + weights.Recency*recency + weights.Outcome*outcome + weights.Decay*decay + weights.TierBias*tierBias + salience
		total := activation - suppression
		ranked = append(ranked, RetrievalHit{
			Memory: h.Memory,
			Score:  total,
			Breakdown: SignalBreakdown{
				Semantic:    semanticScore,
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
		SemanticScoreBand:   defaults.SemanticScoreBand,
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
	if override.SemanticScoreBand != nil {
		base.SemanticScoreBand = *override.SemanticScoreBand
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

// rescoreInBand checks whether a hit's semantic score falls within the band
// around MinSemanticScore. If so, it re-scores using the full-precision float
// cosine path (embeddings.Cosine) to make the threshold decision deterministic
// regardless of which search path (FWHT-quantized or float) produced the
// original score. If re-scoring fails, the original score is returned unchanged
// (fail-open).
func (e *RetrievalEngine) rescoreInBand(ctx context.Context, score float64, memory core.MemoryEntry, queryText string, policy RetrievalPolicySnapshot) float64 {
	band := policy.SemanticScoreBand
	// A band of exactly zero means "no rescoring" (caller explicitly disabled).
	// A negative or zero band from unset defaults is treated as unset → use 0.03.
	// Since the default is 0.03, band==0 only happens from explicit override.
	if band == 0 {
		return score
	}
	low := policy.MinSemanticScore - band
	high := policy.MinSemanticScore + band
	if score < low || score >= high {
		return score
	}

	// Re-score on the float path.
	qv, err := e.vector.Provider().Embed(ctx, queryText)
	if err != nil {
		return score // fail-open
	}

	memoryText := memoryVectorText(memory)
	mv, err := e.vector.Provider().Embed(ctx, memoryText)
	if err != nil {
		return score // fail-open
	}

	floatScore, err := embeddings.Cosine(qv, mv)
	if err != nil {
		return score // fail-open
	}
	return floatScore
}

func (e *RetrievalEngine) retrieveGraphExpand(ctx context.Context, opt RetrievalOptions, policy RetrievalPolicySnapshot) ([]RetrievalHit, error) {
	// 1. Get seed memories via semantic search (limit to TopK seeds)
	seedHits, err := e.vector.SearchWithOptions(ctx, VectorSearchOptions{
		Workspace: opt.Workspace,
		Query:     opt.Query,
		TopK:      opt.TopK,
		Types:     opt.Filters.Types,
		Tiers:     opt.Filters.Tiers,
	})
	if err != nil {
		return nil, err
	}

	// 2. Perform BFS expansion
	depthLimit := opt.Depth
	if depthLimit <= 0 {
		depthLimit = 2
	}

	type bfsNode struct {
		id       string
		distance int
		weight   float64
	}

	queue := make([]bfsNode, 0)
	visited := make(map[string]int)        // ID -> distance
	pathWeight := make(map[string]float64) // ID -> max relation weight

	for _, h := range seedHits {
		queue = append(queue, bfsNode{id: h.Memory.ID, distance: 0, weight: 1.0})
		visited[h.Memory.ID] = 0
		pathWeight[h.Memory.ID] = 1.0
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if curr.distance >= depthLimit {
			continue
		}

		rels, err := e.vector.Store().ListRelations(ctx, curr.id)
		if err != nil {
			continue
		}

		for _, r := range rels {
			newDist := curr.distance + 1
			newWeight := curr.weight * r.Weight

			if prevDist, exists := visited[r.TargetID]; exists {
				if newDist < prevDist {
					visited[r.TargetID] = newDist
					pathWeight[r.TargetID] = newWeight
					queue = append(queue, bfsNode{id: r.TargetID, distance: newDist, weight: newWeight})
				} else if newDist == prevDist && newWeight > pathWeight[r.TargetID] {
					pathWeight[r.TargetID] = newWeight
				}
			} else {
				visited[r.TargetID] = newDist
				pathWeight[r.TargetID] = newWeight
				queue = append(queue, bfsNode{id: r.TargetID, distance: newDist, weight: newWeight})
			}
		}
	}

	// 3. Compute query embedding for scoring
	qv, err := e.vector.Provider().Embed(ctx, opt.Query)
	if err != nil {
		return nil, err
	}

	// Get all cached vectors for workspace to avoid re-embedding
	vectorsMap, err := e.vector.Store().ListMemoryVectorsByWorkspace(ctx, opt.Workspace)
	if err != nil {
		vectorsMap = make(map[string][]float32)
	}

	now := e.clock()
	// 4. Retrieve memory details in a batch query and score them
	visitedIDs := make([]string, 0, len(visited))
	for id := range visited {
		visitedIDs = append(visitedIDs, id)
	}
	memoriesMap, err := e.vector.Store().GetMemoriesByIDs(ctx, visitedIDs)
	if err != nil {
		memoriesMap = make(map[string]core.MemoryEntry)
	}

	ranked := make([]RetrievalHit, 0, len(visited))
	for id, dist := range visited {
		mEntry, ok := memoriesMap[id]
		if !ok {
			continue
		}
		m := &mEntry
		if !matchRetrievalFilters(*m, opt.Filters) {
			continue
		}

		// Compute semantic score
		var score float64
		mv, hasVector := vectorsMap[id]
		if !hasVector || len(mv) == 0 {
			text := memoryVectorText(*m)
			mv, err = e.vector.Provider().Embed(ctx, text)
			if err == nil {
				score, _ = embeddings.Cosine(qv, mv)
				_ = e.vector.Store().UpsertMemoryVector(ctx, m.ID, m.Workspace, e.vector.Provider().Name(), e.vector.Provider().ModelVersion(), mv)
			}
		} else {
			score, _ = embeddings.Cosine(qv, mv)
		}

		// Band re-scoring: if semantic score is near MinSemanticScore,
		// re-check on the float path for determinism regardless of
		// which vector source was used.
		score = e.rescoreInBand(ctx, score, *m, opt.Query, policy)

		// Calculate total score using path distance and relationship weight
		relWeight := pathWeight[id]
		distanceFactor := 1.0 / (1.0 + float64(dist))
		totalScore := math.Max(0, score) * distanceFactor * relWeight

		recency := recencyScore(now, m.UpdatedAt)
		outcome := outcomeScore(opt.Mode, *m)
		decay := decayScore(*m)
		tierBias := tierBiasScore(m.StorageTier)
		salience := salienceSignal(now, *m)
		suppression := suppressionSignal(now, *m)

		ranked = append(ranked, RetrievalHit{
			Memory: *m,
			Score:  totalScore,
			Breakdown: SignalBreakdown{
				Semantic:    score,
				Recency:     recency,
				Outcome:     outcome,
				Decay:       decay,
				TierBias:    tierBias,
				Salience:    salience,
				Suppression: suppression,
				Total:       totalScore,
			},
		})
	}

	sort.Slice(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	return ranked, nil
}
