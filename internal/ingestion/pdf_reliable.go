package ingestion

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"
)

const defaultPDFExtractionOutputLimit = 64 << 20

var ErrPDFTextUntrustworthy = errors.New("PDF text layer could not be decoded safely")

// ReliablePDFExtractor keeps the in-process decoder as the fast path and uses
// a second local decoder only when the first result fails the text-quality
// boundary. Source bytes never leave the current process boundary except via
// the fallback command's stdin.
type ReliablePDFExtractor struct {
	Primary  PDFPageExtractor
	Fallback PDFPageExtractor
}

func (r ReliablePDFExtractor) ExtractPages(ctx context.Context, source []byte) ([]PDFPage, error) {
	if r.Primary == nil || r.Fallback == nil {
		return nil, errors.New("reliable PDF extractors are not configured")
	}
	pages, primaryErr := r.Primary.ExtractPages(ctx, source)
	if primaryErr == nil && pdfPagesTrustworthy(pages) {
		return pages, nil
	}
	fallbackPages, fallbackErr := r.Fallback.ExtractPages(ctx, source)
	if fallbackErr != nil {
		return nil, fmt.Errorf("%w: local fallback failed", ErrPDFTextUntrustworthy)
	}
	if !pdfPagesTrustworthy(fallbackPages) {
		return nil, ErrPDFTextUntrustworthy
	}
	return fallbackPages, nil
}

func pdfPagesTrustworthy(pages []PDFPage) bool {
	total, visible, controls := 0, 0, 0
	for _, page := range pages {
		for _, block := range page.NativeBlocks {
			for _, r := range block.Text {
				total++
				if !unicode.IsSpace(r) {
					visible++
				}
				if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
					controls++
				}
			}
		}
	}
	if total == 0 || visible == 0 {
		return false
	}
	return controls < 2 || controls*200 <= total
}

// PopplerPDFExtractor invokes the packaged local pdftotext binary. It consumes
// PDF bytes on stdin and parses bounded UTF-8 bounding-box output from stdout.
type PopplerPDFExtractor struct {
	Executable     string
	MaxOutputBytes int
}

func (p PopplerPDFExtractor) ExtractPages(ctx context.Context, source []byte) ([]PDFPage, error) {
	executable := strings.TrimSpace(p.Executable)
	if executable == "" {
		executable = "pdftotext"
	}
	limit := p.MaxOutputBytes
	if limit <= 0 {
		limit = defaultPDFExtractionOutputLimit
	}
	command := exec.CommandContext(ctx, executable, "-q", "-bbox-layout", "-enc", "UTF-8", "-", "-")
	command.Stdin = bytes.NewReader(source)
	var output boundedBuffer
	output.limit = limit
	command.Stdout = &output
	command.Stderr = &boundedBuffer{limit: 4096}
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("run local PDF fallback: %w", err)
	}
	return parsePopplerBBox(output.Bytes())
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	if b.Len()+len(value) > b.limit {
		return 0, errors.New("PDF extraction output exceeded its limit")
	}
	return b.Buffer.Write(value)
}

type popplerDocument struct {
	Pages []popplerPage `xml:"body>doc>page"`
}

type popplerPage struct {
	Width  float64       `xml:"width,attr"`
	Height float64       `xml:"height,attr"`
	Flows  []popplerFlow `xml:"flow"`
}

type popplerFlow struct {
	Blocks []popplerBlock `xml:"block"`
}

type popplerBlock struct {
	Lines []popplerLine `xml:"line"`
}

type popplerLine struct {
	XMin  float64       `xml:"xMin,attr"`
	YMin  float64       `xml:"yMin,attr"`
	XMax  float64       `xml:"xMax,attr"`
	YMax  float64       `xml:"yMax,attr"`
	Words []popplerWord `xml:"word"`
}

type popplerWord struct {
	Text string `xml:",chardata"`
}

func parsePopplerBBox(value []byte) ([]PDFPage, error) {
	var document popplerDocument
	if err := xml.Unmarshal(value, &document); err != nil {
		return nil, fmt.Errorf("parse local PDF fallback output: %w", err)
	}
	if len(document.Pages) == 0 {
		return nil, errors.New("local PDF fallback returned no pages")
	}
	pages := make([]PDFPage, 0, len(document.Pages))
	for pageIndex, sourcePage := range document.Pages {
		page := PDFPage{Number: pageIndex + 1}
		for _, flow := range sourcePage.Flows {
			for _, block := range flow.Blocks {
				for _, line := range block.Lines {
					words := make([]string, 0, len(line.Words))
					for _, word := range line.Words {
						if text := strings.TrimSpace(word.Text); text != "" {
							words = append(words, text)
						}
					}
					text := strings.Join(words, " ")
					if text == "" {
						continue
					}
					page.NativeBlocks = append(page.NativeBlocks, PDFBlock{
						ID:         fmt.Sprintf("page-%d-block-%d", page.Number, len(page.NativeBlocks)+1),
						Text:       strings.ToValidUTF8(text, string(utf8.RuneError)),
						X:          line.XMin,
						Y:          sourcePage.Height - line.YMax,
						Width:      line.XMax - line.XMin,
						Height:     line.YMax - line.YMin,
						Confidence: 1,
						Origin:     "poppler-native",
					})
				}
			}
		}
		page.RequiresOCR = len(page.NativeBlocks) == 0
		pages = append(pages, page)
	}
	return pages, nil
}
