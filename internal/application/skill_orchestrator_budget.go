package application

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillEvaluationBudgetRepository interface {
	ReserveSkillEvaluationBudget(context.Context, core.SkillEvaluationBudgetReservationRequest) (core.SkillEvaluationBudgetReservationRecord, error)
	CommitSkillEvaluationBudget(context.Context, core.SkillOrchestratorScope, string, int64, time.Time) error
	ReleaseSkillEvaluationBudget(context.Context, core.SkillOrchestratorScope, string, time.Time) error
}

type PersistentSkillEvaluationBudgetConfig struct {
	LimitUnits     int64
	Period         time.Duration
	ReservationTTL time.Duration
}

func (c PersistentSkillEvaluationBudgetConfig) Validate() error {
	if c.LimitUnits < 1 || c.LimitUnits > 1_000_000_000 || c.Period < time.Hour || c.Period > 31*24*time.Hour || c.ReservationTTL < time.Minute || c.ReservationTTL > c.Period {
		return errors.New("persistent skill evaluation budget bounds are invalid")
	}
	return nil
}

type PersistentSkillEvaluationBudget struct {
	repository SkillEvaluationBudgetRepository
	config     PersistentSkillEvaluationBudgetConfig
	now        func() time.Time
}

func NewPersistentSkillEvaluationBudget(repository SkillEvaluationBudgetRepository, config PersistentSkillEvaluationBudgetConfig, now func() time.Time) (*PersistentSkillEvaluationBudget, error) {
	if repository == nil {
		return nil, errors.New("skill evaluation budget repository is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &PersistentSkillEvaluationBudget{repository: repository, config: config, now: now}, nil
}

func (b *PersistentSkillEvaluationBudget) Reserve(ctx context.Context, request SkillEvaluationBudgetRequest) (SkillEvaluationBudgetReservation, error) {
	if b == nil || request.Scope.Validate() != nil || request.JobID == "" || request.PolicyVersion < 1 || request.Units < 1 || request.Units > b.config.LimitUnits {
		return nil, errors.New("skill evaluation budget request is invalid")
	}
	now := b.now().UTC()
	periodStart := time.Unix(0, now.UnixNano()/int64(b.config.Period)*int64(b.config.Period)).UTC()
	record, err := b.repository.ReserveSkillEvaluationBudget(ctx, core.SkillEvaluationBudgetReservationRequest{Scope: request.Scope, JobID: request.JobID, PolicyVersion: request.PolicyVersion, PeriodStart: periodStart, LimitUnits: b.config.LimitUnits, Units: request.Units, ExpiresAt: now.Add(b.config.ReservationTTL), Now: now})
	if err != nil {
		return nil, err
	}
	return &persistentSkillEvaluationReservation{repository: b.repository, record: record, now: b.now}, nil
}

type persistentSkillEvaluationReservation struct {
	repository SkillEvaluationBudgetRepository
	record     core.SkillEvaluationBudgetReservationRecord
	now        func() time.Time
}

func (r *persistentSkillEvaluationReservation) Commit(ctx context.Context, units int64) error {
	if units < 1 || units > r.record.ReservedUnits {
		return errors.New("skill evaluation committed units exceed reservation")
	}
	return r.repository.CommitSkillEvaluationBudget(ctx, r.record.Scope, r.record.JobID, units, r.now().UTC())
}

func (r *persistentSkillEvaluationReservation) Release(ctx context.Context) error {
	return r.repository.ReleaseSkillEvaluationBudget(ctx, r.record.Scope, r.record.JobID, r.now().UTC())
}
