package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillDependencyCoordinatorSchedulesBlockedThenResolvesAuthoritatively(t *testing.T) {
	now := time.Now().UTC()
	repository := &dependencyCoordinatorRepository{resolution: contracts.SkillDependencyResolution{State: contracts.SkillDependenciesReady}}
	coordinator := NewSkillDependencyCoordinator(repository)
	job := core.SkillJob{ID: "child", WorkflowID: "workflow", Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"},
		Stage: core.SkillStageBuild, ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: "sha256:" + strings.Repeat("a", 64),
		PolicyVersion: 1, State: core.SkillJobQueued, Priority: 100, ReadyAt: now, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now}
	dependency := core.SkillJobDependency{JobID: job.ID, ParentJobID: "parent", AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: now}
	result, err := coordinator.Schedule(context.Background(), contracts.SkillSuccessorSchedule{Job: job, Dependencies: []core.SkillJobDependency{dependency}, ExpectedWorkflowGeneration: 4, Now: now})
	if err != nil || result.State != contracts.SkillDependenciesReady || repository.schedules != 1 || repository.resolutions != 1 {
		t.Fatalf("result=%+v schedules=%d resolutions=%d err=%v", result, repository.schedules, repository.resolutions, err)
	}
	if repository.input.Job.State != core.SkillJobBlocked || repository.input.Job.BlockedReason != "dependencies_pending" || repository.input.Job.DependencyCount != 1 {
		t.Fatalf("successor was not fail-closed before resolution: %+v", repository.input.Job)
	}
}

func TestSkillDependencyCoordinatorRejectsMissingDependencyAndPropagatesStaleGeneration(t *testing.T) {
	repository := &dependencyCoordinatorRepository{scheduleErr: errors.New("stale generation")}
	coordinator := NewSkillDependencyCoordinator(repository)
	if _, err := coordinator.Schedule(context.Background(), contracts.SkillSuccessorSchedule{}); err == nil {
		t.Fatal("expected dependency requirement")
	}
	input := contracts.SkillSuccessorSchedule{Dependencies: []core.SkillJobDependency{{ParentJobID: "parent"}}}
	if _, err := coordinator.Schedule(context.Background(), input); err == nil || repository.resolutions != 0 {
		t.Fatalf("expected schedule error without resolution, resolutions=%d err=%v", repository.resolutions, err)
	}
}

type dependencyCoordinatorRepository struct {
	input       contracts.SkillSuccessorSchedule
	resolution  contracts.SkillDependencyResolution
	scheduleErr error
	schedules   int
	resolutions int
}

func (r *dependencyCoordinatorRepository) ScheduleSkillSuccessor(_ context.Context, input contracts.SkillSuccessorSchedule) (core.SkillJob, bool, error) {
	r.schedules++
	r.input = input
	return input.Job, true, r.scheduleErr
}

func (r *dependencyCoordinatorRepository) ResolveSkillJobDependencies(context.Context, core.SkillOrchestratorScope, string, int64, time.Time) (contracts.SkillDependencyResolution, error) {
	r.resolutions++
	return r.resolution, nil
}
