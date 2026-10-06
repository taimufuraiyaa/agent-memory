package application

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillAttemptRetentionRepository interface {
	PruneSkillOrchestratorAttempts(context.Context, core.SkillOrchestratorScope, time.Time, int) (int64, error)
}

type SkillAttemptRetentionSweep struct {
	repository SkillAttemptRetentionRepository
	retention  time.Duration
}

func NewSkillAttemptRetentionSweep(repository SkillAttemptRetentionRepository, retention time.Duration) (*SkillAttemptRetentionSweep, error) {
	if repository == nil || retention < 24*time.Hour || retention > 10*365*24*time.Hour {
		return nil, errors.New("bounded skill attempt retention configuration is required")
	}
	return &SkillAttemptRetentionSweep{repository: repository, retention: retention}, nil
}

func (s *SkillAttemptRetentionSweep) Sweep(ctx context.Context, request SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
	if s == nil || request.Domain != core.SkillReconcileTerminalCleanup || request.Now.IsZero() || request.Limit < 1 || request.Limit > 1_000 {
		return SkillReconciliationSweepResult{}, errors.New("valid terminal cleanup request is required")
	}
	removed, err := s.repository.PruneSkillOrchestratorAttempts(ctx, request.Scope, request.Now.Add(-s.retention), request.Limit)
	if err != nil {
		return SkillReconciliationSweepResult{}, err
	}
	return SkillReconciliationSweepResult{
		Complete: removed < int64(request.Limit),
		Counters: core.SkillReconciliationCounters{Scanned: removed, Repaired: removed},
	}, nil
}
