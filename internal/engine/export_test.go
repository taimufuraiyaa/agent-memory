package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
)

func TestBuildMarkdownExportGroupsAndFormatsOutcome(t *testing.T) {
	memories := []core.MemoryEntry{
		{
			ID:        "1",
			Type:      core.ProceduralMemory,
			Content:   "Run migration before deploy",
			UpdatedAt: time.Now().UTC(),
		},
		{
			ID:      "2",
			Type:    core.OutcomeMemory,
			Content: "Migration rollout failed once",
			Outcome: &core.Outcome{
				Result: core.OutcomeFailure,
				Reason: "lock timeout",
			},
			UpdatedAt: time.Now().UTC(),
		},
	}
	out := BuildMarkdownExport("ws", memories)
	if !strings.Contains(out, "## Procedural") {
		t.Fatalf("expected procedural section")
	}
	if !strings.Contains(out, "## Outcome") {
		t.Fatalf("expected outcome section")
	}
	if !strings.Contains(out, "reason: lock timeout") {
		t.Fatalf("expected outcome reason formatting")
	}
}

func TestBuildCSVExport(t *testing.T) {
	now := time.Now().UTC()
	memories := []core.MemoryEntry{
		{
			ID:          "1",
			Type:        core.SemanticMemory,
			Content:     "API uses JWT tokens",
			Workspace:   "test-project",
			Confidence:  0.85,
			StorageTier: core.TierVector,
			Pinned:      false,
			AccessCount: 5,
			UsefulCount: 3,
			DecayScore:  0.12,
			CreatedAt:   now.Add(-24 * time.Hour),
			UpdatedAt:   now,
		},
		{
			ID:          "2",
			Type:        core.OutcomeMemory,
			Content:     "Migration attempt",
			Workspace:   "test-project",
			Confidence:  0.75,
			StorageTier: core.TierVectorGraph,
			Pinned:      true,
			AccessCount: 2,
			UsefulCount: 1,
			DecayScore:  0.05,
			Outcome: &core.Outcome{
				Result:   core.OutcomeFailure,
				Approach: "direct migration",
			},
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-1 * time.Hour),
		},
	}
	
	csv, err := BuildCSVExport("test-project", memories)
	if err != nil {
		t.Fatalf("BuildCSVExport failed: %v", err)
	}
	
	// Check header
	if !strings.Contains(csv, "id,type,content,workspace") {
		t.Errorf("CSV missing header")
	}
	
	// Check content
	if !strings.Contains(csv, "API uses JWT tokens") {
		t.Errorf("CSV missing memory content")
	}
	if !strings.Contains(csv, "semantic") {
		t.Errorf("CSV missing memory type")
	}
	if !strings.Contains(csv, "0.85") {
		t.Errorf("CSV missing confidence value")
	}
	if !strings.Contains(csv, "failure") {
		t.Errorf("CSV missing outcome result")
	}
	if !strings.Contains(csv, "direct migration") {
		t.Errorf("CSV missing outcome approach")
	}
	
	// Verify it's valid CSV with proper line count
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) != 3 { // header + 2 memories
		t.Errorf("Expected 3 lines (header + 2 rows), got %d", len(lines))
	}
}

func TestBuildCSVExportHandlesEmptyMemories(t *testing.T) {
	csv, err := BuildCSVExport("empty-workspace", []core.MemoryEntry{})
	if err != nil {
		t.Fatalf("BuildCSVExport failed on empty: %v", err)
	}
	
	// Should still have header
	if !strings.Contains(csv, "id,type,content") {
		t.Errorf("CSV missing header for empty export")
	}
	
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) != 1 { // just header
		t.Errorf("Expected 1 line (header only), got %d", len(lines))
	}
}
