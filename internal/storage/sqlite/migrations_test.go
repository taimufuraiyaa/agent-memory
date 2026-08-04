package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestMigrationsAppliedExactlyOnce(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migrate-once.db")

	// Open once — all three migrations should be recorded.
	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	v1 := appliedVersions(t, s1)
	if len(v1) != 3 || !v1[1] || !v1[2] || !v1[3] {
		t.Fatalf("first open: expected versions {1,2,3}, got %v", v1)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Open again — applied set must be identical and must NOT grow.
	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	v2 := appliedVersions(t, s2)
	if !versionMapsEqual(v1, v2) {
		t.Fatalf("second open changed applied versions: before=%v after=%v", v1, v2)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestMigrationRetriesMissingStep(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migrate-retry.db")

	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Manually delete the v3 record to simulate a crash before version was recorded.
	if _, err := s1.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 3`); err != nil {
		t.Fatalf("delete v3: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen — v3 must be re-applied (idempotent) and re-recorded.
	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()
	v := appliedVersions(t, s2)
	if !v[3] {
		t.Fatalf("expected v3 re-applied, got %v", v)
	}
}

func TestMigrationDowngradeGuard(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migrate-downgrade.db")

	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Insert a version the binary does NOT know about.
	if _, err := s1.db.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (99, 'future', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert future: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = Open(ctx, dbPath)
	if err == nil {
		t.Fatal("expected downgrade guard error, got nil")
	}
	if err.Error() == "" || !contains(err.Error(), "newer") {
		t.Fatalf("downgrade error must mention newer version, got: %v", err)
	}
}

func TestMigrationJSONToBlobAdvisory(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migrate-advisory.db")

	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Seed a memory_vectors row with invalid JSON and no blob.
	seedMemory(t, s1, "adv-1", "project-a")
	if _, err := s1.db.ExecContext(ctx,
		`INSERT INTO memory_vectors (memory_id, workspace, embedding_json, embedding_provider, updated_at) VALUES ('adv-1', 'project-a', 'not-json', '', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed invalid vector: %v", err)
	}
	// Delete v2 record so the scan re-runs.
	if _, err := s1.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 2`); err != nil {
		t.Fatalf("delete v2: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if len(s2.migrationAdvisories) == 0 {
		t.Fatal("expected at least one migration advisory for invalid JSON")
	}
}

func TestM2SessionColumnAndIndexes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migrate-session.db")

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Write a memory whose source includes a session_id.
	m := &core.MemoryEntry{
		ID:          "mem-sess",
		Type:        core.EpisodicMemory,
		Content:     "session-aware content",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation, SessionID: "sess-abc"},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}
	if err := s.UpsertMemory(ctx, m); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The backfill should have extracted session_id from source_json.
	var col string
	if err := s.db.QueryRowContext(ctx, `SELECT session_id FROM memories WHERE id = ?`, m.ID).Scan(&col); err != nil {
		t.Fatalf("read session_id column: %v", err)
	}
	if col != "sess-abc" {
		t.Fatalf("expected session_id column 'sess-abc', got %q", col)
	}

	// GetSessionMemories must use the column.
	hits, err := s.GetSessionMemories(ctx, "ws", "sess-abc")
	if err != nil {
		t.Fatalf("get session memories: %v", err)
	}
	if len(hits) == 0 || hits[0].ID != m.ID {
		t.Fatalf("expected mem-sess in session results, got %v", hits)
	}

	// The composite indexes must exist (smoke: no error on creation, but they
	// were created IF NOT EXISTS — we can at least query index_info).
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_memories_workspace_session'`).Scan(&count); err != nil {
		t.Fatalf("check index: %v", err)
	}
	if count != 1 {
		t.Fatal("expected idx_memories_workspace_session index to exist")
	}
}

// helpers

func seedMemory(t *testing.T, store *Store, id, workspace string) {
	t.Helper()
	if err := store.UpsertMemory(context.Background(), &core.MemoryEntry{
		ID:          id,
		Type:        core.SemanticMemory,
		Content:     "seed " + id,
		Workspace:   workspace,
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func appliedVersions(t *testing.T, s *Store) map[int]bool {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		out[v] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func versionMapsEqual(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || containsCharSeq(s, sub))
}

func containsCharSeq(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
