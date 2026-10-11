package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillOrchestratorCustodyHoldsCancelsAndExportsTombstonedWorkflow(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx, now := context.Background(), time.Now().UTC()
	workflow := sqliteValidSkillWorkflow(now, "workflow-custody", "origin-custody")
	job := sqliteValidSkillJob(now, "job-custody", workflow.ID, core.SkillStageEvaluate)
	job.InputDigest = workflow.InputDigest
	if _, err := store.RouteSkillSignal(ctx, workflow, job, nil); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimSkillJobs(ctx, workflow.Scope, "worker", 1, time.Minute, time.Minute, now.Add(time.Second)); err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	hold := core.SkillOrchestratorLegalHold{ID: "hold-workflow", Scope: workflow.Scope, TargetKind: "workflow", TargetID: workflow.ID, Reason: "investigation", CreatedAt: now}
	if err := store.PlaceSkillOrchestratorLegalHold(ctx, hold); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteSkillOrchestratorRecord(ctx, workflow.Scope, "workflow", workflow.ID, now.Add(2*time.Second)); !errors.Is(err, ErrSkillEvidenceHeld) {
		t.Fatalf("expected hold rejection, got %v", err)
	}
	if err := store.ReleaseSkillOrchestratorLegalHold(ctx, workflow.Scope, hold.ID, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	result, err := store.DeleteSkillOrchestratorRecord(ctx, workflow.Scope, "workflow", workflow.ID, now.Add(4*time.Second))
	if err != nil || result.WorkflowsClosed != 1 || result.JobsCancelled != 0 {
		t.Fatalf("deletion=%+v err=%v", result, err)
	}
	stored, err := store.GetSkillWorkflow(ctx, workflow.Scope, workflow.ID)
	if err != nil || stored.State != core.SkillWorkflowCancelled {
		t.Fatalf("workflow=%+v err=%v", stored, err)
	}
	storedJob, err := store.GetSkillJob(ctx, workflow.Scope, job.ID)
	if err != nil || storedJob.CancelRequestedAt.IsZero() {
		t.Fatalf("job=%+v err=%v", storedJob, err)
	}
	replayed, err := store.DeleteSkillOrchestratorRecord(ctx, workflow.Scope, "workflow", workflow.ID, now.Add(5*time.Second))
	if err != nil || !replayed.Replayed {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	archive, err := store.ExportSkillLifecycle(ctx, workflow.Scope.WorkspaceID)
	if err != nil || len(archive["orchestrator_workflows"]) != 1 || len(archive["orchestrator_jobs"]) != 1 || len(archive["orchestrator_tombstones"]) != 1 {
		t.Fatalf("archive keys=%v err=%v", archive, err)
	}
}

func TestSkillOrchestratorAttemptRetentionPrunesExpiredDeadLetterButHonorsHold(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx, now := context.Background(), time.Now().UTC().Add(-time.Hour)
	for index, held := range []bool{false, true} {
		workflow := sqliteValidSkillWorkflow(now, "workflow-retention-"+string(rune('a'+index)), "origin-retention-"+string(rune('a'+index)))
		job := sqliteValidSkillJob(now, "job-retention-"+string(rune('a'+index)), workflow.ID, core.SkillStageEvaluate)
		job.InputDigest = workflow.InputDigest
		if _, err := store.RouteSkillSignal(ctx, workflow, job, nil); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimSkillJobs(ctx, workflow.Scope, "worker-retention", 1, time.Minute, time.Minute, now.Add(time.Second))
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim=%+v err=%v", claimed, err)
		}
		if err := store.FinalizeSkillJob(ctx, contracts.SkillJobFinalization{Scope: workflow.Scope, JobID: job.ID, Owner: "worker-retention", Fence: 1, ExpectedWorkflowGeneration: 1, ResultKind: core.SkillJobResultRejected, FailureClass: core.SkillFailurePermanentValidation, FailureCode: "invalid", DeadLetter: true, Now: now.Add(2 * time.Second)}); err != nil {
			t.Fatal(err)
		}
		if held {
			if err := store.PlaceSkillOrchestratorLegalHold(ctx, core.SkillOrchestratorLegalHold{ID: "hold-job", Scope: workflow.Scope, TargetKind: "job", TargetID: job.ID, Reason: "investigation", CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	removed, err := store.PruneSkillOrchestratorAttempts(ctx, core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}, time.Now().UTC(), 1)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	var attempts, deadLetters int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_orchestrator_job_attempts`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_orchestrator_jobs WHERE state='dead_lettered'`).Scan(&deadLetters); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || deadLetters != 2 {
		t.Fatalf("attempts=%d dead_letters=%d", attempts, deadLetters)
	}
}

func TestSkillOrchestratorTombstoneExportRestorePreservesScope(t *testing.T) {
	source := openSkillOrchestratorStore(t)
	ctx := context.Background()
	scope := core.SkillOrchestratorScope{TenantID: "tenant-a", WorkspaceID: "ws", Environment: "production"}
	workflow := sqliteValidSkillWorkflow(time.Now().UTC(), "workflow-restore", "origin-restore")
	workflow.Scope = scope
	job := sqliteValidSkillJob(workflow.CreatedAt, "job-restore", workflow.ID, core.SkillStageEvaluate)
	job.Scope = scope
	job.InputDigest = workflow.InputDigest
	if _, err := source.RouteSkillSignal(ctx, workflow, job, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := source.DeleteSkillOrchestratorRecord(ctx, scope, "workflow", workflow.ID, workflow.CreatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	archive, err := source.ExportSkillLifecycle(ctx, scope.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	destination := openSkillOrchestratorStore(t)
	restored, err := destination.RestoreSkillOrchestratorTombstones(ctx, scope, archive)
	if err != nil || restored != 1 {
		t.Fatalf("restored=%d err=%v", restored, err)
	}
	for _, check := range []struct {
		scope core.SkillOrchestratorScope
		want  bool
	}{
		{scope: scope, want: true},
		{scope: core.SkillOrchestratorScope{TenantID: "tenant-a", WorkspaceID: "ws", Environment: "staging"}, want: false},
		{scope: core.SkillOrchestratorScope{TenantID: "tenant-b", WorkspaceID: "ws", Environment: "production"}, want: false},
	} {
		found, findErr := destination.IsSkillOrchestratorSignalTombstoned(ctx, check.scope, "workflow", workflow.ID)
		if findErr != nil || found != check.want {
			t.Fatalf("scope=%+v found=%v want=%v err=%v", check.scope, found, check.want, findErr)
		}
	}
	replayed, err := destination.RestoreSkillOrchestratorTombstones(ctx, scope, archive)
	if err != nil || replayed != 0 {
		t.Fatalf("replayed restore=%d err=%v", replayed, err)
	}
}

func TestSkillOrchestratorConfigurationDeletionIsSelectiveByEnvironment(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx, now := context.Background(), time.Now().UTC()
	production := sqliteSkillConfiguration(now)
	staging := production
	staging.Scope.Environment = "staging"
	for _, configuration := range []core.SkillOrchestratorConfiguration{production, staging} {
		created, err := store.StoreSkillOrchestratorConfiguration(ctx, configuration, core.SkillOrchestratorConfigurationAudit{ActorID: "privacy-operator", RequestID: "custody-" + configuration.Scope.Environment, Operation: "skill_orchestrator.configuration.create", ToVersion: 1, ReasonCode: "custody_fixture", OccurredAt: now})
		if err != nil || !created {
			t.Fatalf("store %s created=%v err=%v", configuration.Scope.Environment, created, err)
		}
	}
	result, err := store.DeleteSkillOrchestratorRecord(ctx, production.Scope, "configuration", "1", now.Add(time.Second))
	if err != nil || result.RecordsDeleted != 1 {
		t.Fatalf("deletion=%+v err=%v", result, err)
	}
	if _, err := store.GetSkillOrchestratorConfiguration(ctx, production.Scope, 1); !errors.Is(err, core.ErrSkillOrchestratorConfigurationNotFound) {
		t.Fatalf("production configuration still present: %v", err)
	}
	if _, err := store.GetSkillOrchestratorConfiguration(ctx, staging.Scope, 1); err != nil {
		t.Fatalf("staging configuration was affected: %v", err)
	}
	productionDeleted, err := store.IsSkillOrchestratorSignalTombstoned(ctx, production.Scope, "configuration", "1")
	if err != nil || !productionDeleted {
		t.Fatalf("production tombstone=%v err=%v", productionDeleted, err)
	}
	stagingDeleted, err := store.IsSkillOrchestratorSignalTombstoned(ctx, staging.Scope, "configuration", "1")
	if err != nil || stagingDeleted {
		t.Fatalf("staging tombstone=%v err=%v", stagingDeleted, err)
	}
}
