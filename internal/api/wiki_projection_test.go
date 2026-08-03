package api

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"testing"
	"time"
)

type wikiRepo struct {
	proposals []core.BookMemoryProposal
	policies  map[string]library.LibraryResourcePolicy
}

func (r *wikiRepo) ListAcceptedBookMemoryProposals(context.Context, string, string) ([]core.BookMemoryProposal, error) {
	return append([]core.BookMemoryProposal(nil), r.proposals...), nil
}
func (r *wikiRepo) GetLibraryResourcePolicy(_ context.Context, _ library.ResourceType, id string) (library.LibraryResourcePolicy, error) {
	return r.policies[id], nil
}
func TestWikiProjectionIsEvidenceExpandableRegenerableAndAuthorizationKeyed(t *testing.T) {
	owner := core.Principal{ID: "reader", Kind: core.PrincipalUser}
	access := core.AccessPolicy{Version: "v1", Ownership: core.ResourceOwnership{Owner: owner, Visibility: core.VisibilityPrivate}}
	locator := core.SourceLocator{Kind: core.LocatorMarkdown, Display: "Chapter", ParserVersion: "v1", NormalizationVersion: "v1", Text: &core.TextLocator{SourceStart: 0, SourceEnd: 1, NormalizedStart: 0, NormalizedEnd: 1}}
	citation := core.Citation{ID: "citation", EditionID: "edition", SourceAssetID: "asset", PassageID: "passage", Locator: locator, PassageFingerprint: "fp"}
	proposal := core.BookMemoryProposal{ID: "p", Workspace: "books", RequestedBy: owner, Content: "Original", Status: core.ProposalAccepted, Citations: []core.Citation{citation}, Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionReader, SubjectID: owner.ID}, Form: core.KnowledgeInsight, Derivation: core.DerivationDiscussed, CitationIDs: []string{"citation"}}, CreatedAt: time.Now().UTC()}
	repo := &wikiRepo{proposals: []core.BookMemoryProposal{proposal}, policies: map[string]library.LibraryResourcePolicy{"edition": {LibraryID: "l", ResourceType: library.ResourceEdition, ResourceID: "edition", Policy: access}}}
	projector := NewWikiProjector(repo)
	scope := libraryScope(owner.ID, nil)
	page, err := projector.Project(context.Background(), "books", "edition", scope, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Statements) != 1 || len(page.Statements[0].Evidence) != 1 || page.Statements[0].Attribution.Kind != core.AttributionReader {
		t.Fatalf("projection lost evidence: %+v", page)
	}
	repo.proposals[0].Content = "Corrected"
	cached, _ := projector.Project(context.Background(), "books", "edition", scope, false)
	if cached.Statements[0].Text != "Original" {
		t.Fatal("cache unexpectedly mutated")
	}
	regenerated, _ := projector.Project(context.Background(), "books", "edition", scope, true)
	if regenerated.Statements[0].Text != "Corrected" {
		t.Fatal("regeneration did not reflect correction")
	}
	peer := scope
	peer.Principal.ID = "peer"
	hidden, _ := projector.Project(context.Background(), "books", "edition", peer, false)
	if len(hidden.Statements) != 0 || hidden.AuthorizationFingerprint == page.AuthorizationFingerprint {
		t.Fatal("authorization-separated projection leaked")
	}
}
