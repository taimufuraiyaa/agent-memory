package ingestion_test

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"testing"
)

type fixtureOCR struct{ confidence float64 }

func (f fixtureOCR) Recognize(context.Context, int, []byte) (ingestion.OCRResult, error) {
	return ingestion.OCRResult{Provider: "fixture", Version: "v1", Confidence: f.confidence, Blocks: []ingestion.PDFBlock{{ID: "ocr", Text: "recognized"}}}, nil
}
func TestPDFOCRPreservesLayersAndConfidencePolicy(t *testing.T) {
	original := ingestion.PDFDocument{Pages: []ingestion.PDFPage{{Number: 1, NativeBlocks: []ingestion.PDFBlock{{ID: "native", Text: "artifact", Origin: "native"}}, RequiresOCR: true}}}
	policy := ingestion.OCRPolicy{MinimumQuoteConfidence: .9}
	result, err := ingestion.ApplyPDFOCR(context.Background(), original, map[int][]byte{1: []byte("image")}, fixtureOCR{confidence: .7}, policy)
	if err != nil {
		t.Fatal(err)
	}
	page := result.Pages[0]
	if len(page.NativeBlocks) != 1 || len(page.OCRBlocks) != 1 || page.OCRProvider != "fixture" || ingestion.OCRQuoteAutoVerifiable(page.OCRBlocks[0], policy) {
		t.Fatalf("OCR provenance or layer isolation failed: %+v", page)
	}
}
