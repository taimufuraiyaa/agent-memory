package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var (
	ErrSkillOrchestratorNotFound   = errors.New("skill orchestrator record not found")
	ErrSkillOrchestratorScope      = errors.New("skill orchestrator scope mismatch")
	ErrSkillOrchestratorConflict   = errors.New("skill orchestrator idempotency conflict")
	ErrSkillOrchestratorStaleLease = errors.New("skill orchestrator lease owner or fence is stale")
	ErrSkillOrchestratorGeneration = errors.New("skill orchestrator workflow generation is stale")
)

type SkillOrchestratorRepository struct{ pool *pgxpool.Pool }

var _ contracts.SkillOrchestratorRepository = (*SkillOrchestratorRepository)(nil)

func NewSkillOrchestratorRepository(pool *pgxpool.Pool) *SkillOrchestratorRepository {
	return &SkillOrchestratorRepository{pool: pool}
}

func (r *SkillOrchestratorRepository) CreateSkillWorkflow(ctx context.Context, workflow core.SkillWorkflow) (core.SkillWorkflow, bool, error) {
	if err := workflow.Validate(); err != nil {
		return core.SkillWorkflow{}, false, err
	}
	tx, err := r.begin(ctx, workflow.Scope)
	if err != nil {
		return core.SkillWorkflow{}, false, err
	}
	defer tx.Rollback(ctx)
	row := tx.QueryRow(ctx, `WITH inserted AS (
		INSERT INTO saas_skill_orchestrator_workflows(
			tenant_id,workspace_id,id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,
			input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at
		) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT(tenant_id,workspace_id,environment,workflow_kind,origin_kind,origin_id,input_digest) DO NOTHING
		RETURNING id::text,tenant_id::text,workspace_id::text,environment,skill_id,origin_kind,origin_id,workflow_kind,
			contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at,true
	) SELECT * FROM inserted UNION ALL
	SELECT id::text,tenant_id::text,workspace_id::text,environment,skill_id,origin_kind,origin_id,workflow_kind,
		contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at,false
	FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$4
		AND workflow_kind=$8 AND origin_kind=$6 AND origin_id=$7 AND input_digest=$10 LIMIT 1`,
		workflow.Scope.TenantID, workflow.Scope.WorkspaceID, workflow.ID, workflow.Scope.Environment, workflow.SkillID,
		workflow.OriginKind, workflow.OriginID, workflow.Kind, workflow.ContractVersion, workflow.InputDigest,
		workflow.State, workflow.CurrentStage, workflow.Generation, workflow.ConfigurationVersion, workflow.PolicyDigest,
		workflow.CreatedAt, workflow.UpdatedAt, nullableSkillOrchestratorTime(workflow.TerminalAt))
	stored, created, err := scanHostedSkillWorkflowCreated(row)
	if err != nil {
		return core.SkillWorkflow{}, false, err
	}
	if stored.ID != workflow.ID || stored.ContractVersion != workflow.ContractVersion || stored.PolicyDigest != workflow.PolicyDigest {
		return core.SkillWorkflow{}, false, ErrSkillOrchestratorConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SkillWorkflow{}, false, err
	}
	return stored, created, nil
}

func (r *SkillOrchestratorRepository) EnqueueSkillJob(ctx context.Context, job core.SkillJob, dependencies []core.SkillJobDependency) (core.SkillJob, bool, error) {
	job.DependencyCount = len(dependencies)
	if err := job.Validate(); err != nil {
		return core.SkillJob{}, false, err
	}
	for index := range dependencies {
		dependencies[index].JobID = job.ID
		if err := dependencies[index].Validate(); err != nil {
			return core.SkillJob{}, false, err
		}
	}
	tx, err := r.begin(ctx, job.Scope)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND environment=$4)`, job.Scope.TenantID, job.Scope.WorkspaceID, job.WorkflowID, job.Scope.Environment).Scan(&exists); err != nil {
		return core.SkillJob{}, false, err
	}
	if !exists {
		return core.SkillJob{}, false, ErrSkillOrchestratorNotFound
	}
	refs := marshalHostedSkillReferences(job.ResultReferences)
	row := tx.QueryRow(ctx, `WITH inserted AS (
		INSERT INTO saas_skill_orchestrator_jobs(
			tenant_id,workspace_id,id,workflow_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
			state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,
			timeout_at,cancel_requested_at,result_kind,result_references,failure_class,failure_code,created_at,updated_at,completed_at
		) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24::jsonb,$25,$26,$27,$28,$29)
		ON CONFLICT(tenant_id,workspace_id,workflow_id,stage,input_digest) DO NOTHING
		RETURNING `+hostedSkillJobColumns+`,true
	) SELECT * FROM inserted UNION ALL SELECT `+hostedSkillJobColumns+`,false
	FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND workflow_id=$4::uuid AND stage=$7 AND input_digest=$9 LIMIT 1`,
		job.Scope.TenantID, job.Scope.WorkspaceID, job.ID, job.WorkflowID, job.Scope.Environment, job.SkillID,
		job.Stage, job.ContractVersion, job.InputDigest, job.PolicyVersion, job.State, job.Priority, job.ReadyAt,
		job.DependencyCount, job.BlockedReason, job.Attempt, job.MaxAttempts, job.LeaseOwner,
		nullableSkillOrchestratorTime(job.LeaseExpiresAt), job.Fence, nullableSkillOrchestratorTime(job.TimeoutAt),
		nullableSkillOrchestratorTime(job.CancelRequestedAt), job.ResultKind, refs, job.FailureClass, job.FailureCode,
		job.CreatedAt, job.UpdatedAt, nullableSkillOrchestratorTime(job.CompletedAt))
	stored, created, err := scanHostedSkillJobCreated(row)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	if stored.ID != job.ID || stored.Scope != job.Scope || stored.ContractVersion != job.ContractVersion {
		return core.SkillJob{}, false, ErrSkillOrchestratorConflict
	}
	if created {
		for _, dependency := range dependencies {
			accepted, _ := json.Marshal(dependency.AcceptedResultKinds)
			if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_job_dependencies(tenant_id,workspace_id,job_id,parent_job_id,accepted_result_kinds,created_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::jsonb,$6)`, job.Scope.TenantID, job.Scope.WorkspaceID, dependency.JobID, dependency.ParentJobID, accepted, dependency.CreatedAt); err != nil {
				return core.SkillJob{}, false, err
			}
		}
		if err := insertHostedSkillEvent(ctx, tx, stored, "job_enqueued", "", string(job.State), "system", 0, "", job.CreatedAt); err != nil {
			return core.SkillJob{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SkillJob{}, false, err
	}
	return stored, created, nil
}

func (r *SkillOrchestratorRepository) ClaimSkillJobs(ctx context.Context, scope core.SkillOrchestratorScope, owner string, limit int, lease, timeout time.Duration, now time.Time) ([]core.SkillJob, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > 256 || limit < 1 || limit > 100 || lease <= 0 || timeout <= 0 || timeout > lease || now.IsZero() {
		return nil, errors.New("invalid skill job claim")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET state='queued',updated_at=$3 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND state='retry_wait' AND ready_at<=$3`, scope.TenantID, scope.WorkspaceID, now); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT j.id,j.state FROM saas_skill_orchestrator_jobs j
		JOIN saas_skill_orchestrator_workflows w ON w.tenant_id=j.tenant_id AND w.workspace_id=j.workspace_id AND w.id=j.workflow_id
		WHERE j.tenant_id=$1::uuid AND j.workspace_id=$2::uuid AND j.environment=$3 AND w.state='open'
		AND j.contract_version='skill-orchestrator/v1' AND j.attempt<j.max_attempts AND (
			(j.state='queued' AND j.ready_at<=$4 AND NOT EXISTS(
				SELECT 1 FROM saas_skill_orchestrator_job_dependencies d
				JOIN saas_skill_orchestrator_jobs p ON p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id AND p.id=d.parent_job_id
				WHERE d.tenant_id=j.tenant_id AND d.workspace_id=j.workspace_id AND d.job_id=j.id
				AND (p.state!='completed' OR NOT (d.accepted_result_kinds ? p.result_kind))
			)) OR (j.state='running' AND j.lease_expires_at<=$4)
		) ORDER BY j.priority DESC,j.ready_at,j.created_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT $5
	) UPDATE saas_skill_orchestrator_jobs j SET state='running',attempt=j.attempt+1,lease_owner=$6,
		lease_expires_at=$7,fence=j.fence+1,timeout_at=$8,blocked_reason='',failure_class='',failure_code='',updated_at=$4
	FROM candidates c WHERE j.tenant_id=$1::uuid AND j.id=c.id
	RETURNING `+qualifiedHostedSkillJobColumns("j"), scope.TenantID, scope.WorkspaceID, scope.Environment, now, limit, owner, now.Add(lease), now.Add(timeout))
	if err != nil {
		return nil, err
	}
	var jobs []core.SkillJob
	for rows.Next() {
		job, err := scanHostedSkillJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, job := range jobs {
		if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_job_attempts SET ended_at=$4,result_kind='cancelled',failure_class='contention',failure_code='lease_expired',duration_ms=GREATEST(0,(EXTRACT(EPOCH FROM ($4-started_at))*1000)::bigint) WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND job_id=$3::uuid AND ended_at IS NULL`, scope.TenantID, scope.WorkspaceID, job.ID, now); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_job_attempts(tenant_id,workspace_id,id,job_id,attempt,owner,fence,started_at,lease_expires_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9)`, scope.TenantID, scope.WorkspaceID, deterministicHostedAttemptID(job.ID, job.Attempt), job.ID, job.Attempt, owner, job.Fence, now, now.Add(lease)); err != nil {
			return nil, err
		}
		if err := insertHostedSkillEvent(ctx, tx, job, "job_claimed", "queued", string(core.SkillJobRunning), owner, job.Fence, "", now); err != nil {
			return nil, err
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Priority != jobs[j].Priority {
			return jobs[i].Priority > jobs[j].Priority
		}
		if !jobs[i].ReadyAt.Equal(jobs[j].ReadyAt) {
			return jobs[i].ReadyAt.Before(jobs[j].ReadyAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *SkillOrchestratorRepository) RenewSkillJobLease(ctx context.Context, scope core.SkillOrchestratorScope, jobID, owner string, fence int64, expiresAt, now time.Time) error {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET lease_expires_at=$7,updated_at=$8 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='running' AND lease_owner=$4 AND fence=$5 AND lease_expires_at>$6 AND cancel_requested_at IS NULL`, scope.TenantID, scope.WorkspaceID, jobID, owner, fence, now, expiresAt, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSkillOrchestratorStaleLease
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_job_attempts SET lease_expires_at=$6,renewal_count=renewal_count+1 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND job_id=$3::uuid AND owner=$4 AND fence=$5 AND ended_at IS NULL`, scope.TenantID, scope.WorkspaceID, jobID, owner, fence, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *SkillOrchestratorRepository) FinalizeSkillJob(ctx context.Context, input contracts.SkillJobFinalization) error {
	target := core.SkillJobCompleted
	if input.DeadLetter {
		target = core.SkillJobDeadLettered
	}
	return r.finish(ctx, input.Scope, input.JobID, input.Owner, input.Fence, input.ExpectedWorkflowGeneration, target, input.ResultKind, input.ResultReferences, input.FailureClass, input.FailureCode, "", time.Time{}, input.Now)
}

func (r *SkillOrchestratorRepository) RetrySkillJob(ctx context.Context, input contracts.SkillJobRetry) error {
	if input.FailureClass != core.SkillFailureContention && input.FailureClass != core.SkillFailureDependencyUnavailable && input.FailureClass != core.SkillFailureUnknownInternal {
		return errors.New("skill job retry requires retryable failure class")
	}
	if !input.ReadyAt.After(input.Now) {
		return errors.New("skill job retry ready_at must follow now")
	}
	return r.finish(ctx, input.Scope, input.JobID, input.Owner, input.Fence, input.ExpectedWorkflowGeneration, core.SkillJobRetryWait, core.SkillJobResultNone, nil, input.FailureClass, input.FailureCode, "", input.ReadyAt, input.Now)
}

func (r *SkillOrchestratorRepository) BlockSkillJob(ctx context.Context, input contracts.SkillJobBlock) error {
	if input.FailureClass != core.SkillFailureInsufficientEvidence && input.FailureClass != core.SkillFailurePolicyBlock {
		return errors.New("skill job block requires blocked failure class")
	}
	if !input.RecheckAt.After(input.Now) {
		return errors.New("skill job block recheck_at must follow now")
	}
	return r.finish(ctx, input.Scope, input.JobID, input.Owner, input.Fence, input.ExpectedWorkflowGeneration, core.SkillJobBlocked, core.SkillJobResultNone, nil, input.FailureClass, "", input.ReasonCode, input.RecheckAt, input.Now)
}

func (r *SkillOrchestratorRepository) finish(ctx context.Context, scope core.SkillOrchestratorScope, jobID, owner string, fence, generation int64, target core.SkillJobState, resultKind core.SkillJobResultKind, refs []core.SkillOrchestratorReference, failureClass core.SkillJobFailureClass, failureCode, blockedReason string, readyAt, now time.Time) error {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	job, err := hostedSkillJobByID(ctx, tx, scope, jobID, true)
	if err != nil {
		return err
	}
	var currentGeneration int64
	if err := tx.QueryRow(ctx, `SELECT generation FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, scope.TenantID, scope.WorkspaceID, job.WorkflowID).Scan(&currentGeneration); err != nil {
		return mapHostedSkillNotFound(err)
	}
	if currentGeneration != generation {
		return ErrSkillOrchestratorGeneration
	}
	if job.State == target && job.Fence == fence && job.ResultKind == resultKind && hostedSkillRefsEqual(job.ResultReferences, refs) && job.FailureClass == failureClass && job.FailureCode == failureCode && job.BlockedReason == blockedReason {
		var attemptOwner string
		if err := tx.QueryRow(ctx, `SELECT owner FROM saas_skill_orchestrator_job_attempts WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND job_id=$3::uuid AND fence=$4`, scope.TenantID, scope.WorkspaceID, job.ID, fence).Scan(&attemptOwner); err == nil && attemptOwner == owner {
			return tx.Commit(ctx)
		}
	}
	if job.State != core.SkillJobRunning || job.LeaseOwner != owner || job.Fence != fence || !job.LeaseExpiresAt.After(now) {
		return ErrSkillOrchestratorStaleLease
	}
	job.State, job.ResultKind, job.ResultReferences = target, resultKind, refs
	job.FailureClass, job.FailureCode, job.BlockedReason = failureClass, failureCode, blockedReason
	job.LeaseOwner, job.LeaseExpiresAt, job.TimeoutAt, job.UpdatedAt = "", time.Time{}, time.Time{}, now
	if !readyAt.IsZero() {
		job.ReadyAt = readyAt
	}
	if target.Terminal() {
		job.CompletedAt = now
	}
	if err := job.Validate(); err != nil {
		return err
	}
	referencesJSON := marshalHostedSkillReferences(refs)
	tag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET state=$7,ready_at=$8,blocked_reason=$9,lease_owner='',lease_expires_at=NULL,timeout_at=NULL,result_kind=$10,result_references=$11::jsonb,failure_class=$12,failure_code=$13,updated_at=$14,completed_at=$15 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='running' AND lease_owner=$4 AND fence=$5 AND lease_expires_at>$6`, scope.TenantID, scope.WorkspaceID, job.ID, owner, fence, now, job.State, job.ReadyAt, job.BlockedReason, job.ResultKind, referencesJSON, job.FailureClass, job.FailureCode, now, nullableSkillOrchestratorTime(job.CompletedAt))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSkillOrchestratorStaleLease
	}
	attemptResult := resultKind
	if attemptResult == core.SkillJobResultNone {
		attemptResult = core.SkillJobResultRejected
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_job_attempts SET ended_at=$6,result_kind=$7,failure_class=$8,failure_code=$9,duration_ms=GREATEST(0,(EXTRACT(EPOCH FROM ($6-started_at))*1000)::bigint) WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND job_id=$3::uuid AND owner=$4 AND fence=$5 AND ended_at IS NULL`, scope.TenantID, scope.WorkspaceID, job.ID, owner, fence, now, attemptResult, failureClass, firstHostedNonEmpty(failureCode, blockedReason)); err != nil {
		return err
	}
	if err := insertHostedSkillEvent(ctx, tx, job, "job_transition", string(core.SkillJobRunning), string(target), owner, fence, firstHostedNonEmpty(failureCode, blockedReason), now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *SkillOrchestratorRepository) CancelSkillJob(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, generation int64, actor string, now time.Time) error {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	job, err := hostedSkillJobByID(ctx, tx, scope, jobID, true)
	if err != nil {
		return err
	}
	var currentGeneration int64
	if err := tx.QueryRow(ctx, `SELECT generation FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, scope.TenantID, scope.WorkspaceID, job.WorkflowID).Scan(&currentGeneration); err != nil {
		return mapHostedSkillNotFound(err)
	}
	if currentGeneration != generation {
		return ErrSkillOrchestratorGeneration
	}
	if job.State == core.SkillJobCancelled {
		return tx.Commit(ctx)
	}
	if job.State == core.SkillJobRunning {
		if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET cancel_requested_at=$4,updated_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='running'`, scope.TenantID, scope.WorkspaceID, job.ID, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if job.State.Terminal() || !core.CanTransitionSkillJob(job.State, core.SkillJobCancelled) {
		return ErrSkillOrchestratorConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET state='cancelled',result_kind='cancelled',failure_class='cancellation',failure_code='operator_cancelled',completed_at=$5,updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state=$4`, scope.TenantID, scope.WorkspaceID, job.ID, job.State, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSkillOrchestratorConflict
	}
	if err := insertHostedSkillEvent(ctx, tx, job, "job_cancelled", string(job.State), string(core.SkillJobCancelled), actor, job.Fence, "operator_cancelled", now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *SkillOrchestratorRepository) GetSkillJob(ctx context.Context, scope core.SkillOrchestratorScope, jobID string) (core.SkillJob, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return core.SkillJob{}, err
	}
	defer tx.Rollback(ctx)
	job, err := hostedSkillJobByID(ctx, tx, scope, jobID, false)
	if err != nil {
		return core.SkillJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SkillJob{}, err
	}
	return job, nil
}

func (r *SkillOrchestratorRepository) ListSkillJobs(ctx context.Context, scope core.SkillOrchestratorScope, workflowID, afterID string, limit int) ([]core.SkillJob, string, error) {
	if limit < 1 || limit > 200 {
		return nil, "", errors.New("invalid skill job page")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+hostedSkillJobColumns+` FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND workflow_id=$3::uuid AND ($4='' OR id>$4::uuid) ORDER BY id LIMIT $5`, scope.TenantID, scope.WorkspaceID, workflowID, afterID, limit+1)
	if err != nil {
		return nil, "", err
	}
	var jobs []core.SkillJob
	for rows.Next() {
		job, err := scanHostedSkillJob(rows)
		if err != nil {
			rows.Close()
			return nil, "", err
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	next := ""
	if len(jobs) > limit {
		next = jobs[limit-1].ID
		jobs = jobs[:limit]
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return jobs, next, nil
}

func (r *SkillOrchestratorRepository) begin(ctx context.Context, scope core.SkillOrchestratorScope) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("skill orchestrator PostgreSQL repository is not configured")
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if scope.TenantID == "" {
		return nil, errors.New("hosted skill orchestrator requires tenant_id")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true),set_config('app.workspace_id',$2,true)", scope.TenantID, scope.WorkspaceID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

const hostedSkillJobColumns = `id::text,workflow_id::text,tenant_id::text,workspace_id::text,environment,skill_id,stage,contract_version,input_digest,policy_version,state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,timeout_at,cancel_requested_at,result_kind,result_references,failure_class,failure_code,created_at,updated_at,completed_at`

func qualifiedHostedSkillJobColumns(alias string) string {
	columns := strings.Split(hostedSkillJobColumns, ",")
	for index := range columns {
		columns[index] = alias + "." + columns[index]
	}
	return strings.Join(columns, ",")
}

type hostedSkillScanner interface{ Scan(...any) error }

func scanHostedSkillJob(scanner hostedSkillScanner) (core.SkillJob, error) {
	var job core.SkillJob
	var lease, timeout, cancel, completed *time.Time
	var refs []byte
	if err := scanner.Scan(&job.ID, &job.WorkflowID, &job.Scope.TenantID, &job.Scope.WorkspaceID, &job.Scope.Environment,
		&job.SkillID, &job.Stage, &job.ContractVersion, &job.InputDigest, &job.PolicyVersion, &job.State, &job.Priority,
		&job.ReadyAt, &job.DependencyCount, &job.BlockedReason, &job.Attempt, &job.MaxAttempts, &job.LeaseOwner,
		&lease, &job.Fence, &timeout, &cancel, &job.ResultKind, &refs, &job.FailureClass, &job.FailureCode,
		&job.CreatedAt, &job.UpdatedAt, &completed); err != nil {
		return core.SkillJob{}, err
	}
	if lease != nil {
		job.LeaseExpiresAt = *lease
	}
	if timeout != nil {
		job.TimeoutAt = *timeout
	}
	if cancel != nil {
		job.CancelRequestedAt = *cancel
	}
	if completed != nil {
		job.CompletedAt = *completed
	}
	if err := json.Unmarshal(refs, &job.ResultReferences); err != nil {
		return core.SkillJob{}, err
	}
	if err := job.Validate(); err != nil {
		return core.SkillJob{}, fmt.Errorf("stored hosted skill job is invalid: %w", err)
	}
	return job, nil
}

func scanHostedSkillJobCreated(scanner hostedSkillScanner) (core.SkillJob, bool, error) {
	// scanHostedSkillJob cannot consume the trailing flag, so mirror its scan once.
	var job core.SkillJob
	var lease, timeout, cancel, completed *time.Time
	var refs []byte
	var created bool
	if err := scanner.Scan(&job.ID, &job.WorkflowID, &job.Scope.TenantID, &job.Scope.WorkspaceID, &job.Scope.Environment,
		&job.SkillID, &job.Stage, &job.ContractVersion, &job.InputDigest, &job.PolicyVersion, &job.State, &job.Priority,
		&job.ReadyAt, &job.DependencyCount, &job.BlockedReason, &job.Attempt, &job.MaxAttempts, &job.LeaseOwner,
		&lease, &job.Fence, &timeout, &cancel, &job.ResultKind, &refs, &job.FailureClass, &job.FailureCode,
		&job.CreatedAt, &job.UpdatedAt, &completed, &created); err != nil {
		return core.SkillJob{}, false, err
	}
	if lease != nil {
		job.LeaseExpiresAt = *lease
	}
	if timeout != nil {
		job.TimeoutAt = *timeout
	}
	if cancel != nil {
		job.CancelRequestedAt = *cancel
	}
	if completed != nil {
		job.CompletedAt = *completed
	}
	if err := json.Unmarshal(refs, &job.ResultReferences); err != nil {
		return core.SkillJob{}, false, err
	}
	if err := job.Validate(); err != nil {
		return core.SkillJob{}, false, err
	}
	return job, created, nil
}

func scanHostedSkillWorkflowCreated(scanner hostedSkillScanner) (core.SkillWorkflow, bool, error) {
	var workflow core.SkillWorkflow
	var terminal *time.Time
	var created bool
	if err := scanner.Scan(&workflow.ID, &workflow.Scope.TenantID, &workflow.Scope.WorkspaceID, &workflow.Scope.Environment,
		&workflow.SkillID, &workflow.OriginKind, &workflow.OriginID, &workflow.Kind, &workflow.ContractVersion,
		&workflow.InputDigest, &workflow.State, &workflow.CurrentStage, &workflow.Generation, &workflow.ConfigurationVersion,
		&workflow.PolicyDigest, &workflow.CreatedAt, &workflow.UpdatedAt, &terminal, &created); err != nil {
		return core.SkillWorkflow{}, false, err
	}
	if terminal != nil {
		workflow.TerminalAt = *terminal
	}
	if err := workflow.Validate(); err != nil {
		return core.SkillWorkflow{}, false, err
	}
	return workflow, created, nil
}

func hostedSkillJobByID(ctx context.Context, tx pgx.Tx, scope core.SkillOrchestratorScope, id string, lock bool) (core.SkillJob, error) {
	query := `SELECT ` + hostedSkillJobColumns + ` FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`
	if lock {
		query += ` FOR UPDATE`
	}
	job, err := scanHostedSkillJob(tx.QueryRow(ctx, query, scope.TenantID, scope.WorkspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SkillJob{}, ErrSkillOrchestratorNotFound
	}
	return job, err
}

func insertHostedSkillEvent(ctx context.Context, tx pgx.Tx, job core.SkillJob, kind, from, to, actor string, fence int64, reason string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_events(tenant_id,workspace_id,workflow_id,job_id,event_kind,from_state,to_state,actor_id,fence,reason_code,created_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11)`, job.Scope.TenantID, job.Scope.WorkspaceID, job.WorkflowID, job.ID, kind, from, to, actor, fence, reason, now)
	return err
}

func deterministicHostedAttemptID(jobID string, attempt int) string {
	return uuid.NewSHA1(uuid.MustParse(jobID), []byte(strconv.Itoa(attempt))).String()
}

func nullableSkillOrchestratorTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
func mapHostedSkillNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSkillOrchestratorNotFound
	}
	return err
}
func firstHostedNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func hostedSkillRefsEqual(left, right []core.SkillOrchestratorReference) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func marshalHostedSkillReferences(references []core.SkillOrchestratorReference) []byte {
	if references == nil {
		references = []core.SkillOrchestratorReference{}
	}
	encoded, _ := json.Marshal(references)
	return encoded
}
