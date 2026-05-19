package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTokenMetricsAggregate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	store, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	if err := store.AddTokenMetric(ctx, "ws", "recall", 40, 100); err != nil {
		t.Fatalf("add metric #1: %v", err)
	}
	if err := store.AddTokenMetric(ctx, "ws", "recall", 20, 50); err != nil {
		t.Fatalf("add metric #2: %v", err)
	}
	out, err := store.AggregateTokenMetrics(ctx, "ws")
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if out.Records != 2 || out.ReturnedTokens != 60 || out.BaselineTokens != 150 || out.SavedTokens != 90 {
		t.Fatalf("unexpected aggregate result: %+v", out)
	}
}

func TestTokenMetricsAggregateByGroup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metrics-group.db")
	store, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	if err := store.AddTokenMetricV2(ctx, "ws", "recall", 10, 100, "on", true); err != nil {
		t.Fatalf("add metric on: %v", err)
	}
	if err := store.AddTokenMetricV2(ctx, "ws", "recall", 0, 0, "off", false); err != nil {
		t.Fatalf("add metric off: %v", err)
	}
	if err := store.AddTokenMetricV2(ctx, "ws", "search", 5, 5, "on", true); err != nil {
		t.Fatalf("add metric on #2: %v", err)
	}

	groups, err := store.AggregateTokenMetricsByGroup(ctx, "ws")
	if err != nil {
		t.Fatalf("aggregate by group: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %+v", groups)
	}
}
