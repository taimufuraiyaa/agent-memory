package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) ApplyRetrievalFeedback(ctx context.Context, memoryID string, feedback core.RetrievalFeedback, at time.Time) (*core.MemoryEntry, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	mem, err := s.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	applyFeedback(mem, feedback, at.UTC())
	if err := s.persistRetrievalState(ctx, mem); err != nil {
		return nil, err
	}
	return s.GetMemory(ctx, memoryID)
}

func (s *Store) ApplyReconsolidation(ctx context.Context, memoryID string, action core.ReconsolidationAction, successorID string, at time.Time) (*core.MemoryEntry, error) {
	if s == nil {
		return nil, errors.New("store is nil")
	}
	mem, err := s.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	at = at.UTC()
	tuning := config.ResolveAdaptiveFeedbackTuning()
	switch action {
	case core.ReconsolidateConfirmed:
		mem.SalienceScore = clampUnit(mem.SalienceScore + tuning.ConfirmedSalienceDelta)
		mem.UsefulCount++
		mem.LastHelpfulAt = at
		mem.FamiliarityBandLast = "strong_recall"
	case core.ReconsolidateClarified:
		mem.SalienceScore = clampUnit(mem.SalienceScore + tuning.ClarifiedSalienceDelta)
		mem.LastHelpfulAt = at
	case core.ReconsolidateContradicted:
		mem.SalienceScore = clampUnit(mem.SalienceScore + tuning.ContradictedSalienceDelta)
		mem.SuppressionScore = clampUnit(mem.SuppressionScore + tuning.ContradictedSuppressionDelta)
		mem.RejectedCount++
		mem.LastRejectedAt = at
		if !mem.Pinned {
			until := at.Add(tuning.ContradictedCooldown)
			mem.SuppressionUntil = &until
		}
		if successorID != "" {
			if err := s.AddRelation(ctx, successorID, memoryID, core.RelContradicts, 1, map[string]string{"source": "reconsolidation"}); err != nil {
				return nil, err
			}
		}
	case core.ReconsolidateSuperseded:
		mem.SalienceScore = clampUnit(mem.SalienceScore + tuning.SupersededSalienceDelta)
		mem.SuppressionScore = clampUnit(mem.SuppressionScore + tuning.SupersededSuppressionDelta)
		mem.LastRejectedAt = at
		if successorID == "" {
			return nil, errors.New("successor_memory_id is required for superseded reconsolidation")
		}
		if err := s.MarkSuperseded(ctx, []string{memoryID}, successorID); err != nil {
			return nil, err
		}
		if err := s.AddRelation(ctx, successorID, memoryID, core.RelSupersedes, 1, map[string]string{"source": "reconsolidation"}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid reconsolidation action: %s", action)
	}
	if err := s.persistRetrievalState(ctx, mem); err != nil {
		return nil, err
	}
	return s.GetMemory(ctx, memoryID)
}

func (s *Store) persistRetrievalState(ctx context.Context, mem *core.MemoryEntry) error {
	if mem == nil {
		return errors.New("memory is nil")
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE memories
SET salience_score = ?,
	suppression_score = ?,
	useful_count = ?,
	ignored_count = ?,
	rejected_count = ?,
	harmful_count = ?,
	last_helpful_at = ?,
	last_rejected_at = ?,
	suppression_until = ?,
	familiarity_band_last = ?
WHERE id = ?`,
		mem.SalienceScore,
		mem.SuppressionScore,
		mem.UsefulCount,
		mem.IgnoredCount,
		mem.RejectedCount,
		mem.HarmfulCount,
		timeStringOrEmpty(mem.LastHelpfulAt),
		timeStringOrEmpty(mem.LastRejectedAt),
		nullTimeString(mem.SuppressionUntil),
		mem.FamiliarityBandLast,
		mem.ID,
	)
	return err
}

func applyFeedback(mem *core.MemoryEntry, feedback core.RetrievalFeedback, at time.Time) {
	if mem == nil {
		return
	}
	tuning := config.ResolveAdaptiveFeedbackTuning()
	switch feedback {
	case core.FeedbackHelpful:
		mem.UsefulCount++
		mem.SalienceScore = clampUnit(mem.SalienceScore + tuning.HelpfulSalienceDelta)
		mem.SuppressionScore = clampUnit(mem.SuppressionScore + tuning.HelpfulSuppressionDelta)
		mem.LastHelpfulAt = at
		mem.SuppressionUntil = nil
		mem.FamiliarityBandLast = "strong_recall"
	case core.FeedbackIgnored:
		mem.IgnoredCount++
		mem.SalienceScore = clampUnit(mem.SalienceScore + tuning.IgnoredSalienceDelta)
		mem.SuppressionScore = clampUnit(mem.SuppressionScore + tuning.IgnoredSuppressionDelta)
		mem.FamiliarityBandLast = "weak_familiarity"
	case core.FeedbackRejected:
		mem.RejectedCount++
		mem.SalienceScore = clampUnit(mem.SalienceScore + tuning.RejectedSalienceDelta)
		mem.SuppressionScore = clampUnit(mem.SuppressionScore + tuning.RejectedSuppressionDelta)
		mem.LastRejectedAt = at
		if !mem.Pinned && !isFailureOutcome(mem) {
			until := at.Add(tuning.RejectedCooldown)
			mem.SuppressionUntil = &until
		}
		mem.FamiliarityBandLast = "suppressed"
	case core.FeedbackHarmful:
		mem.HarmfulCount++
		mem.SalienceScore = clampUnit(mem.SalienceScore + tuning.HarmfulSalienceDelta)
		mem.SuppressionScore = clampUnit(mem.SuppressionScore + tuning.HarmfulSuppressionDelta)
		mem.LastRejectedAt = at
		if !mem.Pinned && !isFailureOutcome(mem) {
			until := at.Add(tuning.HarmfulCooldown)
			mem.SuppressionUntil = &until
		}
		mem.FamiliarityBandLast = "suppressed"
	}
}

func isFailureOutcome(mem *core.MemoryEntry) bool {
	return mem != nil && mem.Outcome != nil && mem.Outcome.Result == core.OutcomeFailure
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
