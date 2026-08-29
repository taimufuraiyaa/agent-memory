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
	{11, "solution-tool-learning", migrateSolutionToolLearning},
	{12, "tool-lesson-promotions", migrateToolLessonPromotions},
	{13, "how-retrieval-feedback", migrateHowRetrievalFeedback},
	{14, "distilled-skill-metadata", migrateDistilledSkillMetadata},
	{15, "solution-activity-review", migrateSolutionActivityReview},
	{16, "graphrag-control-plane", migrateGraphControlPlane},
	{17, "graphrag-normalized-index", migrateGraphNormalizedIndex},
	{18, "graphrag-normalized-metadata", migrateGraphNormalizedMetadata},
	{19, "automatic-skill-revision-lifecycle", migrateAutomaticSkillRevisionLifecycle},
	{20, "skill-activation-operation-lease", migrateSkillActivationOperationLease},
	{21, "skill-approval-audit-events", migrateSkillApprovalAuditEvents},
}

func migrateSkillActivationOperationLease(ctx context.Context, s *Store) error {
	_, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_activation_operation_lease ON skill_activation_operations(workspace, environment, skill_id) WHERE state IN ('reserved', 'materializing')`)
	return err
}

func migrateSkillApprovalAuditEvents(ctx context.Context, s *Store) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS skill_approval_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT, workspace TEXT NOT NULL, approval_id TEXT NOT NULL,
		action TEXT NOT NULL, actor_id TEXT NOT NULL, reason TEXT NOT NULL, created_at TEXT NOT NULL,
		FOREIGN KEY(approval_id) REFERENCES skill_approvals(id) ON DELETE CASCADE
	)`)
	return err
}

func migrateAutomaticSkillRevisionLifecycle(ctx context.Context, s *Store) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS skills (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL,
			trigger_conditions_json TEXT NOT NULL DEFAULT '[]', capabilities_json TEXT NOT NULL DEFAULT '[]',
			risk_tier TEXT NOT NULL, owner_group TEXT NOT NULL, status TEXT NOT NULL, generation INTEGER NOT NULL,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(workspace, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skills_workspace_status ON skills(workspace, status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS skill_aliases (
			workspace TEXT NOT NULL, skill_id TEXT NOT NULL, alias TEXT NOT NULL,
			PRIMARY KEY(workspace, alias), FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS skill_candidates (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, kind TEXT NOT NULL, summary TEXT NOT NULL,
			expected_benefit TEXT NOT NULL, risks_json TEXT NOT NULL DEFAULT '[]', risk_tier TEXT NOT NULL,
			confidence REAL NOT NULL, state TEXT NOT NULL, target_skill_ids_json TEXT NOT NULL DEFAULT '[]',
			deduplication_hash TEXT NOT NULL, created_by TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			UNIQUE(workspace, deduplication_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skill_candidates_workspace_state ON skill_candidates(workspace, state, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS skill_candidate_sources (
			candidate_id TEXT NOT NULL, source_kind TEXT NOT NULL, source_id TEXT NOT NULL,
			PRIMARY KEY(candidate_id, source_kind, source_id),
			FOREIGN KEY(candidate_id) REFERENCES skill_candidates(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS skill_revisions (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, skill_id TEXT NOT NULL, revision_number INTEGER NOT NULL,
			state TEXT NOT NULL, bundle_digest TEXT NOT NULL, manifest_version INTEGER NOT NULL,
			compatibility_json TEXT NOT NULL DEFAULT '{}', risk_tier TEXT NOT NULL, candidate_id TEXT NOT NULL DEFAULT '',
			protected_sections_json TEXT NOT NULL DEFAULT '[]', provenance_json TEXT NOT NULL DEFAULT '{}',
			created_by TEXT NOT NULL, created_at TEXT NOT NULL,
			UNIQUE(workspace, skill_id, revision_number), UNIQUE(workspace, skill_id, bundle_digest),
			FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skill_revisions_state ON skill_revisions(workspace, skill_id, state, revision_number DESC)`,
		`CREATE TABLE IF NOT EXISTS skill_revision_parents (
			revision_id TEXT NOT NULL, parent_revision_id TEXT NOT NULL,
			PRIMARY KEY(revision_id, parent_revision_id),
			FOREIGN KEY(revision_id) REFERENCES skill_revisions(id) ON DELETE CASCADE,
			FOREIGN KEY(parent_revision_id) REFERENCES skill_revisions(id)
		)`,
		`CREATE TABLE IF NOT EXISTS skill_revision_files (
			revision_id TEXT NOT NULL, path TEXT NOT NULL, digest TEXT NOT NULL, size_bytes INTEGER NOT NULL,
			PRIMARY KEY(revision_id, path), FOREIGN KEY(revision_id) REFERENCES skill_revisions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS skill_evaluation_suites (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, skill_id TEXT NOT NULL, version INTEGER NOT NULL,
			digest TEXT NOT NULL, created_by TEXT NOT NULL, created_at TEXT NOT NULL,
			UNIQUE(workspace, skill_id, version), UNIQUE(workspace, skill_id, digest),
			FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS skill_evaluation_cases (
			suite_id TEXT NOT NULL, case_id TEXT NOT NULL, kind TEXT NOT NULL, summary TEXT NOT NULL,
			reference TEXT NOT NULL, required INTEGER NOT NULL,
			PRIMARY KEY(suite_id, case_id), FOREIGN KEY(suite_id) REFERENCES skill_evaluation_suites(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS skill_evaluation_runs (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, skill_id TEXT NOT NULL, revision_id TEXT NOT NULL,
			revision_digest TEXT NOT NULL, baseline_revision_id TEXT NOT NULL DEFAULT '', baseline_digest TEXT NOT NULL DEFAULT '',
			suite_id TEXT NOT NULL, suite_version INTEGER NOT NULL, suite_digest TEXT NOT NULL,
			evaluator TEXT NOT NULL, evaluator_version TEXT NOT NULL, environment_fingerprint TEXT NOT NULL,
			verdict TEXT NOT NULL, started_at TEXT NOT NULL, completed_at TEXT NOT NULL,
			FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			FOREIGN KEY(revision_id) REFERENCES skill_revisions(id),
			FOREIGN KEY(suite_id) REFERENCES skill_evaluation_suites(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skill_evaluation_runs_revision ON skill_evaluation_runs(workspace, revision_id, completed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS skill_evaluation_case_results (
			run_id TEXT NOT NULL, case_id TEXT NOT NULL, passed INTEGER NOT NULL, independently_verified INTEGER NOT NULL,
			failure_class TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(run_id, case_id), FOREIGN KEY(run_id) REFERENCES skill_evaluation_runs(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS skill_promotion_policies (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, version INTEGER NOT NULL, risk_tier TEXT NOT NULL,
			minimum_canary_samples INTEGER NOT NULL, minimum_verified_success_rate REAL NOT NULL,
			maximum_failure_rate REAL NOT NULL, allow_automatic_activation INTEGER NOT NULL,
			created_by TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(workspace, risk_tier, version)
		)`,
		`CREATE TABLE IF NOT EXISTS skill_policy_decisions (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, skill_id TEXT NOT NULL, revision_id TEXT NOT NULL,
			policy_id TEXT NOT NULL, policy_version INTEGER NOT NULL, evaluation_run_ids_json TEXT NOT NULL,
			risk_tier TEXT NOT NULL, decision TEXT NOT NULL, reason_codes_json TEXT NOT NULL, decided_at TEXT NOT NULL,
			FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			FOREIGN KEY(revision_id) REFERENCES skill_revisions(id),
			FOREIGN KEY(policy_id) REFERENCES skill_promotion_policies(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skill_policy_decisions_revision ON skill_policy_decisions(workspace, revision_id, decided_at DESC)`,
		`CREATE TABLE IF NOT EXISTS skill_approvals (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, revision_id TEXT NOT NULL, policy_decision_id TEXT NOT NULL,
			approver_id TEXT NOT NULL, approved INTEGER NOT NULL, reason TEXT NOT NULL, created_at TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT '', UNIQUE(policy_decision_id, approver_id),
			FOREIGN KEY(revision_id) REFERENCES skill_revisions(id),
			FOREIGN KEY(policy_decision_id) REFERENCES skill_policy_decisions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS skill_activations (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, environment TEXT NOT NULL, skill_id TEXT NOT NULL,
			active_revision_id TEXT NOT NULL, active_digest TEXT NOT NULL,
			last_known_good_revision_id TEXT NOT NULL DEFAULT '', last_known_good_digest TEXT NOT NULL DEFAULT '',
			canary_revision_id TEXT NOT NULL DEFAULT '', canary_digest TEXT NOT NULL DEFAULT '',
			generation INTEGER NOT NULL, policy_decision_id TEXT NOT NULL, materialization TEXT NOT NULL,
			activated_by TEXT NOT NULL, activated_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			UNIQUE(workspace, environment, skill_id), FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			FOREIGN KEY(active_revision_id) REFERENCES skill_revisions(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skill_activations_scope ON skill_activations(workspace, environment, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS skill_activation_operations (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, environment TEXT NOT NULL, skill_id TEXT NOT NULL,
			from_revision_id TEXT NOT NULL DEFAULT '', to_revision_id TEXT NOT NULL, expected_generation INTEGER NOT NULL,
			state TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', idempotency_key TEXT NOT NULL,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(workspace, environment, skill_id, idempotency_key),
			FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			FOREIGN KEY(to_revision_id) REFERENCES skill_revisions(id)
		)`,
		`CREATE TABLE IF NOT EXISTS skill_resolutions (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, environment TEXT NOT NULL, principal_id TEXT NOT NULL,
			task_id TEXT NOT NULL, skill_id TEXT NOT NULL, revision_id TEXT NOT NULL, revision_number INTEGER NOT NULL,
			digest TEXT NOT NULL, reason TEXT NOT NULL, policy_version INTEGER NOT NULL,
			fallback_revision_id TEXT NOT NULL DEFAULT '', fallback_digest TEXT NOT NULL DEFAULT '',
			acknowledgement_token_hash TEXT NOT NULL, expires_at TEXT NOT NULL, resolved_at TEXT NOT NULL,
			FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			FOREIGN KEY(revision_id) REFERENCES skill_revisions(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skill_resolutions_scope ON skill_resolutions(workspace, environment, skill_id, resolved_at DESC)`,
		`CREATE TABLE IF NOT EXISTS skill_executions (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, environment TEXT NOT NULL, episode_id TEXT NOT NULL,
			skill_id TEXT NOT NULL, revision_id TEXT NOT NULL, revision_digest TEXT NOT NULL, resolution_id TEXT NOT NULL,
			acknowledged INTEGER NOT NULL, acknowledged_at TEXT NOT NULL, outcome TEXT NOT NULL,
			independently_verified INTEGER NOT NULL, failure_class TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL, completed_at TEXT NOT NULL, duration_ms INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, tool_calls INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			FOREIGN KEY(revision_id) REFERENCES skill_revisions(id),
			FOREIGN KEY(resolution_id) REFERENCES skill_resolutions(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skill_executions_comparison ON skill_executions(workspace, environment, skill_id, revision_id, completed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS skill_rollback_events (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, environment TEXT NOT NULL, skill_id TEXT NOT NULL,
			from_revision_id TEXT NOT NULL, to_revision_id TEXT NOT NULL, reason_code TEXT NOT NULL,
			automatic INTEGER NOT NULL, operation_id TEXT NOT NULL, created_at TEXT NOT NULL,
			UNIQUE(workspace, operation_id), FOREIGN KEY(skill_id) REFERENCES skills(id) ON DELETE CASCADE,
			FOREIGN KEY(from_revision_id) REFERENCES skill_revisions(id), FOREIGN KEY(to_revision_id) REFERENCES skill_revisions(id)
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateGraphNormalizedMetadata(ctx context.Context, s *Store) error {
	statements := []string{
		`ALTER TABLE graph_edge_versions ADD COLUMN origin TEXT NOT NULL DEFAULT 'inferred'`,
		`ALTER TABLE graph_edge_versions ADD COLUMN provenance_approved INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE graph_communities ADD COLUMN configuration_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_communities ADD COLUMN membership_fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_communities ADD COLUMN evidence_fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_reports ADD COLUMN admission_state TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_reports ADD COLUMN model_route TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_reports ADD COLUMN model_fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_reports ADD COLUMN prompt_fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_reports ADD COLUMN membership_fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_reports ADD COLUMN evidence_fingerprint TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_reports ADD COLUMN review_version INTEGER NOT NULL DEFAULT 0`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateGraphNormalizedIndex(ctx context.Context, s *Store) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS graph_entities (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			trust TEXT NOT NULL, record_version INTEGER NOT NULL DEFAULT 1,
			first_revision_id TEXT NOT NULL, last_revision_id TEXT NOT NULL, superseded_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(workspace, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_entities_query ON graph_entities(tenant_id, workspace, trust, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS graph_entity_versions (
			entity_id TEXT NOT NULL, revision_id TEXT NOT NULL, external_id TEXT NOT NULL,
			name TEXT NOT NULL, entity_type TEXT NOT NULL, description TEXT NOT NULL,
			aliases_json TEXT NOT NULL DEFAULT '[]', occurrence_count INTEGER NOT NULL DEFAULT 0, degree INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(entity_id, revision_id), FOREIGN KEY(entity_id) REFERENCES graph_entities(id) ON DELETE CASCADE,
			FOREIGN KEY(revision_id) REFERENCES graph_revisions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS graph_entity_evidence (
			entity_id TEXT NOT NULL, revision_id TEXT NOT NULL, evidence_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL, canonical_kind TEXT NOT NULL,
			canonical_id TEXT NOT NULL, canonical_fingerprint TEXT NOT NULL, locator TEXT NOT NULL DEFAULT '',
			occurrence_count INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(entity_id, revision_id, evidence_id),
			FOREIGN KEY(entity_id, revision_id) REFERENCES graph_entity_versions(entity_id, revision_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_entity_evidence_canonical ON graph_entity_evidence(tenant_id, workspace, canonical_kind, canonical_id)`,
		`CREATE TABLE IF NOT EXISTS graph_edges (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			source_entity_id TEXT NOT NULL, target_entity_id TEXT NOT NULL,
			normalized_kind TEXT NOT NULL, external_kind TEXT NOT NULL DEFAULT '', trust TEXT NOT NULL,
			record_version INTEGER NOT NULL DEFAULT 1, first_revision_id TEXT NOT NULL, last_revision_id TEXT NOT NULL,
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(workspace, id),
			FOREIGN KEY(source_entity_id) REFERENCES graph_entities(id), FOREIGN KEY(target_entity_id) REFERENCES graph_entities(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_edges_query ON graph_edges(tenant_id, workspace, trust, source_entity_id, target_entity_id)`,
		`CREATE TABLE IF NOT EXISTS graph_edge_versions (
			edge_id TEXT NOT NULL, revision_id TEXT NOT NULL, external_id TEXT NOT NULL,
			description TEXT NOT NULL, weight REAL NOT NULL, PRIMARY KEY(edge_id, revision_id),
			FOREIGN KEY(edge_id) REFERENCES graph_edges(id) ON DELETE CASCADE,
			FOREIGN KEY(revision_id) REFERENCES graph_revisions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS graph_edge_evidence (
			edge_id TEXT NOT NULL, revision_id TEXT NOT NULL, evidence_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL, canonical_kind TEXT NOT NULL,
			canonical_id TEXT NOT NULL, canonical_fingerprint TEXT NOT NULL, locator TEXT NOT NULL DEFAULT '',
			occurrence_count INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(edge_id, revision_id, evidence_id),
			FOREIGN KEY(edge_id, revision_id) REFERENCES graph_edge_versions(edge_id, revision_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_edge_evidence_canonical ON graph_edge_evidence(tenant_id, workspace, canonical_kind, canonical_id)`,
		`CREATE TABLE IF NOT EXISTS graph_communities (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			revision_id TEXT NOT NULL, external_id TEXT NOT NULL, parent_id TEXT NOT NULL DEFAULT '', level INTEGER NOT NULL,
			entity_count INTEGER NOT NULL DEFAULT 0, edge_count INTEGER NOT NULL DEFAULT 0,
			source_count INTEGER NOT NULL DEFAULT 0, unresolved_count INTEGER NOT NULL DEFAULT 0,
			UNIQUE(workspace, id), FOREIGN KEY(revision_id) REFERENCES graph_revisions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS graph_community_members (
			community_id TEXT NOT NULL, revision_id TEXT NOT NULL, kind TEXT NOT NULL, target_id TEXT NOT NULL,
			PRIMARY KEY(community_id, revision_id, kind, target_id),
			FOREIGN KEY(community_id) REFERENCES graph_communities(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS graph_reports (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			community_id TEXT NOT NULL, revision_id TEXT NOT NULL, title TEXT NOT NULL, summary TEXT NOT NULL,
			findings_json TEXT NOT NULL DEFAULT '[]', rank REAL NOT NULL DEFAULT 0, trust TEXT NOT NULL,
			stale INTEGER NOT NULL DEFAULT 0, evidence_count INTEGER NOT NULL DEFAULT 0,
			unresolved_count INTEGER NOT NULL DEFAULT 0, UNIQUE(workspace, id),
			FOREIGN KEY(community_id) REFERENCES graph_communities(id) ON DELETE CASCADE,
			FOREIGN KEY(revision_id) REFERENCES graph_revisions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_reports_query ON graph_reports(tenant_id, workspace, stale, trust, rank DESC)`,
		`CREATE TABLE IF NOT EXISTS graph_reviews (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			action TEXT NOT NULL DEFAULT '', target_kind TEXT NOT NULL, target_id TEXT NOT NULL, from_state TEXT NOT NULL, to_state TEXT NOT NULL,
			expected_version INTEGER NOT NULL, reason TEXT NOT NULL DEFAULT '', reviewer_id TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_reviews_target ON graph_reviews(tenant_id, workspace, target_kind, target_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS graph_feedback (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			request_id TEXT NOT NULL, target_kind TEXT NOT NULL, target_id TEXT NOT NULL,
			outcome TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_feedback_target ON graph_feedback(tenant_id, workspace, target_kind, target_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS graph_deletion_tombstones (
			tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL, canonical_kind TEXT NOT NULL,
			canonical_id TEXT NOT NULL, deleted_at TEXT NOT NULL,
			PRIMARY KEY(tenant_id, workspace, canonical_kind, canonical_id)
		)`,
		`CREATE TABLE IF NOT EXISTS graph_repair_queue (
			tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL, canonical_kind TEXT NOT NULL,
			canonical_id TEXT NOT NULL, affected_entities INTEGER NOT NULL DEFAULT 0,
			affected_edges INTEGER NOT NULL DEFAULT 0, affected_reports INTEGER NOT NULL DEFAULT 0,
			deadline_at TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'queued',
			PRIMARY KEY(tenant_id, workspace, canonical_kind, canonical_id)
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateGraphControlPlane(ctx context.Context, s *Store) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS graph_configurations (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			version INTEGER NOT NULL, enabled INTEGER NOT NULL DEFAULT 0,
			adapter_name TEXT NOT NULL, adapter_version TEXT NOT NULL, index_method TEXT NOT NULL,
			projection_version TEXT NOT NULL, artifact_schema_version TEXT NOT NULL,
			prompt_fingerprint TEXT NOT NULL, model_route TEXT NOT NULL,
			active_revision_id TEXT NOT NULL DEFAULT '', previous_revision_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			UNIQUE(workspace, version), UNIQUE(workspace, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_configurations_scope ON graph_configurations(tenant_id, workspace, enabled, version DESC)`,
		`CREATE TABLE IF NOT EXISTS graph_revisions (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			configuration_id TEXT NOT NULL, base_revision_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
			cutoff_sequence INTEGER NOT NULL DEFAULT 0, cutoff_event_time TEXT NOT NULL DEFAULT '', cutoff_digest TEXT NOT NULL DEFAULT '',
			projection_hash TEXT NOT NULL DEFAULT '', artifact_hash TEXT NOT NULL DEFAULT '',
			previous_revision_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			UNIQUE(workspace, configuration_id, id),
			FOREIGN KEY(configuration_id) REFERENCES graph_configurations(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_revisions_scope_state ON graph_revisions(tenant_id, workspace, configuration_id, state, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS graph_jobs (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT '', workspace TEXT NOT NULL,
			configuration_id TEXT NOT NULL, revision_id TEXT NOT NULL, idempotency_key TEXT NOT NULL,
			state TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, lease_owner TEXT NOT NULL DEFAULT '',
			lease_expires_at TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			UNIQUE(workspace, configuration_id, idempotency_key),
			FOREIGN KEY(configuration_id) REFERENCES graph_configurations(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_jobs_claim ON graph_jobs(tenant_id, workspace, state, lease_expires_at, created_at)`,
		`CREATE TABLE IF NOT EXISTS graph_change_journal (
			id TEXT PRIMARY KEY, workspace TEXT NOT NULL, subject_kind TEXT NOT NULL, subject_id TEXT NOT NULL,
			subject_fingerprint TEXT NOT NULL, projection_version TEXT NOT NULL, configuration_version TEXT NOT NULL,
			change_kind TEXT NOT NULL, occurred_at TEXT NOT NULL, processed_revision_id TEXT NOT NULL DEFAULT '',
			UNIQUE(workspace, subject_kind, subject_id, subject_fingerprint, projection_version, configuration_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_graph_change_journal_pending ON graph_change_journal(workspace, processed_revision_id, occurred_at)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateSolutionActivityReview(ctx context.Context, s *Store) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS solution_episode_reviews (
			episode_id TEXT PRIMARY KEY, workspace TEXT NOT NULL, pinned INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL, FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_episode_reviews_workspace ON solution_episode_reviews(workspace, pinned, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS solution_step_reviews (
			step_id TEXT PRIMARY KEY, episode_id TEXT NOT NULL, workspace TEXT NOT NULL,
			misleading INTEGER NOT NULL DEFAULT 0, redacted INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '', reason_class TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL,
			FOREIGN KEY(step_id) REFERENCES solution_steps(id) ON DELETE CASCADE,
			FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_step_reviews_episode ON solution_step_reviews(episode_id, updated_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrateDistilledSkillMetadata(ctx context.Context, s *Store) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS distilled_skill_metadata (
		id TEXT PRIMARY KEY, workspace TEXT NOT NULL, name TEXT NOT NULL, path TEXT NOT NULL,
		memory_ids_json TEXT NOT NULL, tool_lesson_ids_json TEXT NOT NULL, episode_ids_json TEXT NOT NULL,
		created_at TEXT NOT NULL, UNIQUE(workspace, name)
	)`)
	return err
}

func migrateHowRetrievalFeedback(ctx context.Context, s *Store) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS solution_retrieval_feedback (
		id TEXT PRIMARY KEY, workspace TEXT NOT NULL, target_kind TEXT NOT NULL, target_id TEXT NOT NULL,
		outcome TEXT NOT NULL, created_at TEXT NOT NULL
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_solution_retrieval_feedback_target ON solution_retrieval_feedback(workspace, target_kind, target_id, created_at DESC)`)
	return err
}

func migrateToolLessonPromotions(ctx context.Context, s *Store) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS solution_tool_lesson_promotions (
		id TEXT PRIMARY KEY, lesson_id TEXT NOT NULL, episode_id TEXT NOT NULL, memory_id TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', idempotency_key TEXT NOT NULL, policy_identity TEXT NOT NULL,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(lesson_id, idempotency_key),
		FOREIGN KEY(lesson_id) REFERENCES solution_tool_lessons(id) ON DELETE CASCADE,
		FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE
	)`)
	return err
}

func migrateSolutionToolLearning(ctx context.Context, s *Store) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS solution_tool_events (id TEXT PRIMARY KEY, workspace TEXT NOT NULL, episode_id TEXT NOT NULL,
			step_id TEXT NOT NULL, kind TEXT NOT NULL, tool_name TEXT NOT NULL, tool_version TEXT NOT NULL DEFAULT '', operation TEXT NOT NULL,
			capability TEXT NOT NULL, input_summary TEXT NOT NULL DEFAULT '', result_class TEXT NOT NULL, task_verified INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0, evidence_json TEXT NOT NULL, idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL,
			occurred_at TEXT NOT NULL, UNIQUE(episode_id, idempotency_key), FOREIGN KEY(episode_id) REFERENCES solution_episodes(id) ON DELETE CASCADE,
			FOREIGN KEY(step_id) REFERENCES solution_steps(id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_solution_tool_events_identity ON solution_tool_events(workspace, tool_name, operation, occurred_at DESC)`,
		`CREATE TABLE IF NOT EXISTS solution_tool_lessons (id TEXT PRIMARY KEY, workspace TEXT NOT NULL, tool_name TEXT NOT NULL,
			capability TEXT NOT NULL, version INTEGER NOT NULL, lesson_json TEXT NOT NULL, source_hash TEXT NOT NULL, superseded_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, UNIQUE(workspace, tool_name, capability, version), UNIQUE(workspace, source_hash))`,
		`CREATE INDEX IF NOT EXISTS idx_solution_tool_lessons_identity ON solution_tool_lessons(workspace, tool_name, capability, version DESC)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
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
