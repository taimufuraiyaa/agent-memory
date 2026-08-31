package skillreconciler

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

const DatabaseRole = "agent_memory_skill_reconciler"

type PartitionRepository interface {
	ClaimSkillReconciliationPartition(context.Context, core.SkillOrchestratorScope, string, time.Duration, time.Time) (saaspostgres.SkillReconciliationPartitionLease, bool, error)
	ReleaseSkillReconciliationPartition(context.Context, saaspostgres.SkillReconciliationPartitionLease, time.Time) error
}

type Reconciler interface {
	RunOnce(context.Context) (application.SkillReconciliationReport, error)
}

type ReconcilerFactory interface {
	SkillReconcilerFor(core.SkillOrchestratorScope) (Reconciler, error)
}

type RuntimeConfig struct {
	Enabled        bool
	DatabaseURL    string
	DatabaseRole   string
	Owner          string
	Assignments    []core.SkillOrchestratorScope
	PartitionLimit int
	LeaseDuration  time.Duration
	PollInterval   time.Duration
}

func (c RuntimeConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.DatabaseURL) == "" || c.DatabaseRole != DatabaseRole || c.Owner == "" || len(c.Assignments) == 0 || len(c.Assignments) > 10_000 || c.PartitionLimit < 1 || c.PartitionLimit > 1_000 || c.LeaseDuration < time.Second || c.LeaseDuration > time.Hour || c.PollInterval < 10*time.Millisecond || c.PollInterval > time.Hour {
		return errors.New("hosted skill reconciler configuration is invalid")
	}
	seen := make(map[core.SkillOrchestratorScope]struct{}, len(c.Assignments))
	for _, scope := range c.Assignments {
		if err := scope.Validate(); err != nil || scope.TenantID == "" {
			return errors.New("hosted skill reconciler assignments must be tenant-scoped")
		}
		if _, exists := seen[scope]; exists {
			return errors.New("hosted skill reconciler assignments must be unique")
		}
		seen[scope] = struct{}{}
	}
	return nil
}

type CycleReport struct {
	Scanned   int
	Claimed   int
	Completed int
	Failed    int
	Skipped   int
}

type Runtime struct {
	repository PartitionRepository
	factory    ReconcilerFactory
	config     RuntimeConfig
	now        func() time.Time

	mu   sync.Mutex
	next int
}

func NewRuntime(repository PartitionRepository, factory ReconcilerFactory, configuration RuntimeConfig, now func() time.Time) (*Runtime, error) {
	if repository == nil || factory == nil {
		return nil, errors.New("hosted skill reconciler dependencies are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	if !configuration.Enabled {
		return nil, errors.New("hosted skill reconciler runtime is disabled")
	}
	if now == nil {
		now = time.Now
	}
	return &Runtime{repository: repository, factory: factory, config: configuration, now: now}, nil
}

func (r *Runtime) RunOnce(ctx context.Context) CycleReport {
	r.mu.Lock()
	start := r.next % len(r.config.Assignments)
	r.next = (start + r.config.PartitionLimit) % len(r.config.Assignments)
	r.mu.Unlock()
	limit := r.config.PartitionLimit
	if limit > len(r.config.Assignments) {
		limit = len(r.config.Assignments)
	}
	report := CycleReport{}
	for offset := 0; offset < limit; offset++ {
		if ctx.Err() != nil {
			break
		}
		scope := r.config.Assignments[(start+offset)%len(r.config.Assignments)]
		report.Scanned++
		lease, claimed, err := r.repository.ClaimSkillReconciliationPartition(ctx, scope, r.config.Owner, r.config.LeaseDuration, r.now().UTC())
		if err != nil {
			report.Failed++
			continue
		}
		if !claimed {
			report.Skipped++
			continue
		}
		report.Claimed++
		reconciler, err := r.factory.SkillReconcilerFor(scope)
		if err == nil {
			_, err = reconciler.RunOnce(ctx)
		}
		releaseTimeout := 5 * time.Second
		if r.config.LeaseDuration < releaseTimeout {
			releaseTimeout = r.config.LeaseDuration
		}
		releaseContext, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		releaseErr := r.repository.ReleaseSkillReconciliationPartition(releaseContext, lease, r.now().UTC())
		cancelRelease()
		if err != nil || releaseErr != nil {
			report.Failed++
			continue
		}
		report.Completed++
	}
	return report
}

func (r *Runtime) Run(ctx context.Context) {
	ticker := time.NewTicker(r.config.PollInterval)
	defer ticker.Stop()
	for {
		r.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
