package engine

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestTokenClipperHardBudget(t *testing.T) {
	clipper := NewTokenClipper(WhitespaceCounter{})
	hits := []RetrievalHit{
		{Memory: core.MemoryEntry{ID: "a", Content: "one two three"}},     // 3
		{Memory: core.MemoryEntry{ID: "b", Content: "four five six"}},     // 3
		{Memory: core.MemoryEntry{ID: "c", Content: "seven eight nine"}},  // 3
	}
	included, meta := clipper.Clip(hits, 6)
	if len(included) != 2 {
		t.Fatalf("expected 2 included, got %d", len(included))
	}
	if meta.UsedTokens > meta.Budget {
		t.Fatalf("used tokens exceeded budget")
	}
	if meta.ClippedCount != 1 || meta.ClippedDetails[0].Reason != ReasonBudgetExceeded {
		t.Fatalf("expected budget-exceeded clip reason")
	}
}

func TestTokenClipperOversizeSingleEntry(t *testing.T) {
	clipper := NewTokenClipper(WhitespaceCounter{})
	hits := []RetrievalHit{
		{Memory: core.MemoryEntry{ID: "x", Content: "a b c d e f g h i j"}},
	}
	included, meta := clipper.Clip(hits, 3)
	if len(included) != 0 {
		t.Fatalf("expected no included entries")
	}
	if meta.ClippedCount != 1 || meta.ClippedDetails[0].Reason != ReasonItemTooLarge {
		t.Fatalf("expected item_too_large reason")
	}
}

