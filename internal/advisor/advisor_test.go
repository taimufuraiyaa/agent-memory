package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestAnalyzeReturnsNeutralReportWithoutEvidence(t *testing.T) {
	report := Analyze(Snapshot{Workspace: "empty"})

	if !report.Neutral || report.Grade != "N/A" || report.Score != 0 {
		t.Fatalf("expected neutral N/A report, got %+v", report)
	}
	if len(report.Dimensions) != 5 {
		t.Fatalf("expected five dimensions, got %d", len(report.Dimensions))
	}
	for _, dimension := range report.Dimensions {
		if dimension.Available {
			t.Fatalf("expected %s to be unavailable", dimension.Key)
		}
	}
	assertRecommendationIDs(t, report, "feedback-insufficient", "recall-metrics-insufficient")
}

func TestAnalyzeScoresAvailableEvidenceWithoutPenalizingMissingDimensions(t *testing.T) {
	report := Analyze(Snapshot{
		Workspace: "quality-only",
		Requests: []core.RetrievalRequestLog{
			{Score: 5, UsefulCount: 2, TotalCount: 2},
			{Score: 4, UsefulCount: 2, TotalCount: 2},
			{Score: 5, UsefulCount: 1, TotalCount: 1},
		},
	})

	if report.Neutral {
		t.Fatal("expected scored report")
	}
	quality := dimensionByKey(t, report, DimensionQuality)
	if !quality.Available || quality.Score != 96 {
		t.Fatalf("expected quality score 96, got %+v", quality)
	}
	if report.Score != 96 || report.Grade != "A" {
		t.Fatalf("missing dimensions must not reduce score, got score=%d grade=%s", report.Score, report.Grade)
	}
}

func TestAnalyzeBlendsAllHealthyDimensions(t *testing.T) {
	memories := make([]core.MemoryEntry, 10)
	for i := range memories {
		memories[i] = healthyMemory("healthy", i)
	}
	report := Analyze(Snapshot{
		Workspace: "healthy",
		Memories:  memories,
		Requests: []core.RetrievalRequestLog{
			{Score: 5, UsefulCount: 3, TotalCount: 3},
			{Score: 5, UsefulCount: 2, TotalCount: 2},
			{Score: 5, UsefulCount: 1, TotalCount: 1},
		},
		TokenMetricsByOperation: []sqlite.TokenMetricOperationTotals{{
			Operation: "recall",
			TokenMetricTotals: sqlite.TokenMetricTotals{
				Records:        3,
				ReturnedTokens: 500,
				BaselineTokens: 1000,
				SavedTokens:    500,
			},
		}},
	})

	if report.Score != 88 || report.Grade != "B" || report.Neutral {
		t.Fatalf("expected score 88 grade B, got %+v", report)
	}
	if len(report.Recommendations) != 0 {
		t.Fatalf("expected no recommendations for healthy evidence, got %+v", report.Recommendations)
	}
}

func TestRecommendationsExposeRankedActionableProblems(t *testing.T) {
	memories := make([]core.MemoryEntry, 20)
	for i := range memories {
		memories[i] = healthyMemory("private-content-must-not-leak", i)
		if i >= 5 {
			memories[i].AccessCount = 0
		}
		if i < 5 {
			memories[i].DecayScore = 0.85
		}
		if i < 4 {
			memories[i].Confidence = 0.4
		}
		if i < 3 {
			memories[i].Source.Type = ""
		}
	}
	memories[0].RejectedCount = 2
	memories[1].HarmfulCount = 1

	report := Analyze(Snapshot{
		Workspace: "unhealthy",
		Memories:  memories,
		Requests: []core.RetrievalRequestLog{
			{Score: 2, UsefulCount: 1, TotalCount: 4},
			{Score: 2, UsefulCount: 1, TotalCount: 4},
			{Score: 2, UsefulCount: 1, TotalCount: 4},
		},
		TokenMetricsByOperation: []sqlite.TokenMetricOperationTotals{{
			Operation: "recall",
			TokenMetricTotals: sqlite.TokenMetricTotals{
				Records:        5,
				ReturnedTokens: 900,
				BaselineTokens: 1000,
				SavedTokens:    100,
			},
		}},
	})

	if report.Score != 41 || report.Grade != "F" {
		t.Fatalf("expected unhealthy score 41 grade F, got score=%d grade=%s", report.Score, report.Grade)
	}
	assertRecommendationIDs(t, report,
		"memory-harmful-feedback",
		"low-feedback-quality",
		"low-recall-savings",
		"low-useful-ratio",
		"memory-rejected-feedback",
		"stale-memory-share",
		"low-confidence-memories",
		"low-retrieval-coverage",
		"missing-provenance",
	)
	if report.Recommendations[0].Severity != SeverityCritical {
		t.Fatalf("expected critical recommendation first, got %+v", report.Recommendations[0])
	}
	for _, recommendation := range report.Recommendations {
		if strings.Contains(recommendation.Title+recommendation.Detail+recommendation.Metric, "private-content-must-not-leak") {
			t.Fatalf("recommendation leaked memory content: %+v", recommendation)
		}
	}
}

func TestAnalyzeUsesRecallMetricsOnly(t *testing.T) {
	report := Analyze(Snapshot{
		Workspace: "operations",
		TokenMetricsByOperation: []sqlite.TokenMetricOperationTotals{
			{Operation: "search", TokenMetricTotals: sqlite.TokenMetricTotals{Records: 100, BaselineTokens: 1000, SavedTokens: 1000}},
			{Operation: "recall", TokenMetricTotals: sqlite.TokenMetricTotals{Records: 3, BaselineTokens: 1000, SavedTokens: 100}},
		},
	})

	efficiency := dimensionByKey(t, report, DimensionEfficiency)
	if !efficiency.Available || efficiency.Score != 10 {
		t.Fatalf("expected recall-only efficiency score 10, got %+v", efficiency)
	}
	assertRecommendationIDs(t, report, "low-recall-savings", "feedback-insufficient")
}

func healthyMemory(content string, index int) core.MemoryEntry {
	return core.MemoryEntry{
		ID:             content + string(rune('a'+index)),
		Type:           core.SemanticMemory,
		Content:        content,
		Workspace:      "ws",
		Source:         core.MemorySource{Type: core.SourceUserInput},
		Confidence:     0.9,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
		AccessCount:    1,
		DecayScore:     0.1,
		StorageTier:    core.TierVector,
		LastAccessedAt: time.Now().UTC(),
	}
}

func dimensionByKey(t *testing.T, report Report, key DimensionKey) Dimension {
	t.Helper()
	for _, dimension := range report.Dimensions {
		if dimension.Key == key {
			return dimension
		}
	}
	t.Fatalf("dimension %s not found", key)
	return Dimension{}
}

func assertRecommendationIDs(t *testing.T, report Report, want ...string) {
	t.Helper()
	got := make([]string, 0, len(report.Recommendations))
	for _, recommendation := range report.Recommendations {
		got = append(got, recommendation.ID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("recommendation IDs mismatch\nwant: %v\n got: %v", want, got)
	}
}
