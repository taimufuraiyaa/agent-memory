package retention

import (
	"strings"
	"testing"
	"time"
)

func TestDataClassesAreUniqueAndExplicit(t *testing.T) {
	seen := map[string]bool{}
	for _, class := range DataClasses {
		if class == "" || seen[class] {
			t.Fatalf("invalid data class %q", class)
		}
		seen[class] = true
	}
	for _, required := range []string{"source_originals", "source_derived", "audit_events", "backups", "billing_records"} {
		if !seen[required] {
			t.Errorf("missing policy class %s", required)
		}
	}
}

func TestValidatePoliciesRequiresExactCompleteInventory(t *testing.T) {
	now := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	valid := completePolicies(now.Add(-time.Hour))
	if err := ValidatePolicies(valid, now); err != nil {
		t.Fatalf("ValidatePolicies(valid) error = %v", err)
	}

	tests := map[string][]Policy{
		"missing":         append([]Policy(nil), valid[1:]...),
		"duplicate":       append(append([]Policy(nil), valid...), valid[0]),
		"unknown":         append(append([]Policy(nil), valid...), Policy{DataClass: "raw_customer_text"}),
		"missing purpose": mutatePolicy(valid, 0, func(policy *Policy) { policy.Purpose = "" }),
		"missing trigger": mutatePolicy(valid, 0, func(policy *Policy) { policy.Trigger = "" }),
		"negative ttl":    mutatePolicy(valid, 0, func(policy *Policy) { policy.Duration = -time.Second }),
		"future effective": mutatePolicy(valid, 0, func(policy *Policy) {
			policy.EffectiveAt = now.Add(time.Second)
		}),
	}
	for name, policies := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidatePolicies(policies, now); err == nil {
				t.Fatal("ValidatePolicies() accepted invalid inventory")
			}
		})
	}
}

func TestValidatePoliciesRejectsUnboundedGovernanceText(t *testing.T) {
	now := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	policies := completePolicies(now.Add(-time.Hour))
	policies[0].Purpose = strings.Repeat("p", 513)
	if err := ValidatePolicies(policies, now); err == nil {
		t.Fatal("ValidatePolicies() accepted oversized purpose")
	}
}

func completePolicies(effectiveAt time.Time) []Policy {
	policies := make([]Policy, 0, len(DataClasses))
	for _, class := range DataClasses {
		policies = append(policies, Policy{
			DataClass: class, Purpose: "operate the product", Version: "retention-v1",
			Owner: "privacy", Trigger: "record_created", Duration: 24 * time.Hour,
			DeletionMethod: "hard_delete", HoldBehavior: "scoped_hold",
			MigrationPlan: "forward-only", CustomerImpact: "access ends at deletion",
			EffectiveAt: effectiveAt,
		})
	}
	return policies
}

func mutatePolicy(source []Policy, index int, mutate func(*Policy)) []Policy {
	result := append([]Policy(nil), source...)
	mutate(&result[index])
	return result
}
