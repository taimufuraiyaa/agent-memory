package ingestion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

type fixedPDFPageExtractor struct {
	pages []PDFPage
	err   error
}

func (f fixedPDFPageExtractor) ExtractPages(context.Context, []byte) ([]PDFPage, error) {
	return f.pages, f.err
}

func TestReliablePDFExtractorFallsBackWhenPrimaryTextIsUntrustworthy(t *testing.T) {
	primary := []PDFPage{{Number: 1, NativeBlocks: []PDFBlock{{Text: "\x03\x08Î¾Õ corrupted", Origin: "native"}}}}
	fallback := []PDFPage{{Number: 1, NativeBlocks: []PDFBlock{{Text: "Latency is elapsed time.", Origin: "poppler-native"}}}}

	pages, err := (ReliablePDFExtractor{
		Primary:  fixedPDFPageExtractor{pages: primary},
		Fallback: fixedPDFPageExtractor{pages: fallback},
	}).ExtractPages(context.Background(), []byte("%PDF-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || len(pages[0].NativeBlocks) != 1 || pages[0].NativeBlocks[0].Text != "Latency is elapsed time." {
		t.Fatalf("fallback text was not selected: %+v", pages)
	}
}

func TestReliablePDFExtractorFailsClosedWhenEveryDecoderIsUntrustworthy(t *testing.T) {
	untrustworthy := []PDFPage{{Number: 1, NativeBlocks: []PDFBlock{{Text: "\x03\x08\x10Î¾Õ", Origin: "native"}}}}
	_, err := (ReliablePDFExtractor{
		Primary:  fixedPDFPageExtractor{pages: untrustworthy},
		Fallback: fixedPDFPageExtractor{pages: untrustworthy},
	}).ExtractPages(context.Background(), []byte("%PDF-fixture"))
	if !errors.Is(err, ErrPDFTextUntrustworthy) {
		t.Fatalf("error=%v, want ErrPDFTextUntrustworthy", err)
	}
}

func TestParsePopplerBBoxPreservesUnicodePageAndLineCoordinates(t *testing.T) {
	input := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<html><body><doc><page width="612" height="792"><flow><block xMin="72" yMin="58" xMax="240" yMax="74"><line xMin="72" yMin="58" xMax="240" yMax="74"><word xMin="72" yMin="58" xMax="120" yMax="74">Latency</word><word xMin="126" yMin="58" xMax="170" yMax="74">độ trễ</word></line></block></flow></page></doc></body></html>`)

	pages, err := parsePopplerBBox(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || len(pages[0].NativeBlocks) != 1 {
		t.Fatalf("unexpected pages: %+v", pages)
	}
	block := pages[0].NativeBlocks[0]
	if block.Text != "Latency độ trễ" || block.X != 72 || block.Y != 718 || block.Width != 168 || block.Height != 16 || block.Origin != "poppler-native" {
		t.Fatalf("unexpected block: %+v", block)
	}
}

func TestPopplerPDFExtractorReadsPackagedNativeText(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext is not installed in this test environment")
	}
	pages, err := (PopplerPDFExtractor{}).ExtractPages(context.Background(), fallbackTextPDF("Latency is elapsed time."))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || len(pages[0].NativeBlocks) == 0 || pages[0].NativeBlocks[0].Text != "Latency is elapsed time." {
		t.Fatalf("unexpected Poppler extraction: %+v", pages)
	}
}

func fallbackTextPDF(text string) []byte {
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}
