package core

import "testing"

func TestPrincipalValidation(t *testing.T) {
	for _, kind := range []PrincipalKind{PrincipalUser, PrincipalAgent, PrincipalOrganization} {
		principal := Principal{ID: "principal-1", Kind: kind}
		if err := principal.Validate(); err != nil {
			t.Fatalf("expected %q principal to be valid: %v", kind, err)
		}
	}
	if err := (Principal{ID: "principal-1", Kind: "service"}).Validate(); err == nil {
		t.Fatal("expected unknown principal kind to fail")
	}
}

func TestResourceOwnershipFailsClosed(t *testing.T) {
	valid := ResourceOwnership{
		Owner:      Principal{ID: "user-1", Kind: PrincipalUser},
		Visibility: VisibilityPrivate,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected private ownership to be valid: %v", err)
	}

	missingOwner := valid
	missingOwner.Owner = Principal{}
	if err := missingOwner.Validate(); err == nil {
		t.Fatal("expected missing ownership to fail closed")
	}

	organization := valid
	organization.Visibility = VisibilityOrganization
	if err := organization.Validate(); err == nil {
		t.Fatal("expected organization visibility to require an organization")
	}
	organization.OrganizationID = "org-1"
	if err := organization.Validate(); err != nil {
		t.Fatalf("expected organization ownership to be valid: %v", err)
	}
}

func TestAuthorizationRequiresScopeCapabilityAndResourceAccess(t *testing.T) {
	policy := AccessPolicy{
		Version: "policy-v1",
		Ownership: ResourceOwnership{
			Owner:          Principal{ID: "org-1", Kind: PrincipalOrganization},
			Visibility:     VisibilityOrganization,
			OrganizationID: "org-1",
		},
	}
	scope := AuthorizationScope{
		Principal:       Principal{ID: "user-1", Kind: PrincipalUser},
		OrganizationIDs: []string{"org-1"},
		Capabilities:    []Capability{CapabilitySearchSource},
		PolicyVersion:   "membership-v3",
	}

	allowed := Authorize(scope, policy, CapabilitySearchSource)
	if !allowed.Allowed || allowed.PolicyVersion != "policy-v1" {
		t.Fatalf("expected authorized decision with policy version: %+v", allowed)
	}

	denied := Authorize(scope, policy, CapabilityExport)
	if denied.Allowed || denied.PolicyVersion != "policy-v1" {
		t.Fatalf("expected missing capability to deny with policy version: %+v", denied)
	}
}

func TestAuthorizationFailsClosedWithoutValidScope(t *testing.T) {
	policy := AccessPolicy{
		Version: "policy-v1",
		Ownership: ResourceOwnership{
			Owner:      Principal{ID: "user-1", Kind: PrincipalUser},
			Visibility: VisibilityPrivate,
		},
	}
	decision := Authorize(AuthorizationScope{}, policy, CapabilityReadSource)
	if decision.Allowed || decision.PolicyVersion != "policy-v1" {
		t.Fatalf("expected invalid scope to fail closed: %+v", decision)
	}
}
