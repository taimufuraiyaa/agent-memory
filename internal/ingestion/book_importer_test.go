package ingestion_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestBookImporterPersistsEPUBWithFinalAssetAndEditionLocators(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "epub-import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	importer := ingestion.NewBookImporter(store,
		ingestion.EPUBBookExtractor{Adapter: ingestion.EPUBAdapter{ParserVersion: "epub-v1", NormalizationVersion: "text-v1"}},
	)
	input := ingestion.BookImportInput{
		Title: "Portable EPUB", EditionLabel: "First", Language: "en",
		Format: library.FormatEPUB, Source: syntheticEPUB(t), Policy: retainedBookPolicy(),
	}
	first, err := importer.Import(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := importer.Import(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Existing || first.EditionID != second.EditionID || first.AssetID != second.AssetID {
		t.Fatalf("EPUB re-import was not stable: first=%+v second=%+v", first, second)
	}
	passages, err := store.ListPassages(context.Background(), first.EditionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(passages) != 2 {
		t.Fatalf("expected two EPUB passages, got %+v", passages)
	}
	for _, passage := range passages {
		if passage.EditionID != first.EditionID || passage.SourceAssetID != first.AssetID || passage.Locator.EPUB == nil {
			t.Fatalf("EPUB passage lost final identity or locator: %+v", passage)
		}
	}
}

func TestBookImporterPersistsPDFPageStructureAndLocators(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "pdf-import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	importer := ingestion.NewBookImporter(store,
		ingestion.PDFBookExtractor{Adapter: ingestion.PDFAdapter{ParserVersion: "pdf-fixture-v1", NormalizationVersion: "text-v1", Extractor: fixturePDFExtractor{}}},
	)
	result, err := importer.Import(context.Background(), ingestion.BookImportInput{
		Title: "Portable PDF", EditionLabel: "First", Language: "en",
		Format: library.FormatPDF, Source: []byte("%PDF-1.7 fixture"), Policy: retainedBookPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeCount != 2 || result.PassageCount != 2 {
		t.Fatalf("unexpected PDF import counts: %+v", result)
	}
	nodes, err := store.ListStructuralNodes(context.Background(), result.EditionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Title != "Page 1" || nodes[1].Title != "Page 2 (requires OCR)" {
		t.Fatalf("PDF page structure was not retained: %+v", nodes)
	}
	passages, err := store.ListPassages(context.Background(), result.EditionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, passage := range passages {
		if passage.SourceAssetID != result.AssetID || passage.Locator.PDF == nil || passage.Locator.PDF.Page != 1 {
			t.Fatalf("PDF passage lost final asset or page locator: %+v", passage)
		}
	}
}

func retainedBookPolicy() core.SourcePolicy {
	return core.SourcePolicy{
		Retention: core.RetentionRetained, StoreOriginal: true, StoreNormalized: true,
		AllowSearch: true, AllowQuote: true, MaxQuoteRunes: 280,
	}
}
