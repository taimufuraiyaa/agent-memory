package retrieval

import (
	"context"
	"reflect"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func TestAuthorizedLexicalSearchConstrainsCandidateGeneration(t *testing.T) {
	store := &fakePassageStore{
		resources: []library.LibraryResourcePolicy{
			{LibraryID: "org", ResourceType: library.ResourceEdition, ResourceID: "edition-allowed", Policy: core.AccessPolicy{Version: "p1", Ownership: core.ResourceOwnership{Owner: core.Principal{ID: "org-1", Kind: core.PrincipalOrganization}, Visibility: core.VisibilityOrganization, OrganizationID: "org-1"}}},
			{LibraryID: "private", ResourceType: library.ResourceEdition, ResourceID: "edition-denied", Policy: core.AccessPolicy{Version: "p2", Ownership: core.ResourceOwnership{Owner: core.Principal{ID: "user-2", Kind: core.PrincipalUser}, Visibility: core.VisibilityPrivate}}},
		},
		passages: []library.Passage{
			{ID: "p1", EditionID: "edition-allowed", StructuralNodeID: "chapter-1", Text: "Deliberate practice improves focused skill."},
			{ID: "p2", EditionID: "edition-allowed", StructuralNodeID: "chapter-2", Text: "Practice requires feedback."},
		},
	}
	search := NewLexicalPassageSearch(store)
	scope := core.AuthorizationScope{
		Principal: core.Principal{ID: "user-1", Kind: core.PrincipalUser}, OrganizationIDs: []string{"org-1"},
		Capabilities: []core.Capability{core.CapabilitySearchSource}, PolicyVersion: "membership-v1",
	}
	results, err := search.Search(context.Background(), scope, "focused practice", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !reflect.DeepEqual(store.requestedEditions, []string{"edition-allowed"}) {
		t.Fatalf("unauthorized edition entered candidate generation: %v", store.requestedEditions)
	}
	if len(results) != 2 || results[0].Passage.ID != "p1" || results[0].Score <= results[1].Score {
		t.Fatalf("expected deterministic lexical ranking: %+v", results)
	}
}

type fakePassageStore struct {
	resources         []library.LibraryResourcePolicy
	passages          []library.Passage
	requestedEditions []string
}

func (f *fakePassageStore) ListLibraryResourcePolicies(context.Context, library.ResourceType) ([]library.LibraryResourcePolicy, error) {
	return f.resources, nil
}

func (f *fakePassageStore) ListPassagesForEditions(_ context.Context, editionIDs []string) ([]library.Passage, error) {
	f.requestedEditions = append([]string(nil), editionIDs...)
	return f.passages, nil
}
