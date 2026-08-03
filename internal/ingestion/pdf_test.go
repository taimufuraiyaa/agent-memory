package ingestion_test

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"testing"
)

type fixturePDFExtractor struct{}

func (fixturePDFExtractor) ExtractPages(context.Context, []byte) ([]ingestion.PDFPage, error) {
	return []ingestion.PDFPage{{Number: 1, NativeBlocks: []ingestion.PDFBlock{{ID: "right", Text: "Right column", X: 350, Y: 10, Width: 100, Height: 10, Origin: "native"}, {ID: "left", Text: "Left column", X: 10, Y: 20, Width: 100, Height: 10, Origin: "native"}}}, {Number: 2}}, nil
}
func TestPDFPageCoordinatesReadingOrderAndOCRMarker(t *testing.T) {
	document, err := (ingestion.PDFAdapter{ParserVersion: "pdf-fixture-v1", NormalizationVersion: "text-v1", Extractor: fixturePDFExtractor{}}).Extract(context.Background(), "edition", "asset", []byte("%PDF-1.7 fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Passages) != 2 || document.Passages[0].Text != "Left column" || document.Passages[0].Locator.PDF.Page != 1 || len(document.Passages[0].Locator.PDF.BoundingBox) != 4 {
		t.Fatalf("layout/citation coordinates lost: %+v", document)
	}
	if !document.Pages[1].RequiresOCR {
		t.Fatal("scanned page was treated as empty truth")
	}
}
func TestPDFMalformedSourceFails(t *testing.T) {
	if _, err := (ingestion.PDFAdapter{ParserVersion: "v1", NormalizationVersion: "v1", Extractor: fixturePDFExtractor{}}).Extract(context.Background(), "e", "a", []byte("not pdf")); err == nil {
		t.Fatal("malformed PDF accepted")
	}
}
