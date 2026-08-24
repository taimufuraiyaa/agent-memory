package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func TestLibraryMembershipAndResourcePolicyPersistence(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "access.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()
	lib := library.Library{ID: "library-org", Kind: library.LibraryOrganization, Owner: core.Principal{ID: "org-1", Kind: core.PrincipalOrganization}, OrganizationID: "org-1"}
	if err := store.PutLibrary(ctx, lib); err != nil {
		t.Fatalf("put library: %v", err)
	}
	membership := library.Membership{LibraryID: lib.ID, PrincipalID: "user-1", Capabilities: []core.Capability{core.CapabilityReadSource}, Version: "membership-v1", Active: true}
	if err := store.PutMembership(ctx, membership); err != nil {
		t.Fatalf("put membership: %v", err)
	}
	got, found, err := store.GetActiveMembership(ctx, lib.ID, "user-1")
	if err != nil || !found || len(got.Capabilities) != 1 {
		t.Fatalf("get membership: got=%+v found=%v err=%v", got, found, err)
	}
	if err := store.RemoveMembership(ctx, lib.ID, "user-1", "membership-v2"); err != nil {
		t.Fatalf("remove membership: %v", err)
	}
	if _, found, err := store.GetActiveMembership(ctx, lib.ID, "user-1"); err != nil || found {
		t.Fatalf("removed membership must invalidate access immediately: found=%v err=%v", found, err)
	}
}
