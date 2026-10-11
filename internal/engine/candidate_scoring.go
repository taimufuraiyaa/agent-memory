package engine

import (
	"context"
	"math"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

// MatchCandidateFilters applies the same explicit filters to every retrieval
// source, including candidates found through terms or graph relationships.
func MatchCandidateFilters(memory core.MemoryEntry, options RetrievalOptions) bool {
	if !memory.Pinned && ((memory.SuppressionUntil != nil && memory.SuppressionUntil.After(time.Now().UTC())) || suppressionSignal(time.Now().UTC(), memory) >= core.DefaultAdaptiveSignalTuning().SuppressionBandThreshold) {
		return false
	}
	return memory.Workspace == options.Workspace && memory.SupersededBy == nil && memory.SuppressionScore < 1 &&
		matchMemoryFilters(memory, options.Filters.Types, options.Filters.Tiers) && matchRetrievalFilters(memory, options.Filters)
}

// ScoreCandidates ranks exact canonical records without repeating semantic
// discovery or applying its floor: connected evidence may have low cosine.
func (e *RetrievalEngine) ScoreCandidates(ctx context.Context, opt RetrievalOptions, memories []core.MemoryEntry) ([]RetrievalHit, error) {
	if len(memories) == 0 {
		return nil, nil
	}
	qv := e.vector.cache.GetEmbedding(ctx, opt.Query)
	if qv == nil {
		var err error
		qv, err = e.vector.provider.Embed(ctx, opt.Query)
		if err != nil {
			return nil, err
		}
		e.vector.cache.SetEmbedding(ctx, opt.Query, qv)
	}
	ids := make([]string, 0, len(memories))
	for _, m := range memories {
		ids = append(ids, m.ID)
	}
	vectors, err := e.vector.store.MemoryVectorsByIDs(ctx, opt.Workspace, e.vector.provider.Name(), ids)
	if err != nil {
		return nil, err
	}
	weights, now := modeWeights(opt.Mode), e.clock()
	hits := make([]RetrievalHit, 0, len(memories))
	for _, m := range memories {
		if !MatchCandidateFilters(m, opt) {
			continue
		}
		mv := vectors[m.ID]
		if len(mv) == 0 {
			mv, err = e.vector.provider.Embed(ctx, memoryVectorText(m))
			if err != nil {
				return nil, err
			}
			_ = e.vector.store.UpsertMemoryVector(ctx, m.ID, m.Workspace, e.vector.provider.Name(), e.vector.provider.ModelVersion(), mv)
		}
		semantic, err := embeddings.Cosine(qv, mv)
		if err != nil {
			return nil, err
		}
		b := SignalBreakdown{Semantic: semantic, Recency: recencyScore(now, m.UpdatedAt), Outcome: outcomeScore(opt.Mode, m), Decay: decayScore(m), TierBias: tierBiasScore(m.StorageTier), Salience: salienceSignal(now, m), Suppression: suppressionSignal(now, m)}
		b.Activation = weights.Semantic*math.Max(0, semantic) + weights.Recency*b.Recency + weights.Outcome*b.Outcome + weights.Decay*b.Decay + weights.TierBias*b.TierBias + b.Salience
		b.Total = b.Activation - b.Suppression
		hits = append(hits, RetrievalHit{Memory: m, Score: b.Total, Breakdown: b, Band: BandWeakFamiliarity})
	}
	return hits, nil
}
