package application

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillFailureAction string

const (
	SkillFailureRetry            SkillFailureAction = "retry"
	SkillFailureBlock            SkillFailureAction = "block"
	SkillFailureDeadLetter       SkillFailureAction = "dead_letter"
	SkillFailureCompleteRejected SkillFailureAction = "complete_rejected"
	SkillFailureCancel           SkillFailureAction = "cancel"
)

type SkillStageFailure struct {
	Class core.SkillJobFailureClass
	Code  string
}

type SkillFailureDecision struct {
	Action       SkillFailureAction
	FailureClass core.SkillJobFailureClass
	FailureCode  string
	RetryAt      time.Time
	RecheckAt    time.Time
	Attempt      int
}

type SkillRetryPolicyConfig struct {
	InitialBackoff  time.Duration
	MaximumBackoff  time.Duration
	MaximumRetryAge time.Duration
	BlockedRecheck  time.Duration
}

type SkillRetryPolicy struct{ config SkillRetryPolicyConfig }

var safeSkillFailureCodePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

func NewSkillRetryPolicy(config SkillRetryPolicyConfig) (*SkillRetryPolicy, error) {
	if config.InitialBackoff <= 0 || config.MaximumBackoff < config.InitialBackoff || config.MaximumRetryAge <= 0 || config.BlockedRecheck <= 0 {
		return nil, errors.New("skill retry policy durations are invalid")
	}
	return &SkillRetryPolicy{config: config}, nil
}

func (p *SkillRetryPolicy) Decide(job core.SkillJob, failure SkillStageFailure, now time.Time) SkillFailureDecision {
	code := safeSkillFailureCode(failure.Code)
	decision := SkillFailureDecision{FailureClass: failure.Class, FailureCode: code, Attempt: job.Attempt}
	switch failure.Class {
	case core.SkillFailureContention, core.SkillFailureDependencyUnavailable, core.SkillFailureUnknownInternal:
		if job.Attempt >= job.MaxAttempts {
			decision.Action = SkillFailureDeadLetter
			decision.FailureCode = "attempts_exhausted"
			return decision
		}
		if !job.CreatedAt.IsZero() && now.Sub(job.CreatedAt) >= p.config.MaximumRetryAge {
			decision.Action = SkillFailureDeadLetter
			decision.FailureCode = "retry_age_exhausted"
			return decision
		}
		decision.Action = SkillFailureRetry
		decision.RetryAt = now.Add(p.retryDelay(job, failure))
	case core.SkillFailureInsufficientEvidence, core.SkillFailurePolicyBlock:
		decision.Action = SkillFailureBlock
		decision.RecheckAt = now.Add(p.config.BlockedRecheck)
	case core.SkillFailurePermanentValidation:
		decision.Action = SkillFailureDeadLetter
	case core.SkillFailureSafetyRejection:
		decision.Action = SkillFailureCompleteRejected
	case core.SkillFailureCancellation:
		decision.Action = SkillFailureCancel
	default:
		decision.Action = SkillFailureDeadLetter
		decision.FailureClass = core.SkillFailureUnknownInternal
		decision.FailureCode = "invalid_failure_class"
	}
	return decision
}

func (p *SkillRetryPolicy) retryDelay(job core.SkillJob, failure SkillStageFailure) time.Duration {
	delay := p.config.InitialBackoff
	for attempt := 1; attempt < job.Attempt && delay < p.config.MaximumBackoff; attempt++ {
		if delay > p.config.MaximumBackoff/2 {
			delay = p.config.MaximumBackoff
			break
		}
		delay *= 2
	}
	if delay > p.config.MaximumBackoff {
		delay = p.config.MaximumBackoff
	}
	digest := sha256.Sum256([]byte(job.ID + "\x00" + failure.Code + "\x00" + string(failure.Class)))
	// Jitter deterministically within [75%, 100%] so it never exceeds the cap.
	span := delay / 4
	if span == 0 {
		return delay
	}
	jitter := time.Duration(binary.BigEndian.Uint64(digest[:8]) % uint64(span+1))
	return delay - span + jitter
}

func safeSkillFailureCode(code string) string {
	if len(code) == 0 || len(code) > core.MaxSkillOrchestratorFailureCodeBytes || !safeSkillFailureCodePattern.MatchString(code) {
		return "invalid_failure_code"
	}
	return code
}

type SkillDeadLetterReplayRepository interface {
	GetSkillJob(context.Context, core.SkillOrchestratorScope, string) (core.SkillJob, error)
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
	RouteSkillSignal(context.Context, core.SkillWorkflow, core.SkillJob, []core.SkillJobDependency) (contracts.SkillSignalRouteResult, error)
}

type SkillDeadLetterReplayService struct {
	repository SkillDeadLetterReplayRepository
}

type SkillDeadLetterReplayRequest struct {
	Scope          core.SkillOrchestratorScope
	JobID          string
	ActorID        string
	Authorized     bool
	ReasonCode     string
	IdempotencyKey string
	Now            time.Time
}

func NewSkillDeadLetterReplayService(repository SkillDeadLetterReplayRepository) *SkillDeadLetterReplayService {
	return &SkillDeadLetterReplayService{repository: repository}
}

func (s *SkillDeadLetterReplayService) Replay(ctx context.Context, request SkillDeadLetterReplayRequest) (contracts.SkillSignalRouteResult, error) {
	if s == nil || s.repository == nil {
		return contracts.SkillSignalRouteResult{}, errors.New("skill replay repository is required")
	}
	if !request.Authorized {
		return contracts.SkillSignalRouteResult{}, errors.New("skill dead-letter replay is not authorized")
	}
	if err := request.Scope.Validate(); err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if !validSkillSignalIdentifier(request.JobID) || !validSkillSignalIdentifier(request.ActorID) || !validSkillSignalIdentifier(request.IdempotencyKey) || request.Now.IsZero() {
		return contracts.SkillSignalRouteResult{}, errors.New("skill dead-letter replay identity or timestamp is invalid")
	}
	if safeSkillFailureCode(request.ReasonCode) != request.ReasonCode {
		return contracts.SkillSignalRouteResult{}, errors.New("skill dead-letter replay reason is invalid")
	}
	originalJob, err := s.repository.GetSkillJob(ctx, request.Scope, request.JobID)
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if originalJob.State != core.SkillJobDeadLettered {
		return contracts.SkillSignalRouteResult{}, errors.New("only dead-lettered skill jobs can be replayed")
	}
	originalWorkflow, err := s.repository.GetSkillWorkflow(ctx, request.Scope, originalJob.WorkflowID)
	if err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	stableKey := originalJob.ID + "\x00" + request.IdempotencyKey
	workflowID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("skill-replay-workflow\x00"+stableKey)).String()
	jobID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("skill-replay-job\x00"+stableKey)).String()
	now := request.Now.UTC()
	workflow := core.SkillWorkflow{
		ID: workflowID, Scope: request.Scope, SkillID: originalJob.SkillID,
		OriginKind: core.SkillWorkflowOriginOperator, OriginID: request.ActorID, Kind: originalWorkflow.Kind,
		ContractVersion: originalWorkflow.ContractVersion, InputDigest: originalJob.InputDigest,
		State: core.SkillWorkflowOpen, CurrentStage: originalJob.Stage, Generation: 1,
		ConfigurationVersion: originalWorkflow.ConfigurationVersion, PolicyDigest: originalWorkflow.PolicyDigest,
		CreatedAt: now, UpdatedAt: now,
	}
	job := core.SkillJob{
		ID: jobID, WorkflowID: workflowID, Scope: request.Scope, SkillID: originalJob.SkillID,
		Stage: originalJob.Stage, ContractVersion: originalJob.ContractVersion, InputDigest: originalJob.InputDigest,
		PolicyVersion: originalJob.PolicyVersion, State: core.SkillJobQueued, Priority: originalJob.Priority,
		ReadyAt: now, MaxAttempts: originalJob.MaxAttempts, ReplayOfJobID: originalJob.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := workflow.Validate(); err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	if err := job.Validate(); err != nil {
		return contracts.SkillSignalRouteResult{}, err
	}
	return s.repository.RouteSkillSignal(ctx, workflow, job, nil)
}
