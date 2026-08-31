package skillworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
)

type Lane string

const (
	RollbackLane Lane = "rollback"
	OrdinaryLane Lane = "ordinary"
)

type Readiness interface {
	CheckSkillWorkerReadiness(context.Context, RuntimeConfig) error
}

type LaneWorker interface {
	RunSkillWorkerLane(context.Context, core.SkillOrchestratorScope, Lane, int) error
}

type Runtime struct {
	configuration RuntimeConfig
	readiness     Readiness
	worker        LaneWorker
	ready         atomic.Bool
	live          atomic.Bool

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	next   int
}

func NewRuntime(configuration RuntimeConfig, readiness Readiness, worker LaneWorker) (*Runtime, error) {
	if readiness == nil || worker == nil {
		return nil, errors.New("hosted skill worker dependencies are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &Runtime{configuration: configuration, readiness: readiness, worker: worker}, nil
}

func (r *Runtime) Live() bool  { return r != nil && r.live.Load() }
func (r *Runtime) Ready() bool { return r != nil && r.ready.Load() }

func (r *Runtime) Run(parent context.Context) error {
	if r == nil {
		return errors.New("hosted skill worker runtime is not configured")
	}
	r.live.Store(true)
	defer r.live.Store(false)
	if !r.configuration.Enabled {
		<-parent.Done()
		return nil
	}
	if err := r.readiness.CheckSkillWorkerReadiness(parent, r.configuration); err != nil {
		return err
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return errors.New("hosted skill worker runtime is already running")
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	r.cancel, r.done = cancel, done
	r.mu.Unlock()
	r.ready.Store(true)
	defer func() {
		r.ready.Store(false)
		cancel()
		r.mu.Lock()
		r.cancel, r.done = nil, nil
		r.mu.Unlock()
		close(done)
	}()
	ticker := time.NewTicker(r.configuration.PollInterval)
	defer ticker.Stop()
	for {
		if err := r.runCycle(ctx); err != nil && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Runtime) runCycle(ctx context.Context) error {
	assignments := r.configuration.Assignments
	start := r.next % len(assignments)
	ordered := make([]core.SkillOrchestratorScope, len(assignments))
	for offset := range assignments {
		ordered[offset] = assignments[(start+offset)%len(assignments)]
	}
	r.next = (start + 1) % len(assignments)
	errorsByLane := make(chan error, 2)
	var wait sync.WaitGroup
	for _, lane := range []struct {
		name    Lane
		workers int
	}{{RollbackLane, r.configuration.RollbackReserved}, {OrdinaryLane, r.configuration.Concurrency - r.configuration.RollbackReserved}} {
		lane := lane
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByLane <- r.runLanePool(ctx, ordered, lane.name, lane.workers)
		}()
	}
	wait.Wait()
	close(errorsByLane)
	for err := range errorsByLane {
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) runLanePool(ctx context.Context, assignments []core.SkillOrchestratorScope, lane Lane, workers int) error {
	queue := make(chan core.SkillOrchestratorScope)
	failed := make(chan error, 1)
	poolCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for scope := range queue {
				if err := r.worker.RunSkillWorkerLane(poolCtx, scope, lane, 1); err != nil {
					select {
					case failed <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}
send:
	for _, scope := range assignments {
		select {
		case queue <- scope:
		case <-poolCtx.Done():
			break send
		}
	}
	close(queue)
	wait.Wait()
	select {
	case err := <-failed:
		return err
	default:
		return poolCtx.Err()
	}
}

func (r *Runtime) Drain(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	r.ready.Store(false)
	cancel()
	select {
	case <-done:
		for _, scope := range r.configuration.Assignments {
			observability.DefaultSkillOrchestratorMetrics().ObserveDrain(scope.Environment, "success")
		}
		return nil
	case <-ctx.Done():
		for _, scope := range r.configuration.Assignments {
			observability.DefaultSkillOrchestratorMetrics().ObserveDrain(scope.Environment, "timeout")
		}
		return ctx.Err()
	}
}
