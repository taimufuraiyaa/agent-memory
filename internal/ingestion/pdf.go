package ingestion

import (
	"context"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"sort"
	"strconv"
	"strings"
)

type PDFBlock struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	Confidence float64 `json:"confidence,omitempty"`
	Origin     string  `json:"origin"`
}
type PDFPage struct {
	Number       int        `json:"number"`
	NativeBlocks []PDFBlock `json:"native_blocks,omitempty"`
	OCRBlocks    []PDFBlock `json:"ocr_blocks,omitempty"`
	RequiresOCR  bool       `json:"requires_ocr"`
	OCRProvider  string     `json:"ocr_provider,omitempty"`
	OCRVersion   string     `json:"ocr_version,omitempty"`
}
type PDFDocument struct {
	Pages    []PDFPage         `json:"pages"`
	Passages []library.Passage `json:"passages"`
}
type PDFPageExtractor interface {
	ExtractPages(context.Context, []byte) ([]PDFPage, error)
}
type PDFAdapter struct {
	ParserVersion, NormalizationVersion string
	Extractor                           PDFPageExtractor
}

func (a PDFAdapter) Extract(ctx context.Context, editionID, assetID string, source []byte) (PDFDocument, error) {
	if a.Extractor == nil || editionID == "" || assetID == "" || a.ParserVersion == "" || a.NormalizationVersion == "" {
		return PDFDocument{}, errors.New("pdf adapter identity, versions, and extractor are required")
	}
	if !strings.HasPrefix(string(source), "%PDF-") {
		return PDFDocument{}, errors.New("invalid PDF header")
	}
	pages, err := a.Extractor.ExtractPages(ctx, source)
	if err != nil {
		return PDFDocument{}, err
	}
	out := PDFDocument{Pages: pages, Passages: []library.Passage{}}
	for pageIndex := range out.Pages {
		page := &out.Pages[pageIndex]
		if page.Number < 1 {
			return PDFDocument{}, errors.New("PDF page number must be positive")
		}
		sort.SliceStable(page.NativeBlocks, func(i, j int) bool {
			left, right := page.NativeBlocks[i], page.NativeBlocks[j]
			leftColumn, rightColumn := int(left.X/300), int(right.X/300)
			if leftColumn != rightColumn {
				return leftColumn < rightColumn
			}
			if left.Y != right.Y {
				return left.Y < right.Y
			}
			return left.X < right.X
		})
		if len(page.NativeBlocks) == 0 {
			page.RequiresOCR = true
			continue
		}
		for _, block := range page.NativeBlocks {
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			pageNumber := strconv.Itoa(page.Number)
			passage := library.Passage{ID: stableImportID("passage", editionID, pageNumber, block.ID, block.Text), EditionID: editionID, SourceAssetID: assetID, StructuralNodeID: "page:" + stableImportID("node", editionID, pageNumber), Text: strings.TrimSpace(block.Text), Fingerprint: core.FingerprintText(strings.TrimSpace(block.Text)), Locator: core.SourceLocator{Kind: core.LocatorPDF, Display: "Page " + pageNumber, ParserVersion: a.ParserVersion, NormalizationVersion: a.NormalizationVersion, PDF: &core.PDFLocator{Page: page.Number, BlockID: block.ID, BoundingBox: []float64{block.X, block.Y, block.Width, block.Height}}}}
			out.Passages = append(out.Passages, passage)
		}
	}
	return out, nil
}
