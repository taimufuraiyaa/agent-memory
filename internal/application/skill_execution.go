package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillExecutionInput struct {
	ID                    string                     `json:"id"`
	Workspace             string                     `json:"workspace"`
	ResolutionID          string                     `json:"resolution_id"`
	EpisodeID             string                     `json:"episode_id"`
	Outcome               core.SkillExecutionOutcome `json:"outcome"`
	IndependentlyVerified bool                       `json:"independently_verified"`
	FailureClass          string                     `json:"failure_class,omitempty"`
	FeedbackClass         string                     `json:"feedback_class,omitempty"`
	StartedAt             time.Time                  `json:"started_at"`
	CompletedAt           time.Time                  `json:"completed_at"`
	InputTokens           int64                      `json:"input_tokens,omitempty"`
	OutputTokens          int64                      `json:"output_tokens,omitempty"`
	ToolCalls             int64                      `json:"tool_calls,omitempty"`
}

type skillExecutionRepositoryContract interface {
	GetSkillResolution(context.Context, string, string) (core.SkillResolution, error)
	GetSkillResolutionAcknowledgement(context.Context, string, string) (core.SkillResolutionAcknowledgement, error)
	GetSkillExecution(context.Context, string, string) (core.SkillExecution, error)
	CreateSkillExecution(context.Context, core.SkillExecution) error
	PruneSkillExecutions(context.Context, string, time.Time) (int64, error)
}

type SkillExecutionService struct {
	repository skillExecutionRepositoryContract
}

func NewSkillExecutionService(repository skillExecutionRepositoryContract) *SkillExecutionService {
	return &SkillExecutionService{repository: repository}
}

func (s *SkillExecutionService) Complete(ctx context.Context, input SkillExecutionInput) (core.SkillExecution, error) {
	if s == nil || s.repository == nil {
		return core.SkillExecution{}, errors.New("skill execution repository is required")
	}
	if err := validateSkillExecutionInput(input); err != nil {
		return core.SkillExecution{}, err
	}
	resolution, err := s.repository.GetSkillResolution(ctx, input.Workspace, input.ResolutionID)
	if err != nil {
		return core.SkillExecution{}, err
	}
	acknowledgement, err := s.repository.GetSkillResolutionAcknowledgement(ctx, input.Workspace, input.ResolutionID)
	if err != nil {
		return core.SkillExecution{}, errors.New("skill execution requires exact resolution acknowledgement")
	}
	if acknowledgement.TaskID != input.EpisodeID || acknowledgement.RevisionID != resolution.RevisionID || acknowledgement.RevisionDigest != resolution.Digest || acknowledgement.PrincipalID != resolution.PrincipalID {
		return core.SkillExecution{}, errors.New("skill execution acknowledgement scope does not match resolution")
	}
	execution := core.SkillExecution{
		ID: input.ID, Workspace: input.Workspace, Environment: resolution.Environment, EpisodeID: input.EpisodeID,
		SkillID: resolution.SkillID, RevisionID: resolution.RevisionID, RevisionDigest: resolution.Digest,
		ResolutionID: resolution.ID, Acknowledged: true, AcknowledgedAt: acknowledgement.AcknowledgedAt,
		Outcome: input.Outcome, IndependentlyVerified: input.IndependentlyVerified, FailureClass: input.FailureClass,
		StartedAt: input.StartedAt.UTC(), CompletedAt: input.CompletedAt.UTC(), DurationMS: input.CompletedAt.Sub(input.StartedAt).Milliseconds(),
		InputTokens: input.InputTokens, OutputTokens: input.OutputTokens, ToolCalls: input.ToolCalls, FeedbackClass: input.FeedbackClass,
	}
	if err := execution.Validate(); err != nil {
		return core.SkillExecution{}, err
	}
	existing, err := s.repository.GetSkillExecution(ctx, input.Workspace, input.ID)
	if err == nil {
		if existing != execution {
			return core.SkillExecution{}, errors.New("skill execution id is already bound to different telemetry")
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return core.SkillExecution{}, err
	}
	if err := s.repository.CreateSkillExecution(ctx, execution); err != nil {
		return core.SkillExecution{}, err
	}
	return execution, nil
}

func (s *SkillExecutionService) Prune(ctx context.Context, workspace string, before time.Time) (int64, error) {
	if s == nil || s.repository == nil || strings.TrimSpace(workspace) == "" || before.IsZero() {
		return 0, errors.New("skill execution retention scope and cutoff are required")
	}
	return s.repository.PruneSkillExecutions(ctx, workspace, before.UTC())
}

func validateSkillExecutionInput(input SkillExecutionInput) error {
	for field, value := range map[string]string{"id": input.ID, "workspace": input.Workspace, "resolution_id": input.ResolutionID, "episode_id": input.EpisodeID} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return fmt.Errorf("skill execution %s is required and bounded", field)
		}
	}
	if !input.Outcome.Valid() || input.StartedAt.IsZero() || input.CompletedAt.Before(input.StartedAt) || input.InputTokens < 0 || input.OutputTokens < 0 || input.ToolCalls < 0 {
		return errors.New("skill execution outcome, timestamps, or counters are invalid")
	}
	if !allowedSkillFailureClass(input.FailureClass) || !allowedSkillFeedbackClass(input.FeedbackClass) {
		return errors.New("skill execution telemetry classifications are invalid")
	}
	if input.Outcome == core.SkillExecutionFailure && input.FailureClass == "" {
		return errors.New("failed skill execution requires failure_class")
	}
	return nil
}

func allowedSkillFailureClass(value string) bool {
	switch value {
	case "", "incorrect_result", "tool_error", "timeout", "safety_violation", "digest_mismatch", "unauthorized_content", "user_correction":
		return true
	default:
		return false
	}
}

func allowedSkillFeedbackClass(value string) bool {
	switch value {
	case "", "positive", "negative", "harmful", "correction":
		return true
	default:
		return false
	}
}
