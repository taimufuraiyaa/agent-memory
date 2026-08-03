package ingestion_test

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestEPUBStructureCitationAndReimport(t *testing.T) {
	source := syntheticEPUB(t)
	adapter := ingestion.EPUBAdapter{ParserVersion: "epub-v1", NormalizationVersion: "text-v1"}
	first, err := adapter.Extract("edition", "asset:edition", source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.Extract("edition", "asset:edition", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Spine) != 2 || len(first.Document.Nodes) != 2 || first.Passages[0].ID != second.Passages[0].ID {
		t.Fatalf("unstable EPUB extraction: %+v", first)
	}
	db := filepath.Join(t.TempDir(), "epub.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutBookWork(ctx, library.BookWork{ID: "work", Title: "Book", NormalizedTitle: "book"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBookEdition(ctx, library.BookEdition{ID: "edition", WorkID: "work", Label: "v1", Language: "en", ContentFingerprint: core.FingerprintText(first.Document.NormalizedText)}); err != nil {
		t.Fatal(err)
	}
	policy := core.SourcePolicy{Retention: core.RetentionRetained, StoreOriginal: true, StoreNormalized: true, AllowSearch: true, AllowQuote: true, MaxQuoteRunes: 100}
	if err := store.PutSourceAsset(ctx, library.SourceAsset{ID: "asset:edition", EditionID: "edition", Format: library.FormatEPUB, ByteFingerprint: core.FingerprintText(string(source)), NormalizedFingerprint: core.FingerprintText(first.Document.NormalizedText), ParserVersion: "epub-v1", Policy: policy, ImportedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceStructuralNodes(ctx, "edition", first.Document.Nodes); err != nil {
		t.Fatal(err)
	}
	if err := store.PutPassages(ctx, first.Passages); err != nil {
		t.Fatal(err)
	}
	citation, err := library.NewCitationService(store).CitePassage(ctx, first.Passages[0].ID, "First chapter")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = sqlite.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	resolved, err := library.NewCitationService(store).Resolve(ctx, citation.ID)
	if err != nil || resolved.Citation.Locator.EPUB == nil {
		t.Fatalf("EPUB citation did not survive reopen: %+v %v", resolved, err)
	}
}
func TestEPUBMalformedContainerDoesNotPublishPartialDocument(t *testing.T) {
	_, err := (ingestion.EPUBAdapter{ParserVersion: "v1", NormalizationVersion: "v1"}).Extract("edition", "asset", []byte("not a zip"))
	if err == nil {
		t.Fatal("malformed EPUB accepted")
	}
}
func syntheticEPUB(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	files := map[string]string{"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`, "OEBPS/content.opf": `<package><metadata><title>Test Book</title><language>en</language><identifier>id-1</identifier></metadata><manifest><item id="c1" href="c1.xhtml"/><item id="c2" href="c2.xhtml"/></manifest><spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`, "OEBPS/c1.xhtml": `<html><head><title>One</title></head><body><h1>One</h1><p>First chapter text.</p></body></html>`, "OEBPS/c2.xhtml": `<html><head><title>Two</title></head><body><h1>Two</h1><p>Second chapter text.</p></body></html>`}
	for name, value := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
