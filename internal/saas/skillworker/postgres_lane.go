package skillworker

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type LaneRepository interface {
	application.SkillWorkerRepository
	ClaimSkillJobsByLane(context.Context, core.SkillOrchestratorScope, string, int, time.Duration, time.Duration, time.Time, bool) ([]core.SkillJob, error)
}

type PostgresLaneWorker struct {
	repository LaneRepository
	registry   *application.SkillStageRegistry
	config     RuntimeConfig
	capacity   *application.SkillCapacityCoordinator
}

func NewPostgresLaneWorker(repository LaneRepository, registry *application.SkillStageRegistry, configuration RuntimeConfig) (*PostgresLaneWorker, error) {
	if repository == nil || registry == nil {
		return nil, errors.New("hosted PostgreSQL lane worker dependencies are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	stages := make(map[core.SkillOrchestratorStage]int)
	for _, stage := range []core.SkillOrchestratorStage{core.SkillStageDetect, core.SkillStageBuild, core.SkillStageEvaluate, core.SkillStageDecide, core.SkillStageStartCanary, core.SkillStageAnalyzeCanary, core.SkillStageActivate, core.SkillStageObserveSafety, core.SkillStageRollback, core.SkillStageReconcileMaterialization} {
		stages[stage] = configuration.Concurrency
	}
	stages[core.SkillStageEvaluate] = max(1, configuration.Concurrency/2)
	stages[core.SkillStageActivate] = 1
	stages[core.SkillStageRollback] = configuration.RollbackReserved
	capacity, err := application.NewSkillCapacityCoordinator(application.SkillCapacityLimits{Global: configuration.Concurrency, Tenant: configuration.TenantConcurrency, Workspace: configuration.WorkspaceConcurrency, RollbackReserved: configuration.RollbackReserved, Stages: stages})
	if err != nil {
		return nil, err
	}
	return &PostgresLaneWorker{repository: repository, registry: registry, config: configuration, capacity: capacity}, nil
}

func (w *PostgresLaneWorker) RunSkillWorkerLane(ctx context.Context, scope core.SkillOrchestratorScope, lane Lane, limit int) error {
	if lane != RollbackLane && lane != OrdinaryLane {
		return errors.New("hosted skill worker lane is invalid")
	}
	repository := &laneWorkerRepository{LaneRepository: w.repository, rollback: lane == RollbackLane}
	concurrency := w.config.Concurrency
	if concurrency > limit {
		concurrency = limit
	}
	worker, err := application.NewSkillOrchestratorWorker(repository, w.registry, application.SkillWorkerConfig{
		Scope: scope, Owner: w.config.WorkerIdentity + "-" + string(lane), ClaimBatch: limit, Concurrency: concurrency,
		LeaseDuration: w.config.LeaseDuration, RenewalInterval: w.config.LeaseDuration / 3, StageTimeout: w.config.StageTimeout,
	})
	if err != nil {
		return err
	}
	worker.WithCapacityCoordinator(w.capacity)
	_, err = worker.RunOnce(ctx)
	return err
}

type laneWorkerRepository struct {
	LaneRepository
	rollback bool
}

func (r *laneWorkerRepository) ClaimSkillJobs(ctx context.Context, scope core.SkillOrchestratorScope, owner string, limit int, lease, timeout time.Duration, now time.Time) ([]core.SkillJob, error) {
	return r.ClaimSkillJobsByLane(ctx, scope, owner, limit, lease, timeout, now, r.rollback)
}
