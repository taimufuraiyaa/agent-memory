package security

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

type AuditSearch interface {
	Search(context.Context, string, audit.Filter) ([]audit.Event, error)
}
type FindingWriter interface {
	StoreFindings(context.Context, string, []Finding, time.Time) (int, error)
}
type TenantLister interface {
	ActiveTenantIDs(context.Context) ([]string, error)
}

type Detector struct {
	events   AuditSearch
	findings FindingWriter
	tenants  TenantLister
	now      func() time.Time
}

func NewDetector(events AuditSearch, findings FindingWriter, tenants TenantLister, now func() time.Time) *Detector {
	return &Detector{events: events, findings: findings, tenants: tenants, now: now}
}

func (d *Detector) RunOnce(ctx context.Context) (int, error) {
	if d == nil || d.events == nil || d.findings == nil || d.tenants == nil || d.now == nil {
		return 0, errors.New("security detector is not configured")
	}
	now := d.now().UTC()
	ids, err := d.tenants.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, tenantID := range ids {
		events, err := d.events.Search(ctx, tenantID, audit.Filter{From: now.Add(-30 * 24 * time.Hour), To: now, Limit: 500})
		if err != nil {
			return total, err
		}
		created, err := d.findings.StoreFindings(ctx, tenantID, Evaluate(events, now), now)
		total += created
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (d *Detector) Run(ctx context.Context, interval time.Duration, report func(int, error)) {
	if interval <= 0 {
		interval = time.Minute
	}
	if report == nil {
		report = func(int, error) {}
	}
	run := func() { count, err := d.RunOnce(ctx); report(count, err) }
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
