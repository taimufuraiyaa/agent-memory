package engine

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestRebalanceRecallHitsProceduralIntent(t *testing.T) {
	hits := []RetrievalHit{
		{Memory: core.MemoryEntry{ID: "s1", Type: core.SemanticMemory, Content: "Kafka topic naming and retention policy."}, Score: 0.9},
		{Memory: core.MemoryEntry{ID: "p1", Type: core.ProceduralMemory, Content: "Configure Kafka consumer retry with exponential backoff."}, Score: 0.8},
		{Memory: core.MemoryEntry{ID: "o1", Type: core.OutcomeMemory, Content: "Retry config previously failed due to timeout."}, Score: 0.85},
	}
	out := RebalanceRecallHits("How to configure kafka retry?", hits)
	if len(out) != len(hits) {
		t.Fatalf("expected same length, got %d", len(out))
	}
	if out[0].Memory.Type != core.ProceduralMemory {
		t.Fatalf("expected first hit procedural for procedural task, got %s", out[0].Memory.Type)
	}
}

func TestRebalanceRecallHitsKeepsAllIDs(t *testing.T) {
	hits := []RetrievalHit{
		{Memory: core.MemoryEntry{ID: "a", Type: core.SemanticMemory, Content: "A"}, Score: 0.7},
		{Memory: core.MemoryEntry{ID: "b", Type: core.ProceduralMemory, Content: "B"}, Score: 0.6},
		{Memory: core.MemoryEntry{ID: "c", Type: core.OutcomeMemory, Content: "C"}, Score: 0.5},
	}
	out := RebalanceRecallHits("general task", hits)
	seen := map[string]bool{}
	for _, h := range out {
		seen[h.Memory.ID] = true
	}
	for _, h := range hits {
		if !seen[h.Memory.ID] {
			t.Fatalf("missing hit id %s after rebalance", h.Memory.ID)
		}
	}
}
