package skillworker

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestPostgresLaneWorkerUsesSharedFencedExecutorAndPhysicalLane(t *testing.T) {
	configuration := skillWorkerTestConfig()
	scope := configuration.Assignments[0]
	now := time.Now().UTC()
	repository := &laneWorkerTestRepository{job: core.SkillJob{ID: "job-rollback", WorkflowID: "workflow-1", Scope: scope,
		Stage: core.SkillStageRollback, ContractVersion: core.SkillOrchestratorContractVersion,
		InputDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PolicyVersion: 1,
		State: core.SkillJobRunning, Priority: 1_000, ReadyAt: now, Attempt: 1, MaxAttempts: 3,
		LeaseOwner: configuration.WorkerIdentity + "-rollback", LeaseExpiresAt: now.Add(time.Minute), Fence: 1, CreatedAt: now, UpdatedAt: now}}
	registry := application.NewSkillStageRegistry()
	if err := registry.Register(core.SkillOrchestratorContractVersion, core.SkillStageRollback, application.SkillStageAdapterFunc(func(context.Context, core.SkillJob) (application.SkillStageResult, error) {
		return application.SkillStageResult{ResultKind: core.SkillJobResultSucceeded}, nil
	})); err != nil {
		t.Fatal(err)
	}
	worker, err := NewPostgresLaneWorker(repository, registry, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunSkillWorkerLane(context.Background(), scope, RollbackLane, 1); err != nil {
		t.Fatal(err)
	}
	if !repository.rollback || repository.finalized.JobID != repository.job.ID || repository.finalized.Fence != repository.job.Fence {
		t.Fatalf("lane=%v finalization=%+v", repository.rollback, repository.finalized)
	}
}

type laneWorkerTestRepository struct {
	job       core.SkillJob
	rollback  bool
	finalized contracts.SkillJobFinalization
}

func (r *laneWorkerTestRepository) ClaimSkillJobs(context.Context, core.SkillOrchestratorScope, string, int, time.Duration, time.Duration, time.Time) ([]core.SkillJob, error) {
	return []core.SkillJob{r.job}, nil
}
func (r *laneWorkerTestRepository) ClaimSkillJobsByLane(_ context.Context, _ core.SkillOrchestratorScope, _ string, _ int, _, _ time.Duration, _ time.Time, rollback bool) ([]core.SkillJob, error) {
	r.rollback = rollback
	return []core.SkillJob{r.job}, nil
}
func (r *laneWorkerTestRepository) SkillWorkflowGeneration(context.Context, core.SkillOrchestratorScope, string) (int64, error) {
	return 1, nil
}
func (r *laneWorkerTestRepository) RenewSkillJobLease(context.Context, core.SkillOrchestratorScope, string, string, int64, time.Time, time.Time) error {
	return nil
}
func (r *laneWorkerTestRepository) FinalizeSkillJob(_ context.Context, input contracts.SkillJobFinalization) error {
	r.finalized = input
	return nil
}
func (r *laneWorkerTestRepository) RetrySkillJob(context.Context, contracts.SkillJobRetry) error {
	return nil
}
func (r *laneWorkerTestRepository) BlockSkillJob(context.Context, contracts.SkillJobBlock) error {
	return nil
}
