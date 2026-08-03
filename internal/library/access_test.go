package library

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestPersonalAndOrganizationLibraryValidation(t *testing.T) {
	personal := Library{
		ID: "library-personal", Kind: LibraryPersonal,
		Owner: core.Principal{ID: "user-1", Kind: core.PrincipalUser},
	}
	if err := personal.Validate(); err != nil {
		t.Fatalf("personal library: %v", err)
	}
	personal.Owner.Kind = core.PrincipalOrganization
	if err := personal.Validate(); err == nil {
		t.Fatal("personal library must have one user owner")
	}

	organization := Library{
		ID: "library-org", Kind: LibraryOrganization,
		Owner:          core.Principal{ID: "org-1", Kind: core.PrincipalOrganization},
		OrganizationID: "org-1",
	}
	if err := organization.Validate(); err != nil {
		t.Fatalf("organization library: %v", err)
	}
}

func TestMembershipRequiresExplicitCapabilities(t *testing.T) {
	membership := Membership{LibraryID: "library-org", PrincipalID: "user-1", Version: "membership-v1", Active: true}
	if err := membership.Validate(); err == nil {
		t.Fatal("expected membership without capabilities to fail")
	}
	membership.Capabilities = []core.Capability{core.CapabilityReadSource}
	if err := membership.Validate(); err != nil {
		t.Fatalf("valid membership: %v", err)
	}
}
