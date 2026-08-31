package skillorchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func RunRepositoryContract(t *testing.T, repository contracts.SkillOrchestratorRepository, scope core.SkillOrchestratorScope) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 16, 30, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	workflow := core.SkillWorkflow{
		ID: uuid.NewString(), Scope: scope, OriginKind: core.SkillWorkflowOriginToolLesson, OriginID: uuid.NewString(),
		Kind: core.SkillWorkflowAutomaticRevision, ContractVersion: core.SkillOrchestratorContractVersion,
		InputDigest: digest, State: core.SkillWorkflowOpen, CurrentStage: core.SkillStageDetect,
		Generation: 1, ConfigurationVersion: 1, PolicyDigest: digest, CreatedAt: now, UpdatedAt: now,
	}
	stored, created, err := repository.CreateSkillWorkflow(ctx, workflow)
	if err != nil || !created || stored.ID != workflow.ID {
		t.Fatalf("create workflow stored=%+v created=%v err=%v", stored, created, err)
	}
	if _, created, err := repository.CreateSkillWorkflow(ctx, workflow); err != nil || created {
		t.Fatalf("repeat workflow created=%v err=%v", created, err)
	}
	job := core.SkillJob{
		ID: uuid.NewString(), WorkflowID: workflow.ID, Scope: scope, Stage: core.SkillStageDetect,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: digest, PolicyVersion: 1,
		State: core.SkillJobQueued, Priority: 100, ReadyAt: now, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
	}
	if _, enqueued, err := repository.EnqueueSkillJob(ctx, job, nil); err != nil || !enqueued {
		t.Fatalf("enqueue created=%v err=%v", enqueued, err)
	}
	if _, enqueued, err := repository.EnqueueSkillJob(ctx, job, nil); err != nil || enqueued {
		t.Fatalf("repeat enqueue created=%v err=%v", enqueued, err)
	}
	claimed, err := repository.ClaimSkillJobs(ctx, scope, "contract-worker", 1, time.Minute, 45*time.Second, now.Add(time.Second))
	if err != nil || len(claimed) != 1 || claimed[0].Fence != 1 || claimed[0].Attempt != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := repository.RenewSkillJobLease(ctx, scope, job.ID, "contract-worker", 1, now.Add(90*time.Second), now.Add(30*time.Second)); err != nil {
		t.Fatalf("renew: %v", err)
	}
	finalization := contracts.SkillJobFinalization{
		Scope: scope, JobID: job.ID, Owner: "contract-worker", Fence: 1, ExpectedWorkflowGeneration: 1,
		ResultKind: core.SkillJobResultSucceeded, Now: now.Add(time.Minute),
	}
	if err := repository.FinalizeSkillJob(ctx, finalization); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if err := repository.FinalizeSkillJob(ctx, finalization); err != nil {
		t.Fatalf("idempotent finalize: %v", err)
	}
	got, err := repository.GetSkillJob(ctx, scope, job.ID)
	if err != nil || got.State != core.SkillJobCompleted {
		t.Fatalf("get completed=%+v err=%v", got, err)
	}
	listed, next, err := repository.ListSkillJobs(ctx, scope, workflow.ID, "", 10)
	if err != nil || len(listed) != 1 || next != "" {
		t.Fatalf("list=%+v next=%q err=%v", listed, next, err)
	}
}
