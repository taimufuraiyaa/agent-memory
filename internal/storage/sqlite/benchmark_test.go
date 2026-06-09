package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInsertAndListBenchmarkRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "benchmark.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	input := BenchmarkRun{
		Workspace:         "ws",
		RunID:             "run-001",
		SeedCount:         200,
		CaseCount:         10000,
		CaseLimit:         0,
		TopK:              20,
		Budget:            400,
		SeedDurationMs:    2500,
		OnDurationMs:      3400,
		OffDurationMs:     100,
		Precision:         0.61,
		Recall:            0.58,
		GoldRecall:        0.52,
		KeywordCoverage:   0.67,
		NDCG:              0.72,
		F1:                0.59,
		TokenEfficiency:   0.44,
		BaselineTokens:    9000,
		ReturnedTokens:    5000,
		SavedTokens:       4000,
		CostWithMemory:    0.15,
		CostWithoutMemory: 0.27,
		CostSaved:         0.12,
		CostSavedPct:      0.4444,
		CombinedScore:     0.63,
		Verdict:           "GOOD BENEFIT",
		OffCases:          10000,
		OffDisabledCount:  10000,
		OffAllDisabled:    true,
		OffReturnedTokens: 0,
		OffBaselineTokens: 0,
		OffSavedTokens:    0,
		TaskSuccessRate:   0.7,
		OffTaskSuccessRate: 0.2,
		TaskSuccessDelta:  0.5,
		AnswerFactCoverage: 0.8,
		OffAnswerFactCoverage: 0.25,
		AnswerFactCoverageDelta: 0.55,
		AnswerCompleteness: 0.65,
		OffAnswerCompleteness: 0.1,
		AnswerCompletenessDelta: 0.55,
		AvgOnRuntimeMs:    900,
		AvgOffRuntimeMs:   1500,
		RuntimeDeltaMs:    600,
		AvgOnInvestigationEffort: 3,
		AvgOffInvestigationEffort: 5,
		InvestigationEffortDelta: 2,
		ContinuationScore: 0.48,
		ContinuationVerdict: "GOOD BENEFIT",
		GeneratorManifest: map[string]any{"test_case_count": 10000},
		RunManifest:       map[string]any{"run_id": "run-001"},
		Clusters: []BenchmarkClusterSummary{
			{
				ClusterID:             "api_server",
				ClusterTitle:          "API Server",
				Cases:                 400,
				TaskSuccessRate:       0.75,
				TaskSuccessDelta:      0.5,
				AnswerFactCoverage:    0.8,
				AnswerCompleteness:    0.7,
				ContinuationScore:     0.52,
				ContinuationVerdict:   "GOOD BENEFIT",
				Precision:             0.7,
				CombinedScore:         0.68,
				TokenEfficiency:       0.4,
				Verdict:               "GOOD BENEFIT",
			},
		},
		CreatedAt: "2026-05-28T17:00:00Z",
	}

	stored, err := store.InsertBenchmarkRun(ctx, input)
	if err != nil {
		t.Fatalf("insert benchmark run: %v", err)
	}
	if stored.ID == 0 {
		t.Fatalf("expected inserted run ID")
	}
	if stored.RunID != input.RunID {
		t.Fatalf("expected run id %q, got %q", input.RunID, stored.RunID)
	}
	if !stored.OffAllDisabled {
		t.Fatalf("expected off_all_disabled to round-trip")
	}
	if len(stored.Clusters) != 1 || stored.Clusters[0].ClusterID != "api_server" {
		t.Fatalf("unexpected cluster payload: %+v", stored.Clusters)
	}
	if stored.ContinuationScore != input.ContinuationScore || stored.ContinuationVerdict != input.ContinuationVerdict {
		t.Fatalf("unexpected continuation payload: %+v", stored)
	}
	if stored.GeneratorManifest["test_case_count"] != float64(10000) {
		t.Fatalf("unexpected generator manifest: %+v", stored.GeneratorManifest)
	}

	listed, err := store.ListBenchmarkRuns(ctx, "ws", 5)
	if err != nil {
		t.Fatalf("list benchmark runs: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 benchmark run, got %d", len(listed))
	}
	if listed[0].CombinedScore != input.CombinedScore {
		t.Fatalf("expected combined score %f, got %f", input.CombinedScore, listed[0].CombinedScore)
	}
	if listed[0].ContinuationScore != input.ContinuationScore {
		t.Fatalf("expected continuation score %f, got %f", input.ContinuationScore, listed[0].ContinuationScore)
	}
}
