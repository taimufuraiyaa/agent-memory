package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

type SkillJobFinalization = contracts.SkillJobFinalization
type SkillJobRetry = contracts.SkillJobRetry
type SkillJobBlock = contracts.SkillJobBlock

var _ contracts.SkillOrchestratorRepository = (*Store)(nil)

func (s *Store) CreateSkillWorkflow(ctx context.Context, workflow core.SkillWorkflow) (core.SkillWorkflow, bool, error) {
	if err := workflow.Validate(); err != nil {
		return core.SkillWorkflow{}, false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO skill_orchestrator_workflows(
		id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,
		input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(tenant_id,workspace_id,environment,workflow_kind,origin_kind,origin_id,input_digest) DO NOTHING`,
		workflow.ID, workflow.Scope.TenantID, workflow.Scope.WorkspaceID, workflow.Scope.Environment, workflow.SkillID,
		workflow.OriginKind, workflow.OriginID, workflow.Kind, workflow.ContractVersion, workflow.InputDigest,
		workflow.State, workflow.CurrentStage, workflow.Generation, workflow.ConfigurationVersion, workflow.PolicyDigest,
		formatSkillOrchestratorTime(workflow.CreatedAt), formatSkillOrchestratorTime(workflow.UpdatedAt), formatOptionalSkillOrchestratorTime(workflow.TerminalAt))
	if err != nil {
		return core.SkillWorkflow{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return core.SkillWorkflow{}, false, err
	}
	stored, err := s.skillWorkflowByOrigin(ctx, workflow)
	if err != nil {
		return core.SkillWorkflow{}, false, err
	}
	if stored.ID != workflow.ID || stored.ContractVersion != workflow.ContractVersion || stored.PolicyDigest != workflow.PolicyDigest {
		return core.SkillWorkflow{}, false, ErrSkillOrchestratorConflict
	}
	return stored, rows == 1, nil
}

func (s *Store) RouteSkillSignal(ctx context.Context, workflow core.SkillWorkflow, job core.SkillJob, dependencies []core.SkillJobDependency) (contracts.SkillSignalRouteResult, error) {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	defer tx.Rollback()
	workflowResult, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_workflows(
		id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,
		input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(tenant_id,workspace_id,environment,workflow_kind,origin_kind,origin_id,input_digest) DO NOTHING`,
		workflow.ID, workflow.Scope.TenantID, workflow.Scope.WorkspaceID, workflow.Scope.Environment, workflow.SkillID,
		workflow.OriginKind, workflow.OriginID, workflow.Kind, workflow.ContractVersion, workflow.InputDigest,
		workflow.State, workflow.CurrentStage, workflow.Generation, workflow.ConfigurationVersion, workflow.PolicyDigest,
		formatSkillOrchestratorTime(workflow.CreatedAt), formatSkillOrchestratorTime(workflow.UpdatedAt), formatOptionalSkillOrchestratorTime(workflow.TerminalAt))
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	storedWorkflow, err := skillWorkflowByOriginQuery(ctx, tx, workflow)
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if storedWorkflow.ID != workflow.ID || storedWorkflow.PolicyDigest != workflow.PolicyDigest {
		return contracts.SkillSignalRouteResult{}, ErrSkillOrchestratorConflict
	}
	refs := []byte("[]")
	jobResult, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_jobs(
		id,workflow_id,tenant_id,workspace_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
		state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,
		timeout_at,cancel_requested_at,result_kind,result_references_json,failure_class,failure_code,replay_of_job_id,created_at,updated_at,completed_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(workflow_id,stage,input_digest) DO NOTHING`,
		job.ID, job.WorkflowID, job.Scope.TenantID, job.Scope.WorkspaceID, job.Scope.Environment, job.SkillID,
		job.Stage, job.ContractVersion, job.InputDigest, job.PolicyVersion, job.State, job.Priority,
		formatSkillOrchestratorTime(job.ReadyAt), job.DependencyCount, job.BlockedReason, job.Attempt, job.MaxAttempts,
		job.LeaseOwner, formatOptionalSkillOrchestratorTime(job.LeaseExpiresAt), job.Fence,
		formatOptionalSkillOrchestratorTime(job.TimeoutAt), formatOptionalSkillOrchestratorTime(job.CancelRequestedAt),
		job.ResultKind, string(refs), job.FailureClass, job.FailureCode, job.ReplayOfJobID,
		formatSkillOrchestratorTime(job.CreatedAt), formatSkillOrchestratorTime(job.UpdatedAt), formatOptionalSkillOrchestratorTime(job.CompletedAt))
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	storedJob, err := skillJobByBinding(ctx, tx, job.WorkflowID, job.Stage, job.InputDigest)
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if storedJob.ID != job.ID || storedJob.Scope != job.Scope {
		return contracts.SkillSignalRouteResult{}, ErrSkillOrchestratorConflict
	}
	jobRows, _ := jobResult.RowsAffected()
	if jobRows == 1 {
		for _, dependency := range dependencies {
			accepted, _ := json.Marshal(dependency.AcceptedResultKinds)
			if _, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_job_dependencies(job_id,parent_job_id,accepted_result_kinds_json,created_at) VALUES(?,?,?,?)`, dependency.JobID, dependency.ParentJobID, string(accepted), formatSkillOrchestratorTime(dependency.CreatedAt)); err != nil {
				return contracts.SkillSignalRouteResult{}, err
			}
		}
		if err := insertSkillOrchestratorEvent(ctx, tx, job.WorkflowID, job.ID, "signal_routed", "", string(job.State), "router", 0, "", job.CreatedAt); err != nil {
			return contracts.SkillSignalRouteResult{}, err
		}
	}
	workflowRows, _ := workflowResult.RowsAffected()
	if err := tx.Commit(); err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	return contracts.SkillSignalRouteResult{Workflow: storedWorkflow, Job: storedJob, Dependencies: dependencies, Created: workflowRows == 1 || jobRows == 1}, nil
}

func (s *Store) EnqueueSkillJob(ctx context.Context, job core.SkillJob, dependencies []core.SkillJobDependency) (core.SkillJob, bool, error) {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	defer tx.Rollback()
	var tenant, workspace, environment string
	if err := tx.QueryRowContext(ctx, `SELECT tenant_id,workspace_id,environment FROM skill_orchestrator_workflows WHERE id=?`, job.WorkflowID).Scan(&tenant, &workspace, &environment); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.SkillJob{}, false, ErrSkillOrchestratorNotFound
		}
		return core.SkillJob{}, false, err
	}
	if tenant != job.Scope.TenantID || workspace != job.Scope.WorkspaceID || environment != job.Scope.Environment {
		return core.SkillJob{}, false, ErrSkillOrchestratorScope
	}
	resultJSON, _ := json.Marshal(job.ResultReferences)
	result, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_jobs(
		id,workflow_id,tenant_id,workspace_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
		state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,
		timeout_at,cancel_requested_at,result_kind,result_references_json,failure_class,failure_code,replay_of_job_id,created_at,updated_at,completed_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(workflow_id,stage,input_digest) DO NOTHING`,
		job.ID, job.WorkflowID, job.Scope.TenantID, job.Scope.WorkspaceID, job.Scope.Environment, job.SkillID,
		job.Stage, job.ContractVersion, job.InputDigest, job.PolicyVersion, job.State, job.Priority,
		formatSkillOrchestratorTime(job.ReadyAt), job.DependencyCount, job.BlockedReason, job.Attempt, job.MaxAttempts,
		job.LeaseOwner, formatOptionalSkillOrchestratorTime(job.LeaseExpiresAt), job.Fence,
		formatOptionalSkillOrchestratorTime(job.TimeoutAt), formatOptionalSkillOrchestratorTime(job.CancelRequestedAt),
		job.ResultKind, string(resultJSON), job.FailureClass, job.FailureCode, job.ReplayOfJobID,
		formatSkillOrchestratorTime(job.CreatedAt), formatSkillOrchestratorTime(job.UpdatedAt), formatOptionalSkillOrchestratorTime(job.CompletedAt))
	if err != nil {
		return core.SkillJob{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return core.SkillJob{}, false, err
	}
	if inserted == 1 {
		for _, dependency := range dependencies {
			accepted, _ := json.Marshal(dependency.AcceptedResultKinds)
			if _, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_job_dependencies(job_id,parent_job_id,accepted_result_kinds_json,created_at) VALUES(?,?,?,?)`,
				dependency.JobID, dependency.ParentJobID, string(accepted), formatSkillOrchestratorTime(dependency.CreatedAt)); err != nil {
				return core.SkillJob{}, false, err
			}
		}
		if err := insertSkillOrchestratorEvent(ctx, tx, job.WorkflowID, job.ID, "job_enqueued", "", string(job.State), "system", 0, "", job.CreatedAt); err != nil {
			return core.SkillJob{}, false, err
		}
	}
	stored, err := skillJobByBinding(ctx, tx, job.WorkflowID, job.Stage, job.InputDigest)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	if stored.ID != job.ID || stored.Scope != job.Scope || stored.ContractVersion != job.ContractVersion {
		return core.SkillJob{}, false, ErrSkillOrchestratorConflict
	}
	if err := tx.Commit(); err != nil {
		return core.SkillJob{}, false, err
	}
	return stored, inserted == 1, nil
}

func (s *Store) ScheduleSkillSuccessor(ctx context.Context, input contracts.SkillSuccessorSchedule) (core.SkillJob, bool, error) {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	defer tx.Rollback()
	var generation int64
	var state string
	var currentStage core.SkillOrchestratorStage
	if err := tx.QueryRowContext(ctx, `SELECT generation,state,current_stage FROM skill_orchestrator_workflows WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=?`, job.WorkflowID, job.Scope.TenantID, job.Scope.WorkspaceID, job.Scope.Environment).Scan(&generation, &state, &currentStage); err != nil {
		return core.SkillJob{}, false, mapSkillOrchestratorNotFound(err)
	}
	existing, existingErr := skillJobByBinding(ctx, tx, job.WorkflowID, job.Stage, job.InputDigest)
	if existingErr == nil {
		if existing.ID != job.ID || generation != input.ExpectedWorkflowGeneration+1 || currentStage != job.Stage {
			return core.SkillJob{}, false, ErrSkillOrchestratorConflict
		}
		if matches, err := sqliteSkillDependenciesMatch(ctx, tx, job.ID, input.Dependencies); err != nil {
			return core.SkillJob{}, false, err
		} else if !matches {
			return core.SkillJob{}, false, ErrSkillOrchestratorConflict
		}
		if err := tx.Commit(); err != nil {
			return core.SkillJob{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(existingErr, sql.ErrNoRows) {
		return core.SkillJob{}, false, existingErr
	}
	if generation != input.ExpectedWorkflowGeneration {
		return core.SkillJob{}, false, ErrSkillOrchestratorGeneration
	}
	if state != string(core.SkillWorkflowOpen) {
		return core.SkillJob{}, false, ErrSkillOrchestratorConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_jobs(
		id,workflow_id,tenant_id,workspace_id,environment,skill_id,stage,contract_version,input_digest,policy_version,
		state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,
		timeout_at,cancel_requested_at,result_kind,result_references_json,failure_class,failure_code,replay_of_job_id,created_at,updated_at,completed_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(workflow_id,stage,input_digest) DO NOTHING`,
		job.ID, job.WorkflowID, job.Scope.TenantID, job.Scope.WorkspaceID, job.Scope.Environment, job.SkillID,
		job.Stage, job.ContractVersion, job.InputDigest, job.PolicyVersion, job.State, job.Priority,
		formatSkillOrchestratorTime(job.ReadyAt), job.DependencyCount, job.BlockedReason, job.Attempt, job.MaxAttempts,
		job.LeaseOwner, formatOptionalSkillOrchestratorTime(job.LeaseExpiresAt), job.Fence,
		formatOptionalSkillOrchestratorTime(job.TimeoutAt), formatOptionalSkillOrchestratorTime(job.CancelRequestedAt),
		job.ResultKind, "[]", job.FailureClass, job.FailureCode, job.ReplayOfJobID,
		formatSkillOrchestratorTime(job.CreatedAt), formatSkillOrchestratorTime(job.UpdatedAt), formatOptionalSkillOrchestratorTime(job.CompletedAt))
	if err != nil {
		return core.SkillJob{}, false, err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 1 {
		for _, dependency := range input.Dependencies {
			accepted, _ := json.Marshal(dependency.AcceptedResultKinds)
			if _, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_job_dependencies(job_id,parent_job_id,accepted_result_kinds_json,created_at) VALUES(?,?,?,?)`, dependency.JobID, dependency.ParentJobID, string(accepted), formatSkillOrchestratorTime(dependency.CreatedAt)); err != nil {
				return core.SkillJob{}, false, err
			}
		}
		if err := insertSkillOrchestratorEvent(ctx, tx, job.WorkflowID, job.ID, "successor_scheduled", "", string(job.State), "dependency_coordinator", 0, "", input.Now); err != nil {
			return core.SkillJob{}, false, err
		}
	}
	stored, err := skillJobByBinding(ctx, tx, job.WorkflowID, job.Stage, job.InputDigest)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	if stored.ID != job.ID {
		return core.SkillJob{}, false, ErrSkillOrchestratorConflict
	}
	if matches, err := sqliteSkillDependenciesMatch(ctx, tx, job.ID, input.Dependencies); err != nil {
		return core.SkillJob{}, false, err
	} else if !matches {
		return core.SkillJob{}, false, ErrSkillOrchestratorConflict
	}
	workflowUpdate, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_workflows SET current_stage=?,generation=generation+1,updated_at=? WHERE id=? AND generation=? AND state='open'`, job.Stage, formatSkillOrchestratorTime(input.Now), job.WorkflowID, input.ExpectedWorkflowGeneration)
	if err != nil {
		return core.SkillJob{}, false, err
	}
	if changed, _ := workflowUpdate.RowsAffected(); changed != 1 {
		return core.SkillJob{}, false, ErrSkillOrchestratorGeneration
	}
	if err := tx.Commit(); err != nil {
		return core.SkillJob{}, false, err
	}
	return stored, inserted == 1, nil
}

func sqliteSkillDependenciesMatch(ctx context.Context, tx *sql.Tx, jobID string, expected []core.SkillJobDependency) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT parent_job_id,accepted_result_kinds_json FROM skill_orchestrator_job_dependencies WHERE job_id=?`, jobID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	actual := make(map[string]map[core.SkillJobResultKind]bool, len(expected))
	for rows.Next() {
		var parentID, acceptedJSON string
		if err := rows.Scan(&parentID, &acceptedJSON); err != nil {
			return false, err
		}
		var accepted []core.SkillJobResultKind
		if err := json.Unmarshal([]byte(acceptedJSON), &accepted); err != nil {
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

func (s *Store) ResolveSkillJobDependencies(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, expectedGeneration int64, now time.Time) (contracts.SkillDependencyResolution, error) {
	if err := scope.Validate(); err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	if !validSkillOrchestratorStorageID(jobID) || expectedGeneration < 1 || now.IsZero() {
		return contracts.SkillDependencyResolution{}, errors.New("invalid skill dependency resolution")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	defer tx.Rollback()
	job, err := skillJobByID(ctx, tx, scope, jobID)
	if err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	workflow, err := scanSkillWorkflow(tx.QueryRowContext(ctx, `SELECT id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at FROM skill_orchestrator_workflows WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=?`, job.WorkflowID, scope.TenantID, scope.WorkspaceID, scope.Environment))
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
		rows, err := tx.QueryContext(ctx, `SELECT p.state,p.result_kind,d.accepted_result_kinds_json FROM skill_orchestrator_job_dependencies d JOIN skill_orchestrator_jobs p ON p.id=d.parent_job_id WHERE d.job_id=? ORDER BY d.parent_job_id`, job.ID)
		if err != nil {
			return contracts.SkillDependencyResolution{}, err
		}
		count, pending, incompatible, cancelled := 0, 0, false, false
		for rows.Next() {
			count++
			var parentState core.SkillJobState
			var result core.SkillJobResultKind
			var acceptedJSON string
			if err := rows.Scan(&parentState, &result, &acceptedJSON); err != nil {
				rows.Close()
				return contracts.SkillDependencyResolution{}, err
			}
			if !parentState.Terminal() {
				pending++
				continue
			}
			var accepted []core.SkillJobResultKind
			if err := json.Unmarshal([]byte(acceptedJSON), &accepted); err != nil {
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
		if err := rows.Close(); err != nil {
			return contracts.SkillDependencyResolution{}, err
		}
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
		case pending == 0:
			state = contracts.SkillDependenciesReady
		}
	}
	changed := false
	if !job.State.Terminal() {
		from := job.State
		switch state {
		case contracts.SkillDependenciesReady:
			if job.State != core.SkillJobQueued || job.DependencyCount != 0 || job.BlockedReason != "" {
				job.State, job.DependencyCount, job.BlockedReason = core.SkillJobQueued, 0, ""
				job.ReadyAt, job.UpdatedAt, changed = now, now, true
			}
		case contracts.SkillDependenciesPending:
			if job.State != core.SkillJobBlocked || job.BlockedReason != "dependencies_pending" {
				job.State, job.BlockedReason, job.UpdatedAt, changed = core.SkillJobBlocked, "dependencies_pending", now, true
			}
		case contracts.SkillDependenciesRejected, contracts.SkillDependenciesCancelled:
			job.State, job.FailureCode, job.CompletedAt, job.UpdatedAt, changed = core.SkillJobCompleted, terminalCode, now, now, true
			job.ResultKind, job.FailureClass = core.SkillJobResultRejected, core.SkillFailurePermanentValidation
			if state == contracts.SkillDependenciesCancelled {
				job.State, job.ResultKind, job.FailureClass = core.SkillJobCancelled, core.SkillJobResultCancelled, core.SkillFailureCancellation
			}
		}
		if changed {
			if err := job.Validate(); err != nil {
				return contracts.SkillDependencyResolution{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET state=?,ready_at=?,dependency_count=?,blocked_reason=?,result_kind=?,failure_class=?,failure_code=?,updated_at=?,completed_at=? WHERE id=? AND state=?`, job.State, formatSkillOrchestratorTime(job.ReadyAt), job.DependencyCount, job.BlockedReason, job.ResultKind, job.FailureClass, job.FailureCode, formatSkillOrchestratorTime(job.UpdatedAt), formatOptionalSkillOrchestratorTime(job.CompletedAt), job.ID, from); err != nil {
				return contracts.SkillDependencyResolution{}, err
			}
			if err := insertSkillOrchestratorEvent(ctx, tx, job.WorkflowID, job.ID, "dependencies_resolved", string(from), string(job.State), "dependency_coordinator", job.Fence, terminalCode, now); err != nil {
				return contracts.SkillDependencyResolution{}, err
			}
		}
	}
	if state == contracts.SkillDependenciesRejected && workflow.State == core.SkillWorkflowOpen {
		workflow.State, workflow.UpdatedAt, workflow.TerminalAt = core.SkillWorkflowRejected, now, now
		if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_workflows SET state='rejected',updated_at=?,terminal_at=? WHERE id=? AND generation=? AND state='open'`, formatSkillOrchestratorTime(now), formatSkillOrchestratorTime(now), workflow.ID, expectedGeneration); err != nil {
			return contracts.SkillDependencyResolution{}, err
		}
	}
	if state == contracts.SkillDependenciesCancelled && workflow.State == core.SkillWorkflowOpen {
		workflow.State, workflow.UpdatedAt, workflow.TerminalAt = core.SkillWorkflowCancelled, now, now
		if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_workflows SET state='cancelled',updated_at=?,terminal_at=? WHERE id=? AND generation=? AND state='open'`, formatSkillOrchestratorTime(now), formatSkillOrchestratorTime(now), workflow.ID, expectedGeneration); err != nil {
			return contracts.SkillDependencyResolution{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	return contracts.SkillDependencyResolution{Workflow: workflow, Job: job, State: state, Changed: changed}, nil
}

func (s *Store) ClaimSkillJobs(ctx context.Context, scope core.SkillOrchestratorScope, owner string, limit int, lease, timeout time.Duration, now time.Time) ([]core.SkillJob, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if !validSkillOrchestratorStorageID(owner) || limit < 1 || limit > 100 || lease <= 0 || timeout <= 0 || timeout > lease || now.IsZero() {
		return nil, errors.New("invalid skill job claim")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	nowText := formatSkillOrchestratorTime(now)
	if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET state='queued',updated_at=?
		WHERE tenant_id=? AND workspace_id=? AND environment=? AND state='retry_wait' AND ready_at<=?`,
		nowText, scope.TenantID, scope.WorkspaceID, scope.Environment, nowText); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT j.id,j.state FROM skill_orchestrator_jobs j
		JOIN skill_orchestrator_workflows w ON w.id=j.workflow_id
		WHERE j.tenant_id=? AND j.workspace_id=? AND j.environment=? AND w.state='open' AND
		j.contract_version=? AND j.attempt<j.max_attempts AND (
			(j.state='queued' AND j.ready_at<=? AND NOT EXISTS (
				SELECT 1 FROM skill_orchestrator_job_dependencies d
				JOIN skill_orchestrator_jobs p ON p.id=d.parent_job_id
				WHERE d.job_id=j.id AND (p.state!='completed' OR NOT EXISTS (
					SELECT 1 FROM json_each(d.accepted_result_kinds_json) accepted WHERE accepted.value=p.result_kind
				))
			)) OR (j.state='running' AND j.lease_expires_at!='' AND j.lease_expires_at<=?)
		)
		ORDER BY j.priority DESC,j.ready_at ASC,j.created_at ASC,j.id ASC LIMIT ?`,
		scope.TenantID, scope.WorkspaceID, scope.Environment, core.SkillOrchestratorContractVersion, nowText, nowText, limit)
	if err != nil {
		return nil, err
	}
	type candidate struct{ id, state string }
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.state); err != nil {
			_ = rows.Close()
			return nil, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	claimed := make([]core.SkillJob, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.state == string(core.SkillJobRunning) {
			if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_job_attempts SET ended_at=?,result_kind='cancelled',failure_class='contention',failure_code='lease_expired',duration_ms=MAX(0,CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER)) WHERE job_id=? AND ended_at=''`, nowText, nowText, candidate.id); err != nil {
				return nil, err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET state='running',attempt=attempt+1,
			lease_owner=?,lease_expires_at=?,fence=fence+1,timeout_at=?,blocked_reason='',failure_class='',failure_code='',updated_at=?
			WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=? AND attempt<max_attempts AND
			(state='queued' OR (state='running' AND lease_expires_at!='' AND lease_expires_at<=?))`,
			owner, formatSkillOrchestratorTime(now.Add(lease)), formatSkillOrchestratorTime(now.Add(timeout)), nowText,
			candidate.id, scope.TenantID, scope.WorkspaceID, scope.Environment, nowText)
		if err != nil {
			return nil, err
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			continue
		}
		job, err := skillJobByID(ctx, tx, scope, candidate.id)
		if err != nil {
			return nil, err
		}
		attemptID := fmt.Sprintf("%s-attempt-%d", job.ID, job.Attempt)
		if _, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_job_attempts(id,job_id,attempt,owner,fence,started_at,lease_expires_at) VALUES(?,?,?,?,?,?,?)`,
			attemptID, job.ID, job.Attempt, owner, job.Fence, nowText, formatSkillOrchestratorTime(now.Add(lease))); err != nil {
			return nil, err
		}
		if err := insertSkillOrchestratorEvent(ctx, tx, job.WorkflowID, job.ID, "job_claimed", candidate.state, string(core.SkillJobRunning), owner, job.Fence, "", now); err != nil {
			return nil, err
		}
		claimed = append(claimed, job)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) RenewSkillJobLease(ctx context.Context, scope core.SkillOrchestratorScope, jobID, owner string, fence int64, expiresAt, now time.Time) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if !validSkillOrchestratorStorageID(jobID) || !validSkillOrchestratorStorageID(owner) || fence < 1 || !expiresAt.After(now) {
		return errors.New("invalid skill job renewal")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET lease_expires_at=?,updated_at=?
		WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=? AND state='running' AND lease_owner=? AND fence=? AND lease_expires_at>? AND cancel_requested_at=''`,
		formatSkillOrchestratorTime(expiresAt), formatSkillOrchestratorTime(now), jobID, scope.TenantID, scope.WorkspaceID, scope.Environment, owner, fence, formatSkillOrchestratorTime(now))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrSkillOrchestratorStaleLease
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_job_attempts SET lease_expires_at=?,renewal_count=renewal_count+1 WHERE job_id=? AND owner=? AND fence=? AND ended_at=''`, formatSkillOrchestratorTime(expiresAt), jobID, owner, fence); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SkillWorkflowGeneration(ctx context.Context, scope core.SkillOrchestratorScope, workflowID string) (int64, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	var generation int64
	err := s.db.QueryRowContext(ctx, `SELECT generation FROM skill_orchestrator_workflows WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=?`, workflowID, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrSkillOrchestratorNotFound
	}
	return generation, err
}

func (s *Store) FinalizeSkillJob(ctx context.Context, finalization SkillJobFinalization) error {
	target := core.SkillJobCompleted
	if finalization.DeadLetter {
		target = core.SkillJobDeadLettered
	} else if finalization.ResultKind == core.SkillJobResultCancelled {
		target = core.SkillJobCancelled
	}
	return s.finishRunningSkillJob(ctx, finalization.Scope, finalization.JobID, finalization.Owner, finalization.Fence,
		finalization.ExpectedWorkflowGeneration, target, finalization.ResultKind, finalization.ResultReferences,
		finalization.FailureClass, finalization.FailureCode, "", time.Time{}, finalization.Now)
}

func (s *Store) RetrySkillJob(ctx context.Context, retry SkillJobRetry) error {
	if retry.FailureClass != core.SkillFailureContention && retry.FailureClass != core.SkillFailureDependencyUnavailable && retry.FailureClass != core.SkillFailureUnknownInternal {
		return errors.New("skill job retry requires retryable failure class")
	}
	if !retry.ReadyAt.After(retry.Now) {
		return errors.New("skill job retry ready_at must follow now")
	}
	return s.finishRunningSkillJob(ctx, retry.Scope, retry.JobID, retry.Owner, retry.Fence,
		retry.ExpectedWorkflowGeneration, core.SkillJobRetryWait, core.SkillJobResultNone, nil,
		retry.FailureClass, retry.FailureCode, "", retry.ReadyAt, retry.Now)
}

func (s *Store) BlockSkillJob(ctx context.Context, block SkillJobBlock) error {
	if block.FailureClass != core.SkillFailureInsufficientEvidence && block.FailureClass != core.SkillFailurePolicyBlock {
		return errors.New("skill job block requires blocked failure class")
	}
	if !block.RecheckAt.After(block.Now) {
		return errors.New("skill job block recheck_at must follow now")
	}
	return s.finishRunningSkillJob(ctx, block.Scope, block.JobID, block.Owner, block.Fence,
		block.ExpectedWorkflowGeneration, core.SkillJobBlocked, core.SkillJobResultNone, nil,
		block.FailureClass, "", block.ReasonCode, block.RecheckAt, block.Now)
}

func (s *Store) finishRunningSkillJob(ctx context.Context, scope core.SkillOrchestratorScope, jobID, owner string, fence, expectedGeneration int64, target core.SkillJobState, resultKind core.SkillJobResultKind, references []core.SkillOrchestratorReference, failureClass core.SkillJobFailureClass, failureCode, blockedReason string, readyAt, now time.Time) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if !validSkillOrchestratorStorageID(jobID) || !validSkillOrchestratorStorageID(owner) || fence < 1 || expectedGeneration < 1 || now.IsZero() {
		return errors.New("invalid skill job finalization")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := skillJobByID(ctx, tx, scope, jobID)
	if err != nil {
		return err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM skill_orchestrator_workflows WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=?`, job.WorkflowID, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&generation); err != nil {
		return mapSkillOrchestratorNotFound(err)
	}
	if generation != expectedGeneration {
		return ErrSkillOrchestratorGeneration
	}
	if job.State == target && job.Fence == fence && job.ResultKind == resultKind && skillOrchestratorReferencesEqual(job.ResultReferences, references) && job.FailureClass == failureClass && job.FailureCode == failureCode && job.BlockedReason == blockedReason {
		var attemptOwner string
		if err := tx.QueryRowContext(ctx, `SELECT owner FROM skill_orchestrator_job_attempts WHERE job_id=? AND fence=?`, job.ID, fence).Scan(&attemptOwner); err == nil && attemptOwner == owner {
			return tx.Commit()
		}
	}
	if job.State != core.SkillJobRunning || job.LeaseOwner != owner || job.Fence != fence || !job.LeaseExpiresAt.After(now) {
		return ErrSkillOrchestratorStaleLease
	}
	from := job.State
	job.State, job.ResultKind, job.ResultReferences = target, resultKind, references
	job.FailureClass, job.FailureCode, job.BlockedReason = failureClass, failureCode, blockedReason
	job.LeaseOwner, job.LeaseExpiresAt, job.TimeoutAt = "", time.Time{}, time.Time{}
	job.UpdatedAt = now
	if !readyAt.IsZero() {
		job.ReadyAt = readyAt
	}
	if target.Terminal() {
		job.CompletedAt = now
	}
	if err := job.Validate(); err != nil {
		return err
	}
	resultJSON, _ := json.Marshal(job.ResultReferences)
	result, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET state=?,ready_at=?,blocked_reason=?,lease_owner='',lease_expires_at='',timeout_at='',
		result_kind=?,result_references_json=?,failure_class=?,failure_code=?,updated_at=?,completed_at=?
		WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=? AND state='running' AND lease_owner=? AND fence=? AND lease_expires_at>?`,
		job.State, formatSkillOrchestratorTime(job.ReadyAt), job.BlockedReason, job.ResultKind, string(resultJSON), job.FailureClass,
		job.FailureCode, formatSkillOrchestratorTime(now), formatOptionalSkillOrchestratorTime(job.CompletedAt),
		job.ID, scope.TenantID, scope.WorkspaceID, scope.Environment, owner, fence, formatSkillOrchestratorTime(now))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrSkillOrchestratorStaleLease
	}
	attemptResult := resultKind
	if attemptResult == core.SkillJobResultNone {
		attemptResult = core.SkillJobResultRejected
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_job_attempts SET ended_at=?,result_kind=?,failure_class=?,failure_code=?,duration_ms=MAX(0,CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER)) WHERE job_id=? AND owner=? AND fence=? AND ended_at=''`,
		formatSkillOrchestratorTime(now), attemptResult, failureClass, firstNonEmpty(failureCode, blockedReason), formatSkillOrchestratorTime(now), job.ID, owner, fence); err != nil {
		return err
	}
	if err := insertSkillOrchestratorEvent(ctx, tx, job.WorkflowID, job.ID, "job_transition", string(from), string(target), owner, fence, firstNonEmpty(failureCode, blockedReason), now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CancelSkillJob(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, expectedGeneration int64, actor string, now time.Time) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if !validSkillOrchestratorStorageID(jobID) || !validSkillOrchestratorStorageID(actor) || expectedGeneration < 1 || now.IsZero() {
		return errors.New("invalid skill job cancellation")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := skillJobByID(ctx, tx, scope, jobID)
	if err != nil {
		return err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM skill_orchestrator_workflows WHERE id=?`, job.WorkflowID).Scan(&generation); err != nil {
		return mapSkillOrchestratorNotFound(err)
	}
	if generation != expectedGeneration {
		return ErrSkillOrchestratorGeneration
	}
	if job.State == core.SkillJobCancelled {
		return tx.Commit()
	}
	if job.State == core.SkillJobRunning {
		result, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET cancel_requested_at=?,updated_at=? WHERE id=? AND state='running'`, formatSkillOrchestratorTime(now), formatSkillOrchestratorTime(now), job.ID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSkillOrchestratorConflict
		}
		return tx.Commit()
	}
	if job.State.Terminal() || !core.CanTransitionSkillJob(job.State, core.SkillJobCancelled) {
		return ErrSkillOrchestratorConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET state='cancelled',result_kind='cancelled',failure_class='cancellation',failure_code='operator_cancelled',completed_at=?,updated_at=? WHERE id=? AND state=?`, formatSkillOrchestratorTime(now), formatSkillOrchestratorTime(now), job.ID, job.State)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrSkillOrchestratorConflict
	}
	if err := insertSkillOrchestratorEvent(ctx, tx, job.WorkflowID, job.ID, "job_cancelled", string(job.State), string(core.SkillJobCancelled), actor, job.Fence, "operator_cancelled", now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetSkillJob(ctx context.Context, scope core.SkillOrchestratorScope, jobID string) (core.SkillJob, error) {
	if err := scope.Validate(); err != nil {
		return core.SkillJob{}, err
	}
	job, err := skillJobByID(ctx, s.db, scope, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return core.SkillJob{}, ErrSkillOrchestratorNotFound
	}
	return job, err
}

func (s *Store) GetSkillWorkflow(ctx context.Context, scope core.SkillOrchestratorScope, workflowID string) (core.SkillWorkflow, error) {
	if err := scope.Validate(); err != nil {
		return core.SkillWorkflow{}, err
	}
	workflow, err := scanSkillWorkflow(s.db.QueryRowContext(ctx, `SELECT id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at FROM skill_orchestrator_workflows WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=?`, workflowID, scope.TenantID, scope.WorkspaceID, scope.Environment))
	if errors.Is(err, sql.ErrNoRows) {
		return core.SkillWorkflow{}, ErrSkillOrchestratorNotFound
	}
	return workflow, err
}

func (s *Store) LoadSkillReconciliationCursor(ctx context.Context, scope core.SkillOrchestratorScope, domain core.SkillReconciliationDomain, configurationVersion int64, now time.Time) (core.SkillReconciliationCursor, error) {
	if err := scope.Validate(); err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	if !domain.Valid() || configurationVersion < 1 || now.IsZero() {
		return core.SkillReconciliationCursor{}, errors.New("invalid skill reconciliation cursor load")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_reconciliation_cursors(tenant_id,workspace_id,environment,domain,cursor,configuration_version,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,workspace_id,environment,domain) DO NOTHING`, scope.TenantID, scope.WorkspaceID, scope.Environment, domain, "", configurationVersion, formatSkillOrchestratorTime(now)); err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	cursor, err := scanSQLiteSkillReconciliationCursor(tx.QueryRowContext(ctx, `SELECT tenant_id,workspace_id,environment,domain,cursor,configuration_version,last_completed_at,scanned,repaired,skipped,blocked,failed,updated_at FROM skill_orchestrator_reconciliation_cursors WHERE tenant_id=? AND workspace_id=? AND environment=? AND domain=?`, scope.TenantID, scope.WorkspaceID, scope.Environment, domain))
	if err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	if cursor.ConfigurationVersion != configurationVersion {
		if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_reconciliation_cursors SET cursor='',configuration_version=?,last_completed_at='',scanned=0,repaired=0,skipped=0,blocked=0,failed=0,updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND domain=?`, configurationVersion, formatSkillOrchestratorTime(now), scope.TenantID, scope.WorkspaceID, scope.Environment, domain); err != nil {
			return core.SkillReconciliationCursor{}, err
		}
		cursor = core.SkillReconciliationCursor{Scope: scope, Domain: domain, ConfigurationVersion: configurationVersion, UpdatedAt: now}
	}
	if err := tx.Commit(); err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	return cursor, nil
}

func (s *Store) SaveSkillReconciliationCursor(ctx context.Context, input contracts.SkillReconciliationCursorUpdate) error {
	if err := input.Cursor.Validate(); err != nil {
		return err
	}
	if input.ExpectedUpdatedAt.IsZero() || !input.Cursor.UpdatedAt.After(input.ExpectedUpdatedAt) {
		return errors.New("invalid skill reconciliation cursor compare-and-swap")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE skill_orchestrator_reconciliation_cursors SET cursor=?,configuration_version=?,last_completed_at=?,scanned=?,repaired=?,skipped=?,blocked=?,failed=?,updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND domain=? AND updated_at=?`,
		input.Cursor.Cursor, input.Cursor.ConfigurationVersion, formatOptionalSkillOrchestratorTime(input.Cursor.LastCompletedAt), input.Cursor.Counters.Scanned,
		input.Cursor.Counters.Repaired, input.Cursor.Counters.Skipped, input.Cursor.Counters.Blocked, input.Cursor.Counters.Failed,
		formatSkillOrchestratorTime(input.Cursor.UpdatedAt), input.Cursor.Scope.TenantID, input.Cursor.Scope.WorkspaceID, input.Cursor.Scope.Environment,
		input.Cursor.Domain, formatSkillOrchestratorTime(input.ExpectedUpdatedAt))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrSkillOrchestratorConflict
	}
	return nil
}

func scanSQLiteSkillReconciliationCursor(scanner skillOrchestratorScanner) (core.SkillReconciliationCursor, error) {
	var cursor core.SkillReconciliationCursor
	var completedAt, updatedAt string
	err := scanner.Scan(&cursor.Scope.TenantID, &cursor.Scope.WorkspaceID, &cursor.Scope.Environment, &cursor.Domain, &cursor.Cursor,
		&cursor.ConfigurationVersion, &completedAt, &cursor.Counters.Scanned, &cursor.Counters.Repaired, &cursor.Counters.Skipped,
		&cursor.Counters.Blocked, &cursor.Counters.Failed, &updatedAt)
	if err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	if cursor.LastCompletedAt, err = parseOptionalSkillOrchestratorTime(completedAt); err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	if cursor.UpdatedAt, err = parseSkillOrchestratorTime(updatedAt); err != nil {
		return core.SkillReconciliationCursor{}, err
	}
	return cursor, cursor.Validate()
}

func (s *Store) ListSkillJobs(ctx context.Context, scope core.SkillOrchestratorScope, workflowID, afterID string, limit int) ([]core.SkillJob, string, error) {
	if err := scope.Validate(); err != nil {
		return nil, "", err
	}
	if !validSkillOrchestratorStorageID(workflowID) || (afterID != "" && !validSkillOrchestratorStorageID(afterID)) || limit < 1 || limit > 200 {
		return nil, "", errors.New("invalid skill job page")
	}
	rows, err := s.db.QueryContext(ctx, skillJobSelect+` WHERE tenant_id=? AND workspace_id=? AND environment=? AND workflow_id=? AND id>? ORDER BY id LIMIT ?`, scope.TenantID, scope.WorkspaceID, scope.Environment, workflowID, afterID, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]core.SkillJob, 0, limit+1)
	for rows.Next() {
		item, err := scanSkillJob(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func (s *Store) skillWorkflowByOrigin(ctx context.Context, workflow core.SkillWorkflow) (core.SkillWorkflow, error) {
	return skillWorkflowByOriginQuery(ctx, s.db, workflow)
}

func skillWorkflowByOriginQuery(ctx context.Context, queryer skillOrchestratorQueryer, workflow core.SkillWorkflow) (core.SkillWorkflow, error) {
	row := queryer.QueryRowContext(ctx, `SELECT id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at FROM skill_orchestrator_workflows WHERE tenant_id=? AND workspace_id=? AND environment=? AND workflow_kind=? AND origin_kind=? AND origin_id=? AND input_digest=?`,
		workflow.Scope.TenantID, workflow.Scope.WorkspaceID, workflow.Scope.Environment, workflow.Kind, workflow.OriginKind, workflow.OriginID, workflow.InputDigest)
	return scanSkillWorkflow(row)
}

const skillJobSelect = `SELECT id,workflow_id,tenant_id,workspace_id,environment,skill_id,stage,contract_version,input_digest,policy_version,state,priority,ready_at,dependency_count,blocked_reason,attempt,max_attempts,lease_owner,lease_expires_at,fence,timeout_at,cancel_requested_at,result_kind,result_references_json,failure_class,failure_code,replay_of_job_id,created_at,updated_at,completed_at FROM skill_orchestrator_jobs`

type skillOrchestratorScanner interface{ Scan(...any) error }

type skillOrchestratorQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func skillJobByBinding(ctx context.Context, queryer skillOrchestratorQueryer, workflowID string, stage core.SkillOrchestratorStage, digest string) (core.SkillJob, error) {
	return scanSkillJob(queryer.QueryRowContext(ctx, skillJobSelect+` WHERE workflow_id=? AND stage=? AND input_digest=?`, workflowID, stage, digest))
}

func skillJobByID(ctx context.Context, queryer skillOrchestratorQueryer, scope core.SkillOrchestratorScope, jobID string) (core.SkillJob, error) {
	job, err := scanSkillJob(queryer.QueryRowContext(ctx, skillJobSelect+` WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=?`, jobID, scope.TenantID, scope.WorkspaceID, scope.Environment))
	if errors.Is(err, sql.ErrNoRows) {
		return core.SkillJob{}, ErrSkillOrchestratorNotFound
	}
	return job, err
}

func scanSkillJob(scanner skillOrchestratorScanner) (core.SkillJob, error) {
	var job core.SkillJob
	var readyAt, leaseExpiresAt, timeoutAt, cancelAt, referencesJSON, createdAt, updatedAt, completedAt string
	err := scanner.Scan(&job.ID, &job.WorkflowID, &job.Scope.TenantID, &job.Scope.WorkspaceID, &job.Scope.Environment,
		&job.SkillID, &job.Stage, &job.ContractVersion, &job.InputDigest, &job.PolicyVersion, &job.State, &job.Priority,
		&readyAt, &job.DependencyCount, &job.BlockedReason, &job.Attempt, &job.MaxAttempts, &job.LeaseOwner,
		&leaseExpiresAt, &job.Fence, &timeoutAt, &cancelAt, &job.ResultKind, &referencesJSON, &job.FailureClass,
		&job.FailureCode, &job.ReplayOfJobID, &createdAt, &updatedAt, &completedAt)
	if err != nil {
		return core.SkillJob{}, err
	}
	job.ReadyAt, err = parseSkillOrchestratorTime(readyAt)
	if err != nil {
		return core.SkillJob{}, err
	}
	job.LeaseExpiresAt, _ = parseOptionalSkillOrchestratorTime(leaseExpiresAt)
	job.TimeoutAt, _ = parseOptionalSkillOrchestratorTime(timeoutAt)
	job.CancelRequestedAt, _ = parseOptionalSkillOrchestratorTime(cancelAt)
	job.CreatedAt, err = parseSkillOrchestratorTime(createdAt)
	if err != nil {
		return core.SkillJob{}, err
	}
	job.UpdatedAt, err = parseSkillOrchestratorTime(updatedAt)
	if err != nil {
		return core.SkillJob{}, err
	}
	job.CompletedAt, _ = parseOptionalSkillOrchestratorTime(completedAt)
	if err := json.Unmarshal([]byte(referencesJSON), &job.ResultReferences); err != nil {
		return core.SkillJob{}, err
	}
	if err := job.Validate(); err != nil {
		return core.SkillJob{}, fmt.Errorf("stored skill job is invalid: %w", err)
	}
	return job, nil
}

func scanSkillWorkflow(scanner skillOrchestratorScanner) (core.SkillWorkflow, error) {
	var workflow core.SkillWorkflow
	var createdAt, updatedAt, terminalAt string
	if err := scanner.Scan(&workflow.ID, &workflow.Scope.TenantID, &workflow.Scope.WorkspaceID, &workflow.Scope.Environment,
		&workflow.SkillID, &workflow.OriginKind, &workflow.OriginID, &workflow.Kind, &workflow.ContractVersion,
		&workflow.InputDigest, &workflow.State, &workflow.CurrentStage, &workflow.Generation, &workflow.ConfigurationVersion,
		&workflow.PolicyDigest, &createdAt, &updatedAt, &terminalAt); err != nil {
		return core.SkillWorkflow{}, err
	}
	var err error
	workflow.CreatedAt, err = parseSkillOrchestratorTime(createdAt)
	if err != nil {
		return core.SkillWorkflow{}, err
	}
	workflow.UpdatedAt, err = parseSkillOrchestratorTime(updatedAt)
	if err != nil {
		return core.SkillWorkflow{}, err
	}
	workflow.TerminalAt, _ = parseOptionalSkillOrchestratorTime(terminalAt)
	if err := workflow.Validate(); err != nil {
		return core.SkillWorkflow{}, fmt.Errorf("stored skill workflow is invalid: %w", err)
	}
	return workflow, nil
}

func insertSkillOrchestratorEvent(ctx context.Context, tx *sql.Tx, workflowID, jobID, kind, fromState, toState, actor string, fence int64, reason string, now time.Time) error {
	var nullableJob any
	if jobID != "" {
		nullableJob = jobID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_events(workflow_id,job_id,event_kind,from_state,to_state,actor_id,fence,reason_code,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, workflowID, nullableJob, kind, fromState, toState, actor, fence, reason, formatSkillOrchestratorTime(now))
	return err
}

func formatSkillOrchestratorTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func formatOptionalSkillOrchestratorTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatSkillOrchestratorTime(value)
}

func parseSkillOrchestratorTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func parseOptionalSkillOrchestratorTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return parseSkillOrchestratorTime(value)
}

func validSkillOrchestratorStorageID(value string) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= 256 && !strings.ContainsAny(value, "/\\\r\n\t") && !strings.Contains(value, "..")
}

func mapSkillOrchestratorNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSkillOrchestratorNotFound
	}
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func skillOrchestratorReferencesEqual(left, right []core.SkillOrchestratorReference) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *Store) AcquireSkillOrchestratorLeader(ctx context.Context, installationID, databaseID, owner string, leaseDuration time.Duration, now time.Time) (int64, bool, error) {
	if !validSkillOrchestratorStorageID(installationID) || !validSkillOrchestratorStorageID(databaseID) || !validSkillOrchestratorStorageID(owner) || leaseDuration <= 0 || now.IsZero() {
		return 0, false, errors.New("skill orchestrator leader acquisition is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO skill_orchestrator_leader_leases(installation_id,database_id,owner,fence,lease_expires_at,updated_at) VALUES(?,?, '',0,'',?)`, installationID, databaseID, formatSkillTime(now)); err != nil {
		return 0, false, err
	}
	var currentOwner, expires string
	var fence int64
	if err := tx.QueryRowContext(ctx, `SELECT owner,fence,lease_expires_at FROM skill_orchestrator_leader_leases WHERE installation_id=? AND database_id=?`, installationID, databaseID).Scan(&currentOwner, &fence, &expires); err != nil {
		return 0, false, err
	}
	available := currentOwner == "" || currentOwner == owner
	if !available && expires != "" {
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, expires)
		available = parseErr == nil && !expiresAt.After(now)
	}
	if !available {
		return fence, false, tx.Commit()
	}
	nextFence := fence + 1
	result, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_leader_leases SET owner=?,fence=?,lease_expires_at=?,updated_at=? WHERE installation_id=? AND database_id=? AND fence=?`, owner, nextFence, formatSkillTime(now.Add(leaseDuration)), formatSkillTime(now), installationID, databaseID, fence)
	if err != nil {
		return 0, false, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return 0, false, ErrSkillOrchestratorStaleLease
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return nextFence, true, nil
}

func (s *Store) RenewSkillOrchestratorLeader(ctx context.Context, installationID, databaseID, owner string, fence int64, leaseDuration time.Duration, now time.Time) error {
	if fence < 1 || leaseDuration <= 0 || now.IsZero() {
		return errors.New("skill orchestrator leader renewal is invalid")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE skill_orchestrator_leader_leases SET lease_expires_at=?,updated_at=? WHERE installation_id=? AND database_id=? AND owner=? AND fence=? AND lease_expires_at>?`, formatSkillTime(now.Add(leaseDuration)), formatSkillTime(now), installationID, databaseID, owner, fence, formatSkillTime(now))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrSkillOrchestratorStaleLease
	}
	return nil
}

func (s *Store) ReleaseSkillOrchestratorLeader(ctx context.Context, installationID, databaseID, owner string, fence int64, now time.Time) error {
	if fence < 1 || now.IsZero() {
		return errors.New("skill orchestrator leader release is invalid")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE skill_orchestrator_leader_leases SET owner='',lease_expires_at='',updated_at=? WHERE installation_id=? AND database_id=? AND owner=? AND fence=?`, formatSkillTime(now), installationID, databaseID, owner, fence)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrSkillOrchestratorStaleLease
	}
	return nil
}
