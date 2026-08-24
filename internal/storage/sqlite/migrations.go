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
	{5, "solution-path-episodes", migrateSolutionPathEpisodes},
	{6, "solution-working-state", migrateSolutionWorkingState},
	{7, "solution-transition-idempotency", migrateSolutionTransitionIdempotency},
	{8, "solution-reference-scope", migrateSolutionReferenceScope},
	{9, "solution-summaries", migrateSolutionSummaries},
	{10, "solution-promotions", migrateSolutionPromotions},
}

func migrateSolutionPromotions(ctx context.Context, s *Store) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS solution_promotions (
		id TEXT PRIMARY KEY, episode_id TEXT NOT NULL, summary_id TEXT NOT NULL, kind TEXT NOT NULL, memory_type TEXT NOT NULL,
		target_id TEXT NOT NULL DEFAULT '', source_step_ids_json TEXT NOT NULL, observation_ids_json TEXT NOT NULL,
		state TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', policy_identity TEXT NOT NULL, idempotency_key TEXT NOT NULL,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(summary_id, idempotency_key),
		FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE,
		FOREIGN KEY(summary_id) REFERENCES solution_summaries(id) ON DELETE CASCADE
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_solution_promotions_target ON solution_promotions(kind, target_id)`)
	return err
}

func migrateSolutionSummaries(ctx context.Context, s *Store) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS solution_summaries (
		id TEXT PRIMARY KEY, episode_id TEXT NOT NULL, version INTEGER NOT NULL, episode_version INTEGER NOT NULL,
		outcome TEXT NOT NULL, summary TEXT NOT NULL, decisive_step_ids_json TEXT NOT NULL, useful_failure_step_ids_json TEXT NOT NULL,
		evidence_json TEXT NOT NULL, risks_json TEXT NOT NULL, next_guidance TEXT NOT NULL, validation TEXT NOT NULL,
		superseded_by TEXT NOT NULL DEFAULT '', snapshot_hash TEXT NOT NULL, idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL,
		created_at TEXT NOT NULL, UNIQUE(episode_id, version), UNIQUE(episode_id, idempotency_key),
		FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_solution_summaries_episode_version ON solution_summaries(episode_id, version DESC)`)
	return err
}

func migrateSolutionReferenceScope(ctx context.Context, s *Store) error {
	columns := []struct{ name, ddl string }{
		{"workspace", `ALTER TABLE solution_step_references ADD COLUMN workspace TEXT NOT NULL DEFAULT ''`},
		{"session_id", `ALTER TABLE solution_step_references ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`},
		{"resolution_state", `ALTER TABLE solution_step_references ADD COLUMN resolution_state TEXT NOT NULL DEFAULT 'unverified'`},
	}
	for _, column := range columns {
		if err := s.ensureColumn(ctx, "solution_step_references", column.name, column.ddl); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_solution_step_references_scope ON solution_step_references(workspace, session_id, kind, target_id)`)
	return err
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

func migrateSolutionPathEpisodes(ctx context.Context, s *Store) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS solution_episodes (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			session_id TEXT NOT NULL,
			principal_id TEXT NOT NULL,
			client_id TEXT NOT NULL,
			goal_summary TEXT NOT NULL,
			status TEXT NOT NULL,
			capture_policy TEXT NOT NULL,
			retention_class TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			next_step_ordinal INTEGER NOT NULL DEFAULT 1,
			superseded_by TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(workspace, client_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_episodes_workspace_status_updated
			ON solution_episodes(workspace, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_episodes_workspace_session
			ON solution_episodes(workspace, session_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS solution_step_requests (
			episode_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			step_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(episode_id, idempotency_key),
			UNIQUE(step_id),
			FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS solution_steps (
			id TEXT PRIMARY KEY,
			episode_id TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			summary TEXT NOT NULL,
			rationale_summary TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL,
			parent_step_ids_json TEXT NOT NULL DEFAULT '[]',
			confidence REAL NOT NULL,
			sensitivity TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(episode_id, ordinal),
			FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE,
			FOREIGN KEY(id) REFERENCES solution_step_requests(step_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_steps_episode_ordinal
			ON solution_steps(episode_id, ordinal)`,
		`CREATE TABLE IF NOT EXISTS solution_step_references (
			step_id TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			kind TEXT NOT NULL,
			target_id TEXT NOT NULL,
			locator TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(step_id, ordinal),
			FOREIGN KEY(step_id) REFERENCES solution_steps(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_step_references_target
			ON solution_step_references(kind, target_id)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateSolutionWorkingState(ctx context.Context, s *Store) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS solution_working_state (
			episode_id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			session_id TEXT NOT NULL,
			principal_id TEXT NOT NULL,
			state_json TEXT NOT NULL,
			generation INTEGER NOT NULL,
			sensitivity TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_working_state_expiry ON solution_working_state(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_working_state_owner ON solution_working_state(workspace, principal_id, episode_id)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateSolutionTransitionIdempotency(ctx context.Context, s *Store) error {
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_solution_episode_one_active_session
			ON solution_episodes(workspace, session_id, client_id)
			WHERE status IN ('active', 'paused')`,
		`CREATE TABLE IF NOT EXISTS solution_transition_requests (
			episode_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			actor_principal_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			result_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			PRIMARY KEY(episode_id, actor_principal_id, idempotency_key),
			FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
