package ingestion

import (
	"context"
	"errors"
)

type OCRResult struct {
	Blocks     []PDFBlock `json:"blocks"`
	Provider   string     `json:"provider"`
	Version    string     `json:"version"`
	Confidence float64    `json:"confidence"`
}
type OCRProvider interface {
	Recognize(context.Context, int, []byte) (OCRResult, error)
}
type OCRPolicy struct {
	MinimumQuoteConfidence float64 `json:"minimum_quote_confidence"`
}

func ApplyPDFOCR(ctx context.Context, document PDFDocument, pageImages map[int][]byte, provider OCRProvider, policy OCRPolicy) (PDFDocument, error) {
	if provider == nil || policy.MinimumQuoteConfidence < 0 || policy.MinimumQuoteConfidence > 1 {
		return PDFDocument{}, errors.New("valid OCR provider and threshold are required")
	}
	for i := range document.Pages {
		page := &document.Pages[i]
		if !page.RequiresOCR {
			continue
		}
		image, ok := pageImages[page.Number]
		if !ok {
			continue
		}
		result, err := provider.Recognize(ctx, page.Number, image)
		if err != nil {
			return PDFDocument{}, err
		}
		if result.Provider == "" || result.Version == "" || result.Confidence < 0 || result.Confidence > 1 {
			return PDFDocument{}, errors.New("invalid OCR result provenance")
		}
		page.OCRBlocks = append([]PDFBlock(nil), result.Blocks...)
		for j := range page.OCRBlocks {
			page.OCRBlocks[j].Origin = "ocr"
			if page.OCRBlocks[j].Confidence == 0 {
				page.OCRBlocks[j].Confidence = result.Confidence
			}
		}
		page.OCRProvider, page.OCRVersion = result.Provider, result.Version
	}
	return document, nil
}
func OCRQuoteAutoVerifiable(block PDFBlock, policy OCRPolicy) bool {
	return block.Origin != "ocr" || block.Confidence >= policy.MinimumQuoteConfidence
}
