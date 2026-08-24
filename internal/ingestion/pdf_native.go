package ingestion

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	pdfreader "github.com/ledongthuc/pdf"
)

// NativePDFExtractor extracts positioned native text entirely in-process.
// Pages without native text are left empty so PDFAdapter can mark them for OCR.
type NativePDFExtractor struct{}

func (NativePDFExtractor) ExtractPages(ctx context.Context, source []byte) ([]PDFPage, error) {
	reader, err := pdfreader.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	if reader.NumPage() < 1 {
		return nil, fmt.Errorf("PDF has no pages")
	}
	pages := make([]PDFPage, 0, reader.NumPage())
	for pageNumber := 1; pageNumber <= reader.NumPage(); pageNumber++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		content, err := safePDFPageContent(reader.Page(pageNumber))
		if err != nil {
			return nil, fmt.Errorf("extract PDF page %d: %w", pageNumber, err)
		}
		blocks := mergePDFTextRuns(pageNumber, content.Text)
		pages = append(pages, PDFPage{Number: pageNumber, NativeBlocks: blocks, RequiresOCR: len(blocks) == 0})
	}
	return pages, nil
}

func safePDFPageContent(page pdfreader.Page) (content pdfreader.Content, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("PDF content parser failed: %v", recovered)
		}
	}()
	return page.Content(), nil
}

func mergePDFTextRuns(pageNumber int, text []pdfreader.Text) []PDFBlock {
	positioned := append([]pdfreader.Text(nil), text...)
	sort.SliceStable(positioned, func(i, j int) bool {
		if math.Abs(positioned[i].Y-positioned[j].Y) > 1 {
			return positioned[i].Y > positioned[j].Y
		}
		return positioned[i].X < positioned[j].X
	})

	blocks := make([]PDFBlock, 0, len(positioned))
	for _, item := range positioned {
		value := strings.ReplaceAll(item.S, "\x00", "")
		if value == "" {
			continue
		}
		height := item.FontSize
		if height <= 0 {
			height = 1
		}
		if len(blocks) == 0 || !samePDFTextLine(blocks[len(blocks)-1], item, height) {
			blocks = append(blocks, PDFBlock{Text: value, X: item.X, Y: item.Y, Width: math.Max(item.W, 0.1), Height: height, Confidence: 1, Origin: "native"})
			continue
		}
		block := &blocks[len(blocks)-1]
		gap := item.X - (block.X + block.Width)
		if gap > height*0.2 && !strings.HasSuffix(block.Text, " ") && !strings.HasPrefix(value, " ") {
			block.Text += " "
		}
		block.Text += value
		end := item.X + math.Max(item.W, 0.1)
		if end > block.X+block.Width {
			block.Width = end - block.X
		}
		if height > block.Height {
			block.Height = height
		}
	}
	compact := blocks[:0]
	for _, block := range blocks {
		block.Text = strings.Join(strings.Fields(block.Text), " ")
		if block.Text == "" {
			continue
		}
		block.ID = fmt.Sprintf("page-%d-block-%d", pageNumber, len(compact)+1)
		compact = append(compact, block)
	}
	return compact
}

func samePDFTextLine(block PDFBlock, item pdfreader.Text, height float64) bool {
	if math.Abs(block.Y-item.Y) > math.Max(2, height*0.35) {
		return false
	}
	gap := item.X - (block.X + block.Width)
	return gap >= -height && gap <= math.Max(8, height*1.5)
}
