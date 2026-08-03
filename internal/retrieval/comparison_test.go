package retrieval_test

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestCrossBookComparisonBalancesEvidenceAndHidesPrivateSources(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "compare.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := core.Principal{ID: "reader", Kind: core.PrincipalUser}
	other := core.Principal{ID: "other", Kind: core.PrincipalUser}
	if err := store.PutLibrary(ctx, library.Library{ID: "l", Kind: library.LibraryPersonal, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBookWork(ctx, library.BookWork{ID: "work", Title: "Books", NormalizedTitle: "books"}); err != nil {
		t.Fatal(err)
	}
	policy := core.SourcePolicy{Retention: core.RetentionRetained, AllowSearch: true}
	for index, id := range []string{"edition-a", "edition-b", "edition-private"} {
		if err := store.PutBookEdition(ctx, library.BookEdition{ID: id, WorkID: "work", Label: id, Language: "en", ContentFingerprint: "fp-" + id}); err != nil {
			t.Fatal(err)
		}
		asset := "asset-" + id
		if err := store.PutSourceAsset(ctx, library.SourceAsset{ID: asset, EditionID: id, Format: library.FormatMarkdown, ByteFingerprint: "byte-" + id, NormalizedFingerprint: "norm-" + id, ParserVersion: "v1", Policy: policy, ImportedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		locator := core.SourceLocator{Kind: core.LocatorMarkdown, Display: "Chapter", ParserVersion: "v1", NormalizationVersion: "v1", Text: &core.TextLocator{SourceStart: 0, SourceEnd: 5, NormalizedStart: 0, NormalizedEnd: 5}}
		text := "shared claim from " + id
		if err := store.ReplaceStructuralNodes(ctx, id, []library.StructuralNode{{ID: "n-" + id, EditionID: id, Kind: library.NodeChapter, Ordinal: 0, Title: "Chapter", StartOffset: 0, EndOffset: len(text), Explicit: true}}); err != nil {
			t.Fatal(err)
		}
		if err := store.PutPassages(ctx, []library.Passage{{ID: "p-" + id, EditionID: id, SourceAssetID: asset, StructuralNodeID: "n-" + id, Text: text, Fingerprint: core.FingerprintText(text), Locator: locator}}); err != nil {
			t.Fatal(err)
		}
		resourceOwner := owner
		if index == 2 {
			resourceOwner = other
		}
		access := core.AccessPolicy{Version: "v1", Ownership: core.ResourceOwnership{Owner: resourceOwner, Visibility: core.VisibilityPrivate}}
		if err := store.PutLibraryResourcePolicy(ctx, library.LibraryResourcePolicy{LibraryID: "l", ResourceType: library.ResourceEdition, ResourceID: id, Policy: access}); err != nil {
			t.Fatal(err)
		}
	}
	scope := core.AuthorizationScope{Principal: owner, Capabilities: []core.Capability{core.CapabilitySearchSource}, PolicyVersion: "v1"}
	result, err := retrieval.NewCrossBookPlanner(store).Plan(ctx, scope, "shared claim", []string{"edition-a", "edition-b", "edition-private"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Editions[0].Results) != 1 || len(result.Editions[1].Results) != 1 || !result.Editions[2].Missing {
		t.Fatalf("comparison was unbalanced or leaked: %+v", result)
	}
}
