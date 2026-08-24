package search

import (
	"context"
	"errors"
	"time"
)

const FullTextProjectionVersion = "postgres-simple-v1"

type ProjectionStats struct {
	Pending int `json:"pending"`
	Ready   int `json:"ready"`
	Stale   int `json:"stale"`
}

type FullTextRepository interface {
	ActiveTenantIDs(context.Context) ([]string, error)
	ProjectNextFullText(context.Context, string, string, time.Time) (bool, error)
	PurgeUnauthorizedFullText(context.Context, string) (int64, error)
	FullTextProjectionStats(context.Context, string, string) (ProjectionStats, error)
	ResetFullTextProjection(context.Context, string, string) error
}

type FullTextProjector struct {
	repository FullTextRepository
	now        func() time.Time
}

func NewFullTextProjector(repository FullTextRepository, now func() time.Time) (*FullTextProjector, error) {
	if repository == nil {
		return nil, errors.New("full-text projector repository is required")
	}
	if now == nil {
		now = time.Now
	}
	return &FullTextProjector{repository: repository, now: now}, nil
}

func (p *FullTextProjector) ProcessOnce(ctx context.Context) (int, error) {
	tenants, err := p.repository.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	processed := 0
	var failures []error
	for _, tenant := range tenants {
		if _, err := p.repository.PurgeUnauthorizedFullText(ctx, tenant); err != nil {
			failures = append(failures, err)
			continue
		}
		projected, err := p.repository.ProjectNextFullText(ctx, tenant, FullTextProjectionVersion, p.now().UTC())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if projected {
			processed++
		}
	}
	return processed, errors.Join(failures...)
}

func (p *FullTextProjector) Rebuild(ctx context.Context, tenantID, sourceID string) error {
	if tenantID == "" || sourceID == "" {
		return errors.New("tenant and source are required")
	}
	return p.repository.ResetFullTextProjection(ctx, tenantID, sourceID)
}

func (p *FullTextProjector) Run(ctx context.Context, poll time.Duration, report func(error)) {
	if poll <= 0 {
		poll = time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if _, err := p.ProcessOnce(ctx); err != nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
