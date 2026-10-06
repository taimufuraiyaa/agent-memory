package retrieval

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphRerankDeduplicatesContentAndNeverBoostsConflict(t *testing.T) {
	candidates := []GraphHybridCandidate{
		graphCandidate("direct", "same fact", 0.8, true, "source-a"),
		{Memory: graphMemory("duplicate", "  SAME   FACT "), BaseScore: 0.4, PathScore: 1, Trust: core.GraphTrustApproved, EvidenceCount: 2, SourceKey: "source-a"},
		{Memory: graphMemory("conflict", "opposing fact"), BaseScore: 0.7, PathScore: 1, Trust: core.GraphTrustApproved, EvidenceCount: 3, Conflict: true, SourceKey: "source-b"},
	}
	result := RerankGraphCandidates(candidates, DefaultGraphRerankPolicy())
	if len(result) != 2 {
		t.Fatalf("deduplicated candidates = %#v", result)
	}
	if result[0].Memory.ID != "direct" {
		t.Fatalf("derived duplicate displaced original evidence: %#v", result)
	}
	for _, candidate := range result {
		if candidate.Memory.ID == "conflict" && candidate.AdjustedScore > 0.7 {
			t.Fatalf("conflict boosted support score: %#v", candidate)
		}
	}
}

func TestGraphRerankAppliesSourceDiversityBeforeRepeats(t *testing.T) {
	candidates := []GraphHybridCandidate{
		graphCandidate("a1", "one", 0.9, true, "a"),
		graphCandidate("a2", "two", 0.89, true, "a"),
		graphCandidate("b1", "three", 0.7, true, "b"),
	}
	result := RerankGraphCandidates(candidates, GraphRerankPolicy{MaxCandidates: 3, DiversityWindow: 2})
	if len(result) != 3 || result[0].SourceKey == result[1].SourceKey {
		t.Fatalf("source diversity was not applied: %#v", result)
	}
}

func graphMemory(id, content string) core.MemoryEntry {
	return core.MemoryEntry{ID: id, Content: content, Type: core.SemanticMemory}
}

func graphCandidate(id, content string, score float64, direct bool, source string) GraphHybridCandidate {
	return GraphHybridCandidate{Memory: graphMemory(id, content), BaseScore: score, Direct: direct, SourceKey: source}
}
