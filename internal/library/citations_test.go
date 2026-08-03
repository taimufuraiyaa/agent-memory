package library_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestMarkdownCitationResolvesExactPassageAndSurvivesNewEdition(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "citations.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	importer := ingestion.NewMarkdownImporter(store, ingestion.MarkdownAdapter{ParserVersion: "markdown-v1", NormalizationVersion: "text-v1"})
	input := ingestion.MarkdownImportInput{
		Title: "Cited Book", EditionLabel: "Imported", Language: "en",
		Source: []byte("# Chapter\n\nExact evidence lives here.\n"),
		Policy: core.SourcePolicy{Retention: core.RetentionRetained, StoreOriginal: true, StoreNormalized: true, AllowSearch: true, AllowQuote: true, MaxQuoteRunes: 100},
	}
	first, err := importer.Import(ctx, input)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	passages, err := store.ListPassages(ctx, first.EditionID)
	if err != nil || len(passages) != 1 {
		t.Fatalf("list passages: passages=%+v err=%v", passages, err)
	}
	service := library.NewCitationService(store)
	citation, err := service.CitePassage(ctx, passages[0].ID, "Exact evidence lives here.")
	if err != nil {
		t.Fatalf("cite passage: %v", err)
	}
	resolved, err := service.Resolve(ctx, citation.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Passage.EditionID != first.EditionID || resolved.Citation.Locator.Text == nil || resolved.Citation.ShortQuote != "Exact evidence lives here." {
		t.Fatalf("unexpected resolved citation: %+v", resolved)
	}

	input.Source = []byte("# Chapter\n\nChanged evidence lives here.\n")
	if _, err := importer.Import(ctx, input); err != nil {
		t.Fatalf("import new edition: %v", err)
	}
	resolvedAgain, err := service.Resolve(ctx, citation.ID)
	if err != nil || resolvedAgain.Passage.Fingerprint != passages[0].Fingerprint {
		t.Fatalf("historical citation changed: resolved=%+v err=%v", resolvedAgain, err)
	}
}

func TestCitationDetectsStalePassageFingerprint(t *testing.T) {
	service := library.NewCitationService(&staleCitationRepository{})
	_, err := service.Resolve(context.Background(), "citation-1")
	if err == nil {
		t.Fatal("expected stale passage fingerprint to fail")
	}
}

type staleCitationRepository struct{}

func (*staleCitationRepository) GetPassage(context.Context, string) (library.Passage, error) {
	return library.Passage{ID: "passage-1", Fingerprint: "sha256:new"}, nil
}

func (*staleCitationRepository) GetSourceAsset(context.Context, string) (library.SourceAsset, error) {
	return library.SourceAsset{}, nil
}

func (*staleCitationRepository) PutCitation(context.Context, core.Citation) error { return nil }

func (*staleCitationRepository) GetCitation(context.Context, string) (core.Citation, error) {
	return core.Citation{ID: "citation-1", PassageID: "passage-1", PassageFingerprint: "sha256:old"}, nil
}
