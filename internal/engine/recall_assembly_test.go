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

func TestMemoryTextForRecallCorrectionFlags(t *testing.T) {
	// 1. Regular memory
	m1 := core.MemoryEntry{
		ID:      "m1",
		Type:    core.SemanticMemory,
		Content: "This is a fact.",
	}
	t1 := memoryTextForRecall(m1)
	if t1 != "This is a fact." {
		t.Errorf("expected clean content, got: %q", t1)
	}

	// 2. Superseded memory
	nextID := "m2"
	m2 := core.MemoryEntry{
		ID:           "m1",
		Type:         core.SemanticMemory,
		Content:      "This is a fact.",
		SupersededBy: &nextID,
	}
	t2 := memoryTextForRecall(m2)
	expectedT2 := "[OUT-OF-DATE: Superseded by memory m2]\nThis is a fact."
	if t2 != expectedT2 {
		t.Errorf("expected superseded prefix, got: %q", t2)
	}

	// 3. Successor (corrected) memory
	m3 := core.MemoryEntry{
		ID:      "m2",
		Type:    core.SemanticMemory,
		Content: "This is the corrected fact.",
		Relations: []core.Relation{
			{
				TargetID: "m1",
				Type:     core.RelSupersedes,
			},
		},
	}
	t3 := memoryTextForRecall(m3)
	expectedT3 := "[CORRECTED: Supersedes memory m1]\nThis is the corrected fact."
	if t3 != expectedT3 {
		t.Errorf("expected corrected prefix, got: %q", t3)
	}
}

