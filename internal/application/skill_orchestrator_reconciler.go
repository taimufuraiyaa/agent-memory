package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
)

type SkillReconciliationRequest struct {
	Scope                core.SkillOrchestratorScope
	Domain               core.SkillReconciliationDomain
	Cursor               string
	ConfigurationVersion int64
	Limit                int
	Now                  time.Time
}

type SkillReconciliationSweepResult struct {
	NextCursor string
	Complete   bool
	Counters   core.SkillReconciliationCounters
}

type SkillReconciliationSweep interface {
	Sweep(context.Context, SkillReconciliationRequest) (SkillReconciliationSweepResult, error)
}

type SkillReconciliationSweepFunc func(context.Context, SkillReconciliationRequest) (SkillReconciliationSweepResult, error)

func (f SkillReconciliationSweepFunc) Sweep(ctx context.Context, request SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
	return f(ctx, request)
}

type SkillReconciliationRegistry struct {
	mu     sync.RWMutex
	sweeps map[core.SkillReconciliationDomain]SkillReconciliationSweep
}

func NewSkillReconciliationRegistry() *SkillReconciliationRegistry {
	return &SkillReconciliationRegistry{sweeps: make(map[core.SkillReconciliationDomain]SkillReconciliationSweep)}
}

func (r *SkillReconciliationRegistry) Register(domain core.SkillReconciliationDomain, sweep SkillReconciliationSweep) error {
	if r == nil || !domain.Valid() || sweep == nil {
		return errors.New("valid skill reconciliation registration is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sweeps[domain]; exists {
		return errors.New("skill reconciliation domain is already registered")
	}
	r.sweeps[domain] = sweep
	return nil
}

func (r *SkillReconciliationRegistry) resolve(domain core.SkillReconciliationDomain) (SkillReconciliationSweep, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sweep, ok := r.sweeps[domain]
	return sweep, ok
}

type SkillReconciliationRepository interface {
	LoadSkillReconciliationCursor(context.Context, core.SkillOrchestratorScope, core.SkillReconciliationDomain, int64, time.Time) (core.SkillReconciliationCursor, error)
	SaveSkillReconciliationCursor(context.Context, contracts.SkillReconciliationCursorUpdate) error
}

type SkillReconcilerConfig struct {
	Scope                core.SkillOrchestratorScope
	ConfigurationVersion int64
	BatchSize            int
	TimeBudget           time.Duration
	DomainTimeout        time.Duration
	Domains              []core.SkillReconciliationDomain
}

func (c SkillReconcilerConfig) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if c.ConfigurationVersion < 1 || c.BatchSize < 1 || c.BatchSize > 1_000 || c.TimeBudget <= 0 || c.TimeBudget > time.Hour || c.DomainTimeout <= 0 || c.DomainTimeout > c.TimeBudget || len(c.Domains) == 0 || len(c.Domains) > 16 {
		return errors.New("skill reconciler configuration is invalid")
	}
	seen := make(map[core.SkillReconciliationDomain]bool, len(c.Domains))
	for _, domain := range c.Domains {
		if !domain.Valid() || seen[domain] {
			return errors.New("skill reconciler domains are invalid or duplicated")
		}
		seen[domain] = true
	}
	return nil
}

type SkillReconciliationDomainReport struct {
	Domain   core.SkillReconciliationDomain
	Counters core.SkillReconciliationCounters
	Complete bool
	Failed   bool
	Code     string
}

type SkillReconciliationReport struct {
	Domains   []SkillReconciliationDomainReport
	TimedOut  bool
	Cancelled bool
}

type SkillOrchestratorReconciler struct {
	repository SkillReconciliationRepository
	registry   *SkillReconciliationRegistry
	config     SkillReconcilerConfig
	metrics    *observability.SkillOrchestratorMetrics
	now        func() time.Time
}

func NewSkillOrchestratorReconciler(repository SkillReconciliationRepository, registry *SkillReconciliationRegistry, config SkillReconcilerConfig) (*SkillOrchestratorReconciler, error) {
	if repository == nil || registry == nil {
		return nil, errors.New("skill reconciliation repository and registry are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &SkillOrchestratorReconciler{repository: repository, registry: registry, config: config, metrics: observability.DefaultSkillOrchestratorMetrics(), now: time.Now}, nil
}

func (r *SkillOrchestratorReconciler) RunOnce(ctx context.Context) (SkillReconciliationReport, error) {
	if r == nil {
		return SkillReconciliationReport{}, errors.New("skill reconciler is not configured")
	}
	started := r.now().UTC()
	deadline := started.Add(r.config.TimeBudget)
	report := SkillReconciliationReport{Domains: make([]SkillReconciliationDomainReport, 0, len(r.config.Domains))}
	for _, domain := range r.config.Domains {
		if err := ctx.Err(); err != nil {
			report.Cancelled = true
			break
		}
		now := r.now().UTC()
		if !now.Before(deadline) {
			report.TimedOut = true
			break
		}
		domainReport := r.runDomain(ctx, domain, now, deadline)
		r.metrics.ObserveReconciliation(domain, "scanned", r.config.Scope.Environment, domainReport.Counters.Scanned)
		r.metrics.ObserveReconciliation(domain, "repaired", r.config.Scope.Environment, domainReport.Counters.Repaired)
		r.metrics.ObserveReconciliation(domain, "skipped", r.config.Scope.Environment, domainReport.Counters.Skipped)
		r.metrics.ObserveReconciliation(domain, "blocked", r.config.Scope.Environment, domainReport.Counters.Blocked)
		r.metrics.ObserveReconciliation(domain, "failed", r.config.Scope.Environment, domainReport.Counters.Failed)
		report.Domains = append(report.Domains, domainReport)
	}
	return report, nil
}

func (r *SkillOrchestratorReconciler) runDomain(ctx context.Context, domain core.SkillReconciliationDomain, now, runDeadline time.Time) SkillReconciliationDomainReport {
	report := SkillReconciliationDomainReport{Domain: domain}
	sweep, ok := r.registry.resolve(domain)
	if !ok {
		report.Failed, report.Code = true, "sweep_unregistered"
		return report
	}
	cursor, err := r.repository.LoadSkillReconciliationCursor(ctx, r.config.Scope, domain, r.config.ConfigurationVersion, now)
	if err != nil {
		report.Failed, report.Code = true, "cursor_load_failed"
		return report
	}
	domainDeadline := now.Add(r.config.DomainTimeout)
	if runDeadline.Before(domainDeadline) {
		domainDeadline = runDeadline
	}
	domainCtx, cancel := context.WithDeadline(ctx, domainDeadline)
	result, sweepErr := sweep.Sweep(domainCtx, SkillReconciliationRequest{Scope: r.config.Scope, Domain: domain,
		Cursor: cursor.Cursor, ConfigurationVersion: r.config.ConfigurationVersion, Limit: r.config.BatchSize, Now: now})
	cancel()
	if sweepErr != nil {
		report.Failed, report.Code = true, "sweep_failed"
		result.Counters = core.SkillReconciliationCounters{Scanned: 1, Failed: 1}
		result.NextCursor = cursor.Cursor
	}
	if len(result.NextCursor) > core.MaxSkillReconciliationCursorBytes || strings.TrimSpace(result.NextCursor) != result.NextCursor || strings.ContainsAny(result.NextCursor, "\r\n\t") || result.Counters.Validate() != nil {
		report.Failed, report.Code = true, "invalid_sweep_result"
		result = SkillReconciliationSweepResult{NextCursor: cursor.Cursor, Counters: core.SkillReconciliationCounters{Scanned: 1, Failed: 1}}
	}
	updated := cursor
	updated.Cursor = result.NextCursor
	updated.ConfigurationVersion = r.config.ConfigurationVersion
	updated.Counters = result.Counters
	updated.UpdatedAt = r.now().UTC()
	if result.Complete && !report.Failed {
		updated.LastCompletedAt = updated.UpdatedAt
	}
	if err := r.repository.SaveSkillReconciliationCursor(ctx, contracts.SkillReconciliationCursorUpdate{Cursor: updated, ExpectedUpdatedAt: cursor.UpdatedAt}); err != nil {
		report.Failed, report.Code = true, "cursor_save_failed"
	}
	report.Counters, report.Complete = result.Counters, result.Complete && !report.Failed
	if report.Failed && report.Code == "" {
		report.Code = fmt.Sprintf("%s_failed", domain)
	}
	return report
}
