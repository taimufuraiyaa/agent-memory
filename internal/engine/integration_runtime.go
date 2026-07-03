package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// SessionEndLifecycleResult combines extraction output with the opportunistic REM run.
type SessionEndLifecycleResult struct {
	TotalExtracted  int               `json:"total_extracted"`
	WrittenIDs      []string          `json:"written_ids"`
	LifecycleRan    bool              `json:"lifecycle_ran"`
	LifecycleMetrics *LifecycleMetrics `json:"lifecycle_metrics,omitempty"`
}

// RunSessionEndLifecycle executes session-end extraction and then runs the full lifecycle chain.
func RunSessionEndLifecycle(ctx context.Context, workspace, transcript string, store *sqlite.Store, pipeline *WritePipeline) (*SessionEndLifecycleResult, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if pipeline == nil {
		return nil, errors.New("pipeline is required")
	}
	extractor := NewSessionEndExtractor(pipeline)
	out, err := extractor.ExtractAndStore(ctx, workspace, transcript)
	if err != nil {
		return nil, err
	}
	lifecycle := NewLifecycleManager(store, pipeline)
	metrics, err := lifecycle.Run(ctx, workspace)
	if err != nil {
		return nil, err
	}
	return &SessionEndLifecycleResult{
		TotalExtracted:  out.TotalExtracted,
		WrittenIDs:      append([]string(nil), out.WrittenIDs...),
		LifecycleRan:    true,
		LifecycleMetrics: metrics,
	}, nil
}

// RecallReconstructionMeta exposes what the automatic reconstruction pass did.
type RecallReconstructionMeta struct {
	Attempted       bool    `json:"attempted"`
	Triggered       bool    `json:"triggered"`
	RequiresConfirm bool    `json:"requires_confirm"`
	Included        bool    `json:"included"`
	Strategy        string  `json:"strategy,omitempty"`
	Confidence      float64 `json:"confidence"`
	ReconstructedID string  `json:"reconstructed_id,omitempty"`
	Reason          string  `json:"reason,omitempty"`
}

// AugmentRecallWithReconstruction attempts a tombstone-based auto reconstruction for recall.
func AugmentRecallWithReconstruction(
	ctx context.Context,
	workspace string,
	query string,
	retrieved *RetrievalResult,
	retrieval *RetrievalEngine,
	store *sqlite.Store,
	pipeline *WritePipeline,
	topK int,
) (*RetrievalResult, *RecallReconstructionMeta, error) {
	meta := &RecallReconstructionMeta{}
	if retrieved == nil || retrieval == nil || store == nil || pipeline == nil {
		return retrieved, meta, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return retrieved, meta, nil
	}
	meta.Attempted = true

	re := NewReconstructionEngine(store, pipeline)
	out, err := re.Reconstruct(ctx, workspace, query, false)
	if err != nil {
		return nil, meta, err
	}
	meta.Triggered = out.Triggered
	meta.RequiresConfirm = out.RequiresConfirm
	meta.Strategy = out.Strategy
	meta.Confidence = out.Confidence
	meta.ReconstructedID = out.ReconstructedID
	meta.Reason = out.Reason
	if !out.Triggered || strings.TrimSpace(out.ReconstructedID) == "" {
		return retrieved, meta, nil
	}
	if hasRetrievalHitID(retrieved.Hits, out.ReconstructedID) {
		meta.Included = true
		return retrieved, meta, nil
	}

	refreshed, err := retrieval.Retrieve(ctx, RetrievalOptions{
		Workspace: workspace,
		Query:     query,
		TopK:      max(topK, len(retrieved.Hits)+1),
		Mode:      retrieved.Mode,
	})
	if err != nil {
		return nil, meta, err
	}
	if hasRetrievalHitID(refreshed.Hits, out.ReconstructedID) {
		meta.Included = true
		return refreshed, meta, nil
	}

	mem, err := store.GetMemory(ctx, out.ReconstructedID)
	if err != nil || mem == nil {
		return refreshed, meta, nil
	}
	fallback := reconstructedFallbackHit(*mem, refreshed.Mode, refreshed.Weights)
	refreshed.Hits = appendUniqueRetrievalHit(refreshed.Hits, fallback)
	refreshed.StrongHits = appendUniqueRetrievalHit(refreshed.StrongHits, fallback)
	meta.Included = true
	return refreshed, meta, nil
}

func reconstructedFallbackHit(m core.MemoryEntry, mode RetrievalMode, weights SignalWeights) RetrievalHit {
	now := time.Now().UTC()
	semantic := 0.82
	recency := recencyScore(now, m.UpdatedAt)
	outcome := outcomeScore(mode, m)
	decay := decayScore(m)
	tierBias := tierBiasScore(m.StorageTier)
	activation := weights.Semantic*semantic + weights.Recency*recency + weights.Outcome*outcome + weights.Decay*decay + weights.TierBias*tierBias
	return RetrievalHit{
		Memory: m,
		Score:  activation,
		Breakdown: SignalBreakdown{
			Semantic:       semantic,
			Recency:        recency,
			Outcome:        outcome,
			Decay:          decay,
			TierBias:       tierBias,
			Suppression:    0,
			Salience:       0,
			RelativeToBest: 1,
			Activation:     activation,
			Total:          activation,
		},
		Band: BandStrongRecall,
	}
}

func hasRetrievalHitID(hits []RetrievalHit, id string) bool {
	for _, h := range hits {
		if h.Memory.ID == id {
			return true
		}
	}
	return false
}

func appendUniqueRetrievalHit(hits []RetrievalHit, hit RetrievalHit) []RetrievalHit {
	if hasRetrievalHitID(hits, hit.Memory.ID) {
		return hits
	}
	return append(hits, hit)
}
