// Package retention owns versioned policy for every hosted data class.
package retention

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var DataClasses = []string{"account_identity", "sessions_credentials", "memory_content", "source_originals", "source_derived", "exports", "model_usage", "audit_events", "security_cases", "billing_records", "backups", "analytics"}

type Policy struct {
	DataClass, Version, Owner, Trigger, DeletionMethod, HoldBehavior, MigrationPlan, CustomerImpact string
	Duration                                                                                        time.Duration
	EffectiveAt                                                                                     time.Time
}
type Registry struct{ pool *pgxpool.Pool }

func NewRegistry(pool *pgxpool.Pool) *Registry { return &Registry{pool: pool} }
func (r *Registry) Active(ctx context.Context, dataClass string) (Policy, error) {
	if r == nil || r.pool == nil {
		return Policy{}, errors.New("retention registry is not configured")
	}
	var p Policy
	var seconds int64
	err := r.pool.QueryRow(ctx, `SELECT data_class,version,owner,retention_trigger,duration_seconds,deletion_method,hold_behavior,migration_plan,customer_impact,effective_at FROM saas_retention_policies WHERE data_class=$1 AND retired_at IS NULL`, dataClass).Scan(&p.DataClass, &p.Version, &p.Owner, &p.Trigger, &seconds, &p.DeletionMethod, &p.HoldBehavior, &p.MigrationPlan, &p.CustomerImpact, &p.EffectiveAt)
	p.Duration = time.Duration(seconds) * time.Second
	return p, err
}
func (r *Registry) ValidateCoverage(ctx context.Context) error {
	for _, class := range DataClasses {
		policy, err := r.Active(ctx, class)
		if err != nil {
			return err
		}
		if policy.Version == "" || policy.Owner == "" || policy.DeletionMethod == "" || policy.HoldBehavior == "" || policy.MigrationPlan == "" || policy.CustomerImpact == "" {
			return errors.New("retention policy is incomplete for " + class)
		}
	}
	return nil
}
