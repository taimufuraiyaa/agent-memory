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
