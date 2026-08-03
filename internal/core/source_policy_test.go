package core

import "testing"

func TestSourcePolicySupportsRetentionModesAndIndependentUses(t *testing.T) {
	policy := SourcePolicy{
		Retention:       RetentionRetained,
		StoreOriginal:   true,
		StoreNormalized: true,
		AllowSearch:     true,
		AllowQuote:      true,
		AllowShare:      false,
		AllowExport:     false,
		MaxQuoteRunes:   120,
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("expected retained private searchable source policy: %v", err)
	}
	if !policy.CanQuote("short quote") || policy.CanShare() {
		t.Fatal("expected quote and share permissions to remain independent")
	}
}

func TestSourcePolicyFailsClosedForDeletedAndSessionOnlySources(t *testing.T) {
	deleted := SourcePolicy{Retention: RetentionDeleted, AllowSearch: true}
	if err := deleted.Validate(); err == nil {
		t.Fatal("expected deleted source with active use permission to fail")
	}

	sessionOnly := SourcePolicy{Retention: RetentionSessionOnly, StoreOriginal: true}
	if err := sessionOnly.Validate(); err == nil {
		t.Fatal("expected session-only source to reject persistent storage")
	}
}

func TestSourcePolicyEnforcesQuoteLength(t *testing.T) {
	policy := SourcePolicy{Retention: RetentionOnDemand, StoreOriginal: true, AllowQuote: true, MaxQuoteRunes: 5}
	if !policy.CanQuote("five!") {
		t.Fatal("expected quote at configured rune limit to pass")
	}
	if policy.CanQuote("longer") {
		t.Fatal("expected quote over configured rune limit to fail")
	}
}
