package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const skillWorkflowControlSelect = `SELECT id,tenant_id,workspace_id,environment,skill_id,origin_kind,origin_id,workflow_kind,contract_version,input_digest,state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at,terminal_at FROM skill_orchestrator_workflows`

func (s *Store) SetSkillWorkflowPaused(ctx context.Context, scope core.SkillOrchestratorScope, workflowID string, expectedGeneration int64, paused bool, actor string, now time.Time) (core.SkillWorkflow, error) {
	if err := scope.Validate(); err != nil {
		return core.SkillWorkflow{}, err
	}
	if !validSkillOrchestratorStorageID(workflowID) || !validSkillOrchestratorStorageID(actor) || expectedGeneration < 1 || now.IsZero() {
		return core.SkillWorkflow{}, errors.New("invalid skill workflow pause request")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SkillWorkflow{}, err
	}
	defer tx.Rollback()
	workflow, err := scanSkillWorkflow(tx.QueryRowContext(ctx, skillWorkflowControlSelect+` WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=?`, workflowID, scope.TenantID, scope.WorkspaceID, scope.Environment))
	if errors.Is(err, sql.ErrNoRows) {
		return core.SkillWorkflow{}, ErrSkillOrchestratorNotFound
	}
	if err != nil {
		return core.SkillWorkflow{}, err
	}
	if workflow.Generation != expectedGeneration {
		return core.SkillWorkflow{}, ErrSkillOrchestratorGeneration
	}
	target := core.SkillWorkflowOpen
	if paused {
		target = core.SkillWorkflowPaused
	}
	if workflow.State == target {
		return workflow, tx.Commit()
	}
	if !core.CanTransitionSkillWorkflow(workflow.State, target) {
		return core.SkillWorkflow{}, ErrSkillOrchestratorConflict
	}
	from := workflow.State
	workflow.State, workflow.Generation, workflow.UpdatedAt = target, workflow.Generation+1, now
	result, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_workflows SET state=?,generation=?,updated_at=? WHERE id=? AND tenant_id=? AND workspace_id=? AND environment=? AND generation=? AND state=?`, workflow.State, workflow.Generation, formatSkillOrchestratorTime(now), workflow.ID, scope.TenantID, scope.WorkspaceID, scope.Environment, expectedGeneration, from)
	if err != nil {
		return core.SkillWorkflow{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return core.SkillWorkflow{}, ErrSkillOrchestratorConflict
	}
	if err := insertSkillOrchestratorEvent(ctx, tx, workflow.ID, "", "workflow_control", string(from), string(target), actor, 0, map[bool]string{true: "operator_paused", false: "operator_resumed"}[paused], now); err != nil {
		return core.SkillWorkflow{}, err
	}
	return workflow, tx.Commit()
}

func (s *Store) RetrySkillJobByOperator(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, expectedGeneration int64, actor string, now time.Time) (core.SkillJob, error) {
	if err := scope.Validate(); err != nil {
		return core.SkillJob{}, err
	}
	if !validSkillOrchestratorStorageID(jobID) || !validSkillOrchestratorStorageID(actor) || expectedGeneration < 1 || now.IsZero() {
		return core.SkillJob{}, errors.New("invalid skill operator retry")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.SkillJob{}, err
	}
	defer tx.Rollback()
	job, err := skillJobByID(ctx, tx, scope, jobID)
	if err != nil {
		return core.SkillJob{}, err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM skill_orchestrator_workflows WHERE id=?`, job.WorkflowID).Scan(&generation); err != nil {
		return core.SkillJob{}, mapSkillOrchestratorNotFound(err)
	}
	if generation != expectedGeneration {
		return core.SkillJob{}, ErrSkillOrchestratorGeneration
	}
	if job.State == core.SkillJobQueued {
		return job, tx.Commit()
	}
	if job.State != core.SkillJobBlocked && job.State != core.SkillJobRetryWait {
		return core.SkillJob{}, ErrSkillOrchestratorConflict
	}
	from := job.State
	job.State, job.ReadyAt, job.UpdatedAt = core.SkillJobQueued, now, now
	job.BlockedReason, job.FailureClass, job.FailureCode = "", core.SkillFailureNone, ""
	result, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET state='queued',ready_at=?,blocked_reason='',failure_class='',failure_code='',updated_at=? WHERE id=? AND state=?`, formatSkillOrchestratorTime(now), formatSkillOrchestratorTime(now), job.ID, from)
	if err != nil {
		return core.SkillJob{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return core.SkillJob{}, ErrSkillOrchestratorConflict
	}
	if err := insertSkillOrchestratorEvent(ctx, tx, job.WorkflowID, job.ID, "job_operator_retry", string(from), string(core.SkillJobQueued), actor, job.Fence, "operator_retry", now); err != nil {
		return core.SkillJob{}, err
	}
	return job, tx.Commit()
}

func (s *Store) ListSkillOrchestrationEvents(ctx context.Context, scope core.SkillOrchestratorScope, workflowID string, afterID int64, limit int) ([]core.SkillOrchestrationEvent, int64, error) {
	if err := scope.Validate(); err != nil {
		return nil, 0, err
	}
	if !validSkillOrchestratorStorageID(workflowID) || afterID < 0 || limit < 1 || limit > 200 {
		return nil, 0, errors.New("invalid skill orchestration event page")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.workflow_id,COALESCE(e.job_id,''),e.event_kind,e.from_state,e.to_state,e.actor_id,e.fence,e.reason_code,e.created_at FROM skill_orchestrator_events e JOIN skill_orchestrator_workflows w ON w.id=e.workflow_id WHERE e.workflow_id=? AND w.tenant_id=? AND w.workspace_id=? AND w.environment=? AND e.id>? ORDER BY e.id LIMIT ?`, workflowID, scope.TenantID, scope.WorkspaceID, scope.Environment, afterID, limit+1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	events := make([]core.SkillOrchestrationEvent, 0, limit)
	for rows.Next() {
		var event core.SkillOrchestrationEvent
		var created string
		if err := rows.Scan(&event.ID, &event.WorkflowID, &event.JobID, &event.Kind, &event.FromState, &event.ToState, &event.ActorID, &event.Fence, &event.ReasonCode, &created); err != nil {
			return nil, 0, err
		}
		event.CreatedAt, err = parseSkillOrchestratorTime(created)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, event)
	}
	next := int64(0)
	if len(events) > limit {
		next = events[limit-1].ID
		events = events[:limit]
	}
	return events, next, rows.Err()
}
