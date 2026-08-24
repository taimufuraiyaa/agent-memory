package libraryevaluation

import (
	"testing"
)

func TestLibraryMetricsDefinitionsAndGates(t *testing.T) {
	if len(LibraryMetricDefinitions) != 8 {
		t.Fatal("metric definitions changed without versioned gate update")
	}
	passing := []LibrarySample{}
	for _, format := range []string{"markdown", "epub", "pdf", "web"} {
		passing = append(passing, LibrarySample{Format: format, Workflow: "direct", CitationCorrect: true, QuoteExact: true, EntailmentCorrect: true, AttributionCorrect: true, UnanswerableCorrect: true, LatencyMS: 100, Tokens: 500, RoleRevisions: 0})
	}
	report := EvaluateLibraryMetrics(passing)
	if len(report.Failures) != 0 || len(report.Diagnostics) != 4 {
		t.Fatalf("passing release corpus failed: %+v", report)
	}
	passing[0].QuoteExact = false
	failed := EvaluateLibraryMetrics(passing)
	if len(failed.Failures) == 0 {
		t.Fatal("quality regression did not fail release gate")
	}
}
