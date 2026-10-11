package application

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillDependencyRepository interface {
	ScheduleSkillSuccessor(context.Context, contracts.SkillSuccessorSchedule) (core.SkillJob, bool, error)
	ResolveSkillJobDependencies(context.Context, core.SkillOrchestratorScope, string, int64, time.Time) (contracts.SkillDependencyResolution, error)
}

type SkillDependencyCoordinator struct{ repository SkillDependencyRepository }

func NewSkillDependencyCoordinator(repository SkillDependencyRepository) *SkillDependencyCoordinator {
	return &SkillDependencyCoordinator{repository: repository}
}

func (c *SkillDependencyCoordinator) Schedule(ctx context.Context, input contracts.SkillSuccessorSchedule) (contracts.SkillDependencyResolution, error) {
	if c == nil || c.repository == nil {
		return contracts.SkillDependencyResolution{}, errors.New("skill dependency repository is required")
	}
	if len(input.Dependencies) == 0 {
		return contracts.SkillDependencyResolution{}, errors.New("skill successor requires dependencies")
	}
	input.Job.State = core.SkillJobBlocked
	input.Job.BlockedReason = "dependencies_pending"
	input.Job.DependencyCount = len(input.Dependencies)
	if _, _, err := c.repository.ScheduleSkillSuccessor(ctx, input); err != nil {
		return contracts.SkillDependencyResolution{}, err
	}
	return c.repository.ResolveSkillJobDependencies(ctx, input.Job.Scope, input.Job.ID, input.ExpectedWorkflowGeneration+1, input.Now)
}

func (c *SkillDependencyCoordinator) Resolve(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, generation int64, now time.Time) (contracts.SkillDependencyResolution, error) {
	if c == nil || c.repository == nil {
		return contracts.SkillDependencyResolution{}, errors.New("skill dependency repository is required")
	}
	return c.repository.ResolveSkillJobDependencies(ctx, scope, jobID, generation, now)
}
