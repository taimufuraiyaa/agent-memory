package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	orchestratortest "github.com/taimufuraiyaa/agent-memory/internal/testkit/skillorchestrator"
)

func TestPostgresSkillOrchestratorSharedRepositoryContract(t *testing.T) {
	pool := openSkillOrchestratorPostgres(t)
	scope := createSkillOrchestratorHostedScope(t, pool)
	repository := NewSkillOrchestratorRepository(pool)
	orchestratortest.RunRepositoryContract(t, repository, scope)
}

func TestPostgresSkillSignalRouteIsAtomicAndIdempotent(t *testing.T) {
	pool := openSkillOrchestratorPostgres(t)
	scope := createSkillOrchestratorHostedScope(t, pool)
	repository := NewSkillOrchestratorRepository(pool)
	ctx := context.Background()
	now := time.Now().UTC()
	workflow, job := hostedSkillWorkflowAndJob(now, scope)
	workflow.OriginKind = core.SkillWorkflowOriginLifecycleSignal

	first, err := repository.RouteSkillSignal(ctx, workflow, job, nil)
	if err != nil || !first.Created {
		t.Fatalf("first route=%+v err=%v", first, err)
	}
	second, err := repository.RouteSkillSignal(ctx, workflow, job, nil)
	if err != nil || second.Created || second.Job.ID != first.Job.ID {
		t.Fatalf("duplicate route=%+v err=%v", second, err)
	}

	rollbackWorkflow, rollbackJob := hostedSkillWorkflowAndJob(now, scope)
	rollbackWorkflow.OriginKind = core.SkillWorkflowOriginLifecycleSignal
	dependency := core.SkillJobDependency{JobID: rollbackJob.ID, ParentJobID: uuid.NewString(), AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: now}
	if _, err := repository.RouteSkillSignal(ctx, rollbackWorkflow, rollbackJob, []core.SkillJobDependency{dependency}); err == nil {
		t.Fatal("expected missing parent dependency to roll back route")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1 AND id=$2`, scope.TenantID, rollbackWorkflow.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("transaction left partial workflow count=%d", count)
	}
}

func TestPostgresSkillOrchestratorConcurrentClaimLeaseTakeoverAndScopeSafety(t *testing.T) {
	pool := openSkillOrchestratorPostgres(t)
	scope := createSkillOrchestratorHostedScope(t, pool)
	repository := NewSkillOrchestratorRepository(pool)
	ctx := context.Background()
	now := time.Now().UTC()
	workflow, job := hostedSkillWorkflowAndJob(now, scope)
	if _, _, err := repository.CreateSkillWorkflow(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.EnqueueSkillJob(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		jobs []core.SkillJob
		err  error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for _, owner := range []string{"hosted-worker-a", "hosted-worker-b"} {
		owner := owner
		go func() {
			<-start
			jobs, err := repository.ClaimSkillJobs(ctx, scope, owner, 1, time.Minute, 45*time.Second, now.Add(time.Second))
			results <- claimResult{jobs: jobs, err: err}
		}()
	}
	close(start)
	var first core.SkillJob
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if len(result.jobs) == 1 {
			if first.ID != "" {
				t.Fatal("two workers claimed one job")
			}
			first = result.jobs[0]
		}
	}
	if first.ID == "" {
		t.Fatal("no worker claimed ready job")
	}
	reclaimed, err := repository.ClaimSkillJobs(ctx, scope, "hosted-worker-new", 1, time.Minute, 45*time.Second, now.Add(2*time.Minute))
	if err != nil || len(reclaimed) != 1 || reclaimed[0].Fence != 2 || reclaimed[0].Attempt != 2 {
		t.Fatalf("reclaimed=%+v err=%v", reclaimed, err)
	}
	if err := repository.FinalizeSkillJob(ctx, contracts.SkillJobFinalization{
		Scope: scope, JobID: job.ID, Owner: first.LeaseOwner, Fence: 1, ExpectedWorkflowGeneration: 1,
		ResultKind: core.SkillJobResultSucceeded, Now: now.Add(2 * time.Minute),
	}); !errors.Is(err, ErrSkillOrchestratorStaleLease) {
		t.Fatalf("expected stale worker rejection, got %v", err)
	}
	forged := scope
	forged.WorkspaceID = uuid.NewString()
	if _, err := repository.GetSkillJob(ctx, forged, job.ID); !errors.Is(err, ErrSkillOrchestratorNotFound) {
		t.Fatalf("expected timing-safe scoped not found, got %v", err)
	}
}

func TestPostgresSkillOrchestratorDeadLetterAndNoisyTenantIsolation(t *testing.T) {
	pool := openSkillOrchestratorPostgres(t)
	scopeA := createSkillOrchestratorHostedScope(t, pool)
	scopeB := createSkillOrchestratorHostedScope(t, pool)
	repository := NewSkillOrchestratorRepository(pool)
	ctx := context.Background()
	now := time.Now().UTC()
	for index := range 5 {
		workflow, job := hostedSkillWorkflowAndJob(now.Add(time.Duration(index)*time.Millisecond), scopeA)
		if _, _, err := repository.CreateSkillWorkflow(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		if _, _, err := repository.EnqueueSkillJob(ctx, job, nil); err != nil {
			t.Fatal(err)
		}
	}
	workflowB, jobB := hostedSkillWorkflowAndJob(now, scopeB)
	if _, _, err := repository.CreateSkillWorkflow(ctx, workflowB); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.EnqueueSkillJob(ctx, jobB, nil); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimSkillJobs(ctx, scopeB, "tenant-b-worker", 1, time.Minute, 45*time.Second, now.Add(time.Second))
	if err != nil || len(claimed) != 1 || claimed[0].ID != jobB.ID {
		t.Fatalf("tenant B claim=%+v err=%v", claimed, err)
	}
	if err := repository.FinalizeSkillJob(ctx, contracts.SkillJobFinalization{
		Scope: scopeB, JobID: jobB.ID, Owner: "tenant-b-worker", Fence: 1, ExpectedWorkflowGeneration: 1,
		ResultKind: core.SkillJobResultRejected, FailureClass: core.SkillFailurePermanentValidation,
		FailureCode: "unsupported_contract", DeadLetter: true, Now: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repository.GetSkillJob(ctx, scopeB, jobB.ID)
	if err != nil || got.State != core.SkillJobDeadLettered {
		t.Fatalf("dead letter=%+v err=%v", got, err)
	}
}

func openSkillOrchestratorPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	pool, err := Open(context.Background(), connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := Apply(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func createSkillOrchestratorHostedScope(t *testing.T, pool *pgxpool.Pool) core.SkillOrchestratorScope {
	t.Helper()
	ctx := context.Background()
	account, tenant, workspace := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`, account, account.String(), account.String()+"@example.test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at) VALUES($1,'personal','active',$2,$3,$3)`, tenant, account, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_workspaces(tenant_id,id,name,state,created_at,updated_at) VALUES($1,$2,'skill-orchestrator','active',$3,$3)`, tenant, workspace, now); err != nil {
		t.Fatal(err)
	}
	return core.SkillOrchestratorScope{TenantID: tenant.String(), WorkspaceID: workspace.String(), Environment: "production"}
}

func hostedSkillWorkflowAndJob(now time.Time, scope core.SkillOrchestratorScope) (core.SkillWorkflow, core.SkillJob) {
	digest := "sha256:" + strings.Repeat("b", 64)
	workflow := core.SkillWorkflow{
		ID: uuid.NewString(), Scope: scope, OriginKind: core.SkillWorkflowOriginToolLesson, OriginID: uuid.NewString(),
		Kind: core.SkillWorkflowAutomaticRevision, ContractVersion: core.SkillOrchestratorContractVersion,
		InputDigest: digest, State: core.SkillWorkflowOpen, CurrentStage: core.SkillStageDetect,
		Generation: 1, ConfigurationVersion: 1, PolicyDigest: digest, CreatedAt: now, UpdatedAt: now,
	}
	job := core.SkillJob{
		ID: uuid.NewString(), WorkflowID: workflow.ID, Scope: scope, Stage: core.SkillStageDetect,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: digest, PolicyVersion: 1,
		State: core.SkillJobQueued, Priority: 100, ReadyAt: now, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
	}
	return workflow, job
}
