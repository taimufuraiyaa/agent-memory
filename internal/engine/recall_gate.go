package engine

import (
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type RecallStrategy string

const (
	RecallStrategyDirectRecall    RecallStrategy = "direct_recall"
	RecallStrategySearchSatisfied RecallStrategy = "search_satisfied"
	RecallStrategyEscalatedRecall RecallStrategy = "escalated_recall"
)

type RecallTrigger string

const (
	RecallTriggerContinuationPrompt RecallTrigger = "continuation_prompt"
	RecallTriggerSearchSatisfied    RecallTrigger = "search_satisfied"
	RecallTriggerSearchEmpty        RecallTrigger = "search_empty"
	RecallTriggerWeakResults        RecallTrigger = "weak_results"
	RecallTriggerLowConfidence      RecallTrigger = "low_confidence_search"
	RecallTriggerAmbiguousSearch    RecallTrigger = "ambiguous_search"
)

type RecallSearchProbeHit struct {
	MemoryID       string          `json:"memory_id"`
	MemoryType     core.MemoryType `json:"memory_type"`
	Score          float64         `json:"score"`
	SemanticScore  float64         `json:"semantic_score"`
	RelativeToBest float64         `json:"relative_to_best"`
}

type RecallSearchProbe struct {
	StrongHitCount     int                    `json:"strong_hit_count"`
	WeakHitCount       int                    `json:"weak_hit_count"`
	SuppressedHitCount int                    `json:"suppressed_hit_count"`
	Policy             RetrievalPolicySnapshot `json:"policy"`
	TopStrongHit       *RecallSearchProbeHit  `json:"top_strong_hit,omitempty"`
}

type RecallGateDecision struct {
	Strategy         RecallStrategy     `json:"strategy"`
	Trigger          RecallTrigger      `json:"trigger"`
	SearchSufficient bool               `json:"search_sufficient"`
	Probe            *RecallSearchProbe `json:"probe,omitempty"`
}

const (
	recallSearchMinTopSemantic   = 0.45
	recallSearchMinTopTotal      = 0.30
	recallSearchAmbiguityRatio   = 0.92
	recallSearchMinTopConfidence = 0.55
)

func IsContinuationPrompt(task string) bool {
	task = strings.ToLower(strings.TrimSpace(task))
	if task == "" {
		return false
	}
	phrases := []string{
		"continue",
		"resume",
		"what were we doing",
		"pick up where we left off",
		"pick up where we left",
		"continue from last time",
		"resume from last time",
		"continue previous work",
		"resume previous work",
		"previous session",
		"last session",
		"last time",
	}
	for _, phrase := range phrases {
		if strings.Contains(task, phrase) {
			return true
		}
	}
	return false
}

func BuildRecallSearchProbe(result *RetrievalResult) *RecallSearchProbe {
	if result == nil {
		return nil
	}
	probe := &RecallSearchProbe{
		StrongHitCount:     len(result.StrongHits),
		WeakHitCount:       len(result.WeakHits),
		SuppressedHitCount: len(result.SuppressedHits),
		Policy:             result.Policy,
	}
	if len(result.StrongHits) == 0 {
		return probe
	}
	top := result.StrongHits[0]
	probe.TopStrongHit = &RecallSearchProbeHit{
		MemoryID:       top.Memory.ID,
		MemoryType:     top.Memory.Type,
		Score:          top.Score,
		SemanticScore:  top.Breakdown.Semantic,
		RelativeToBest: top.Breakdown.RelativeToBest,
	}
	return probe
}

func DecideRecallGate(task string, probe *RetrievalResult) RecallGateDecision {
	if IsContinuationPrompt(task) {
		return RecallGateDecision{
			Strategy:         RecallStrategyDirectRecall,
			Trigger:          RecallTriggerContinuationPrompt,
			SearchSufficient: false,
		}
	}
	summary := BuildRecallSearchProbe(probe)
	if probe == nil || len(probe.StrongHits) == 0 {
		trigger := RecallTriggerSearchEmpty
		if probe != nil && (len(probe.WeakHits) > 0 || len(probe.SuppressedHits) > 0) {
			trigger = RecallTriggerWeakResults
		}
		return RecallGateDecision{
			Strategy:         RecallStrategyEscalatedRecall,
			Trigger:          trigger,
			SearchSufficient: false,
			Probe:            summary,
		}
	}

	top := probe.StrongHits[0]
	minSemantic := maxFloat64(probe.Policy.MinSemanticScore, recallSearchMinTopSemantic)
	minTotal := maxFloat64(probe.Policy.MinTotalScore, recallSearchMinTopTotal)
	if top.Breakdown.Semantic < minSemantic || top.Score < minTotal || top.Memory.Confidence < recallSearchMinTopConfidence {
		return RecallGateDecision{
			Strategy:         RecallStrategyEscalatedRecall,
			Trigger:          RecallTriggerLowConfidence,
			SearchSufficient: false,
			Probe:            summary,
		}
	}
	if len(probe.StrongHits) > 1 && top.Score > 0 {
		second := probe.StrongHits[1]
		if second.Score/top.Score >= recallSearchAmbiguityRatio {
			return RecallGateDecision{
				Strategy:         RecallStrategyEscalatedRecall,
				Trigger:          RecallTriggerAmbiguousSearch,
				SearchSufficient: false,
				Probe:            summary,
			}
		}
	}
	return RecallGateDecision{
		Strategy:         RecallStrategySearchSatisfied,
		Trigger:          RecallTriggerSearchSatisfied,
		SearchSufficient: true,
		Probe:            summary,
	}
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
