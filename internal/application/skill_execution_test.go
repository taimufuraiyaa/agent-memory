package application

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillExecutionServiceCompletesAcknowledgedOutcomes(t *testing.T) {
	for _, outcome := range []core.SkillExecutionOutcome{core.SkillExecutionSuccess, core.SkillExecutionPartial, core.SkillExecutionFailure} {
		fixture := newSkillExecutionFixture()
		input := fixture.input(outcome)
		execution, err := fixture.service.Complete(context.Background(), input)
		if err != nil || execution.Outcome != outcome || execution.RevisionID != fixture.resolution.RevisionID {
			t.Fatalf("outcome %s execution = %+v, %v", outcome, execution, err)
		}
	}
}

func TestSkillExecutionServiceReplaysExactDuplicate(t *testing.T) {
	fixture := newSkillExecutionFixture()
	input := fixture.input(core.SkillExecutionSuccess)
	first, err := fixture.service.Complete(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Complete(context.Background(), input)
	if err != nil || first.ID != second.ID || fixture.repository.writes != 1 {
		t.Fatalf("duplicate = %+v, writes %d, err %v", second, fixture.repository.writes, err)
	}
}

func TestSkillExecutionServiceRejectsUnacknowledgedAndUnsafeTelemetry(t *testing.T) {
	fixture := newSkillExecutionFixture()
	fixture.repository.ack = core.SkillResolutionAcknowledgement{}
	if _, err := fixture.service.Complete(context.Background(), fixture.input(core.SkillExecutionSuccess)); err == nil {
		t.Fatal("unacknowledged resolution entered execution telemetry")
	}
	fixture = newSkillExecutionFixture()
	input := fixture.input(core.SkillExecutionFailure)
	input.FailureClass = "raw secret: password=example"
	if _, err := fixture.service.Complete(context.Background(), input); err == nil {
		t.Fatal("unbounded raw failure content entered telemetry")
	}
}

func TestSkillExecutionServiceAllowsMissingOptionalCountersAndPrunesRetention(t *testing.T) {
	fixture := newSkillExecutionFixture()
	input := fixture.input(core.SkillExecutionSuccess)
	input.InputTokens, input.OutputTokens, input.ToolCalls = 0, 0, 0
	if _, err := fixture.service.Complete(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	removed, err := fixture.service.Prune(context.Background(), "ws", fixture.now.Add(time.Hour))
	if err != nil || removed != 1 || len(fixture.repository.executions) != 0 {
		t.Fatalf("prune removed %d, executions %d, err %v", removed, len(fixture.repository.executions), err)
	}
}

type skillExecutionRepository struct {
	resolution core.SkillResolution
	ack        core.SkillResolutionAcknowledgement
	executions map[string]core.SkillExecution
	writes     int
}

func (r *skillExecutionRepository) GetSkillResolution(_ context.Context, workspace, resolutionID string) (core.SkillResolution, error) {
	if workspace != r.resolution.Workspace || resolutionID != r.resolution.ID {
		return core.SkillResolution{}, errors.New("resolution not found")
	}
	return r.resolution, nil
}

func (r *skillExecutionRepository) GetSkillResolutionAcknowledgement(_ context.Context, workspace, resolutionID string) (core.SkillResolutionAcknowledgement, error) {
	if r.ack.ResolutionID == "" || workspace != r.ack.Workspace || resolutionID != r.ack.ResolutionID {
		return core.SkillResolutionAcknowledgement{}, sql.ErrNoRows
	}
	return r.ack, nil
}

func (r *skillExecutionRepository) GetSkillExecution(_ context.Context, workspace, executionID string) (core.SkillExecution, error) {
	execution, exists := r.executions[executionID]
	if !exists || execution.Workspace != workspace {
		return core.SkillExecution{}, sql.ErrNoRows
	}
	return execution, nil
}

func (r *skillExecutionRepository) CreateSkillExecution(_ context.Context, execution core.SkillExecution) error {
	r.executions[execution.ID] = execution
	r.writes++
	return nil
}

func (r *skillExecutionRepository) PruneSkillExecutions(_ context.Context, workspace string, before time.Time) (int64, error) {
	var removed int64
	for id, execution := range r.executions {
		if execution.Workspace == workspace && execution.CompletedAt.Before(before) {
			delete(r.executions, id)
			removed++
		}
	}
	return removed, nil
}

type skillExecutionFixture struct {
	repository *skillExecutionRepository
	service    *SkillExecutionService
	resolution core.SkillResolution
	now        time.Time
}

func newSkillExecutionFixture() skillExecutionFixture {
	now := time.Date(2026, 8, 29, 23, 0, 0, 0, time.UTC)
	resolution := core.SkillResolution{ID: "resolution-1", Workspace: "ws", Environment: "local", PrincipalID: "agent-1", TaskID: "episode-1", SkillID: "skill-1", RevisionID: "revision-2", RevisionNumber: 2, Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Reason: core.SkillResolutionCanary, PolicyVersion: 1, AcknowledgementTokenHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExpiresAt: now.Add(time.Minute), ResolvedAt: now}
	ack := core.SkillResolutionAcknowledgement{Workspace: "ws", ResolutionID: resolution.ID, PrincipalID: resolution.PrincipalID, TaskID: resolution.TaskID, RevisionID: resolution.RevisionID, RevisionDigest: resolution.Digest, AcknowledgedAt: now.Add(time.Second)}
	repository := &skillExecutionRepository{resolution: resolution, ack: ack, executions: map[string]core.SkillExecution{}}
	return skillExecutionFixture{repository: repository, service: NewSkillExecutionService(repository), resolution: resolution, now: now}
}

func (f skillExecutionFixture) input(outcome core.SkillExecutionOutcome) SkillExecutionInput {
	failure := ""
	if outcome == core.SkillExecutionFailure {
		failure = "incorrect_result"
	}
	return SkillExecutionInput{ID: "execution-1", Workspace: "ws", ResolutionID: f.resolution.ID, EpisodeID: f.resolution.TaskID, Outcome: outcome, IndependentlyVerified: true, FailureClass: failure, FeedbackClass: "positive", StartedAt: f.now.Add(time.Second), CompletedAt: f.now.Add(10 * time.Second), InputTokens: 100, OutputTokens: 50, ToolCalls: 2}
}
