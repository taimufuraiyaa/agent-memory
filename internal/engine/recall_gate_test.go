package engine

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestIsContinuationPrompt(t *testing.T) {
	cases := []struct {
		task string
		want bool
	}{
		{task: "continue", want: true},
		{task: "resume previous work on dashboard", want: true},
		{task: "what were we doing on the benchmark?", want: true},
		{task: "pick up where we left off", want: true},
		{task: "find the config file for redis", want: false},
	}
	for _, tc := range cases {
		if got := IsContinuationPrompt(tc.task); got != tc.want {
			t.Fatalf("IsContinuationPrompt(%q)=%v want %v", tc.task, got, tc.want)
		}
	}
}

func TestDecideRecallGate(t *testing.T) {
	strong := RetrievalHit{
		Memory: core.MemoryEntry{
			ID:         "m1",
			Type:       core.SemanticMemory,
			Confidence: 0.9,
		},
		Score: 0.48,
		Breakdown: SignalBreakdown{
			Semantic:       0.61,
			RelativeToBest: 1,
		},
	}
	secondStrong := RetrievalHit{
		Memory: core.MemoryEntry{
			ID:         "m2",
			Type:       core.SemanticMemory,
			Confidence: 0.9,
		},
		Score: 0.46,
		Breakdown: SignalBreakdown{
			Semantic:       0.58,
			RelativeToBest: 0.96,
		},
	}
	weakOnly := &RetrievalResult{
		Policy: RetrievalPolicySnapshot{MinSemanticScore: 0.3, MinTotalScore: 0.02},
		WeakHits: []RetrievalHit{
			{Memory: core.MemoryEntry{ID: "w1", Type: core.SemanticMemory, Confidence: 0.8}, Score: 0.12, Breakdown: SignalBreakdown{Semantic: 0.22}},
		},
	}
	cases := []struct {
		name    string
		task    string
		probe   *RetrievalResult
		strat   RecallStrategy
		trigger RecallTrigger
	}{
		{
			name:    "continuation bypasses search",
			task:    "continue where we left off",
			probe:   nil,
			strat:   RecallStrategyDirectRecall,
			trigger: RecallTriggerContinuationPrompt,
		},
		{
			name: "search satisfied",
			task: "find redis config path",
			probe: &RetrievalResult{
				Policy:     RetrievalPolicySnapshot{MinSemanticScore: 0.3, MinTotalScore: 0.02},
				StrongHits: []RetrievalHit{strong},
			},
			strat:   RecallStrategySearchSatisfied,
			trigger: RecallTriggerSearchSatisfied,
		},
		{
			name:    "weak only escalates",
			task:    "find redis config path",
			probe:   weakOnly,
			strat:   RecallStrategyEscalatedRecall,
			trigger: RecallTriggerWeakResults,
		},
		{
			name: "low confidence escalates",
			task: "find redis config path",
			probe: &RetrievalResult{
				Policy: RetrievalPolicySnapshot{MinSemanticScore: 0.3, MinTotalScore: 0.02},
				StrongHits: []RetrievalHit{
					{
						Memory: core.MemoryEntry{ID: "m3", Type: core.SemanticMemory, Confidence: 0.4},
						Score:  0.44,
						Breakdown: SignalBreakdown{
							Semantic:       0.62,
							RelativeToBest: 1,
						},
					},
				},
			},
			strat:   RecallStrategyEscalatedRecall,
			trigger: RecallTriggerLowConfidence,
		},
		{
			name: "ambiguous strong hits escalate",
			task: "find redis config path",
			probe: &RetrievalResult{
				Policy:     RetrievalPolicySnapshot{MinSemanticScore: 0.3, MinTotalScore: 0.02},
				StrongHits: []RetrievalHit{strong, secondStrong},
			},
			strat:   RecallStrategyEscalatedRecall,
			trigger: RecallTriggerAmbiguousSearch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideRecallGate(tc.task, tc.probe)
			if got.Strategy != tc.strat {
				t.Fatalf("strategy=%s want %s", got.Strategy, tc.strat)
			}
			if got.Trigger != tc.trigger {
				t.Fatalf("trigger=%s want %s", got.Trigger, tc.trigger)
			}
		})
	}
}
