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

type fakePassageRanker struct {
	available bool
	err       error
	matches   []library.PassageMatch
	passages  map[string]library.Passage
	editions  []string
	calls     int
}

func (f *fakePassageRanker) LexicalIndexAvailable(context.Context) bool { return f.available }
func (f *fakePassageRanker) SearchPassagesLexical(_ context.Context, editionIDs []string, _ string, _ int) ([]library.PassageMatch, error) {
	f.calls++
	f.editions = append([]string(nil), editionIDs...)
	return f.matches, f.err
}
func (f *fakePassageRanker) GetPassage(_ context.Context, id string) (library.Passage, error) {
	return f.passages[id], nil
}

func rankedPassageFixture() (*fakePassageStore, core.AuthorizationScope) {
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
	scope := core.AuthorizationScope{
		Principal: core.Principal{ID: "user-1", Kind: core.PrincipalUser}, OrganizationIDs: []string{"org-1"},
		Capabilities: []core.Capability{core.CapabilitySearchSource}, PolicyVersion: "membership-v1",
	}
	return store, scope
}

func TestRankedPassageSearchUsesBM25OnlyForAuthorizedEditions(t *testing.T) {
	store, scope := rankedPassageFixture()
	ranker := &fakePassageRanker{
		available: true,
		matches: []library.PassageMatch{
			{PassageID: "n1", Score: 4.5}, {PassageID: "n2", Score: 3.0}, {PassageID: "n3", Score: 2.0}, {PassageID: "n4", Score: 1.0},
		},
		passages: map[string]library.Passage{
			"n1": {ID: "n1", StructuralNodeID: "node-a"}, "n2": {ID: "n2", StructuralNodeID: "node-a"},
			"n3": {ID: "n3", StructuralNodeID: "node-a"}, "n4": {ID: "n4", StructuralNodeID: "node-b"},
		},
	}
	results, err := NewLexicalPassageSearch(store).WithRanker(ranker).Search(context.Background(), scope, "focused practice", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ranker.editions, []string{"edition-allowed"}) {
		t.Fatalf("ranker received unauthorized editions: %v", ranker.editions)
	}
	if store.requestedEditions != nil {
		t.Fatalf("legacy scan must not run when the ranker serves the query: %v", store.requestedEditions)
	}
	ids := []string{}
	for _, result := range results {
		ids = append(ids, result.Passage.ID)
	}
	// node-a is capped at two passages; node-b fills the third slot.
	if !reflect.DeepEqual(ids, []string{"n1", "n2", "n4"}) {
		t.Fatalf("ranked order or diversity wrong: %v", ids)
	}
	if results[0].Relevance != 4.5 || results[0].Score <= results[1].Score {
		t.Fatalf("relevance not carried: %+v", results[0])
	}
}

func TestRankedPassageSearchFallsBackToScanWhenIndexUnavailableOrFails(t *testing.T) {
	for name, ranker := range map[string]*fakePassageRanker{
		"unavailable": {available: false},
		"error":       {available: true, err: context.DeadlineExceeded},
	} {
		t.Run(name, func(t *testing.T) {
			store, scope := rankedPassageFixture()
			results, err := NewLexicalPassageSearch(store).WithRanker(ranker).Search(context.Background(), scope, "focused practice", 5)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 2 || results[0].Passage.ID != "p1" {
				t.Fatalf("fallback scan did not serve the query: %+v", results)
			}
			if !reflect.DeepEqual(store.requestedEditions, []string{"edition-allowed"}) {
				t.Fatalf("fallback scan did not run on authorized editions: %v", store.requestedEditions)
			}
		})
	}
}
