package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLLMUsageAggregateByGroup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "llm-usage.db")
	store, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	if err := store.AddLLMUsageMetric(ctx, LLMUsageInsert{
		Workspace:        "ws",
		Provider:         "openai",
		Model:            "gpt-x",
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
		RunLabel:         "on",
		MemoryEnabled:    true,
	}); err != nil {
		t.Fatalf("add metric #1: %v", err)
	}
	if err := store.AddLLMUsageMetric(ctx, LLMUsageInsert{
		Workspace:        "ws",
		Provider:         "openai",
		Model:            "gpt-x",
		PromptTokens:     120,
		CompletionTokens: 30,
		TotalTokens:      150,
		RunLabel:         "off",
		MemoryEnabled:    false,
	}); err != nil {
		t.Fatalf("add metric #2: %v", err)
	}

	totals, err := store.AggregateLLMUsageTotals(ctx, "ws")
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	if totals.Records != 2 || totals.TotalTokens != 300 {
		t.Fatalf("unexpected totals: %+v", totals)
	}

	groups, err := store.AggregateLLMUsageByGroup(ctx, "ws")
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %+v", groups)
	}
}

