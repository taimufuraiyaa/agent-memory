package application

import (
	"context"
	"errors"
	"sync"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillCapacityLimits struct {
	Global           int
	Tenant           int
	Workspace        int
	RollbackReserved int
	Stages           map[core.SkillOrchestratorStage]int
}

func (l SkillCapacityLimits) Validate() error {
	if l.Global < 1 || l.Global > 10_000 || l.Tenant < 1 || l.Tenant > l.Global || l.Workspace < 1 || l.Workspace > l.Tenant || l.RollbackReserved < 1 || l.RollbackReserved >= l.Global {
		return errors.New("skill capacity limits are invalid")
	}
	for stage, limit := range l.Stages {
		if !stage.Valid() || limit < 1 || limit > l.Global {
			return errors.New("skill stage capacity limit is invalid")
		}
	}
	return nil
}

type SkillCapacityPermit interface {
	Release()
}

type SkillCapacityCoordinator struct {
	mu         sync.Mutex
	limits     SkillCapacityLimits
	global     int
	ordinary   int
	tenants    map[string]int
	workspaces map[string]int
	stages     map[core.SkillOrchestratorStage]int
	waiters    []*skillCapacityWaiter
}

type skillCapacityWaiter struct {
	job     core.SkillJob
	granted chan *skillCapacityLease
}

type skillCapacityLease struct {
	once        sync.Once
	coordinator *SkillCapacityCoordinator
	job         core.SkillJob
}

func NewSkillCapacityCoordinator(limits SkillCapacityLimits) (*SkillCapacityCoordinator, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &SkillCapacityCoordinator{limits: limits, tenants: make(map[string]int), workspaces: make(map[string]int), stages: make(map[core.SkillOrchestratorStage]int)}, nil
}

func (c *SkillCapacityCoordinator) Acquire(ctx context.Context, job core.SkillJob) (SkillCapacityPermit, error) {
	if c == nil || ctx == nil || job.Scope.Validate() != nil || !job.Stage.Valid() {
		return nil, errors.New("valid skill capacity request is required")
	}
	waiter := &skillCapacityWaiter{job: job, granted: make(chan *skillCapacityLease, 1)}
	c.mu.Lock()
	c.waiters = append(c.waiters, waiter)
	c.grantEligibleLocked()
	c.mu.Unlock()
	select {
	case lease := <-waiter.granted:
		return lease, nil
	case <-ctx.Done():
		c.mu.Lock()
		for index, queued := range c.waiters {
			if queued == waiter {
				c.waiters = append(c.waiters[:index], c.waiters[index+1:]...)
				break
			}
		}
		c.mu.Unlock()
		select {
		case lease := <-waiter.granted:
			lease.Release()
		default:
		}
		return nil, ctx.Err()
	}
}

func (c *SkillCapacityCoordinator) grantEligibleLocked() {
	for index := 0; index < len(c.waiters); {
		waiter := c.waiters[index]
		if !c.canGrantLocked(waiter.job) {
			index++
			continue
		}
		c.reserveLocked(waiter.job)
		c.waiters = append(c.waiters[:index], c.waiters[index+1:]...)
		waiter.granted <- &skillCapacityLease{coordinator: c, job: waiter.job}
	}
}

func (c *SkillCapacityCoordinator) canGrantLocked(job core.SkillJob) bool {
	if c.global >= c.limits.Global {
		return false
	}
	if limit := c.limits.Stages[job.Stage]; limit > 0 && c.stages[job.Stage] >= limit {
		return false
	}
	if job.Stage == core.SkillStageRollback {
		return true
	}
	return c.tenants[capacityTenantKey(job.Scope)] < c.limits.Tenant && c.workspaces[capacityWorkspaceKey(job.Scope)] < c.limits.Workspace && c.ordinary < c.limits.Global-c.limits.RollbackReserved
}

func (c *SkillCapacityCoordinator) reserveLocked(job core.SkillJob) {
	c.global++
	if job.Stage != core.SkillStageRollback {
		c.ordinary++
	}
	if job.Stage != core.SkillStageRollback {
		c.tenants[capacityTenantKey(job.Scope)]++
		c.workspaces[capacityWorkspaceKey(job.Scope)]++
	}
	c.stages[job.Stage]++
}

func (l *skillCapacityLease) Release() {
	if l == nil || l.coordinator == nil {
		return
	}
	l.once.Do(func() {
		c := l.coordinator
		c.mu.Lock()
		c.global--
		if l.job.Stage != core.SkillStageRollback {
			c.ordinary--
		}
		if l.job.Stage != core.SkillStageRollback {
			decrementCapacityCount(c.tenants, capacityTenantKey(l.job.Scope))
			decrementCapacityCount(c.workspaces, capacityWorkspaceKey(l.job.Scope))
		}
		decrementCapacityCount(c.stages, l.job.Stage)
		c.grantEligibleLocked()
		c.mu.Unlock()
	})
}

func capacityWorkspaceKey(scope core.SkillOrchestratorScope) string {
	return scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + scope.Environment
}

func capacityTenantKey(scope core.SkillOrchestratorScope) string {
	if scope.TenantID != "" {
		return scope.TenantID
	}
	return "portable\x00" + scope.WorkspaceID
}

func decrementCapacityCount[K comparable](counts map[K]int, key K) {
	if counts[key] <= 1 {
		delete(counts, key)
		return
	}
	counts[key]--
}
