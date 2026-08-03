package ingestion_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestMarkdownImportIsIdempotentAndVersionsChangedContent(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	importer := ingestion.NewMarkdownImporter(store, ingestion.MarkdownAdapter{ParserVersion: "markdown-v1", NormalizationVersion: "text-v1"})
	input := ingestion.MarkdownImportInput{
		Title: "The Book", EditionLabel: "Imported edition", Language: "en",
		Source: []byte("# Chapter\n\nKnowledge lives here.\n"),
		Policy: core.SourcePolicy{Retention: core.RetentionRetained, StoreOriginal: true, StoreNormalized: true, AllowSearch: true},
	}

	first, err := importer.Import(ctx, input)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	second, err := importer.Import(ctx, input)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if !second.Existing || first.WorkID != second.WorkID || first.EditionID != second.EditionID || first.AssetID != second.AssetID {
		t.Fatalf("expected stable identical import: first=%+v second=%+v", first, second)
	}

	input.Source = []byte("# Chapter\n\nKnowledge changed here.\n")
	changed, err := importer.Import(ctx, input)
	if err != nil {
		t.Fatalf("changed import: %v", err)
	}
	if changed.WorkID != first.WorkID || changed.EditionID == first.EditionID || changed.AssetID == first.AssetID {
		t.Fatalf("expected same work with new edition and asset: first=%+v changed=%+v", first, changed)
	}
	if _, err := store.GetBookEdition(ctx, first.EditionID); err != nil {
		t.Fatalf("historical edition should remain: %v", err)
	}
}
