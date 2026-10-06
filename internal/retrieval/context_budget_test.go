package retrieval

import "testing"

func TestGraphContextClipKeepsHardBudgetAndReportsOmissions(t *testing.T) {
	candidates := []GraphHybridCandidate{
		graphCandidate("one", "one two three", 1, true, ""),
		graphCandidate("two", "four five six", 0.9, true, ""),
	}
	included, metadata := ClipGraphContext(candidates, 3)
	if len(included) != 1 || metadata.UsedTokens > 3 || metadata.ClippedCount != 1 {
		t.Fatalf("bounded graph context = included=%#v metadata=%#v", included, metadata)
	}
}
