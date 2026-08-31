package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSQLiteSkillMigrationInventoryIsReadOnlyScopedAndRestoreAware(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx, now := context.Background(), time.Now().UTC()
	scope := core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_candidates(id,workspace,kind,summary,expected_benefit,risks_json,risk_tier,confidence,state,target_skill_ids_json,deduplication_hash,created_by,created_at,updated_at) VALUES('candidate-migration','ws','create','summary','benefit','[]','low',0.9,'accepted','[]','sha256:candidate','agent',?,?)`, formatSkillTime(now), formatSkillTime(now)); err != nil {
		t.Fatal(err)
	}
	workflow := sqliteValidSkillWorkflow(now, "workflow-migration", "candidate-migration")
	workflow.OriginKind = core.SkillWorkflowOriginLifecycleSignal
	job := sqliteValidSkillJob(now, "job-migration", workflow.ID, core.SkillStageBuild)
	job.InputDigest = workflow.InputDigest
	if _, err := store.RouteSkillSignal(ctx, workflow, job, nil); err != nil {
		t.Fatal(err)
	}
	before := countSkillMigrationRows(t, store)
	inventory, err := store.InspectSkillOrchestratorMigration(ctx, scope, 100)
	if err != nil || inventory.SchemaVersion != "30" || inventory.RestorePaused || inventory.ConfigurationMode != core.SkillOrchestratorDisabled || len(inventory.Items) != 1 || !inventory.Items[0].ExistingOpenWorkflow || inventory.ExistingWorkflows != 1 {
		t.Fatalf("inventory=%+v err=%v", inventory, err)
	}
	if after := countSkillMigrationRows(t, store); after != before {
		t.Fatalf("shadow inventory mutated rows before=%d after=%d", before, after)
	}
	if err := store.SetSkillOrchestratorMigrationRestorePaused(ctx, scope, true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	inventory, err = store.InspectSkillOrchestratorMigration(ctx, scope, 100)
	if err != nil || !inventory.RestorePaused {
		t.Fatalf("restore inventory=%+v err=%v", inventory, err)
	}
	other := scope
	other.Environment = "staging"
	inventory, err = store.InspectSkillOrchestratorMigration(ctx, other, 100)
	if err != nil || inventory.RestorePaused || len(inventory.Items) != 1 || inventory.Items[0].ExistingOpenWorkflow {
		t.Fatalf("staging inventory=%+v err=%v", inventory, err)
	}
}

func countSkillMigrationRows(t *testing.T, store *Store) int {
	t.Helper()
	var candidates, workflows, jobs int
	for query, target := range map[string]*int{
		`SELECT COUNT(*) FROM skill_candidates`:             &candidates,
		`SELECT COUNT(*) FROM skill_orchestrator_workflows`: &workflows,
		`SELECT COUNT(*) FROM skill_orchestrator_jobs`:      &jobs,
	} {
		if err := store.db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	return candidates + workflows + jobs
}
