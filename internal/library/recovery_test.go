package library_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestLibraryDeletionHonorsEveryRetentionModeAndKeepsCitationLineage(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "deletion.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PutBookWork(ctx, library.BookWork{ID: "work", Title: "Book", NormalizedTitle: "book"}); err != nil {
		t.Fatal(err)
	}
	modes := []core.RetentionMode{core.RetentionRetained, core.RetentionOnDemand, core.RetentionSessionOnly, core.RetentionDeleted}
	for index, mode := range modes {
		id, edition, asset, node := string(mode), "edition-"+string(mode), "asset-"+string(mode), "node-"+string(mode)
		if err := store.PutBookEdition(ctx, library.BookEdition{ID: edition, WorkID: "work", Label: id, Language: "en", ContentFingerprint: "fp-" + id}); err != nil {
			t.Fatal(err)
		}
		policy := core.SourcePolicy{Retention: mode}
		if mode == core.RetentionRetained || mode == core.RetentionOnDemand {
			policy.AllowSearch, policy.AllowQuote, policy.MaxQuoteRunes = true, true, 100
		}
		if err := store.PutSourceAsset(ctx, library.SourceAsset{ID: asset, EditionID: edition, Format: library.FormatMarkdown, ByteFingerprint: "byte-" + id, NormalizedFingerprint: "norm-" + id, ParserVersion: "v1", Policy: policy, ImportedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := store.ReplaceStructuralNodes(ctx, edition, []library.StructuralNode{{ID: node, EditionID: edition, Kind: library.NodeChapter, Ordinal: 0, Title: "Chapter", StartOffset: 0, EndOffset: 5, Explicit: true}}); err != nil {
			t.Fatal(err)
		}
		locator := core.SourceLocator{Kind: core.LocatorMarkdown, Display: "Chapter", ParserVersion: "v1", NormalizationVersion: "v1", Text: &core.TextLocator{SourceStart: 0, SourceEnd: 5, NormalizedStart: 0, NormalizedEnd: 5}}
		passage := library.Passage{ID: "passage-" + id, EditionID: edition, SourceAssetID: asset, StructuralNodeID: node, Text: "claim", Fingerprint: core.FingerprintText("claim"), Locator: locator}
		if err := store.PutPassages(ctx, []library.Passage{passage}); err != nil {
			t.Fatal(err)
		}
		var citation core.Citation
		if index == 3 {
			citation, err = library.NewCitationService(store).CitePassage(ctx, passage.ID, "")
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := store.ApplySourceRetention(ctx, asset, mode, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		available, err := store.IsSourceAvailable(ctx, asset)
		if err != nil {
			t.Fatal(err)
		}
		if available != (mode == core.RetentionRetained) {
			t.Fatalf("mode %s availability=%v", mode, available)
		}
		passages, err := store.ListPassages(ctx, edition)
		if err != nil {
			t.Fatal(err)
		}
		if (mode == core.RetentionRetained) != (len(passages) == 1) {
			t.Fatalf("mode %s retrieval mismatch: %d", mode, len(passages))
		}
		if index == 3 {
			if _, err := library.NewCitationService(store).Resolve(ctx, citation.ID); err != nil {
				t.Fatalf("historical citation lost after deletion: %v", err)
			}
			assetValue, err := store.GetSourceAsset(ctx, asset)
			if err != nil || assetValue.Policy.Retention != core.RetentionDeleted {
				t.Fatal("deleted source policy not enforced")
			}
		}
	}
}
