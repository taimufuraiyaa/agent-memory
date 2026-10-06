package application

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillOrchestrationControlRepository interface {
	contracts.SkillOrchestratorRepository
	SetSkillWorkflowPaused(context.Context, core.SkillOrchestratorScope, string, int64, bool, string, time.Time) (core.SkillWorkflow, error)
	RetrySkillJobByOperator(context.Context, core.SkillOrchestratorScope, string, int64, string, time.Time) (core.SkillJob, error)
	ListSkillOrchestrationEvents(context.Context, core.SkillOrchestratorScope, string, int64, int) ([]core.SkillOrchestrationEvent, int64, error)
}

type SkillOrchestrationStatus struct {
	Workflow        core.SkillWorkflow             `json:"workflow"`
	Jobs            []core.SkillJob                `json:"jobs"`
	Events          []core.SkillOrchestrationEvent `json:"events"`
	NextJobCursor   string                         `json:"next_job_cursor,omitempty"`
	NextEventCursor string                         `json:"next_event_cursor,omitempty"`
}

type SkillOrchestrationControlService struct {
	repository SkillOrchestrationControlRepository
	now        func() time.Time
}

func NewSkillOrchestrationControlService(repository SkillOrchestrationControlRepository, now func() time.Time) *SkillOrchestrationControlService {
	if now == nil {
		now = time.Now
	}
	return &SkillOrchestrationControlService{repository: repository, now: now}
}

func (s *SkillOrchestrationControlService) Status(ctx context.Context, scope core.SkillOrchestratorScope, workflowID, jobCursor, eventCursor string, limit int) (SkillOrchestrationStatus, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(workflowID) == "" || limit < 1 || limit > 200 {
		return SkillOrchestrationStatus{}, errors.New("invalid skill orchestration status request")
	}
	eventAfter := int64(0)
	if eventCursor != "" {
		value, err := strconv.ParseInt(eventCursor, 10, 64)
		if err != nil || value < 0 {
			return SkillOrchestrationStatus{}, errors.New("invalid skill orchestration event cursor")
		}
		eventAfter = value
	}
	workflow, err := s.repository.GetSkillWorkflow(ctx, scope, workflowID)
	if err != nil {
		return SkillOrchestrationStatus{}, err
	}
	jobs, nextJob, err := s.repository.ListSkillJobs(ctx, scope, workflowID, jobCursor, limit)
	if err != nil {
		return SkillOrchestrationStatus{}, err
	}
	events, nextEvent, err := s.repository.ListSkillOrchestrationEvents(ctx, scope, workflowID, eventAfter, limit)
	if err != nil {
		return SkillOrchestrationStatus{}, err
	}
	status := SkillOrchestrationStatus{Workflow: workflow, Jobs: jobs, Events: events, NextJobCursor: nextJob}
	if nextEvent > 0 {
		status.NextEventCursor = strconv.FormatInt(nextEvent, 10)
	}
	return status, nil
}

func (s *SkillOrchestrationControlService) SetPaused(ctx context.Context, scope core.SkillOrchestratorScope, workflowID string, generation int64, paused bool, actor string) (core.SkillWorkflow, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(actor) == "" {
		return core.SkillWorkflow{}, errors.New("skill orchestration actor is required")
	}
	return s.repository.SetSkillWorkflowPaused(ctx, scope, workflowID, generation, paused, actor, s.now().UTC())
}

func (s *SkillOrchestrationControlService) Cancel(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, generation int64, actor string) error {
	if s == nil || s.repository == nil || strings.TrimSpace(actor) == "" {
		return errors.New("skill orchestration actor is required")
	}
	return s.repository.CancelSkillJob(ctx, scope, jobID, generation, actor, s.now().UTC())
}

func (s *SkillOrchestrationControlService) Retry(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, generation int64, actor string) (core.SkillJob, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(actor) == "" {
		return core.SkillJob{}, errors.New("skill orchestration actor is required")
	}
	return s.repository.RetrySkillJobByOperator(ctx, scope, jobID, generation, actor, s.now().UTC())
}

func (s *SkillOrchestrationControlService) Replay(ctx context.Context, request SkillDeadLetterReplayRequest) (contracts.SkillSignalRouteResult, error) {
	request.Now = s.now().UTC()
	return NewSkillDeadLetterReplayService(s.repository).Replay(ctx, request)
}

type SkillOrchestrationReconcileReport struct {
	Scanned int `json:"scanned"`
	Changed int `json:"changed"`
	Skipped int `json:"skipped"`
}

func (s *SkillOrchestrationControlService) Reconcile(ctx context.Context, scope core.SkillOrchestratorScope, workflowID string, generation int64, limit int) (SkillOrchestrationReconcileReport, error) {
	if limit < 1 || limit > 200 {
		return SkillOrchestrationReconcileReport{}, errors.New("invalid skill reconciliation limit")
	}
	workflow, err := s.repository.GetSkillWorkflow(ctx, scope, workflowID)
	if err != nil {
		return SkillOrchestrationReconcileReport{}, err
	}
	if workflow.Generation != generation {
		return SkillOrchestrationReconcileReport{}, errors.New("skill workflow generation conflict")
	}
	jobs, _, err := s.repository.ListSkillJobs(ctx, scope, workflowID, "", limit)
	if err != nil {
		return SkillOrchestrationReconcileReport{}, err
	}
	report := SkillOrchestrationReconcileReport{}
	for _, job := range jobs {
		if job.State != core.SkillJobBlocked || job.DependencyCount == 0 {
			report.Skipped++
			continue
		}
		report.Scanned++
		resolution, resolveErr := s.repository.ResolveSkillJobDependencies(ctx, scope, job.ID, generation, s.now().UTC())
		if resolveErr != nil {
			return report, resolveErr
		}
		if resolution.Changed {
			report.Changed++
		} else {
			report.Skipped++
		}
	}
	return report, nil
}
