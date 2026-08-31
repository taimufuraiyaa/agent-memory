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

func (r *SkillOrchestratorRepository) RouteSkillSignal(ctx context.Context, workflow core.SkillWorkflow, job core.SkillJob, dependencies []core.SkillJobDependency) (contracts.SkillSignalRouteResult, error) {
	job.DependencyCount = len(dependencies)
	if err := workflow.Validate(); err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if err := job.Validate(); err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if job.WorkflowID != workflow.ID || job.Scope != workflow.Scope || job.InputDigest != workflow.InputDigest {
		return contracts.SkillSignalRouteResult{}, ErrSkillOrchestratorScope
	}
	for index := range dependencies {
		dependencies[index].JobID = job.ID
		if err := dependencies[index].Validate(); err != nil {
			return contracts.SkillSignalRouteResult{}, err
		}
	}
	tx, err := r.begin(ctx, workflow.Scope)
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	defer tx.Rollback(ctx)
	workflowTag, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_workflows(
		tenant_id,workspace_id,id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,
		input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
	ON CONFLICT(tenant_id,workspace_id,environment,workflow_kind,origin_kind,origin_id,input_digest) DO NOTHING`,
		workflow.Scope.TenantID, workflow.Scope.WorkspaceID, workflow.ID, workflow.Scope.Environment, workflow.SkillID,
		workflow.OriginKind, workflow.OriginID, workflow.Kind, workflow.ContractVersion, workflow.InputDigest,
		workflow.State, workflow.CurrentStage, workflow.Generation, workflow.ConfigurationVersion, workflow.PolicyDigest,
		workflow.CreatedAt, workflow.UpdatedAt, nullableSkillOrchestratorTime(workflow.TerminalAt))
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	storedWorkflow, _, err := scanHostedSkillWorkflowCreated(tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,workspace_id::text,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at,false FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND workflow_kind=$4 AND origin_kind=$5 AND origin_id=$6 AND input_digest=$7`, workflow.Scope.TenantID, workflow.Scope.WorkspaceID, workflow.Scope.Environment, workflow.Kind, workflow.OriginKind, workflow.OriginID, workflow.InputDigest))
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if storedWorkflow.ID != workflow.ID || storedWorkflow.PolicyDigest != workflow.PolicyDigest {
		return contracts.SkillSignalRouteResult{}, ErrSkillOrchestratorConflict
	}
	refs := marshalHostedSkillReferences(nil)
	jobTag, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_jobs(
		tenant_id,workspace_id,id,workflow_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
		state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,
		timeout_at,cancel_requested_at,result_kind,result_references,failure_class,failure_code,replay_of_job_id,created_at,updated_at,completed_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24::jsonb,$25,$26,NULLIF($27,'')::uuid,$28,$29,$30)
	ON CONFLICT(tenant_id,workspace_id,workflow_id,stage,input_digest) DO NOTHING`,
		job.Scope.TenantID, job.Scope.WorkspaceID, job.ID, job.WorkflowID, job.Scope.Environment, job.SkillID,
		job.Stage, job.ContractVersion, job.InputDigest, job.PolicyVersion, job.State, job.Priority, job.ReadyAt,
		job.DependencyCount, job.BlockedReason, job.Attempt, job.MaxAttempts, job.LeaseOwner,
		nullableSkillOrchestratorTime(job.LeaseExpiresAt), job.Fence, nullableSkillOrchestratorTime(job.TimeoutAt),
		nullableSkillOrchestratorTime(job.CancelRequestedAt), job.ResultKind, refs, job.FailureClass, job.FailureCode, job.ReplayOfJobID,
		job.CreatedAt, job.UpdatedAt, nullableSkillOrchestratorTime(job.CompletedAt))
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	storedJob, err := scanHostedSkillJob(tx.QueryRow(ctx, `SELECT `+hostedSkillJobColumns+` FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND workflow_id=$3::uuid AND stage=$4 AND input_digest=$5`, job.Scope.TenantID, job.Scope.WorkspaceID, job.WorkflowID, job.Stage, job.InputDigest))
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if storedJob.ID != job.ID || storedJob.Scope != job.Scope {
		return contracts.SkillSignalRouteResult{}, ErrSkillOrchestratorConflict
	}
	if jobTag.RowsAffected() == 1 {
		for _, dependency := range dependencies {
			accepted, _ := json.Marshal(dependency.AcceptedResultKinds)
			if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_job_dependencies(tenant_id,workspace_id,job_id,parent_job_id,accepted_result_kinds,created_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::jsonb,$6)`, job.Scope.TenantID, job.Scope.WorkspaceID, dependency.JobID, dependency.ParentJobID, accepted, dependency.CreatedAt); err != nil {
				return contracts.SkillSignalRouteResult{}, err
			}
		}
		if err := insertHostedSkillEvent(ctx, tx, storedJob, "signal_routed", "", string(job.State), "router", 0, "", job.CreatedAt); err != nil {
			return contracts.SkillSignalRouteResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	return contracts.SkillSignalRouteResult{Workflow: storedWorkflow, Job: storedJob, Dependencies: dependencies, Created: workflowTag.RowsAffected() == 1 || jobTag.RowsAffected() == 1}, nil
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
			timeout_at,cancel_requested_at,result_kind,result_references,failure_class,failure_code,replay_of_job_id,created_at,updated_at,completed_at
		) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24::jsonb,$25,$26,NULLIF($27,'')::uuid,$28,$29,$30)
		ON CONFLICT(tenant_id,workspace_id,workflow_id,stage,input_digest) DO NOTHING
		RETURNING `+hostedSkillJobColumns+`,true
	) SELECT * FROM inserted UNION ALL SELECT `+hostedSkillJobColumns+`,false
	FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND workflow_id=$4::uuid AND stage=$7 AND input_digest=$9 LIMIT 1`,
		job.Scope.TenantID, job.Scope.WorkspaceID, job.ID, job.WorkflowID, job.Scope.Environment, job.SkillID,
		job.Stage, job.ContractVersion, job.InputDigest, job.PolicyVersion, job.State, job.Priority, job.ReadyAt,
		job.DependencyCount, job.BlockedReason, job.Attempt, job.MaxAttempts, job.LeaseOwner,
		nullableSkillOrchestratorTime(job.LeaseExpiresAt), job.Fence, nullableSkillOrchestratorTime(job.TimeoutAt),
		nullableSkillOrchestratorTime(job.CancelRequestedAt), job.ResultKind, refs, job.FailureClass, job.FailureCode, job.ReplayOfJobID,
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

func (r *SkillOrchestratorRepository) ScheduleSkillSuccessor(ctx context.Context, input contracts.SkillSuccessorSchedule) (core.SkillJob, bool, error) {
	job := input.Job
	job.DependencyCount = len(input.Dependencies)
	if input.ExpectedWorkflowGeneration < 1 || input.Now.IsZero() || len(input.Dependencies) == 0 {
		return core.SkillJob{}, false, errors.New("invalid skill successor schedule")
	}
	if err := job.Validate(); err != nil {
		return core.SkillJob{}, false, err
	}
	for index := range input.Dependencies {
		input.Dependencies[index].JobID = job.ID
		if err := input.Dependencies[index].Validate(); err != nil {
			return core.SkillJob{}, false, err
		}
	}
	tx, err := r.begin(ctx, job.Scope)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	defer tx.Rollback(ctx)
	var generation int64
	var state core.SkillWorkflowState
	var currentStage core.SkillOrchestratorStage
	if err := tx.QueryRow(ctx, `SELECT generation,state,current_stage FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid FOR UPDATE`, job.Scope.TenantID, job.Scope.WorkspaceID, job.WorkflowID).Scan(&generation, &state, &currentStage); err != nil {
		return core.SkillJob{}, false, mapHostedSkillNotFound(err)
	}
	existing, existingErr := hostedSkillJobByBinding(ctx, tx, job.Scope, job.WorkflowID, job.Stage, job.InputDigest)
	if existingErr == nil {
		if existing.ID != job.ID || generation != input.ExpectedWorkflowGeneration+1 || currentStage != job.Stage {
			return core.SkillJob{}, false, ErrSkillOrchestratorConflict
		}
		if matches, err := hostedSkillDependenciesMatch(ctx, tx, job.Scope, job.ID, input.Dependencies); err != nil {
			return core.SkillJob{}, false, err
		} else if !matches {
			return core.SkillJob{}, false, ErrSkillOrchestratorConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return core.SkillJob{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return core.SkillJob{}, false, existingErr
	}
	if generation != input.ExpectedWorkflowGeneration {
		return core.SkillJob{}, false, ErrSkillOrchestratorGeneration
	}
	if state != core.SkillWorkflowOpen {
		return core.SkillJob{}, false, ErrSkillOrchestratorConflict
	}
	row := tx.QueryRow(ctx, `WITH inserted AS (
		INSERT INTO saas_skill_orchestrator_jobs(
			tenant_id,workspace_id,id,workflow_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
			state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,
			timeout_at,cancel_requested_at,result_kind,result_references,failure_class,failure_code,replay_of_job_id,created_at,updated_at,completed_at
		) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,'[]'::jsonb,$24,$25,NULLIF($26,'')::uuid,$27,$28,$29)
		ON CONFLICT(tenant_id,workspace_id,workflow_id,stage,input_digest) DO NOTHING
		RETURNING `+hostedSkillJobColumns+`,true
	) SELECT * FROM inserted UNION ALL SELECT `+hostedSkillJobColumns+`,false FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND workflow_id=$4::uuid AND stage=$7 AND input_digest=$9 LIMIT 1`,
		job.Scope.TenantID, job.Scope.WorkspaceID, job.ID, job.WorkflowID, job.Scope.Environment, job.SkillID,
		job.Stage, job.ContractVersion, job.InputDigest, job.PolicyVersion, job.State, job.Priority, job.ReadyAt,
		job.DependencyCount, job.BlockedReason, job.Attempt, job.MaxAttempts, job.LeaseOwner,
		nullableSkillOrchestratorTime(job.LeaseExpiresAt), job.Fence, nullableSkillOrchestratorTime(job.TimeoutAt),
		nullableSkillOrchestratorTime(job.CancelRequestedAt), job.ResultKind, job.FailureClass, job.FailureCode, job.ReplayOfJobID,
		job.CreatedAt, job.UpdatedAt, nullableSkillOrchestratorTime(job.CompletedAt))
	stored, created, err := scanHostedSkillJobCreated(row)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	if stored.ID != job.ID {
		return core.SkillJob{}, false, ErrSkillOrchestratorConflict
	}
	if created {
		for _, dependency := range input.Dependencies {
			accepted, _ := json.Marshal(dependency.AcceptedResultKinds)
			if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_job_dependencies(tenant_id,workspace_id,job_id,parent_job_id,accepted_result_kinds,created_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::jsonb,$6)`, job.Scope.TenantID, job.Scope.WorkspaceID, dependency.JobID, dependency.ParentJobID, accepted, dependency.CreatedAt); err != nil {
				return core.SkillJob{}, false, err
			}
		}
		if err := insertHostedSkillEvent(ctx, tx, job, "successor_scheduled", "", string(job.State), "dependency_coordinator", 0, "", input.Now); err != nil {
			return core.SkillJob{}, false, err
		}
	}
	if matches, err := hostedSkillDependenciesMatch(ctx, tx, job.Scope, job.ID, input.Dependencies); err != nil {
		return core.SkillJob{}, false, err
	} else if !matches {
		return core.SkillJob{}, false, ErrSkillOrchestratorConflict
	}
	workflowTag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_workflows SET current_stage=$4,generation=generation+1,updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND generation=$6 AND state='open'`, job.Scope.TenantID, job.Scope.WorkspaceID, job.WorkflowID, job.Stage, input.Now, input.ExpectedWorkflowGeneration)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	if workflowTag.RowsAffected() != 1 {
		return core.SkillJob{}, false, ErrSkillOrchestratorGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SkillJob{}, false, err
	}
	return stored, created, nil
}

func hostedSkillDependenciesMatch(ctx context.Context, tx pgx.Tx, scope core.SkillOrchestratorScope, jobID string, expected []core.SkillJobDependency) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT parent_job_id::text,accepted_result_kinds FROM saas_skill_orchestrator_job_dependencies WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND job_id=$3::uuid`, scope.TenantID, scope.WorkspaceID, jobID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	actual := make(map[string]map[core.SkillJobResultKind]bool, len(expected))
	for rows.Next() {
		var parentID string
		var acceptedJSON []byte
		if err := rows.Scan(&parentID, &acceptedJSON); err != nil {
			return false, err
		}
		var accepted []core.SkillJobResultKind
		if err := json.Unmarshal(acceptedJSON, &accepted); err != nil {
			return false, err
		}
		actual[parentID] = make(map[core.SkillJobResultKind]bool, len(accepted))
		for _, kind := range accepted {
			actual[parentID][kind] = true
		}
	}
	if err := rows.Err(); err != nil || len(actual) != len(expected) {
		return false, err
	}
	for _, dependency := range expected {
		accepted, ok := actual[dependency.ParentJobID]
		if !ok || len(accepted) != len(dependency.AcceptedResultKinds) {
			return false, nil
		}
		for _, kind := range dependency.AcceptedResultKinds {
			if !accepted[kind] {
				return false, nil
			}
		}
	}
	return true, nil
}

func (r *SkillOrchestratorRepository) ResolveSkillJobDependencies(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, expectedGeneration int64, now time.Time) (contracts.SkillDependencyResolution, error) {
	if expectedGeneration < 1 || now.IsZero() || strings.TrimSpace(jobID) == "" {
		return contracts.SkillDependencyResolution{}, errors.New("invalid skill dependency resolution")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	defer tx.Rollback(ctx)
	job, err := hostedSkillJobByID(ctx, tx, scope, jobID, true)
	if err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	workflow, _, err := scanHostedSkillWorkflowCreated(tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,workspace_id::text,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at,false FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid FOR UPDATE`, scope.TenantID, scope.WorkspaceID, job.WorkflowID))
	if err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	if workflow.Generation != expectedGeneration {
		return contracts.SkillDependencyResolution{}, ErrSkillOrchestratorGeneration
	}
	state := contracts.SkillDependenciesPending
	terminalCode := ""
	if workflow.State != core.SkillWorkflowOpen {
		if workflow.State == core.SkillWorkflowCancelled {
			state, terminalCode = contracts.SkillDependenciesCancelled, "workflow_cancelled"
		} else {
			state, terminalCode = contracts.SkillDependenciesRejected, "workflow_not_open"
		}
	} else {
		rows, err := tx.Query(ctx, `SELECT p.state,p.result_kind,d.accepted_result_kinds FROM saas_skill_orchestrator_job_dependencies d JOIN saas_skill_orchestrator_jobs p ON p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id AND p.id=d.parent_job_id WHERE d.tenant_id=$1::uuid AND d.workspace_id=$2::uuid AND d.job_id=$3::uuid ORDER BY d.parent_job_id FOR UPDATE OF p`, scope.TenantID, scope.WorkspaceID, job.ID)
		if err != nil {
			return contracts.SkillDependencyResolution{}, err
		}
		count, pending, incompatible, cancelled := 0, 0, false, false
		for rows.Next() {
			count++
			var parentState core.SkillJobState
			var result core.SkillJobResultKind
			var acceptedJSON []byte
			if err := rows.Scan(&parentState, &result, &acceptedJSON); err != nil {
				rows.Close()
				return contracts.SkillDependencyResolution{}, err
			}
			if !parentState.Terminal() {
				pending++
				continue
			}
			var accepted []core.SkillJobResultKind
			if err := json.Unmarshal(acceptedJSON, &accepted); err != nil {
				rows.Close()
				return contracts.SkillDependencyResolution{}, err
			}
			matched := false
			for _, allowed := range accepted {
				matched = matched || allowed == result
			}
			if !matched {
				incompatible = true
				cancelled = cancelled || result == core.SkillJobResultCancelled
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return contracts.SkillDependencyResolution{}, err
		}
		rows.Close()
		if count == 0 {
			return contracts.SkillDependencyResolution{}, errors.New("skill job has no dependencies")
		}
		switch {
		case incompatible && cancelled:
			state, terminalCode = contracts.SkillDependenciesCancelled, "dependency_cancelled"
		case incompatible:
			state, terminalCode = contracts.SkillDependenciesRejected, "dependency_rejected"
		case pending > 0:
			state = contracts.SkillDependenciesPending
		default:
			state = contracts.SkillDependenciesReady
		}
	}
	changed := false
	if !job.State.Terminal() {
		from := job.State
		switch state {
		case contracts.SkillDependenciesReady:
			if job.State != core.SkillJobQueued || job.DependencyCount != 0 || job.BlockedReason != "" {
				job.State, job.DependencyCount, job.BlockedReason, job.ReadyAt, job.UpdatedAt, changed = core.SkillJobQueued, 0, "", now, now, true
			}
		case contracts.SkillDependenciesPending:
			if job.State != core.SkillJobBlocked || job.BlockedReason != "dependencies_pending" {
				job.State, job.BlockedReason, job.UpdatedAt, changed = core.SkillJobBlocked, "dependencies_pending", now, true
			}
		case contracts.SkillDependenciesRejected, contracts.SkillDependenciesCancelled:
			job.State, job.ResultKind, job.FailureClass = core.SkillJobCompleted, core.SkillJobResultRejected, core.SkillFailurePermanentValidation
			if state == contracts.SkillDependenciesCancelled {
				job.State, job.ResultKind, job.FailureClass = core.SkillJobCancelled, core.SkillJobResultCancelled, core.SkillFailureCancellation
			}
			job.FailureCode, job.UpdatedAt, job.CompletedAt, changed = terminalCode, now, now, true
		}
		if changed {
			if err := job.Validate(); err != nil {
				return contracts.SkillDependencyResolution{}, err
			}
			tag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET state=$4,ready_at=$5,dependency_count=$6,blocked_reason=$7,result_kind=$8,failure_class=$9,failure_code=$10,updated_at=$11,completed_at=$12 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state=$13`, scope.TenantID, scope.WorkspaceID, job.ID, job.State, job.ReadyAt, job.DependencyCount, job.BlockedReason, job.ResultKind, job.FailureClass, job.FailureCode, job.UpdatedAt, nullableSkillOrchestratorTime(job.CompletedAt), from)
			if err != nil {
				return contracts.SkillDependencyResolution{}, err
			}
			if tag.RowsAffected() != 1 {
				return contracts.SkillDependencyResolution{}, ErrSkillOrchestratorConflict
			}
			if err := insertHostedSkillEvent(ctx, tx, job, "dependencies_resolved", string(from), string(job.State), "dependency_coordinator", job.Fence, terminalCode, now); err != nil {
				return contracts.SkillDependencyResolution{}, err
			}
		}
	}
	if state == contracts.SkillDependenciesRejected && workflow.State == core.SkillWorkflowOpen {
		workflow.State, workflow.UpdatedAt, workflow.TerminalAt = core.SkillWorkflowRejected, now, now
		if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_workflows SET state='rejected',updated_at=$4,terminal_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND generation=$5 AND state='open'`, scope.TenantID, scope.WorkspaceID, workflow.ID, now, expectedGeneration); err != nil {
			return contracts.SkillDependencyResolution{}, err
		}
	}
	if state == contracts.SkillDependenciesCancelled && workflow.State == core.SkillWorkflowOpen {
		workflow.State, workflow.UpdatedAt, workflow.TerminalAt = core.SkillWorkflowCancelled, now, now
		if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_workflows SET state='cancelled',updated_at=$4,terminal_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND generation=$5 AND state='open'`, scope.TenantID, scope.WorkspaceID, workflow.ID, now, expectedGeneration); err != nil {
			return contracts.SkillDependencyResolution{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	return contracts.SkillDependencyResolution{Workflow: workflow, Job: job, State: state, Changed: changed}, nil
}

func (r *SkillOrchestratorRepository) ClaimSkillJobs(ctx context.Context, scope core.SkillOrchestratorScope, owner string, limit int, lease, timeout time.Duration, now time.Time) ([]core.SkillJob, error) {
	return r.claimSkillJobs(ctx, scope, owner, limit, lease, timeout, now, "")
}

func (r *SkillOrchestratorRepository) SkillOrchestratorQueueSnapshots(ctx context.Context, scope core.SkillOrchestratorScope) ([]core.SkillOrchestratorQueueSnapshot, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT stage,state,failure_class,COUNT(*)::int,MIN(CASE WHEN state IN ('queued','retry_wait') THEN ready_at ELSE updated_at END) FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND state IN ('queued','blocked','running','retry_wait','dead_lettered') GROUP BY stage,state,failure_class ORDER BY stage,state,failure_class`, scope.TenantID, scope.WorkspaceID, scope.Environment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []core.SkillOrchestratorQueueSnapshot{}
	for rows.Next() {
		var value core.SkillOrchestratorQueueSnapshot
		if err := rows.Scan(&value.Stage, &value.State, &value.FailureClass, &value.Depth, &value.OldestAt); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *SkillOrchestratorRepository) ClaimSkillJobsByLane(ctx context.Context, scope core.SkillOrchestratorScope, owner string, limit int, lease, timeout time.Duration, now time.Time, rollback bool) ([]core.SkillJob, error) {
	lane := "ordinary"
	if rollback {
		lane = "rollback"
	}
	return r.claimSkillJobs(ctx, scope, owner, limit, lease, timeout, now, lane)
}

func (r *SkillOrchestratorRepository) claimSkillJobs(ctx context.Context, scope core.SkillOrchestratorScope, owner string, limit int, lease, timeout time.Duration, now time.Time, lane string) ([]core.SkillJob, error) {
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
		AND ($9='' OR ($9='rollback' AND j.stage='rollback') OR ($9='ordinary' AND j.stage<>'rollback'))
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
		RETURNING `+qualifiedHostedSkillJobColumns("j"), scope.TenantID, scope.WorkspaceID, scope.Environment, now, limit, owner, now.Add(lease), now.Add(timeout), lane)
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

func (r *SkillOrchestratorRepository) SkillWorkflowGeneration(ctx context.Context, scope core.SkillOrchestratorScope, workflowID string) (int64, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var generation int64
	if err := tx.QueryRow(ctx, `SELECT generation FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, scope.TenantID, scope.WorkspaceID, workflowID).Scan(&generation); err != nil {
		return 0, mapHostedSkillNotFound(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return generation, nil
}

func (r *SkillOrchestratorRepository) FinalizeSkillJob(ctx context.Context, input contracts.SkillJobFinalization) error {
	target := core.SkillJobCompleted
	if input.DeadLetter {
		target = core.SkillJobDeadLettered
	} else if input.ResultKind == core.SkillJobResultCancelled {
		target = core.SkillJobCancelled
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

func (r *SkillOrchestratorRepository) GetSkillWorkflow(ctx context.Context, scope core.SkillOrchestratorScope, workflowID string) (core.SkillWorkflow, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return core.SkillWorkflow{}, err
	}
	defer tx.Rollback(ctx)
	workflow, _, err := scanHostedSkillWorkflowCreated(tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,workspace_id::text,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at,false FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, scope.TenantID, scope.WorkspaceID, workflowID))
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SkillWorkflow{}, ErrSkillOrchestratorNotFound
	}
	if err != nil {
		return core.SkillWorkflow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SkillWorkflow{}, err
	}
	return workflow, nil
}

func (r *SkillOrchestratorRepository) LoadSkillReconciliationCursor(ctx context.Context, scope core.SkillOrchestratorScope, domain core.SkillReconciliationDomain, configurationVersion int64, now time.Time) (core.SkillReconciliationCursor, error) {
	if !domain.Valid() || configurationVersion < 1 || now.IsZero() {
		return core.SkillReconciliationCursor{}, errors.New("invalid skill reconciliation cursor load")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_reconciliation_cursors(tenant_id,workspace_id,environment,domain,cursor_value,configuration_version,updated_at) VALUES($1::uuid,$2::uuid,$3,$4,'',$5,$6) ON CONFLICT(tenant_id,workspace_id,environment,domain) DO NOTHING`, scope.TenantID, scope.WorkspaceID, scope.Environment, domain, configurationVersion, now); err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	cursor, err := scanHostedSkillReconciliationCursor(tx.QueryRow(ctx, `SELECT tenant_id::text,workspace_id::text,environment,domain,cursor_value,configuration_version,last_completed_at,scanned,repaired,skipped,blocked,failed,updated_at FROM saas_skill_orchestrator_reconciliation_cursors WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND domain=$4 FOR UPDATE`, scope.TenantID, scope.WorkspaceID, scope.Environment, domain))
	if err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	if cursor.ConfigurationVersion != configurationVersion {
		if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_reconciliation_cursors SET cursor_value='',configuration_version=$5,last_completed_at=NULL,scanned=0,repaired=0,skipped=0,blocked=0,failed=0,updated_at=$6 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND domain=$4`, scope.TenantID, scope.WorkspaceID, scope.Environment, domain, configurationVersion, now); err != nil {
			return core.SkillReconciliationCursor{}, err
		}
		cursor = core.SkillReconciliationCursor{Scope: scope, Domain: domain, ConfigurationVersion: configurationVersion, UpdatedAt: now}
	}
	if err := tx.Commit(ctx); err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	return cursor, nil
}

func (r *SkillOrchestratorRepository) SaveSkillReconciliationCursor(ctx context.Context, input contracts.SkillReconciliationCursorUpdate) error {
	if err := input.Cursor.Validate(); err != nil {
		return err
	}
	if input.ExpectedUpdatedAt.IsZero() || !input.Cursor.UpdatedAt.After(input.ExpectedUpdatedAt) {
		return errors.New("invalid skill reconciliation cursor compare-and-swap")
	}
	tx, err := r.begin(ctx, input.Cursor.Scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_reconciliation_cursors SET cursor_value=$5,configuration_version=$6,last_completed_at=$7,scanned=$8,repaired=$9,skipped=$10,blocked=$11,failed=$12,updated_at=$13 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND domain=$4 AND updated_at=$14`,
		input.Cursor.Scope.TenantID, input.Cursor.Scope.WorkspaceID, input.Cursor.Scope.Environment, input.Cursor.Domain,
		input.Cursor.Cursor, input.Cursor.ConfigurationVersion, nullableSkillOrchestratorTime(input.Cursor.LastCompletedAt),
		input.Cursor.Counters.Scanned, input.Cursor.Counters.Repaired, input.Cursor.Counters.Skipped, input.Cursor.Counters.Blocked,
		input.Cursor.Counters.Failed, input.Cursor.UpdatedAt, input.ExpectedUpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSkillOrchestratorConflict
	}
	return tx.Commit(ctx)
}

func scanHostedSkillReconciliationCursor(scanner hostedSkillScanner) (core.SkillReconciliationCursor, error) {
	var cursor core.SkillReconciliationCursor
	var completedAt *time.Time
	if err := scanner.Scan(&cursor.Scope.TenantID, &cursor.Scope.WorkspaceID, &cursor.Scope.Environment, &cursor.Domain, &cursor.Cursor,
		&cursor.ConfigurationVersion, &completedAt, &cursor.Counters.Scanned, &cursor.Counters.Repaired, &cursor.Counters.Skipped,
		&cursor.Counters.Blocked, &cursor.Counters.Failed, &cursor.UpdatedAt); err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	if completedAt != nil {
		cursor.LastCompletedAt = completedAt.UTC()
	}
	return cursor, cursor.Validate()
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

const hostedSkillJobColumns = `id::text,workflow_id::text,tenant_id::text,workspace_id::text,environment,skill_id,stage,contract_version,input_digest,policy_version,state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,timeout_at,cancel_requested_at,result_kind,result_references,failure_class,failure_code,CASE WHEN replay_of_job_id IS NULL THEN '' ELSE replay_of_job_id::text END,created_at,updated_at,completed_at`

func qualifiedHostedSkillJobColumns(alias string) string {
	columns := strings.Split(hostedSkillJobColumns, ",")
	for index := range columns {
		if strings.HasPrefix(columns[index], "CASE WHEN replay_of_job_id") {
			columns[index] = strings.ReplaceAll(columns[index], "replay_of_job_id", alias+".replay_of_job_id")
		} else {
			columns[index] = alias + "." + columns[index]
		}
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
		&lease, &job.Fence, &timeout, &cancel, &job.ResultKind, &refs, &job.FailureClass, &job.FailureCode, &job.ReplayOfJobID,
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
		&lease, &job.Fence, &timeout, &cancel, &job.ResultKind, &refs, &job.FailureClass, &job.FailureCode, &job.ReplayOfJobID,
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

func hostedSkillJobByBinding(ctx context.Context, tx pgx.Tx, scope core.SkillOrchestratorScope, workflowID string, stage core.SkillOrchestratorStage, digest string) (core.SkillJob, error) {
	job, err := scanHostedSkillJob(tx.QueryRow(ctx, `SELECT `+hostedSkillJobColumns+` FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND workflow_id=$3::uuid AND stage=$4 AND input_digest=$5`, scope.TenantID, scope.WorkspaceID, workflowID, stage, digest))
	if err != nil {
		return core.SkillJob{}, err
	}
	return job, nil
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
