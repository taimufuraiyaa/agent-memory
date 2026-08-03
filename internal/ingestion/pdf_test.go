package ingestion_test

import (
	"bytes"
	"context"
	"fmt"
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

func TestNativePDFExtractorReadsPositionedTextFromRealPDFBytes(t *testing.T) {
	pages, err := (ingestion.NativePDFExtractor{}).ExtractPages(context.Background(), minimalTextPDF("A citable sentence."))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || len(pages[0].NativeBlocks) == 0 {
		t.Fatalf("expected native text blocks, got %+v", pages)
	}
	if pages[0].NativeBlocks[0].Text == "" || pages[0].NativeBlocks[0].Width <= 0 || pages[0].NativeBlocks[0].Height <= 0 {
		t.Fatalf("positioned text metadata is incomplete: %+v", pages[0].NativeBlocks[0])
	}
}

func minimalTextPDF(text string) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len("BT /F1 12 Tf 72 720 Td ("+text+") Tj ET"), "BT /F1 12 Tf 72 720 Td ("+text+") Tj ET"),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}
