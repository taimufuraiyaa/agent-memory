package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestInsertObservationDedupConcurrent verifies that concurrent identical inserts
// through one store yield exactly one row: the unique (workspace, content_hash)
// index makes the dedup insert atomic, so there is no check-then-insert TOCTOU.
func TestInsertObservationDedupConcurrent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "dedup-concurrent.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	in := ObservationInsert{
		Workspace:     "ws",
		SessionID:     "s1",
		OccurredAt:    time.Now().UTC().Add(-time.Minute),
		Kind:          "tool_result",
		Summary:       "identical summary across goroutines",
		SourceAgent:   "codex",
		SchemaVersion: "v1",
	}

	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	inserted, duplicates, failures := 0, 0, 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, dedup, err := store.InsertObservationDedupWindow(ctx, in, time.Minute)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures++
				return
			}
			if dedup {
				duplicates++
			} else {
				inserted++
			}
		}()
	}
	wg.Wait()

	if failures != 0 {
		t.Fatalf("expected 0 insert failures, got %d", failures)
	}
	if inserted != 1 {
		t.Errorf("expected exactly 1 successful insert, got %d", inserted)
	}
	if duplicates != workers-1 {
		t.Errorf("expected %d duplicate detections, got %d", workers-1, duplicates)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE workspace = 'ws'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 observation row, got %d", count)
	}
}

// TestInsertObservationDedupDuplicateKeepsEarliest verifies that the duplicate
// path returns the existing observation with its original occurred_at instead of
// inserting a second row, even when the duplicate insert carries a later time.
func TestInsertObservationDedupDuplicateKeepsEarliest(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "dedup-earliest.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	base := ObservationInsert{
		Workspace:     "ws",
		SessionID:     "s1",
		Kind:          "tool_result",
		Summary:       "same summary",
		ContentHash:   "fixed-hash",
		SourceAgent:   "codex",
		SchemaVersion: "v1",
	}

	earliest := time.Now().UTC().Add(-2 * time.Hour)
	first := base
	first.OccurredAt = earliest
	got, dedup, err := store.InsertObservationDedupWindow(ctx, first, time.Minute)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if dedup {
		t.Fatal("first insert should not be a duplicate")
	}

	later := base
	later.OccurredAt = earliest.Add(2 * time.Hour)
	dup, dedup, err := store.InsertObservationDedupWindow(ctx, later, time.Minute)
	if err != nil {
		t.Fatalf("duplicate insert: %v", err)
	}
	if !dedup {
		t.Fatal("expected duplicate detection for identical content hash")
	}
	if dup.ID != got.ID {
		t.Errorf("duplicate path returned id %q, want original id %q", dup.ID, got.ID)
	}
	if !dup.OccurredAt.Equal(earliest) {
		t.Errorf("duplicate path returned occurred_at %v, want earliest %v", dup.OccurredAt, earliest)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE workspace = 'ws'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 observation row, got %d", count)
	}
}

// TestObservationLegacyDuplicateCollapse seeds duplicate rows via raw SQL (as a
// pre-unique-index database would contain) and verifies that the lazy migration
// collapses them to the earliest row and installs the unique index.
func TestObservationLegacyDuplicateCollapse(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "dedup-collapse.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Confirm the freshly migrated schema has the non-unique index, so this test
	// exercises the legacy → unique upgrade path.
	unique, err := store.observationDedupIndexIsUnique(ctx)
	if err != nil {
		t.Fatalf("index check: %v", err)
	}
	if unique {
		t.Fatal("expected non-unique dedup index before migration")
	}

	// Seed three legacy duplicates with the same (workspace, content_hash).
	base := time.Now().UTC().Add(-3 * time.Hour)
	seeded := []struct {
		id         string
		occurredAt time.Time
	}{
		{"dup-late", base.Add(2 * time.Hour)},
		{"dup-early", base},
		{"dup-mid", base.Add(time.Hour)},
	}
	for _, s := range seeded {
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO observations (id, workspace, session_id, occurred_at, kind, summary, content_hash, created_at)
VALUES (?, 'ws', 'legacy-session', ?, 'tool_result', 'legacy duplicate', 'legacy-hash', ?)
`, s.id, s.occurredAt.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("seed duplicate %s: %v", s.id, err)
		}
	}

	// First dedup insert triggers the lazy migration (collapse + unique index).
	_, _, err = store.InsertObservationDedupWindow(ctx, ObservationInsert{
		Workspace:     "ws",
		SessionID:     "s2",
		Kind:          "prompt",
		Summary:       "fresh observation",
		ContentHash:   "fresh-hash",
		SourceAgent:   "codex",
		SchemaVersion: "v1",
	}, time.Minute)
	if err != nil {
		t.Fatalf("trigger insert: %v", err)
	}

	unique, err = store.observationDedupIndexIsUnique(ctx)
	if err != nil {
		t.Fatalf("index check after migration: %v", err)
	}
	if !unique {
		t.Error("expected unique dedup index after migration")
	}

	// Only the earliest legacy duplicate may remain, and exactly one row.
	rows, err := store.db.QueryContext(ctx, `
SELECT id FROM observations WHERE workspace = 'ws' AND content_hash = 'legacy-hash' ORDER BY occurred_at ASC
`)
	if err != nil {
		t.Fatalf("query legacy rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var survivors []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		survivors = append(survivors, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(survivors) != 1 {
		t.Fatalf("expected 1 surviving legacy row, got %d (%v)", len(survivors), survivors)
	}
	if survivors[0] != "dup-early" {
		t.Errorf("expected earliest legacy row to survive, got %q", survivors[0])
	}

	var total int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE workspace = 'ws'`).Scan(&total); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 rows total (1 collapsed survivor + 1 fresh), got %d", total)
	}

	// Migration must be idempotent: a second insert must not re-collapse or fail.
	_, _, err = store.InsertObservationDedupWindow(ctx, ObservationInsert{
		Workspace:     "ws",
		SessionID:     "s2",
		Kind:          "prompt",
		Summary:       "another fresh observation",
		ContentHash:   "fresh-hash-2",
		SourceAgent:   "codex",
		SchemaVersion: "v1",
	}, time.Minute)
	if err != nil {
		t.Fatalf("second insert after migration: %v", err)
	}
}
