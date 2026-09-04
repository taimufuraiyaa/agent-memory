package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillLifecycleMigrationCreatesNormalizedRegistry(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "skill-lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, table := range []string{
		"skills", "skill_aliases", "skill_candidates", "skill_candidate_sources",
		"skill_revisions", "skill_revision_parents", "skill_revision_files",
		"skill_evaluation_suites", "skill_evaluation_cases", "skill_evaluation_runs", "skill_evaluation_case_results",
		"skill_promotion_policies", "skill_policy_decisions", "skill_approvals",
		"skill_activations", "skill_activation_operations", "skill_resolutions", "skill_executions", "skill_rollback_events",
		"skill_legal_holds", "skill_evidence_tombstones",
	} {
		var name string
		if err := store.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("expected table %s: %v", table, err)
		}
	}
}

func TestSkillLifecycleMigrationEnforcesOneActivationPerScope(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "skill-activation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 29, 13, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if _, err := store.db.ExecContext(ctx, `INSERT INTO skills(id,workspace,name,description,risk_tier,owner_group,status,generation,created_at,updated_at) VALUES('skill-1','ws','restore','desc','low','platform','active',1,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_revisions(id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,compatibility_json,risk_tier,candidate_id,created_by,created_at) VALUES('revision-1','ws','skill-1',1,'active',?,1,'{}','low','','agent',?)`, digest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_activations(id,workspace,environment,skill_id,active_revision_id,active_digest,last_known_good_revision_id,last_known_good_digest,canary_revision_id,canary_digest,generation,policy_decision_id,materialization,activated_by,activated_at,updated_at) VALUES('activation-1','ws','production','skill-1','revision-1',?,'','','','',1,'import','ready','migration',?,?)`, digest, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_activations(id,workspace,environment,skill_id,active_revision_id,active_digest,last_known_good_revision_id,last_known_good_digest,canary_revision_id,canary_digest,generation,policy_decision_id,materialization,activated_by,activated_at,updated_at) VALUES('activation-2','ws','production','skill-1','revision-1',?,'','','','',2,'import','ready','migration',?,?)`, digest, now, now); err == nil {
		t.Fatal("expected duplicate workspace/environment/skill activation to fail")
	}
}

func TestSkillLifecycleMigrationEnforcesRevisionLineageForeignKeys(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "skill-lineage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_revision_parents(revision_id,parent_revision_id) VALUES('missing-child','missing-parent')`); err == nil {
		t.Fatal("expected missing revision foreign keys to fail")
	}
}
