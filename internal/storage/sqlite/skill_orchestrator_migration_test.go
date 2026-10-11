package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkillOrchestratorMigrationCreatesDurableQueueSchema(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "skill-orchestrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, table := range []string{
		"skill_orchestrator_workflows", "skill_orchestrator_jobs", "skill_orchestrator_job_dependencies",
		"skill_orchestrator_job_attempts", "skill_orchestrator_safety_signals", "skill_orchestrator_configurations",
		"skill_orchestrator_leader_leases", "skill_orchestrator_reconciliation_cursors", "skill_orchestrator_events",
	} {
		var name string
		if err := store.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("expected table %s: %v", table, err)
		}
	}
	for _, index := range []string{
		"idx_skill_orchestrator_jobs_ready", "idx_skill_orchestrator_jobs_expired",
		"idx_skill_orchestrator_jobs_workflow", "idx_skill_orchestrator_workflows_status",
	} {
		var name string
		if err := store.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&name); err != nil {
			t.Fatalf("expected index %s: %v", index, err)
		}
	}
}

func TestSkillOrchestratorMigrationEnforcesLineageUniquenessAndBounds(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "skill-orchestrator-constraints.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	insertWorkflow := `INSERT INTO skill_orchestrator_workflows(
		id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,
		input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	args := []any{"workflow-1", "", "ws", "production", "", "tool_lesson", "lesson-1", "automatic_revision", "skill-orchestrator/v1", digest, "open", "detect", 1, 1, digest, now, now}
	if _, err := store.db.ExecContext(ctx, insertWorkflow, args...); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	duplicate := append([]any(nil), args...)
	duplicate[0] = "workflow-2"
	if _, err := store.db.ExecContext(ctx, insertWorkflow, duplicate...); err == nil {
		t.Fatal("expected duplicate workflow origin and digest to fail")
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_orchestrator_jobs(
		id,workflow_id,tenant_id,workspace_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
		state,priority,ready_at,dependency_count,attempt,max_attempts,fence,result_references_json,created_at,updated_at
	) VALUES('job-bad','missing-workflow','','ws','production','','detect','skill-orchestrator/v1',?,1,'queued',100,?,0,0,3,0,'[]',?,?)`, digest, now, now, now); err == nil {
		t.Fatal("expected missing workflow foreign key to fail")
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_orchestrator_jobs(
		id,workflow_id,tenant_id,workspace_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
		state,priority,ready_at,dependency_count,attempt,max_attempts,fence,result_references_json,created_at,updated_at
	) VALUES('job-content','workflow-1','','ws','production','','detect','skill-orchestrator/v1',?,1,'queued',100,?,0,0,3,0,?, ?,?)`, digest, now, strings.Repeat("x", 9000), now, now); err == nil {
		t.Fatal("expected oversized result references to fail")
	}
}

func TestSkillOrchestratorMigrationReadyClaimUsesBoundedIndex(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "skill-orchestrator-plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	rows, err := store.db.QueryContext(ctx, `EXPLAIN QUERY PLAN
		SELECT id FROM skill_orchestrator_jobs
		WHERE tenant_id=? AND workspace_id=? AND environment=? AND state='queued' AND ready_at<=?
		ORDER BY priority DESC, ready_at ASC, created_at ASC LIMIT ?`, "", "ws", "production", time.Now().UTC().Format(time.RFC3339Nano), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if !strings.Contains(plan.String(), "idx_skill_orchestrator_jobs_ready") {
		t.Fatalf("expected ready claim index, plan=%s", plan.String())
	}
}

func TestSkillOrchestratorMigrationReapplyPreservesLifecycleState(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "skill-orchestrator-upgrade.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skills(id,workspace,name,description,risk_tier,owner_group,status,generation,created_at,updated_at) VALUES('skill-preserved','ws','preserved','desc','low','platform','active',1,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=26`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after incomplete migration: %v", err)
	}
	defer reopened.Close()
	var count int
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE id='skill-preserved'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected lifecycle state preserved, count=%d", count)
	}
}

func TestSkillOrchestratorMigrationAllowsReaderDuringQueueWrite(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "skill-orchestrator-reader.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	readTx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer readTx.Rollback()
	var before int
	if err := readTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_orchestrator_workflows`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	digest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_orchestrator_workflows(
		id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,
		state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at
	) VALUES('workflow-reader','','ws','production','','tool_lesson','lesson-reader','automatic_revision','skill-orchestrator/v1',?,'open','detect',1,1,?,?,?)`, digest, digest, now, now); err != nil {
		t.Fatalf("queue write while reader active: %v", err)
	}
}

func TestSkillOrchestratorMigrationCascadesRetainedWorkflowLineage(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "skill-orchestrator-retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_orchestrator_workflows(
		id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,
		state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at
	) VALUES('workflow-retention','','ws','production','','tool_lesson','lesson-retention','automatic_revision','skill-orchestrator/v1',?,'open','detect',1,1,?,?,?)`, digest, digest, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_orchestrator_jobs(
		id,workflow_id,tenant_id,workspace_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
		state,priority,ready_at,dependency_count,attempt,max_attempts,fence,result_references_json,created_at,updated_at
	) VALUES('job-retention','workflow-retention','','ws','production','','detect','skill-orchestrator/v1',?,1,'queued',100,?,0,0,3,0,'[]',?,?)`, digest, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM skill_orchestrator_workflows WHERE id='workflow-retention'`); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_orchestrator_jobs WHERE id='job-retention'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("expected workflow retention deletion to cascade job lineage, jobs=%d", jobs)
	}
}
