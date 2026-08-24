package library

import (
	"context"
	"errors"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestAuthorizedLibraryRepositoryHidesUnauthorizedEditions(t *testing.T) {
	store := &fakeProtectedEditionStore{
		editions: []BookEdition{
			{ID: "edition-org", WorkID: "work-1", Label: "Org", Language: "en", ContentFingerprint: "sha256:org"},
			{ID: "edition-private", WorkID: "work-2", Label: "Private", Language: "en", ContentFingerprint: "sha256:private"},
		},
		policies: map[string]LibraryResourcePolicy{
			"edition-org": {
				LibraryID: "library-org", ResourceType: ResourceEdition, ResourceID: "edition-org",
				Policy: core.AccessPolicy{Version: "policy-org", Ownership: core.ResourceOwnership{
					Owner: core.Principal{ID: "org-1", Kind: core.PrincipalOrganization}, Visibility: core.VisibilityOrganization, OrganizationID: "org-1",
				}},
			},
			"edition-private": {
				LibraryID: "library-private", ResourceType: ResourceEdition, ResourceID: "edition-private",
				Policy: core.AccessPolicy{Version: "policy-private", Ownership: core.ResourceOwnership{
					Owner: core.Principal{ID: "user-2", Kind: core.PrincipalUser}, Visibility: core.VisibilityPrivate,
				}},
			},
		},
	}
	repository := NewAuthorizedRepository(store)
	scope := core.AuthorizationScope{
		Principal: core.Principal{ID: "user-1", Kind: core.PrincipalUser}, OrganizationIDs: []string{"org-1"},
		Capabilities: []core.Capability{core.CapabilityReadSource}, PolicyVersion: "membership-v1",
	}

	if _, err := repository.GetEdition(context.Background(), scope, "edition-org"); err != nil {
		t.Fatalf("authorized edition: %v", err)
	}
	if _, err := repository.GetEdition(context.Background(), scope, "edition-private"); !errors.Is(err, ErrLibraryResourceNotFound) {
		t.Fatalf("unauthorized resource should be indistinguishable from missing: %v", err)
	}
	listed, err := repository.ListEditions(context.Background(), scope)
	if err != nil || len(listed) != 1 || listed[0].ID != "edition-org" {
		t.Fatalf("authorized list: listed=%+v err=%v", listed, err)
	}
	count, err := repository.CountEditions(context.Background(), scope)
	if err != nil || count != 1 {
		t.Fatalf("authorized count: count=%d err=%v", count, err)
	}
}

type fakeProtectedEditionStore struct {
	editions []BookEdition
	policies map[string]LibraryResourcePolicy
}

func (f *fakeProtectedEditionStore) GetBookEdition(_ context.Context, id string) (BookEdition, error) {
	for _, edition := range f.editions {
		if edition.ID == id {
			return edition, nil
		}
	}
	return BookEdition{}, ErrLibraryResourceNotFound
}

func (f *fakeProtectedEditionStore) ListBookEditions(context.Context) ([]BookEdition, error) {
	return f.editions, nil
}

func (f *fakeProtectedEditionStore) GetLibraryResourcePolicy(_ context.Context, _ ResourceType, id string) (LibraryResourcePolicy, error) {
	policy, ok := f.policies[id]
	if !ok {
		return LibraryResourcePolicy{}, ErrLibraryResourceNotFound
	}
	return policy, nil
}
