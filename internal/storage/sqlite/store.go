package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
)

// Store provides SQLite-backed persistence for memory entries.
type Store struct {
	db            *sql.DB
	turbovecIndex *TurbovecIndex
	useTurbovec   bool
}

// ErrDuplicateContent indicates idempotency duplicate detection hit.
var ErrDuplicateContent = errors.New("duplicate content hash")

// Open creates a SQLite store, applies pragmas, and runs migrations.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	// Add query parameters for WAL mode and busy timeout
	// These need to be in the connection string for modernc.org/sqlite
	connStr := dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)"

	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Connection pool configuration for concurrent access
	// WAL mode allows multiple readers + one writer concurrently
	db.SetMaxOpenConns(10)           // Allow up to 10 concurrent connections
	db.SetMaxIdleConns(5)            // Keep 5 warm connections in pool
	db.SetConnMaxLifetime(time.Hour) // Rotate connections every hour

	if err := ping(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := applyPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Load configuration to check if we should use turbovec
	if cfg, err := config.Load(""); err == nil && cfg != nil {
		if cfg.Storage.VectorBackend == "turbovec" {
			s.useTurbovec = true
			s.turbovecIndex = NewTurbovecIndex()
			_ = s.hydrateTurbovecIndex(ctx)
		}
	}

	return s, nil
}

func ping(ctx context.Context, db *sql.DB) error {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(c); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}
	return nil
}

func applyPragmas(ctx context.Context, db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 10000", // 10 seconds for concurrent operations
		"PRAGMA cache_size = -64000",  // 64MB cache
	}
	for _, stmt := range pragmas {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply pragma %q: %w", stmt, err)
		}
	}
	return nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// slowQueryThreshold is the latency above which we log a warning and record a metric.
const slowQueryThreshold = 100 * time.Millisecond

// logSlowQuery records Prometheus storage duration, warns on slow queries (>100ms), and updates connection/size metrics.
func (s *Store) logSlowQuery(ctx context.Context, operation, workspace string, d time.Duration) {
	metrics := observability.GetRegistry()
	metrics.StorageDuration.WithLabelValues(workspace, operation).Observe(d.Seconds())
	if d >= slowQueryThreshold {
		log.Printf("[agent-memory] slow query detected: operation=%s workspace=%s duration=%s", operation, workspace, d.Round(time.Millisecond))
	}

	if s != nil && s.db != nil {
		stats := s.db.Stats()
		metrics.DBConnections.Set(float64(stats.OpenConnections))

		var pageCount, pageSize int64
		if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err == nil {
			if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err == nil {
				metrics.DBSize.WithLabelValues(workspace).Set(float64(pageCount * pageSize))
			}
		}
	}
}

// Migrate applies schema changes idempotently.
func (s *Store) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			content TEXT NOT NULL,
			diagram_lang TEXT NOT NULL DEFAULT '',
			diagram_code TEXT NOT NULL DEFAULT '',
			workspace TEXT NOT NULL,
			content_hash TEXT NOT NULL DEFAULT '',
			source_json TEXT NOT NULL,
			entities_json TEXT NOT NULL,
			tags_json TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0,
			storage_tier TEXT NOT NULL DEFAULT 'vector',
			pinned INTEGER NOT NULL DEFAULT 0,
			superseded_by TEXT,
			access_count INTEGER NOT NULL DEFAULT 0,
			last_accessed TEXT NOT NULL DEFAULT '',
			decay_score REAL NOT NULL DEFAULT 0,
			salience_score REAL NOT NULL DEFAULT 0,
			suppression_score REAL NOT NULL DEFAULT 0,
			useful_count INTEGER NOT NULL DEFAULT 0,
			ignored_count INTEGER NOT NULL DEFAULT 0,
			rejected_count INTEGER NOT NULL DEFAULT 0,
			harmful_count INTEGER NOT NULL DEFAULT 0,
			last_helpful_at TEXT NOT NULL DEFAULT '',
			last_rejected_at TEXT NOT NULL DEFAULT '',
			suppression_until TEXT NOT NULL DEFAULT '',
			familiarity_band_last TEXT NOT NULL DEFAULT '',
			outcome_json TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_workspace ON memories(workspace)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_updated_at ON memories(updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_workspace_hash_unique ON memories(workspace, content_hash) WHERE content_hash != ''`,
		`CREATE TABLE IF NOT EXISTS notes (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			path TEXT NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			properties_json TEXT NOT NULL DEFAULT '{}',
			revision INTEGER NOT NULL DEFAULT 1,
			content_hash TEXT NOT NULL,
			index_state TEXT NOT NULL DEFAULT 'pending',
			indexed_revision INTEGER NOT NULL DEFAULT 0,
			index_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_notes_workspace_path_active
			ON notes(workspace, path COLLATE NOCASE) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_notes_workspace_updated
			ON notes(workspace, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS note_revisions (
			note_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			revision INTEGER NOT NULL,
			path TEXT NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			properties_json TEXT NOT NULL DEFAULT '{}',
			content_hash TEXT NOT NULL,
			author_kind TEXT NOT NULL DEFAULT 'human',
			created_at TEXT NOT NULL,
			PRIMARY KEY(note_id, revision),
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS note_links (
			source_note_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			revision INTEGER NOT NULL,
			target_note_id TEXT,
			raw_target TEXT NOT NULL,
			line INTEGER NOT NULL DEFAULT 1,
			snippet TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(source_note_id, revision, raw_target, line),
			FOREIGN KEY(source_note_id) REFERENCES notes(id) ON DELETE CASCADE,
			FOREIGN KEY(target_note_id) REFERENCES notes(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_note_links_target
			ON note_links(workspace, target_note_id)`,
		`CREATE TABLE IF NOT EXISTS note_memory_chunks (
			workspace TEXT NOT NULL,
			note_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			ordinal INTEGER NOT NULL,
			heading TEXT NOT NULL DEFAULT '',
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			content_hash TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(note_id, revision, ordinal),
			FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE,
			FOREIGN KEY(memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_note_memory_chunks_active
			ON note_memory_chunks(workspace, note_id, active)`,
		`CREATE TABLE IF NOT EXISTS memory_terms (
			workspace TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			normalized_term TEXT NOT NULL,
			display_term TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL,
			ordinal INTEGER NOT NULL DEFAULT 0,
			normalization_version TEXT NOT NULL,
			extractor_version TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(workspace, memory_id, normalized_term, normalization_version),
			FOREIGN KEY(memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_terms_workspace_term ON memory_terms(workspace, normalized_term)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_terms_workspace_memory ON memory_terms(workspace, memory_id)`,
		`CREATE TABLE IF NOT EXISTS term_index_state (
			workspace TEXT PRIMARY KEY,
			bitmap BLOB,
			state TEXT NOT NULL DEFAULT 'dirty',
			format_version TEXT NOT NULL DEFAULT '',
			normalization_version TEXT NOT NULL DEFAULT '',
			extractor_version TEXT NOT NULL DEFAULT '',
			hash_version TEXT NOT NULL DEFAULT '',
			bit_count INTEGER NOT NULL DEFAULT 0,
			hash_count INTEGER NOT NULL DEFAULT 0,
			distinct_item_count INTEGER NOT NULL DEFAULT 0,
			planned_capacity INTEGER NOT NULL DEFAULT 0,
			estimated_fpp REAL NOT NULL DEFAULT 0,
			stale_delete_count INTEGER NOT NULL DEFAULT 0,
			corpus_generation INTEGER NOT NULL DEFAULT 0,
			filter_generation INTEGER NOT NULL DEFAULT 0,
			checksum TEXT NOT NULL DEFAULT '',
			rebuild_reason TEXT NOT NULL DEFAULT '',
			built_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		)`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_terms_insert_state
		AFTER INSERT ON memory_terms
		BEGIN
			INSERT INTO term_index_state (workspace, state, corpus_generation, updated_at)
			VALUES (NEW.workspace, 'dirty', 1, NEW.created_at)
			ON CONFLICT(workspace) DO UPDATE SET
				state = 'dirty',
				corpus_generation = term_index_state.corpus_generation + 1,
				updated_at = excluded.updated_at;
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_memory_terms_delete_state
		AFTER DELETE ON memory_terms
		BEGIN
			INSERT INTO term_index_state (workspace, state, corpus_generation, updated_at)
			VALUES (OLD.workspace, 'dirty', 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
			ON CONFLICT(workspace) DO UPDATE SET
				state = 'dirty',
				stale_delete_count = term_index_state.stale_delete_count + 1,
				corpus_generation = term_index_state.corpus_generation + 1,
				updated_at = excluded.updated_at;
		END`,
		`CREATE TABLE IF NOT EXISTS memory_vectors (
			memory_id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			embedding_json TEXT NOT NULL,
			embedding_provider TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			FOREIGN KEY(memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_vectors_workspace ON memory_vectors(workspace)`,
		`CREATE TRIGGER IF NOT EXISTS trg_memories_delete_vectors
		AFTER DELETE ON memories
		BEGIN
			DELETE FROM memory_vectors WHERE memory_id = OLD.id;
		END`,
		`CREATE TABLE IF NOT EXISTS relations (
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			type TEXT NOT NULL,
			weight REAL NOT NULL DEFAULT 1,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			PRIMARY KEY(source_id, target_id, type),
			FOREIGN KEY(source_id) REFERENCES memories(id) ON DELETE CASCADE,
			FOREIGN KEY(target_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_relations_source ON relations(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_relations_target ON relations(target_id)`,
		`CREATE TABLE IF NOT EXISTS tier_transitions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id TEXT NOT NULL,
			from_tier TEXT NOT NULL,
			to_tier TEXT NOT NULL,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tier_transitions_memory ON tier_transitions(memory_id)`,
		`CREATE TABLE IF NOT EXISTS memory_tombstones (
			id TEXT PRIMARY KEY,
			memory_id TEXT NOT NULL,
			workspace TEXT NOT NULL,
			type TEXT NOT NULL,
			entity_hash TEXT NOT NULL DEFAULT '',
			fragment_summary TEXT NOT NULL DEFAULT '',
			eviction_reason TEXT NOT NULL,
			lineage_memory_id TEXT NOT NULL DEFAULT '',
			evicted_at TEXT NOT NULL,
			cooldown_until TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tombstones_workspace ON memory_tombstones(workspace)`,
		`CREATE INDEX IF NOT EXISTS idx_tombstones_entity_hash ON memory_tombstones(entity_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_tombstones_evicted_at ON memory_tombstones(evicted_at DESC)`,
		`CREATE TABLE IF NOT EXISTS reconstruction_lineage (
			reconstructed_id TEXT NOT NULL,
			tombstone_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(reconstructed_id, tombstone_id),
			FOREIGN KEY(reconstructed_id) REFERENCES memories(id) ON DELETE CASCADE,
			FOREIGN KEY(tombstone_id) REFERENCES memory_tombstones(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reconstruction_lineage_reconstructed ON reconstruction_lineage(reconstructed_id)`,
		`CREATE TABLE IF NOT EXISTS token_metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace TEXT NOT NULL,
			operation TEXT NOT NULL,
			returned_tokens INTEGER NOT NULL,
			baseline_tokens INTEGER NOT NULL,
			saved_tokens INTEGER NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_token_metrics_workspace ON token_metrics(workspace)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			workspace TEXT NOT NULL,
			session_id TEXT NOT NULL,
			project_root TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL DEFAULT '',
			ended_at TEXT NOT NULL DEFAULT '',
			observation_count INTEGER NOT NULL DEFAULT 0,
			last_seen_at TEXT NOT NULL,
			PRIMARY KEY(workspace, session_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_workspace_last_seen ON sessions(workspace, last_seen_at DESC)`,
		`CREATE TABLE IF NOT EXISTS observations (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			session_id TEXT NOT NULL,
			occurred_at TEXT NOT NULL,
			kind TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL,
			content_hash TEXT NOT NULL DEFAULT '',
			source_agent TEXT NOT NULL DEFAULT '',
			source_adapter TEXT NOT NULL DEFAULT '',
			hook_event TEXT NOT NULL DEFAULT '',
			external_event_id TEXT NOT NULL DEFAULT '',
			schema_version TEXT NOT NULL DEFAULT '',
			capture_mode TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_observations_workspace_occurred ON observations(workspace, occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_observations_workspace_session_occurred ON observations(workspace, session_id, occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_observations_dedup ON observations(workspace, content_hash, occurred_at DESC) WHERE content_hash != ''`,
		`CREATE TABLE IF NOT EXISTS llm_usage_metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace TEXT NOT NULL,
			provider TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			prompt_tokens INTEGER NOT NULL,
			completion_tokens INTEGER NOT NULL,
			total_tokens INTEGER NOT NULL,
			run_label TEXT NOT NULL DEFAULT '',
			memory_enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_usage_workspace_created ON llm_usage_metrics(workspace, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_usage_workspace_group ON llm_usage_metrics(workspace, run_label, memory_enabled)`,
		`CREATE TABLE IF NOT EXISTS benchmark_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace TEXT NOT NULL,
			run_id TEXT NOT NULL,
			seed_count INTEGER NOT NULL DEFAULT 0,
			case_count INTEGER NOT NULL DEFAULT 0,
			case_limit INTEGER NOT NULL DEFAULT 0,
			top_k INTEGER NOT NULL DEFAULT 0,
			budget INTEGER NOT NULL DEFAULT 0,
			seed_duration_ms INTEGER NOT NULL DEFAULT 0,
			on_duration_ms INTEGER NOT NULL DEFAULT 0,
			off_duration_ms INTEGER NOT NULL DEFAULT 0,
			precision REAL NOT NULL DEFAULT 0,
			recall REAL NOT NULL DEFAULT 0,
			gold_recall REAL NOT NULL DEFAULT 0,
			keyword_coverage REAL NOT NULL DEFAULT 0,
			ndcg REAL NOT NULL DEFAULT 0,
			f1 REAL NOT NULL DEFAULT 0,
			token_efficiency REAL NOT NULL DEFAULT 0,
			baseline_tokens INTEGER NOT NULL DEFAULT 0,
			returned_tokens INTEGER NOT NULL DEFAULT 0,
			saved_tokens INTEGER NOT NULL DEFAULT 0,
			cost_with_memory REAL NOT NULL DEFAULT 0,
			cost_without_memory REAL NOT NULL DEFAULT 0,
			cost_saved REAL NOT NULL DEFAULT 0,
			cost_saved_pct REAL NOT NULL DEFAULT 0,
			combined_score REAL NOT NULL DEFAULT 0,
			verdict TEXT NOT NULL DEFAULT '',
			off_cases INTEGER NOT NULL DEFAULT 0,
			off_disabled_count INTEGER NOT NULL DEFAULT 0,
			off_all_disabled INTEGER NOT NULL DEFAULT 0,
			off_returned_tokens INTEGER NOT NULL DEFAULT 0,
			off_baseline_tokens INTEGER NOT NULL DEFAULT 0,
			off_saved_tokens INTEGER NOT NULL DEFAULT 0,
			task_success_rate REAL NOT NULL DEFAULT 0,
			off_task_success_rate REAL NOT NULL DEFAULT 0,
			task_success_delta REAL NOT NULL DEFAULT 0,
			answer_fact_coverage REAL NOT NULL DEFAULT 0,
			off_answer_fact_coverage REAL NOT NULL DEFAULT 0,
			answer_fact_coverage_delta REAL NOT NULL DEFAULT 0,
			answer_completeness REAL NOT NULL DEFAULT 0,
			off_answer_completeness REAL NOT NULL DEFAULT 0,
			answer_completeness_delta REAL NOT NULL DEFAULT 0,
			avg_on_runtime_ms REAL NOT NULL DEFAULT 0,
			avg_off_runtime_ms REAL NOT NULL DEFAULT 0,
			runtime_delta_ms REAL NOT NULL DEFAULT 0,
			avg_on_investigation_effort REAL NOT NULL DEFAULT 0,
			avg_off_investigation_effort REAL NOT NULL DEFAULT 0,
			investigation_effort_delta REAL NOT NULL DEFAULT 0,
			continuation_score REAL NOT NULL DEFAULT 0,
			continuation_verdict TEXT NOT NULL DEFAULT '',
			generator_manifest_json TEXT NOT NULL DEFAULT '{}',
			run_manifest_json TEXT NOT NULL DEFAULT '{}',
			clusters_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			UNIQUE(workspace, run_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_benchmark_runs_workspace_created ON benchmark_runs(workspace, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS scheduler_workspace_state (
			workspace TEXT PRIMARY KEY,
			last_scheduled_at TEXT NOT NULL DEFAULT '',
			last_completed_at TEXT NOT NULL DEFAULT '',
			last_result TEXT NOT NULL DEFAULT '',
			last_skip_reason TEXT NOT NULL DEFAULT '',
			last_duration_ms INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS scheduler_run_history (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			started_at TEXT NOT NULL,
			completed_at TEXT NOT NULL DEFAULT '',
			trigger TEXT NOT NULL,
			result TEXT NOT NULL,
			skip_reason TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			decay_updated INTEGER NOT NULL DEFAULT 0,
			consolidated INTEGER NOT NULL DEFAULT 0,
			conflicts_found INTEGER NOT NULL DEFAULT 0,
			evicted INTEGER NOT NULL DEFAULT 0,
			promoted INTEGER NOT NULL DEFAULT 0,
			demoted INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_scheduler_run_history_workspace_started ON scheduler_run_history(workspace, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_scheduler_run_history_started ON scheduler_run_history(started_at DESC)`,
		`CREATE TABLE IF NOT EXISTS retrieval_requests (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			request_type TEXT NOT NULL,
			query TEXT NOT NULL,
			score INTEGER NOT NULL DEFAULT -1,
			reason TEXT NOT NULL DEFAULT '',
			useful_count INTEGER NOT NULL DEFAULT -1,
			total_count INTEGER NOT NULL DEFAULT -1,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_retrieval_requests_workspace_created ON retrieval_requests(workspace, created_at)`,
		`CREATE TABLE IF NOT EXISTS book_works (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			normalized_title TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_book_works_normalized_title ON book_works(normalized_title)`,
		`CREATE TABLE IF NOT EXISTS book_editions (
			id TEXT PRIMARY KEY,
			work_id TEXT NOT NULL,
			label TEXT NOT NULL,
			language TEXT NOT NULL,
			content_fingerprint TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(work_id) REFERENCES book_works(id) ON DELETE CASCADE,
			UNIQUE(work_id, content_fingerprint)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_book_editions_work ON book_editions(work_id)`,
		`CREATE TABLE IF NOT EXISTS source_assets (
			id TEXT PRIMARY KEY,
			edition_id TEXT NOT NULL,
			format TEXT NOT NULL,
			byte_fingerprint TEXT NOT NULL UNIQUE,
			normalized_fingerprint TEXT NOT NULL,
			parser_version TEXT NOT NULL,
			policy_json TEXT NOT NULL,
			imported_at TEXT NOT NULL,
			FOREIGN KEY(edition_id) REFERENCES book_editions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_source_assets_edition ON source_assets(edition_id)`,
		`CREATE TABLE IF NOT EXISTS structural_nodes (
			id TEXT PRIMARY KEY,
			edition_id TEXT NOT NULL,
			parent_id TEXT,
			kind TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			title TEXT NOT NULL,
			start_offset INTEGER NOT NULL DEFAULT 0,
			end_offset INTEGER NOT NULL DEFAULT 0,
			explicit INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(edition_id) REFERENCES book_editions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_structural_nodes_edition_order ON structural_nodes(edition_id, ordinal)`,
		`CREATE TABLE IF NOT EXISTS source_passages (
			id TEXT PRIMARY KEY,
			edition_id TEXT NOT NULL,
			source_asset_id TEXT NOT NULL,
			structural_node_id TEXT NOT NULL,
			text TEXT NOT NULL,
			locator_json TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			FOREIGN KEY(edition_id) REFERENCES book_editions(id) ON DELETE CASCADE,
			FOREIGN KEY(source_asset_id) REFERENCES source_assets(id) ON DELETE CASCADE,
			FOREIGN KEY(structural_node_id) REFERENCES structural_nodes(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_source_passages_edition ON source_passages(edition_id)`,
		`CREATE TABLE IF NOT EXISTS source_citations (
			id TEXT PRIMARY KEY,
			passage_id TEXT NOT NULL,
			citation_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(passage_id) REFERENCES source_passages(id) ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_source_citations_passage ON source_citations(passage_id)`,
		`CREATE TABLE IF NOT EXISTS libraries (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			owner_kind TEXT NOT NULL,
			organization_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS library_memberships (
			library_id TEXT NOT NULL,
			principal_id TEXT NOT NULL,
			capabilities_json TEXT NOT NULL,
			version TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY(library_id, principal_id),
			FOREIGN KEY(library_id) REFERENCES libraries(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS library_resources (
			library_id TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			policy_json TEXT NOT NULL,
			PRIMARY KEY(resource_type, resource_id),
			FOREIGN KEY(library_id) REFERENCES libraries(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_library_resources_library ON library_resources(library_id, resource_type)`,
		`CREATE TABLE IF NOT EXISTS book_annotations (
			id TEXT PRIMARY KEY,
			edition_id TEXT NOT NULL,
			citation_id TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			owner_kind TEXT NOT NULL,
			visibility TEXT NOT NULL,
			organization_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY(edition_id) REFERENCES book_editions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_book_annotations_edition ON book_annotations(edition_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS book_memory_proposals (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			status TEXT NOT NULL,
			proposal_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_book_memory_proposals_workspace ON book_memory_proposals(workspace, status)`,
		`CREATE TABLE IF NOT EXISTS book_memory_lineage (
			memory_id TEXT PRIMARY KEY,
			proposal_id TEXT NOT NULL,
			lineage_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(memory_id) REFERENCES memories(id) ON DELETE CASCADE,
			FOREIGN KEY(proposal_id) REFERENCES book_memory_proposals(id) ON DELETE RESTRICT
		)`,
		`CREATE TABLE IF NOT EXISTS study_sessions (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, session_json TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS study_turns (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL, turn_json TEXT NOT NULL, created_at TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES study_sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_study_turns_session ON study_turns(session_id,created_at)`,
		`CREATE TABLE IF NOT EXISTS reading_progress (
			principal_id TEXT NOT NULL, edition_id TEXT NOT NULL, progress_json TEXT NOT NULL, updated_at TEXT NOT NULL,
			PRIMARY KEY(principal_id,edition_id), FOREIGN KEY(edition_id) REFERENCES book_editions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS knowledge_edges (
			id TEXT PRIMARY KEY, from_id TEXT NOT NULL, to_id TEXT NOT NULL, edge_json TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_edges_from ON knowledge_edges(from_id,id)`,
		`CREATE TABLE IF NOT EXISTS knowledge_reviews (id TEXT PRIMARY KEY, review_json TEXT NOT NULL, state TEXT NOT NULL, version INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS knowledge_review_transitions (review_id TEXT NOT NULL, version INTEGER NOT NULL, transition_json TEXT NOT NULL, occurred_at TEXT NOT NULL, PRIMARY KEY(review_id,version), FOREIGN KEY(review_id) REFERENCES knowledge_reviews(id) ON DELETE RESTRICT)`,
		`CREATE TABLE IF NOT EXISTS book_reconsolidations (id TEXT PRIMARY KEY, previous_memory_id TEXT NOT NULL, new_memory_id TEXT NOT NULL, record_json TEXT NOT NULL, created_at TEXT NOT NULL, FOREIGN KEY(previous_memory_id) REFERENCES memories(id) ON DELETE RESTRICT, FOREIGN KEY(new_memory_id) REFERENCES memories(id) ON DELETE RESTRICT)`,
		`CREATE TABLE IF NOT EXISTS library_import_jobs (id TEXT PRIMARY KEY, workspace TEXT NOT NULL, state TEXT NOT NULL, job_json TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_library_import_jobs_workspace ON library_import_jobs(workspace,state)`,
		`CREATE TABLE IF NOT EXISTS source_deletions (source_asset_id TEXT PRIMARY KEY, mode TEXT NOT NULL, deleted_at TEXT NOT NULL, FOREIGN KEY(source_asset_id) REFERENCES source_assets(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			schema_version TEXT NOT NULL DEFAULT 'v1',
			workspace TEXT NOT NULL,
			operation TEXT NOT NULL,
			outcome TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL DEFAULT '',
			target_ids_json TEXT NOT NULL DEFAULT '[]',
			target_count INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			occurred_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_workspace_occurred ON audit_events(workspace, occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_workspace_operation ON audit_events(workspace, operation, occurred_at DESC)`,
		`CREATE TABLE IF NOT EXISTS memory_observation_provenance (
			memory_id TEXT NOT NULL,
			observation_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(memory_id, observation_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_observation_observation ON memory_observation_provenance(observation_id)`,
		`CREATE TABLE IF NOT EXISTS replay_import_checkpoints (
			workspace TEXT NOT NULL,
			source_path TEXT NOT NULL,
			line_number INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(workspace, source_path)
		)`,
		`CREATE TABLE IF NOT EXISTS connector_checkpoints (
			connector_id TEXT PRIMARY KEY,
			state_json TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			emitted_count INTEGER NOT NULL DEFAULT 0,
			coalesced_count INTEGER NOT NULL DEFAULT 0,
			rescanned_count INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	if err := s.ensureColumn(ctx, "scheduler_workspace_state", "last_impacts", `ALTER TABLE scheduler_workspace_state ADD COLUMN last_impacts INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	observationColumns := []struct{ name, sql string }{
		{"source_agent", `ALTER TABLE observations ADD COLUMN source_agent TEXT NOT NULL DEFAULT ''`},
		{"source_adapter", `ALTER TABLE observations ADD COLUMN source_adapter TEXT NOT NULL DEFAULT ''`},
		{"hook_event", `ALTER TABLE observations ADD COLUMN hook_event TEXT NOT NULL DEFAULT ''`},
		{"external_event_id", `ALTER TABLE observations ADD COLUMN external_event_id TEXT NOT NULL DEFAULT ''`},
		{"schema_version", `ALTER TABLE observations ADD COLUMN schema_version TEXT NOT NULL DEFAULT ''`},
		{"capture_mode", `ALTER TABLE observations ADD COLUMN capture_mode TEXT NOT NULL DEFAULT ''`},
	}
	for _, column := range observationColumns {
		if err := s.ensureColumn(ctx, "observations", column.name, column.sql); err != nil {
			return err
		}
	}
	hasContentHash, err := s.hasColumn(ctx, "memories", "content_hash")
	if err != nil {
		return fmt.Errorf("check content_hash column: %w", err)
	}
	if !hasContentHash {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE memories ADD COLUMN content_hash TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add content_hash column: %w", err)
		}
	}
	if err := s.ensureColumn(ctx, "memories", "access_count", `ALTER TABLE memories ADD COLUMN access_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "last_accessed", `ALTER TABLE memories ADD COLUMN last_accessed TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "decay_score", `ALTER TABLE memories ADD COLUMN decay_score REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "salience_score", `ALTER TABLE memories ADD COLUMN salience_score REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "suppression_score", `ALTER TABLE memories ADD COLUMN suppression_score REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "useful_count", `ALTER TABLE memories ADD COLUMN useful_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "ignored_count", `ALTER TABLE memories ADD COLUMN ignored_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "rejected_count", `ALTER TABLE memories ADD COLUMN rejected_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "harmful_count", `ALTER TABLE memories ADD COLUMN harmful_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "last_helpful_at", `ALTER TABLE memories ADD COLUMN last_helpful_at TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "last_rejected_at", `ALTER TABLE memories ADD COLUMN last_rejected_at TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "suppression_until", `ALTER TABLE memories ADD COLUMN suppression_until TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "familiarity_band_last", `ALTER TABLE memories ADD COLUMN familiarity_band_last TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "superseded_by", `ALTER TABLE memories ADD COLUMN superseded_by TEXT`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "pinned", `ALTER TABLE memories ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "diagram_lang", `ALTER TABLE memories ADD COLUMN diagram_lang TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memories", "diagram_code", `ALTER TABLE memories ADD COLUMN diagram_code TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memory_vectors", "embedding_provider", `ALTER TABLE memory_vectors ADD COLUMN embedding_provider TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memory_vectors", "embedding_model_version", `ALTER TABLE memory_vectors ADD COLUMN embedding_model_version TEXT NOT NULL DEFAULT 'unknown'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "memory_vectors", "embedding_blob", `ALTER TABLE memory_vectors ADD COLUMN embedding_blob BLOB`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "token_metrics", "run_label", `ALTER TABLE token_metrics ADD COLUMN run_label TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "token_metrics", "memory_enabled", `ALTER TABLE token_metrics ADD COLUMN memory_enabled INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "task_success_rate", `ALTER TABLE benchmark_runs ADD COLUMN task_success_rate REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "off_task_success_rate", `ALTER TABLE benchmark_runs ADD COLUMN off_task_success_rate REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "task_success_delta", `ALTER TABLE benchmark_runs ADD COLUMN task_success_delta REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "answer_fact_coverage", `ALTER TABLE benchmark_runs ADD COLUMN answer_fact_coverage REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "off_answer_fact_coverage", `ALTER TABLE benchmark_runs ADD COLUMN off_answer_fact_coverage REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "answer_fact_coverage_delta", `ALTER TABLE benchmark_runs ADD COLUMN answer_fact_coverage_delta REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "answer_completeness", `ALTER TABLE benchmark_runs ADD COLUMN answer_completeness REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "off_answer_completeness", `ALTER TABLE benchmark_runs ADD COLUMN off_answer_completeness REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "answer_completeness_delta", `ALTER TABLE benchmark_runs ADD COLUMN answer_completeness_delta REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "avg_on_runtime_ms", `ALTER TABLE benchmark_runs ADD COLUMN avg_on_runtime_ms REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "avg_off_runtime_ms", `ALTER TABLE benchmark_runs ADD COLUMN avg_off_runtime_ms REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "runtime_delta_ms", `ALTER TABLE benchmark_runs ADD COLUMN runtime_delta_ms REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "avg_on_investigation_effort", `ALTER TABLE benchmark_runs ADD COLUMN avg_on_investigation_effort REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "avg_off_investigation_effort", `ALTER TABLE benchmark_runs ADD COLUMN avg_off_investigation_effort REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "investigation_effort_delta", `ALTER TABLE benchmark_runs ADD COLUMN investigation_effort_delta REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "continuation_score", `ALTER TABLE benchmark_runs ADD COLUMN continuation_score REAL NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "benchmark_runs", "continuation_verdict", `ALTER TABLE benchmark_runs ADD COLUMN continuation_verdict TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "retrieval_requests", "reason", `ALTER TABLE retrieval_requests ADD COLUMN reason TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "retrieval_requests", "useful_count", `ALTER TABLE retrieval_requests ADD COLUMN useful_count INTEGER NOT NULL DEFAULT -1`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "retrieval_requests", "total_count", `ALTER TABLE retrieval_requests ADD COLUMN total_count INTEGER NOT NULL DEFAULT -1`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "term_index_state", "extractor_version", `ALTER TABLE term_index_state ADD COLUMN extractor_version TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "term_index_state", "stale_delete_count", `ALTER TABLE term_index_state ADD COLUMN stale_delete_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	// Recreate the delete trigger so databases created before stale-delete tracking
	// receive the same pressure accounting as new databases.
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS trg_memory_terms_delete_state`); err != nil {
		return fmt.Errorf("drop legacy memory term delete trigger: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER trg_memory_terms_delete_state
		AFTER DELETE ON memory_terms
		BEGIN
			INSERT INTO term_index_state (workspace, state, corpus_generation, stale_delete_count, updated_at)
			VALUES (OLD.workspace, 'dirty', 1, 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
			ON CONFLICT(workspace) DO UPDATE SET
				state = 'dirty',
				stale_delete_count = term_index_state.stale_delete_count + 1,
				corpus_generation = term_index_state.corpus_generation + 1,
				updated_at = excluded.updated_at;
		END`); err != nil {
		return fmt.Errorf("create memory term delete trigger: %w", err)
	}
	// Add indexes for vector provenance columns
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_vectors_provider ON memory_vectors(embedding_provider)`); err != nil {
		return fmt.Errorf("create embedding_provider index: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_memory_vectors_model_version ON memory_vectors(embedding_model_version)`); err != nil {
		return fmt.Errorf("create embedding_model_version index: %w", err)
	}

	// Migrate existing memory vectors from json to blob
	if err := s.migrateJSONVectorsToBlobs(ctx); err != nil {
		return fmt.Errorf("migrate JSON vectors to blobs: %w", err)
	}
	return nil
}

// UpsertMemory inserts or updates a memory entry by ID.
func (s *Store) UpsertMemory(ctx context.Context, m *core.MemoryEntry) error {
	if m.Keywords == nil {
		return s.upsertMemory(ctx, s.db, m, "")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.upsertMemory(ctx, tx, m, ""); err != nil {
		return err
	}
	if err := replaceMemoryTermsTx(ctx, tx, m.Workspace, m.ID, m.Keywords); err != nil {
		return err
	}
	return tx.Commit()
}

// InsertMemoryByHash inserts a memory while enforcing workspace+content_hash idempotency.
// Returns ErrDuplicateContent when the hash already exists in the same workspace.
func (s *Store) InsertMemoryByHash(ctx context.Context, m *core.MemoryEntry, contentHash string, termSets ...[]core.MemoryTerm) error {
	if strings.TrimSpace(contentHash) == "" {
		return errors.New("content hash is required")
	}
	if err := m.Validate(); err != nil {
		return err
	}

	sourceJSON, err := json.Marshal(m.Source)
	if err != nil {
		return fmt.Errorf("marshal source: %w", err)
	}
	entitiesJSON, err := json.Marshal(m.Entities)
	if err != nil {
		return fmt.Errorf("marshal entities: %w", err)
	}
	tagsJSON, err := json.Marshal(m.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}
	var outcomeJSON []byte
	if m.Outcome != nil {
		outcomeJSON, err = json.Marshal(m.Outcome)
		if err != nil {
			return fmt.Errorf("marshal outcome: %w", err)
		}
	}

	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	m.UpdatedAt = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	query := `
INSERT OR IGNORE INTO memories (id, type, content, diagram_lang, diagram_code, workspace, content_hash, source_json, entities_json, tags_json, confidence, storage_tier, pinned, superseded_by, access_count, last_accessed, decay_score, salience_score, suppression_score, useful_count, ignored_count, rejected_count, harmful_count, last_helpful_at, last_rejected_at, suppression_until, familiarity_band_last, outcome_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_startInsert := time.Now()
	res, err := tx.ExecContext(
		ctx,
		query,
		m.ID,
		string(m.Type),
		m.Content,
		nullDiagramLang(m),
		nullDiagramCode(m),
		m.Workspace,
		contentHash,
		string(sourceJSON),
		string(entitiesJSON),
		string(tagsJSON),
		m.Confidence,
		string(m.StorageTier),
		boolToInt(m.Pinned),
		nullIfEmptyString(m.SupersededBy),
		m.AccessCount,
		m.LastAccessedAt.Format(time.RFC3339Nano),
		m.DecayScore,
		m.SalienceScore,
		m.SuppressionScore,
		m.UsefulCount,
		m.IgnoredCount,
		m.RejectedCount,
		m.HarmfulCount,
		timeStringOrEmpty(m.LastHelpfulAt),
		timeStringOrEmpty(m.LastRejectedAt),
		nullTimeString(m.SuppressionUntil),
		strings.TrimSpace(m.FamiliarityBandLast),
		nullIfEmpty(outcomeJSON),
		m.CreatedAt.Format(time.RFC3339Nano),
		m.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return ErrDuplicateContent
	}
	if len(termSets) > 0 {
		if err := replaceMemoryTermsTx(ctx, tx, m.Workspace, m.ID, termSets[0]); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert memory: %w", err)
	}
	committed = true
	s.logSlowQuery(ctx, "insert_memory", m.Workspace, time.Since(_startInsert))
	return nil
}

// InsertMemoryByHashWithVector inserts a memory and its embedding atomically
// in a single transaction, enforcing workspace+content_hash idempotency the
// same way InsertMemoryByHash does. It returns ErrDuplicateContent (without
// touching memory_vectors) when the hash already exists in the workspace.
//
// Combining the memory insert and vector upsert into one transaction closes
// a window where a memory row could exist without its embedding -- e.g. if
// the process crashed between two separate statements -- which the write
// pipeline previously only guarded against with a manual compensating
// delete after the fact.
func (s *Store) InsertMemoryByHashWithVector(ctx context.Context, m *core.MemoryEntry, contentHash, embeddingProvider, embeddingModelVersion string, embedding []float32, termSets ...[]core.MemoryTerm) error {
	if strings.TrimSpace(contentHash) == "" {
		return errors.New("content hash is required")
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(embeddingProvider) == "" {
		return errors.New("embedding provider is required")
	}
	if strings.TrimSpace(embeddingModelVersion) == "" {
		embeddingModelVersion = "unknown"
	}

	sourceJSON, err := json.Marshal(m.Source)
	if err != nil {
		return fmt.Errorf("marshal source: %w", err)
	}
	entitiesJSON, err := json.Marshal(m.Entities)
	if err != nil {
		return fmt.Errorf("marshal entities: %w", err)
	}
	tagsJSON, err := json.Marshal(m.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}
	var outcomeJSON []byte
	if m.Outcome != nil {
		outcomeJSON, err = json.Marshal(m.Outcome)
		if err != nil {
			return fmt.Errorf("marshal outcome: %w", err)
		}
	}
	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("marshal embedding: %w", err)
	}

	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	m.UpdatedAt = time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	_startInsert := time.Now()
	res, err := tx.ExecContext(
		ctx,
		`
INSERT OR IGNORE INTO memories (id, type, content, diagram_lang, diagram_code, workspace, content_hash, source_json, entities_json, tags_json, confidence, storage_tier, pinned, superseded_by, access_count, last_accessed, decay_score, salience_score, suppression_score, useful_count, ignored_count, rejected_count, harmful_count, last_helpful_at, last_rejected_at, suppression_until, familiarity_band_last, outcome_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID,
		string(m.Type),
		m.Content,
		nullDiagramLang(m),
		nullDiagramCode(m),
		m.Workspace,
		contentHash,
		string(sourceJSON),
		string(entitiesJSON),
		string(tagsJSON),
		m.Confidence,
		string(m.StorageTier),
		boolToInt(m.Pinned),
		nullIfEmptyString(m.SupersededBy),
		m.AccessCount,
		m.LastAccessedAt.Format(time.RFC3339Nano),
		m.DecayScore,
		m.SalienceScore,
		m.SuppressionScore,
		m.UsefulCount,
		m.IgnoredCount,
		m.RejectedCount,
		m.HarmfulCount,
		timeStringOrEmpty(m.LastHelpfulAt),
		timeStringOrEmpty(m.LastRejectedAt),
		nullTimeString(m.SuppressionUntil),
		strings.TrimSpace(m.FamiliarityBandLast),
		nullIfEmpty(outcomeJSON),
		m.CreatedAt.Format(time.RFC3339Nano),
		m.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("insert memory rows: %w", err)
	}
	if affected == 0 {
		return ErrDuplicateContent
	}

	_startVec := time.Now()
	_, err = tx.ExecContext(ctx, `
INSERT INTO memory_vectors (memory_id, workspace, embedding_json, embedding_provider, embedding_model_version, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(memory_id) DO UPDATE SET
	workspace=excluded.workspace,
	embedding_json=excluded.embedding_json,
	embedding_provider=excluded.embedding_provider,
	embedding_model_version=excluded.embedding_model_version,
	updated_at=excluded.updated_at
`, m.ID, m.Workspace, string(embJSON), strings.TrimSpace(embeddingProvider), strings.TrimSpace(embeddingModelVersion), m.UpdatedAt.Format(time.RFC3339Nano))
	vecDuration := time.Since(_startVec)
	if err != nil {
		return fmt.Errorf("upsert memory vector: %w", err)
	}
	if len(termSets) > 0 {
		if err := replaceMemoryTermsTx(ctx, tx, m.Workspace, m.ID, termSets[0]); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert memory with vector: %w", err)
	}
	committed = true
	s.logSlowQuery(ctx, "insert_memory", m.Workspace, time.Since(_startInsert))
	s.logSlowQuery(ctx, "upsert_memory_vector", m.Workspace, vecDuration)
	return nil
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) upsertMemory(ctx context.Context, execer sqlExecer, m *core.MemoryEntry, contentHash string) error {
	if err := m.Validate(); err != nil {
		return err
	}
	sourceJSON, err := json.Marshal(m.Source)
	if err != nil {
		return fmt.Errorf("marshal source: %w", err)
	}
	entitiesJSON, err := json.Marshal(m.Entities)
	if err != nil {
		return fmt.Errorf("marshal entities: %w", err)
	}
	tagsJSON, err := json.Marshal(m.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}
	var outcomeJSON []byte
	if m.Outcome != nil {
		outcomeJSON, err = json.Marshal(m.Outcome)
		if err != nil {
			return fmt.Errorf("marshal outcome: %w", err)
		}
	}

	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	m.UpdatedAt = time.Now().UTC()

	query := `
INSERT INTO memories (id, type, content, diagram_lang, diagram_code, workspace, content_hash, source_json, entities_json, tags_json, confidence, storage_tier, pinned, superseded_by, access_count, last_accessed, decay_score, salience_score, suppression_score, useful_count, ignored_count, rejected_count, harmful_count, last_helpful_at, last_rejected_at, suppression_until, familiarity_band_last, outcome_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	type=excluded.type,
	content=excluded.content,
	diagram_lang=excluded.diagram_lang,
	diagram_code=excluded.diagram_code,
	workspace=excluded.workspace,
	content_hash=excluded.content_hash,
	source_json=excluded.source_json,
	entities_json=excluded.entities_json,
	tags_json=excluded.tags_json,
	confidence=excluded.confidence,
	storage_tier=excluded.storage_tier,
	pinned=excluded.pinned,
	superseded_by=excluded.superseded_by,
	access_count=excluded.access_count,
	last_accessed=excluded.last_accessed,
	decay_score=excluded.decay_score,
	salience_score=excluded.salience_score,
	suppression_score=excluded.suppression_score,
	useful_count=excluded.useful_count,
	ignored_count=excluded.ignored_count,
	rejected_count=excluded.rejected_count,
	harmful_count=excluded.harmful_count,
	last_helpful_at=excluded.last_helpful_at,
	last_rejected_at=excluded.last_rejected_at,
	suppression_until=excluded.suppression_until,
	familiarity_band_last=excluded.familiarity_band_last,
	outcome_json=excluded.outcome_json,
	updated_at=excluded.updated_at`

	_, err = execer.ExecContext(
		ctx,
		query,
		m.ID,
		string(m.Type),
		m.Content,
		nullDiagramLang(m),
		nullDiagramCode(m),
		m.Workspace,
		contentHash,
		string(sourceJSON),
		string(entitiesJSON),
		string(tagsJSON),
		m.Confidence,
		string(m.StorageTier),
		boolToInt(m.Pinned),
		nullIfEmptyString(m.SupersededBy),
		m.AccessCount,
		m.LastAccessedAt.Format(time.RFC3339Nano),
		m.DecayScore,
		m.SalienceScore,
		m.SuppressionScore,
		m.UsefulCount,
		m.IgnoredCount,
		m.RejectedCount,
		m.HarmfulCount,
		timeStringOrEmpty(m.LastHelpfulAt),
		timeStringOrEmpty(m.LastRejectedAt),
		nullTimeString(m.SuppressionUntil),
		strings.TrimSpace(m.FamiliarityBandLast),
		nullIfEmpty(outcomeJSON),
		m.CreatedAt.Format(time.RFC3339Nano),
		m.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert memory: %w", err)
	}
	return nil
}

// GetMemory loads one memory entry by ID.
func (s *Store) GetMemory(ctx context.Context, id string) (*core.MemoryEntry, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, type, content, diagram_lang, diagram_code, workspace, source_json, entities_json, tags_json, confidence, storage_tier, pinned, superseded_by, access_count, last_accessed, decay_score, salience_score, suppression_score, useful_count, ignored_count, rejected_count, harmful_count, last_helpful_at, last_rejected_at, suppression_until, familiarity_band_last, outcome_json, created_at, updated_at
FROM memories WHERE id = ?`, id)

	var m core.MemoryEntry
	var sourceJSON, entitiesJSON, tagsJSON string
	var outcomeJSON sql.NullString
	var diagramLang, diagramCode string
	var pinned int
	var supersededBy sql.NullString
	var createdAt, updatedAt, lastAccessed, lastHelpfulAt, lastRejectedAt, suppressionUntil string
	if err := row.Scan(
		&m.ID,
		&m.Type,
		&m.Content,
		&diagramLang,
		&diagramCode,
		&m.Workspace,
		&sourceJSON,
		&entitiesJSON,
		&tagsJSON,
		&m.Confidence,
		&m.StorageTier,
		&pinned,
		&supersededBy,
		&m.AccessCount,
		&lastAccessed,
		&m.DecayScore,
		&m.SalienceScore,
		&m.SuppressionScore,
		&m.UsefulCount,
		&m.IgnoredCount,
		&m.RejectedCount,
		&m.HarmfulCount,
		&lastHelpfulAt,
		&lastRejectedAt,
		&suppressionUntil,
		&m.FamiliarityBandLast,
		&outcomeJSON,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(sourceJSON), &m.Source); err != nil {
		return nil, fmt.Errorf("unmarshal source: %w", err)
	}
	if err := json.Unmarshal([]byte(entitiesJSON), &m.Entities); err != nil {
		return nil, fmt.Errorf("unmarshal entities: %w", err)
	}
	if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
		return nil, fmt.Errorf("unmarshal tags: %w", err)
	}
	if outcomeJSON.Valid && outcomeJSON.String != "" {
		var out core.Outcome
		if err := json.Unmarshal([]byte(outcomeJSON.String), &out); err != nil {
			return nil, fmt.Errorf("unmarshal outcome: %w", err)
		}
		m.Outcome = &out
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		m.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		m.UpdatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, lastAccessed); err == nil {
		m.LastAccessedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, lastHelpfulAt); err == nil {
		m.LastHelpfulAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, lastRejectedAt); err == nil {
		m.LastRejectedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, suppressionUntil); err == nil {
		m.SuppressionUntil = &t
	}
	if supersededBy.Valid && supersededBy.String != "" {
		m.SupersededBy = &supersededBy.String
	}
	m.Pinned = pinned == 1
	applyDiagram(&m, diagramLang, diagramCode)
	return &m, nil
}

// GetMemoryByHash returns memory for workspace+content hash.
func (s *Store) GetMemoryByHash(ctx context.Context, workspace, contentHash string) (*core.MemoryEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id FROM memories WHERE workspace = ? AND content_hash = ?`, workspace, contentHash)
	var id string
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	return s.GetMemory(ctx, id)
}

// ListMemoriesByWorkspace returns all memories in a workspace ordered by recency.
func (s *Store) ListMemoriesByWorkspace(ctx context.Context, workspace string) ([]core.MemoryEntry, error) {
	_startList := time.Now()
	rows, err := s.db.QueryContext(ctx, `
SELECT id, type, content, diagram_lang, diagram_code, workspace, source_json, entities_json, tags_json, confidence, storage_tier, pinned, superseded_by, access_count, last_accessed, decay_score, salience_score, suppression_score, useful_count, ignored_count, rejected_count, harmful_count, last_helpful_at, last_rejected_at, suppression_until, familiarity_band_last, outcome_json, created_at, updated_at
FROM memories
WHERE workspace = ?
ORDER BY updated_at DESC`, workspace)
	if err != nil {
		s.logSlowQuery(ctx, "list_memories_by_workspace", workspace, time.Since(_startList))
		return nil, err
	}
	defer func() {
		s.logSlowQuery(ctx, "list_memories_by_workspace", workspace, time.Since(_startList))
	}()
	defer func() { _ = rows.Close() }()

	out := make([]core.MemoryEntry, 0)
	for rows.Next() {
		var m core.MemoryEntry
		var sourceJSON, entitiesJSON, tagsJSON string
		var outcomeJSON sql.NullString
		var diagramLang, diagramCode string
		var pinned int
		var supersededBy sql.NullString
		var createdAt, updatedAt, lastAccessed, lastHelpfulAt, lastRejectedAt, suppressionUntil string
		if err := rows.Scan(
			&m.ID, &m.Type, &m.Content, &diagramLang, &diagramCode, &m.Workspace, &sourceJSON, &entitiesJSON, &tagsJSON,
			&m.Confidence, &m.StorageTier, &pinned, &supersededBy, &m.AccessCount, &lastAccessed, &m.DecayScore, &m.SalienceScore, &m.SuppressionScore, &m.UsefulCount, &m.IgnoredCount, &m.RejectedCount, &m.HarmfulCount, &lastHelpfulAt, &lastRejectedAt, &suppressionUntil, &m.FamiliarityBandLast, &outcomeJSON, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(sourceJSON), &m.Source); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entitiesJSON), &m.Entities); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
			return nil, err
		}
		if outcomeJSON.Valid && outcomeJSON.String != "" {
			var o core.Outcome
			if err := json.Unmarshal([]byte(outcomeJSON.String), &o); err != nil {
				return nil, err
			}
			m.Outcome = &o
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			m.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			m.UpdatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, lastAccessed); err == nil {
			m.LastAccessedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, lastHelpfulAt); err == nil {
			m.LastHelpfulAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, lastRejectedAt); err == nil {
			m.LastRejectedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, suppressionUntil); err == nil {
			m.SuppressionUntil = &t
		}
		if supersededBy.Valid && supersededBy.String != "" {
			m.SupersededBy = &supersededBy.String
		}
		applyDiagram(&m, diagramLang, diagramCode)
		m.Pinned = pinned == 1
		out = append(out, m)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}
	return s.PopulateSupersedesRelations(ctx, out)
}

func (s *Store) ListRecentMemoriesByWorkspace(ctx context.Context, workspace string, limit int) ([]core.MemoryEntry, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, type, content, diagram_lang, diagram_code, workspace, source_json, entities_json, tags_json, confidence, storage_tier, pinned, superseded_by, access_count, last_accessed, decay_score, salience_score, suppression_score, useful_count, ignored_count, rejected_count, harmful_count, last_helpful_at, last_rejected_at, suppression_until, familiarity_band_last, outcome_json, created_at, updated_at
FROM memories
WHERE workspace = ?
ORDER BY created_at DESC
LIMIT ?`, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]core.MemoryEntry, 0, limit)
	for rows.Next() {
		var m core.MemoryEntry
		var sourceJSON, entitiesJSON, tagsJSON string
		var outcomeJSON sql.NullString
		var diagramLang, diagramCode string
		var pinned int
		var supersededBy sql.NullString
		var createdAt, updatedAt, lastAccessed, lastHelpfulAt, lastRejectedAt, suppressionUntil string
		if err := rows.Scan(
			&m.ID, &m.Type, &m.Content, &diagramLang, &diagramCode, &m.Workspace, &sourceJSON, &entitiesJSON, &tagsJSON,
			&m.Confidence, &m.StorageTier, &pinned, &supersededBy, &m.AccessCount, &lastAccessed, &m.DecayScore, &m.SalienceScore, &m.SuppressionScore, &m.UsefulCount, &m.IgnoredCount, &m.RejectedCount, &m.HarmfulCount, &lastHelpfulAt, &lastRejectedAt, &suppressionUntil, &m.FamiliarityBandLast, &outcomeJSON, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(sourceJSON), &m.Source); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entitiesJSON), &m.Entities); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
			return nil, err
		}
		if outcomeJSON.Valid && outcomeJSON.String != "" {
			var o core.Outcome
			if err := json.Unmarshal([]byte(outcomeJSON.String), &o); err != nil {
				return nil, err
			}
			m.Outcome = &o
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			m.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			m.UpdatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, lastAccessed); err == nil {
			m.LastAccessedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, lastHelpfulAt); err == nil {
			m.LastHelpfulAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, lastRejectedAt); err == nil {
			m.LastRejectedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, suppressionUntil); err == nil {
			m.SuppressionUntil = &t
		}
		if supersededBy.Valid && supersededBy.String != "" {
			m.SupersededBy = &supersededBy.String
		}
		applyDiagram(&m, diagramLang, diagramCode)
		m.Pinned = pinned == 1
		out = append(out, m)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}
	return s.PopulateSupersedesRelations(ctx, out)
}

// CountMemories returns the number of persisted memories.
func (s *Store) CountMemories(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// PopulateSupersedesRelations finds outgoing 'supersedes' relations for the given memories
// and populates their Relations slice.
func (s *Store) PopulateSupersedesRelations(ctx context.Context, memories []core.MemoryEntry) ([]core.MemoryEntry, error) {
	if len(memories) == 0 {
		return memories, nil
	}
	ids := make([]string, len(memories))
	idToIdx := make(map[string][]int)
	for i, m := range memories {
		ids[i] = m.ID
		idToIdx[m.ID] = append(idToIdx[m.ID], i)
	}

	// Build IN placeholders
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT source_id, target_id, weight, metadata_json 
		FROM relations 
		WHERE type = 'supersedes' AND source_id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return memories, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var sourceID, targetID string
		var weight float64
		var metaJSON string
		if err := rows.Scan(&sourceID, &targetID, &weight, &metaJSON); err == nil {
			var metadata map[string]string
			if metaJSON != "" {
				_ = json.Unmarshal([]byte(metaJSON), &metadata)
			}
			rel := core.Relation{
				TargetID: targetID,
				Type:     core.RelSupersedes,
				Weight:   weight,
				Metadata: metadata,
			}
			for _, idx := range idToIdx[sourceID] {
				memories[idx].Relations = append(memories[idx].Relations, rel)
			}
		}
	}
	return memories, rows.Err()
}

// MarkAccessed increments access_count and updates last_accessed for provided IDs.
func (s *Store) MarkAccessed(ctx context.Context, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE memories SET access_count = access_count + 1, last_accessed = ? WHERE id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	now := at.Format(time.RFC3339Nano)
	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, now, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SetPinned updates the pin state for a single memory entry and returns the fresh row.
func (s *Store) SetPinned(ctx context.Context, id string, pinned bool) (*core.MemoryEntry, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("memory id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE memories SET pinned = ?, updated_at = ? WHERE id = ?`, boolToInt(pinned), now, id)
	if err != nil {
		return nil, fmt.Errorf("set pinned: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("set pinned rows: %w", err)
	}
	if affected == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetMemory(ctx, id)
}

// SetDecayScores applies decay scores in one transaction.
func (s *Store) SetDecayScores(ctx context.Context, byID map[string]float64) error {
	if len(byID) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE memories SET decay_score = ?, updated_at = ? WHERE id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for id, score := range byID {
		if _, err := stmt.ExecContext(ctx, score, now, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func nullDiagramLang(m *core.MemoryEntry) string {
	if m == nil || m.Diagram == nil {
		return ""
	}
	return strings.TrimSpace(m.Diagram.Lang)
}

func nullDiagramCode(m *core.MemoryEntry) string {
	if m == nil || m.Diagram == nil {
		return ""
	}
	return m.Diagram.Code
}

func applyDiagram(m *core.MemoryEntry, lang, code string) {
	if m == nil {
		return
	}
	lang = strings.TrimSpace(lang)
	if lang == "" && strings.TrimSpace(code) == "" {
		return
	}
	m.Diagram = &core.Diagram{Lang: lang, Code: code}
}

func nullIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func nullIfEmptyString(s *string) any {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return *s
}

func nullTimeString(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func timeStringOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) hasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// GetSessionMemories returns memories in a session ordered by created_at DESC.
func (s *Store) GetSessionMemories(ctx context.Context, workspace, sessionID string) ([]core.MemoryEntry, error) {
	_startList := time.Now()
	rows, err := s.db.QueryContext(ctx, `
SELECT id, type, content, diagram_lang, diagram_code, workspace, source_json, entities_json, tags_json, confidence, storage_tier, pinned, superseded_by, access_count, last_accessed, decay_score, salience_score, suppression_score, useful_count, ignored_count, rejected_count, harmful_count, last_helpful_at, last_rejected_at, suppression_until, familiarity_band_last, outcome_json, created_at, updated_at
FROM memories
WHERE workspace = ? AND json_extract(source_json, '$.session_id') = ?
ORDER BY created_at DESC`, workspace, sessionID)
	if err != nil {
		s.logSlowQuery(ctx, "get_session_memories", workspace, time.Since(_startList))
		return nil, err
	}
	defer func() {
		s.logSlowQuery(ctx, "get_session_memories", workspace, time.Since(_startList))
	}()
	defer func() { _ = rows.Close() }()

	out := make([]core.MemoryEntry, 0)
	for rows.Next() {
		var m core.MemoryEntry
		var sourceJSON, entitiesJSON, tagsJSON string
		var outcomeJSON sql.NullString
		var diagramLang, diagramCode string
		var pinned int
		var supersededBy sql.NullString
		var createdAt, updatedAt, lastAccessed, lastHelpfulAt, lastRejectedAt, suppressionUntil string
		if err := rows.Scan(
			&m.ID, &m.Type, &m.Content, &diagramLang, &diagramCode, &m.Workspace, &sourceJSON, &entitiesJSON, &tagsJSON,
			&m.Confidence, &m.StorageTier, &pinned, &supersededBy, &m.AccessCount, &lastAccessed, &m.DecayScore, &m.SalienceScore, &m.SuppressionScore, &m.UsefulCount, &m.IgnoredCount, &m.RejectedCount, &m.HarmfulCount, &lastHelpfulAt, &lastRejectedAt, &suppressionUntil, &m.FamiliarityBandLast, &outcomeJSON, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(sourceJSON), &m.Source); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entitiesJSON), &m.Entities); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
			return nil, err
		}
		if outcomeJSON.Valid && outcomeJSON.String != "" {
			var o core.Outcome
			if err := json.Unmarshal([]byte(outcomeJSON.String), &o); err != nil {
				return nil, err
			}
			m.Outcome = &o
		}
		if supersededBy.Valid {
			m.SupersededBy = &supersededBy.String
		}
		if diagramLang != "" || diagramCode != "" {
			m.Diagram = &core.Diagram{Lang: diagramLang, Code: diagramCode}
		}
		m.Pinned = pinned != 0
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			m.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			m.UpdatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, lastAccessed); err == nil {
			m.LastAccessedAt = t
		}
		if lastHelpfulAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, lastHelpfulAt); err == nil {
				m.LastHelpfulAt = t
			}
		}
		if lastRejectedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, lastRejectedAt); err == nil {
				m.LastRejectedAt = t
			}
		}
		if suppressionUntil != "" {
			if t, err := time.Parse(time.RFC3339Nano, suppressionUntil); err == nil {
				m.SuppressionUntil = &t
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMemoriesByIDs loads multiple memory entries by their IDs in a single query.
// Returns a map from memory ID to memory entry.
func (s *Store) GetMemoriesByIDs(ctx context.Context, ids []string) (map[string]core.MemoryEntry, error) {
	if len(ids) == 0 {
		return make(map[string]core.MemoryEntry), nil
	}

	// Build query with IN placeholders
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
SELECT id, type, content, diagram_lang, diagram_code, workspace, source_json, entities_json, tags_json, confidence, storage_tier, pinned, superseded_by, access_count, last_accessed, decay_score, salience_score, suppression_score, useful_count, ignored_count, rejected_count, harmful_count, last_helpful_at, last_rejected_at, suppression_until, familiarity_band_last, outcome_json, created_at, updated_at
FROM memories WHERE id IN (%s)`, strings.Join(placeholders, ","))

	_startGet := time.Now()
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		s.logSlowQuery(ctx, "get_memories_by_ids", "", time.Since(_startGet))
		return nil, err
	}
	defer func() {
		s.logSlowQuery(ctx, "get_memories_by_ids", "", time.Since(_startGet))
	}()
	defer func() { _ = rows.Close() }()

	out := make(map[string]core.MemoryEntry)
	for rows.Next() {
		var m core.MemoryEntry
		var sourceJSON, entitiesJSON, tagsJSON string
		var outcomeJSON sql.NullString
		var diagramLang, diagramCode string
		var pinned int
		var supersededBy sql.NullString
		var createdAt, updatedAt, lastAccessed, lastHelpfulAt, lastRejectedAt, suppressionUntil string
		if err := rows.Scan(
			&m.ID, &m.Type, &m.Content, &diagramLang, &diagramCode, &m.Workspace, &sourceJSON, &entitiesJSON, &tagsJSON,
			&m.Confidence, &m.StorageTier, &pinned, &supersededBy, &m.AccessCount, &lastAccessed, &m.DecayScore, &m.SalienceScore, &m.SuppressionScore, &m.UsefulCount, &m.IgnoredCount, &m.RejectedCount, &m.HarmfulCount, &lastHelpfulAt, &lastRejectedAt, &suppressionUntil, &m.FamiliarityBandLast, &outcomeJSON, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(sourceJSON), &m.Source); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entitiesJSON), &m.Entities); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &m.Tags); err != nil {
			return nil, err
		}
		if outcomeJSON.Valid && outcomeJSON.String != "" {
			var o core.Outcome
			if err := json.Unmarshal([]byte(outcomeJSON.String), &o); err != nil {
				return nil, err
			}
			m.Outcome = &o
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			m.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			m.UpdatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, lastAccessed); err == nil {
			m.LastAccessedAt = t
		}
		if lastHelpfulAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, lastHelpfulAt); err == nil {
				m.LastHelpfulAt = t
			}
		}
		if lastRejectedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, lastRejectedAt); err == nil {
				m.LastRejectedAt = t
			}
		}
		if suppressionUntil != "" {
			if t, err := time.Parse(time.RFC3339Nano, suppressionUntil); err == nil {
				m.SuppressionUntil = &t
			}
		}
		if supersededBy.Valid && supersededBy.String != "" {
			m.SupersededBy = &supersededBy.String
		}
		m.Pinned = pinned == 1
		applyDiagram(&m, diagramLang, diagramCode)
		out[m.ID] = m
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}
	if len(out) > 0 {
		var list []core.MemoryEntry
		for _, m := range out {
			list = append(list, m)
		}
		populated, err := s.PopulateSupersedesRelations(ctx, list)
		if err == nil {
			for _, m := range populated {
				out[m.ID] = m
			}
		}
	}
	return out, nil
}

// ListMemoryLightweightForInference returns only the necessary fields (ID, Entities, StorageTier, CreatedAt) for non-cold memories.
// This optimizes writes by preventing loading the full text content and parsing all JSON fields of all memories in the workspace.
func (s *Store) ListMemoryLightweightForInference(ctx context.Context, workspace string) ([]core.MemoryEntry, error) {
	_startList := time.Now()
	rows, err := s.db.QueryContext(ctx, `
SELECT id, entities_json, storage_tier, created_at
FROM memories
WHERE workspace = ? AND storage_tier != 'cold'`, workspace)
	if err != nil {
		s.logSlowQuery(ctx, "list_memory_lightweight_for_inference", workspace, time.Since(_startList))
		return nil, err
	}
	defer func() {
		s.logSlowQuery(ctx, "list_memory_lightweight_for_inference", workspace, time.Since(_startList))
	}()
	defer func() { _ = rows.Close() }()

	out := make([]core.MemoryEntry, 0)
	for rows.Next() {
		var m core.MemoryEntry
		var entitiesJSON string
		var createdAt string
		if err := rows.Scan(
			&m.ID,
			&entitiesJSON,
			&m.StorageTier,
			&createdAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entitiesJSON), &m.Entities); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			m.CreatedAt = t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListMemoryLightweightForInferenceRecent returns lightweight (ID, Entities,
// StorageTier, CreatedAt) records for the most recently created non-cold
// memories in a workspace, most-recent first, capped at limit.
//
// This exists for the write pipeline's entity co-occurrence inference
// (FR-SDO-12, see WritePipeline.inferRelationships), which runs
// synchronously on every Write(). ListMemoryLightweightForInference loads
// and JSON-decodes every non-cold memory's entity list on every write -- an
// O(N) cost per write that grows with the size of the workspace. Bounding
// the candidate set to the most recently created memories keeps per-write
// cost roughly constant while still covering the memories most likely to be
// contextually related to a brand-new entry.
func (s *Store) ListMemoryLightweightForInferenceRecent(ctx context.Context, workspace string, limit int) ([]core.MemoryEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	_startList := time.Now()
	rows, err := s.db.QueryContext(ctx, `
SELECT id, entities_json, storage_tier, created_at
FROM memories
WHERE workspace = ? AND storage_tier != 'cold'
ORDER BY created_at DESC
LIMIT ?`, workspace, limit)
	if err != nil {
		s.logSlowQuery(ctx, "list_memory_lightweight_for_inference_recent", workspace, time.Since(_startList))
		return nil, err
	}
	defer func() {
		s.logSlowQuery(ctx, "list_memory_lightweight_for_inference_recent", workspace, time.Since(_startList))
	}()
	defer func() { _ = rows.Close() }()

	out := make([]core.MemoryEntry, 0, limit)
	for rows.Next() {
		var m core.MemoryEntry
		var entitiesJSON string
		var createdAt string
		if err := rows.Scan(
			&m.ID,
			&entitiesJSON,
			&m.StorageTier,
			&createdAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(entitiesJSON), &m.Entities); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			m.CreatedAt = t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ensureColumn(ctx context.Context, table, column, alterSQL string) error {
	ok, err := s.hasColumn(ctx, table, column)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, alterSQL); err != nil {
		return err
	}
	return nil
}

func (s *Store) migrateJSONVectorsToBlobs(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT memory_id, embedding_json FROM memory_vectors WHERE embedding_blob IS NULL OR length(embedding_blob) = 0")
	if err != nil {
		return fmt.Errorf("query unmigrated memory vectors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type migrationItem struct {
		id   string
		json string
	}
	var items []migrationItem
	for rows.Next() {
		var item migrationItem
		if err := rows.Scan(&item.id, &item.json); err != nil {
			return fmt.Errorf("scan unmigrated memory vector: %w", err)
		}
		items = append(items, item)
	}
	_ = rows.Close()

	for _, item := range items {
		var emb []float32
		if err := json.Unmarshal([]byte(item.json), &emb); err != nil {
			// If JSON is invalid, skip instead of failing the entire migration
			continue
		}
		if len(emb) == 0 {
			continue
		}
		blob := encodeFloat32Slice(emb)
		_, err = s.db.ExecContext(ctx, "UPDATE memory_vectors SET embedding_blob = ? WHERE memory_id = ?", blob, item.id)
		if err != nil {
			return fmt.Errorf("migrate memory vector %s: %w", item.id, err)
		}
	}
	return nil
}

func (s *Store) hydrateTurbovecIndex(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT memory_id, embedding_blob, embedding_json FROM memory_vectors")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id string
		var blob []byte
		var jsonStr string
		if err := rows.Scan(&id, &blob, &jsonStr); err == nil {
			var vec []float32
			if len(blob) > 0 {
				vec, _ = decodeFloat32Slice(blob)
			} else if len(jsonStr) > 0 {
				_ = json.Unmarshal([]byte(jsonStr), &vec)
			}
			if len(vec) > 0 {
				_ = s.turbovecIndex.Upsert(id, vec)
			}
		}
	}
	return nil
}
