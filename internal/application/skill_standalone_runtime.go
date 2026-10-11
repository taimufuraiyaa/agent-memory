package application

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrSkillStandaloneLeaderContended = errors.New("skill standalone leader lease is held by another owner")

type SkillStandaloneLeaderRepository interface {
	AcquireSkillOrchestratorLeader(context.Context, string, string, string, time.Duration, time.Time) (int64, bool, error)
	RenewSkillOrchestratorLeader(context.Context, string, string, string, int64, time.Duration, time.Time) error
	ReleaseSkillOrchestratorLeader(context.Context, string, string, string, int64, time.Time) error
}

type SkillStandaloneWorker interface {
	RunOnce(context.Context) (SkillWorkerRunReport, error)
}

type SkillStandaloneReconciler interface {
	RunOnce(context.Context) (SkillReconciliationReport, error)
}

type SkillStandaloneRuntimeConfig struct {
	Enabled                bool
	InstallationID         string
	DatabaseID             string
	Owner                  string
	PollInterval           time.Duration
	ReconciliationInterval time.Duration
	LeaderLeaseDuration    time.Duration
	LeaderRenewalInterval  time.Duration
	DrainTimeout           time.Duration
}

func (c SkillStandaloneRuntimeConfig) Validate() error {
	if !validSkillSignalIdentifier(c.InstallationID) || !validSkillSignalIdentifier(c.DatabaseID) || !validSkillSignalIdentifier(c.Owner) {
		return errors.New("skill standalone runtime identity is invalid")
	}
	if c.PollInterval < 10*time.Millisecond || c.PollInterval > time.Hour || c.ReconciliationInterval < 10*time.Millisecond || c.ReconciliationInterval > 24*time.Hour || c.LeaderLeaseDuration < time.Second || c.LeaderLeaseDuration > time.Hour || c.LeaderRenewalInterval <= 0 || c.LeaderRenewalInterval >= c.LeaderLeaseDuration || c.DrainTimeout <= 0 || c.DrainTimeout > time.Hour {
		return errors.New("skill standalone runtime timing is invalid")
	}
	return nil
}

type SkillStandaloneRuntime struct {
	leaders    SkillStandaloneLeaderRepository
	worker     SkillStandaloneWorker
	reconciler SkillStandaloneReconciler
	config     SkillStandaloneRuntimeConfig
	now        func() time.Time

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewSkillStandaloneRuntime(leaders SkillStandaloneLeaderRepository, worker SkillStandaloneWorker, reconciler SkillStandaloneReconciler, config SkillStandaloneRuntimeConfig, now func() time.Time) (*SkillStandaloneRuntime, error) {
	if leaders == nil || worker == nil || reconciler == nil {
		return nil, errors.New("skill standalone runtime dependencies are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &SkillStandaloneRuntime{leaders: leaders, worker: worker, reconciler: reconciler, config: config, now: now}, nil
}

func (r *SkillStandaloneRuntime) Run(parent context.Context) error {
	if r == nil {
		return errors.New("skill standalone runtime is not configured")
	}
	if !r.config.Enabled {
		return nil
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return errors.New("skill standalone runtime is already running")
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	r.cancel, r.done = cancel, done
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		r.cancel, r.done = nil, nil
		r.mu.Unlock()
		close(done)
	}()

	fence, acquired, err := r.leaders.AcquireSkillOrchestratorLeader(ctx, r.config.InstallationID, r.config.DatabaseID, r.config.Owner, r.config.LeaderLeaseDuration, r.now().UTC())
	if err != nil {
		return err
	}
	if !acquired {
		return ErrSkillStandaloneLeaderContended
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(parent), r.config.DrainTimeout)
		defer releaseCancel()
		_ = r.leaders.ReleaseSkillOrchestratorLeader(releaseCtx, r.config.InstallationID, r.config.DatabaseID, r.config.Owner, fence, r.now().UTC())
	}()

	renewErrors := make(chan error, 1)
	renewCtx, renewCancel := context.WithCancel(ctx)
	defer renewCancel()
	go r.renewLeader(renewCtx, fence, cancel, renewErrors)

	workerTicker := time.NewTicker(r.config.PollInterval)
	reconcileTicker := time.NewTicker(r.config.ReconciliationInterval)
	defer workerTicker.Stop()
	defer reconcileTicker.Stop()
	workerReady, reconcileReady := true, true
	for {
		if workerReady {
			workerReady = false
			if _, err := r.worker.RunOnce(ctx); err != nil && ctx.Err() == nil {
				return err
			}
		}
		if reconcileReady {
			reconcileReady = false
			if _, err := r.reconciler.RunOnce(ctx); err != nil && ctx.Err() == nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			select {
			case err := <-renewErrors:
				return err
			default:
				return nil
			}
		case err := <-renewErrors:
			return err
		case <-workerTicker.C:
			workerReady = true
		case <-reconcileTicker.C:
			reconcileReady = true
		}
	}
}

func (r *SkillStandaloneRuntime) renewLeader(ctx context.Context, fence int64, stop context.CancelFunc, failed chan<- error) {
	ticker := time.NewTicker(r.config.LeaderRenewalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.leaders.RenewSkillOrchestratorLeader(ctx, r.config.InstallationID, r.config.DatabaseID, r.config.Owner, fence, r.config.LeaderLeaseDuration, r.now().UTC()); err != nil {
				select {
				case failed <- err:
				default:
				}
				stop()
				return
			}
		}
	}
}

func (r *SkillStandaloneRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
