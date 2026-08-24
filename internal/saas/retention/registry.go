// Package retention owns versioned policy for every hosted data class.
package retention

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var DataClasses = []string{"account_identity", "sessions_credentials", "memory_content", "source_originals", "source_derived", "exports", "model_usage", "audit_events", "security_cases", "billing_records", "backups", "analytics"}

type Policy struct {
	DataClass, Purpose, Version, Owner, Trigger, DeletionMethod, HoldBehavior, MigrationPlan, CustomerImpact string
	Duration                                                                                                 time.Duration
	EffectiveAt                                                                                              time.Time
}
type Registry struct{ pool *pgxpool.Pool }

func NewRegistry(pool *pgxpool.Pool) *Registry { return &Registry{pool: pool} }
func (r *Registry) Active(ctx context.Context, dataClass string) (Policy, error) {
	if r == nil || r.pool == nil {
		return Policy{}, errors.New("retention registry is not configured")
	}
	var p Policy
	var seconds int64
	err := r.pool.QueryRow(ctx, `SELECT data_class,purpose,version,owner,retention_trigger,duration_seconds,deletion_method,hold_behavior,migration_plan,customer_impact,effective_at FROM saas_retention_policies WHERE data_class=$1 AND retired_at IS NULL`, dataClass).Scan(&p.DataClass, &p.Purpose, &p.Version, &p.Owner, &p.Trigger, &seconds, &p.DeletionMethod, &p.HoldBehavior, &p.MigrationPlan, &p.CustomerImpact, &p.EffectiveAt)
	p.Duration = time.Duration(seconds) * time.Second
	return p, err
}

func (r *Registry) ListActive(ctx context.Context) ([]Policy, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("retention registry is not configured")
	}
	rows, err := r.pool.Query(ctx, `SELECT data_class,purpose,version,owner,retention_trigger,duration_seconds,deletion_method,hold_behavior,migration_plan,customer_impact,effective_at FROM saas_retention_policies WHERE retired_at IS NULL ORDER BY data_class`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := make([]Policy, 0, len(DataClasses))
	for rows.Next() {
		var policy Policy
		var seconds int64
		if err := rows.Scan(&policy.DataClass, &policy.Purpose, &policy.Version, &policy.Owner, &policy.Trigger, &seconds, &policy.DeletionMethod, &policy.HoldBehavior, &policy.MigrationPlan, &policy.CustomerImpact, &policy.EffectiveAt); err != nil {
			return nil, err
		}
		policy.Duration = time.Duration(seconds) * time.Second
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return policies, nil
}

func (r *Registry) ValidateCoverage(ctx context.Context) error {
	policies, err := r.ListActive(ctx)
	if err != nil {
		return err
	}
	return ValidatePolicies(policies, time.Now().UTC())
}

func ValidatePolicies(policies []Policy, now time.Time) error {
	if now.IsZero() {
		return errors.New("retention policy validation time is required")
	}
	expected := make(map[string]struct{}, len(DataClasses))
	for _, dataClass := range DataClasses {
		expected[dataClass] = struct{}{}
	}
	if len(policies) != len(expected) {
		return fmt.Errorf("retention policy inventory has %d classes; want %d", len(policies), len(expected))
	}
	seen := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		if _, ok := expected[policy.DataClass]; !ok {
			return fmt.Errorf("retention policy has unknown data class %q", policy.DataClass)
		}
		if _, duplicate := seen[policy.DataClass]; duplicate {
			return fmt.Errorf("retention policy is duplicated for %q", policy.DataClass)
		}
		seen[policy.DataClass] = struct{}{}
		for name, value := range map[string]string{
			"purpose": policy.Purpose, "version": policy.Version, "owner": policy.Owner,
			"trigger": policy.Trigger, "deletion method": policy.DeletionMethod,
			"hold behavior": policy.HoldBehavior, "migration plan": policy.MigrationPlan,
			"customer impact": policy.CustomerImpact,
		} {
			if !boundedText(value, 512) {
				return fmt.Errorf("retention policy %s is invalid for %q", name, policy.DataClass)
			}
		}
		if policy.Duration < 0 {
			return fmt.Errorf("retention policy duration is invalid for %q", policy.DataClass)
		}
		if policy.EffectiveAt.IsZero() || policy.EffectiveAt.After(now) {
			return fmt.Errorf("retention policy effective time is invalid for %q", policy.DataClass)
		}
	}
	return nil
}

func boundedText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum
}
