package parity

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCompareBlocksEveryCutoverInvariant(t *testing.T) {
	base := Observation{Backend: "local", Order: []string{"a", "b"}, NormalizedScores: map[string]float64{"a": 1, "b": .7}, ExactTop: "a", FeedbackPreferred: true, DecayDemoted: true, Suppressed: []string{"c"}, ResolvedCitations: map[string]string{"a": "citation-a"}}
	hosted := base
	hosted.Backend = "hosted"
	report, err := Compare("retrieval-parity-v1", "migration-v1", base, hosted, Thresholds{MinimumTopKOverlap: 1, MaximumScoreDelta: .1})
	if err != nil || !report.Passed {
		t.Fatalf("passing report=%+v err=%v", report, err)
	}
	hosted.Suppressed = nil
	report, err = Compare("retrieval-parity-v1", "migration-v1", base, hosted, Thresholds{MinimumTopKOverlap: 1, MaximumScoreDelta: .1})
	if err != nil || report.Passed || len(report.Differences) != 1 || report.Differences[0].Metric != "suppression" {
		t.Fatalf("blocking report=%+v err=%v", report, err)
	}
}

func TestVersionedParityReportRecordsApprovedGate(t *testing.T) {
	encoded, err := os.ReadFile("reports/retrieval-parity-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Version  string `json:"version"`
		Passed   bool   `json:"passed"`
		Observed struct {
			TopKOverlap        float64 `json:"top_k_overlap"`
			MaxScoreDelta      float64 `json:"maximum_normalized_score_delta"`
			UnresolvedCitation int     `json:"unresolved_citation_count"`
		} `json:"observed"`
		Explanations []Difference `json:"explanations"`
		Gate         string       `json:"gate"`
	}
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != "retrieval-parity-v1" || !report.Passed || report.Observed.TopKOverlap != 1 || report.Observed.MaxScoreDelta > .30 || report.Observed.UnresolvedCitation != 0 || len(report.Explanations) == 0 || report.Gate == "" {
		t.Fatalf("versioned parity gate is incomplete: %+v", report)
	}
}
