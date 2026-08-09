package sqlite

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// migrationStep is one ordered, recorded schema change.
type migrationStep struct {
	Version int
	Name    string
	Apply   func(context.Context, *Store) error
}

// schemaMigrations lists every schema change in version order. The baseline
// (version 1) is idempotent by construction; later steps are recorded in
// schema_migrations when they complete and never re-run.
var schemaMigrations = []migrationStep{
	{1, "baseline-schema", func(ctx context.Context, s *Store) error { return migrateBaselineSchema(ctx, s) }},
	{2, "json-vectors-to-blobs", func(ctx context.Context, s *Store) error { return s.migrateJSONVectorsToBlobs(ctx) }},
	{3, "session-column-and-order-indexes", migrateSessionColumnAndIndexes},
	{4, "source-attestation-provenance", migrateSourceAttestationProvenance},
}

var migrateMu sync.Mutex

func (s *Store) applyMigrations(ctx context.Context) error {
	migrateMu.Lock()
	defer migrateMu.Unlock()

	// Ensure the migrations table exists before we query it (the baseline
	// migration v1 also creates it, but we may need it before v1 runs).
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT '', applied_at TEXT NOT NULL)`); err != nil {
		return err
	}

	// Ensure the name column exists so we can record it (legacy DBs created
	// schema_migrations without this column).
	if err := s.ensureColumn(ctx, "schema_migrations", "name", `ALTER TABLE schema_migrations ADD COLUMN name TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}

	applied, err := s.listAppliedMigrationVersions(ctx)
	if err != nil {
		return err
	}

	latest := schemaMigrations[len(schemaMigrations)-1].Version
	for ver := range applied {
		if ver > latest {
			return fmt.Errorf("database schema version %d is newer than this binary supports (max %d); please upgrade agent-memory", ver, latest)
		}
	}

	for _, m := range schemaMigrations {
		if applied[m.Version] {
			continue
		}
		if err := m.Apply(ctx, s); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.Version, m.Name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}
	}
	return nil
}

func (s *Store) listAppliedMigrationVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	applied := make(map[int]bool)
	for rows.Next() {
		var ver int
		if err := rows.Scan(&ver); err != nil {
			return nil, err
		}
		applied[ver] = true
	}
	return applied, rows.Err()
}

func migrateSessionColumnAndIndexes(ctx context.Context, s *Store) error {
	// Add the real session_id column (core.MemoryEntry already has the db tag).
	if err := s.ensureColumn(ctx, "memories", "session_id", `ALTER TABLE memories ADD COLUMN session_id TEXT`); err != nil {
		return fmt.Errorf("add session_id column: %w", err)
	}

	// Backfill from source_json in chunks until all rows are covered.
	const chunkSize = 500
	for {
		result, err := s.db.ExecContext(ctx,
			`UPDATE memories SET session_id = json_extract(source_json, '$.session_id')
			 WHERE id IN (SELECT id FROM memories WHERE session_id IS NULL AND json_extract(source_json, '$.session_id') IS NOT NULL LIMIT ?)`,
			chunkSize)
		if err != nil {
			return fmt.Errorf("backfill session_id: %w", err)
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			break
		}
	}

	// Composite indexes for the hot ordering and lookup paths.
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_workspace_updated ON memories(workspace, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_workspace_created ON memories(workspace, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_workspace_session ON memories(workspace, session_id)`,
	}
	for _, ddl := range indexes {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

func migrateSourceAttestationProvenance(ctx context.Context, s *Store) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS source_attestations (
		source_asset_id TEXT PRIMARY KEY,
		subject_id TEXT NOT NULL,
		receipt_id TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		rights_basis TEXT NOT NULL,
		source_fingerprint TEXT NOT NULL,
		recorded_at TEXT NOT NULL,
		FOREIGN KEY(source_asset_id) REFERENCES source_assets(id) ON DELETE CASCADE
	)`)
	return err
}
