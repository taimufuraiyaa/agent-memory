package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestAnalyzeReturnsInsufficientDataGradeWithoutEvidence(t *testing.T) {
	report := Analyze(Snapshot{Workspace: "empty"})

	if !report.Neutral || report.Grade != "U" || report.Score != 0 {
		t.Fatalf("expected U (insufficient data) report with score 0, got score=%d grade=%s neutral=%v",
			report.Score, report.Grade, report.Neutral)
	}
	if len(report.Dimensions) != 5 {
		t.Fatalf("expected five dimensions, got %d", len(report.Dimensions))
	}
	for _, dimension := range report.Dimensions {
		if dimension.Sufficient {
			t.Fatalf("expected %s to be insufficient", dimension.Key)
		}
		if dimension.Available {
			t.Fatalf("expected %s to be unavailable (no data)", dimension.Key)
		}
		if dimension.Reason == "" || !strings.Contains(dimension.Reason, "insufficient_evidence") {
			t.Fatalf("expected insufficient_evidence reason for %s, got %q", dimension.Key, dimension.Reason)
		}
	}
	assertRecommendationIDs(t, report, "feedback-insufficient", "recall-metrics-insufficient")
}

func TestThreeSampleWorkspaceCannotReachGradeA(t *testing.T) {
	// 3 quality-only feedback samples — below the quality evidence floor of 10.
	// No other dimension meets its floor, so all dimensions are insufficient.
	report := Analyze(Snapshot{
		Workspace: "quality-only",
		Requests: []core.RetrievalRequestLog{
			{Score: 5, UsefulCount: 2, TotalCount: 2},
			{Score: 4, UsefulCount: 2, TotalCount: 2},
			{Score: 5, UsefulCount: 1, TotalCount: 1},
		},
	})

	quality := dimensionByKey(t, report, DimensionQuality)
	if quality.Sufficient {
		t.Fatalf("expected quality insufficient (3 < 10 floor), got sufficient")
	}
	if !quality.Available {
		t.Fatalf("expected quality available (has data), got unavailable")
	}
	// With log scaling: log(3)/log(10) ≈ 0.48, so score ≈ 96 * 0.48 ≈ 46.
	if quality.Score < 40 || quality.Score > 55 {
		t.Fatalf("expected quality score ~46 with log down-weighting, got %d", quality.Score)
	}

	// All dimensions insufficient → grade U.
	if report.Grade != "U" {
		t.Fatalf("3-sample workspace must not reach grade A; expected U, got grade=%s score=%d", report.Grade, report.Score)
	}
	if !report.Neutral {
		t.Fatal("expected neutral flag true when all dimensions insufficient")
	}
}

func TestZeroFeedbackReturnsInsufficientDataGrade(t *testing.T) {
	// 4 memories: below trust floor (5), coverage floor (10), hygiene floor (100).
	// 0 feedback requests: quality and efficiency also insufficient.
	// All dimensions insufficient → grade "U".
	memories := make([]core.MemoryEntry, 4)
	for i := range memories {
		memories[i] = healthyMemory("some-content", i)
	}
	report := Analyze(Snapshot{
		Workspace: "no-feedback",
		Memories:  memories,
	})

	if report.Grade != "U" {
		t.Fatalf("expected U grade for 0 feedback, got grade=%s score=%d", report.Grade, report.Score)
	}
	if !report.Neutral {
		t.Fatal("expected neutral flag true for 0 feedback")
	}

	quality := dimensionByKey(t, report, DimensionQuality)
	if quality.Sufficient || quality.Available {
		t.Fatalf("expected quality insufficient and unavailable with 0 feedback, got available=%v sufficient=%v",
			quality.Available, quality.Sufficient)
	}
}

func TestAnalyzeBlendsAllSufficientDimensions(t *testing.T) {
	// Build a workspace where every dimension meets its evidence floor.
	// 120 total memories (hygiene needs 100), all healthy.
	memories := make([]core.MemoryEntry, 120)
	for i := range memories {
		memories[i] = healthyMemory("healthy", i)
	}

	// 10 scored requests (quality floor), all perfect.
	requests := make([]core.RetrievalRequestLog, 10)
	for i := range requests {
		requests[i] = core.RetrievalRequestLog{Score: 5, UsefulCount: 3, TotalCount: 3}
	}

	// 10 recall records (efficiency floor), 50% savings.
	report := Analyze(Snapshot{
		Workspace: "healthy",
		Memories:  memories,
		Requests:  requests,
		TokenMetricsByOperation: []sqlite.TokenMetricOperationTotals{{
			Operation: "recall",
			TokenMetricTotals: sqlite.TokenMetricTotals{
				Records:        10,
				ReturnedTokens: 500,
				BaselineTokens: 1000,
				SavedTokens:    500,
			},
		}},
	})

	if report.Neutral {
		t.Fatal("expected non-neutral report with sufficient evidence")
	}
	// All dimensions sufficient → completeness factor = 5/5 = 1.0.
	// quality: 100, efficiency: 50, hygiene: 100, coverage: 100, trust: 100
	// weighted avg: 100*0.30 + 50*0.25 + 100*0.20 + 100*0.15 + 100*0.10 = 30 + 12.5 + 20 + 15 + 10 = 87.5 → 88
	if report.Score != 88 || report.Grade != "B" {
		t.Fatalf("expected score 88 grade B, got score=%d grade=%s", report.Score, report.Grade)
	}
	if len(report.Recommendations) != 0 {
		t.Fatalf("expected no recommendations for healthy evidence, got %+v", report.Recommendations)
	}

	// Verify all dimensions are sufficient.
	for _, dim := range report.Dimensions {
		if !dim.Sufficient {
			t.Fatalf("expected %s sufficient, got insufficient: %s", dim.Key, dim.Reason)
		}
		if dim.Reason != "sufficient evidence" {
			t.Fatalf("expected 'sufficient evidence' reason for %s, got %q", dim.Key, dim.Reason)
		}
	}
}

func TestRecommendationsExposeRankedActionableProblems(t *testing.T) {
	memories := make([]core.MemoryEntry, 120)
	for i := range memories {
		memories[i] = healthyMemory("private-content-must-not-leak", i)
		if i >= 10 {
			memories[i].AccessCount = 0
		}
		if i < 30 {
			memories[i].DecayScore = 0.85
		}
		if i < 12 {
			memories[i].Confidence = 0.4
		}
		if i < 12 {
			memories[i].Source.Type = ""
		}
	}
	memories[0].RejectedCount = 2
	memories[1].HarmfulCount = 1

	requests := make([]core.RetrievalRequestLog, 10)
	for i := range requests {
		requests[i] = core.RetrievalRequestLog{Score: 2, UsefulCount: 1, TotalCount: 4}
	}

	report := Analyze(Snapshot{
		Workspace: "unhealthy",
		Memories:  memories,
		Requests:  requests,
		TokenMetricsByOperation: []sqlite.TokenMetricOperationTotals{{
			Operation: "recall",
			TokenMetricTotals: sqlite.TokenMetricTotals{
				Records:        10,
				ReturnedTokens: 900,
				BaselineTokens: 1000,
				SavedTokens:    100,
			},
		}},
	})

	// All 5 dimensions sufficient (120 memories >= 100 hygiene floor, 10 requests, 10 recall records).
	// quality: avg 2/5 * 100 = 40, useful ratio 0.25 -> blended 40*0.60 + 25*0.40 = 34
	// efficiency: 10% savings -> 10
	// hygiene: negative=2, stale=30 -> raw = 100 - 2/120*60 - 30/120*40 = 100 - 1 - 10 = 89
	// coverage: 10 reached / 120 active = 8.33 -> 8
	// trust: lowConf=12, missingSrc=12 -> 100 - 12/120*60 - 12/120*40 = 100 - 6 - 4 = 90
	// weighted avg: 34*0.30 + 10*0.25 + 89*0.20 + 8*0.15 + 90*0.10
	//   = 10.2 + 2.5 + 17.8 + 1.2 + 9.0 = 40.7
	// completeness = 5/5 = 1.0 -> composite = 41
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
		t.Fatalf("expected recall-only efficiency available with score 10, got available=%v score=%d",
			efficiency.Available, efficiency.Score)
	}
	if efficiency.Sufficient {
		t.Fatalf("expected efficiency insufficient (3 < 10 floor), got sufficient")
	}
	if !strings.Contains(efficiency.Reason, "insufficient_evidence") {
		t.Fatalf("expected insufficient_evidence reason, got %q", efficiency.Reason)
	}
	assertRecommendationIDs(t, report, "low-recall-savings", "feedback-insufficient")
}

func TestPerDimensionEvidenceFloorsEnforced(t *testing.T) {
	t.Run("quality floor", func(t *testing.T) {
		// 9 scored requests — just below the floor of 10.
		requests := make([]core.RetrievalRequestLog, 9)
		for i := range requests {
			requests[i] = core.RetrievalRequestLog{Score: 5, UsefulCount: 2, TotalCount: 2}
		}
		report := Analyze(Snapshot{Workspace: "quality-9", Requests: requests})
		quality := dimensionByKey(t, report, DimensionQuality)
		if quality.Sufficient {
			t.Fatal("expected quality insufficient with 9 scored requests")
		}
		if quality.EvidenceCount != 9 {
			t.Fatalf("expected evidence_count 9, got %d", quality.EvidenceCount)
		}
	})

	t.Run("trust floor", func(t *testing.T) {
		// 4 active memories — just below the floor of 5.
		memories := make([]core.MemoryEntry, 4)
		for i := range memories {
			memories[i] = healthyMemory("trust", i)
		}
		report := Analyze(Snapshot{Workspace: "trust-4", Memories: memories})
		trust := dimensionByKey(t, report, DimensionTrust)
		if trust.Sufficient {
			t.Fatal("expected trust insufficient with 4 active memories")
		}
		if trust.EvidenceCount != 4 {
			t.Fatalf("expected evidence_count 4, got %d", trust.EvidenceCount)
		}
	})

	t.Run("coverage floor", func(t *testing.T) {
		// 9 reached memories — below the floor of 10.
		memories := make([]core.MemoryEntry, 20)
		for i := range memories {
			memories[i] = healthyMemory("cov", i)
			if i >= 9 {
				memories[i].AccessCount = 0
			}
		}
		report := Analyze(Snapshot{Workspace: "coverage-9", Memories: memories})
		coverage := dimensionByKey(t, report, DimensionCoverage)
		if coverage.Sufficient {
			t.Fatal("expected coverage insufficient with 9 retrieved memories")
		}
		if coverage.EvidenceCount != 9 {
			t.Fatalf("expected evidence_count 9, got %d", coverage.EvidenceCount)
		}
	})

	t.Run("hygiene floor", func(t *testing.T) {
		// 99 total memories — below the floor of 100.
		memories := make([]core.MemoryEntry, 99)
		for i := range memories {
			memories[i] = healthyMemory("hyg", i)
		}
		report := Analyze(Snapshot{Workspace: "hygiene-99", Memories: memories})
		hygiene := dimensionByKey(t, report, DimensionHygiene)
		if hygiene.Sufficient {
			t.Fatal("expected hygiene insufficient with 99 total memories")
		}
		if hygiene.EvidenceCount != 99 {
			t.Fatalf("expected evidence_count 99, got %d", hygiene.EvidenceCount)
		}
	})

	t.Run("efficiency floor", func(t *testing.T) {
		// 9 recall records — below the floor of 10.
		report := Analyze(Snapshot{
			Workspace: "efficiency-9",
			TokenMetricsByOperation: []sqlite.TokenMetricOperationTotals{{
				Operation: "recall",
				TokenMetricTotals: sqlite.TokenMetricTotals{
					Records:        9,
					BaselineTokens: 1000,
					SavedTokens:    500,
				},
			}},
		})
		efficiency := dimensionByKey(t, report, DimensionEfficiency)
		if efficiency.Sufficient {
			t.Fatal("expected efficiency insufficient with 9 recall records")
		}
		if efficiency.EvidenceCount != 9 {
			t.Fatalf("expected evidence_count 9, got %d", efficiency.EvidenceCount)
		}
	})
}

func TestDeletionAboveBaselineDoesNotImproveHygiene(t *testing.T) {
	// Scenario A: 10 active memories, all clean (no negative, no stale).
	// Without anti-gaming, raw hygiene = 100. With cap (10/50), max = 20.
	memories := make([]core.MemoryEntry, 10)
	for i := range memories {
		memories[i] = healthyMemory("clean", i)
	}
	reportLow := Analyze(Snapshot{Workspace: "low-count", Memories: memories})
	hygieneLow := dimensionByKey(t, reportLow, DimensionHygiene)

	// Hygiene should be capped: raw 100 * (10/50) = 20.
	if hygieneLow.Score > 25 {
		t.Fatalf("expected hygiene capped near 20 for 10 memories, got %d", hygieneLow.Score)
	}

	// Scenario B: 60 active memories, 6 negative, 12 stale.
	// raw: 100 - (6/60)*60 - (12/60)*40 = 100 - 6 - 8 = 86. No cap (60 >= 50).
	memories2 := make([]core.MemoryEntry, 60)
	for i := range memories2 {
		memories2[i] = healthyMemory("some", i)
		if i < 6 {
			memories2[i].RejectedCount = 1
		}
		if i < 12 {
			memories2[i].DecayScore = 0.85
		}
	}
	reportHigh := Analyze(Snapshot{Workspace: "high-count", Memories: memories2})
	hygieneHigh := dimensionByKey(t, reportHigh, DimensionHygiene)

	// With issues, hygiene should be notably lower than 100 but not capped.
	if hygieneHigh.Score < 70 || hygieneHigh.Score > 95 {
		t.Fatalf("expected uncapped hygiene ~86 for 60 memories with issues, got %d", hygieneHigh.Score)
	}

	// Key assertion: low-count clean workspace must not outscore a larger workspace
	// just by having deleted memories.
	if hygieneLow.Score >= hygieneHigh.Score {
		t.Fatalf("anti-gaming failed: low-count clean hygiene %d >= high-count real hygiene %d",
			hygieneLow.Score, hygieneHigh.Score)
	}
}

func TestGradingOutputShape(t *testing.T) {
	memories := make([]core.MemoryEntry, 120)
	for i := range memories {
		memories[i] = healthyMemory("shape", i)
	}
	requests := make([]core.RetrievalRequestLog, 10)
	for i := range requests {
		requests[i] = core.RetrievalRequestLog{Score: 5, UsefulCount: 2, TotalCount: 2}
	}

	report := Analyze(Snapshot{
		Workspace: "shape-test",
		Memories:  memories,
		Requests:  requests,
		TokenMetricsByOperation: []sqlite.TokenMetricOperationTotals{{
			Operation: "recall",
			TokenMetricTotals: sqlite.TokenMetricTotals{
				Records:        10,
				BaselineTokens: 1000,
				SavedTokens:    500,
			},
		}},
	})

	// Verify top-level report shape.
	if report.Workspace != "shape-test" {
		t.Fatalf("expected workspace shape-test, got %s", report.Workspace)
	}
	if report.Evidence.MemoryCount != 120 {
		t.Fatalf("expected memory_count 120, got %d", report.Evidence.MemoryCount)
	}
	if report.Evidence.ActiveMemoryCount != 120 {
		t.Fatalf("expected active_memory_count 120, got %d", report.Evidence.ActiveMemoryCount)
	}
	if report.Evidence.ScoredRequestCount != 10 {
		t.Fatalf("expected scored_request_count 10, got %d", report.Evidence.ScoredRequestCount)
	}
	if report.Evidence.RecallMetricRecords != 10 {
		t.Fatalf("expected recall_metric_records 10, got %d", report.Evidence.RecallMetricRecords)
	}

	// Verify per-dimension metadata.
	for _, dim := range report.Dimensions {
		if dim.EvidenceCount <= 0 {
			t.Fatalf("expected positive evidence_count for %s, got %d", dim.Key, dim.EvidenceCount)
		}
		if !dim.Sufficient {
			t.Fatalf("expected %s sufficient with full evidence, got insufficient", dim.Key)
		}
		if dim.Reason == "" {
			t.Fatalf("expected non-empty reason for %s", dim.Key)
		}
		if dim.Score < 0 || dim.Score > 100 {
			t.Fatalf("expected score 0-100 for %s, got %d", dim.Key, dim.Score)
		}
		// Verify JSON-visible fields.
		if dim.Key == "" || dim.Label == "" {
			t.Fatalf("dimension %s missing key or label", dim.Key)
		}
		if dim.Weight <= 0 {
			t.Fatalf("expected positive weight for %s, got %f", dim.Key, dim.Weight)
		}
	}
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
