package engine

import (
	"context"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// ConfidenceBand classifies a confidence score into an action.
type ConfidenceBand string

const (
	ConfidenceHigh   ConfidenceBand = "high"   // >= 0.8 — store immediately
	ConfidenceMedium ConfidenceBand = "medium"  // 0.5–0.8 — store with low-confidence tag
	ConfidenceLow    ConfidenceBand = "low"     // < 0.5 — discard
)

const (
	tagLowConfidence = "low-confidence"

	thresholdHigh   = 0.8
	thresholdMedium = 0.5
)

// ClassifyConfidence maps a score to a band.
func ClassifyConfidence(score float64) ConfidenceBand {
	switch {
	case score >= thresholdHigh:
		return ConfidenceHigh
	case score >= thresholdMedium:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

// isFailureOutcome returns true when the write is a failure outcome.
// Failures always bypass the confidence gate.
func isFailureOutcome(in WriteInput) bool {
	return in.Type == core.OutcomeMemory &&
		in.Outcome != nil &&
		in.Outcome.Result == core.OutcomeFailure
}

// EstimateConfidence scores how confident we are in a piece of knowledge.
//
// Signals (all additive, capped at 1.0):
//   - Base score by source type
//   - Corroboration boost: existing memories with overlapping content
//   - Contradiction penalty: existing memories that contradict this one
func EstimateConfidence(ctx context.Context, in WriteInput, store *sqlite.Store) float64 {
	score := baseConfidenceBySource(in.Source.Type)

	existing, err := store.ListMemoriesByWorkspace(ctx, in.Workspace)
	if err != nil || len(existing) == 0 {
		return clamp(score)
	}

	corroborations := 0
	contradictions := 0

	for _, m := range existing {
		if m.SupersededBy != nil {
			continue
		}
		sim := overlap(in.Content, m.Content)
		if sim >= 0.6 {
			corroborations++
		} else if sim >= 0.3 && isLikelyContradiction(in.Content, m.Content) {
			contradictions++
		}
	}

	// Each corroboration adds 0.05, capped at +0.2
	corroborationBoost := float64(corroborations) * 0.05
	if corroborationBoost > 0.2 {
		corroborationBoost = 0.2
	}

	// Each contradiction subtracts 0.15
	contradictionPenalty := float64(contradictions) * 0.15

	score = score + corroborationBoost - contradictionPenalty
	return clamp(score)
}

// baseConfidenceBySource returns a starting score based on how the memory was produced.
func baseConfidenceBySource(t core.SourceType) float64 {
	switch t {
	case core.SourceAgentObservation, core.SourceCodeAnalysis, core.SourceUserInput:
		return 0.8 // direct observation
	case core.SourceConsolidation, core.SourceReflection:
		return 0.65 // derived
	case core.SourceReconstruction:
		return 0.5 // reconstructed — starts at medium
	default:
		return 0.75
	}
}

// isLikelyContradiction uses simple negation heuristics.
func isLikelyContradiction(a, b string) bool {
	negations := []string{"not ", "never ", "no ", "doesn't ", "does not ", "isn't ", "is not "}
	aLow := strings.ToLower(a)
	bLow := strings.ToLower(b)
	for _, neg := range negations {
		aHas := strings.Contains(aLow, neg)
		bHas := strings.Contains(bLow, neg)
		if aHas != bHas {
			return true
		}
	}
	return false
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
