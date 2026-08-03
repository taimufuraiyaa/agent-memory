package ingestion

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkdownBuildsDeterministicStructureAndSourceMap(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "book.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	adapter := MarkdownAdapter{ParserVersion: "markdown-v1", NormalizationVersion: "text-v1"}
	doc, err := adapter.Extract("edition-1", content)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(doc.Nodes) != 3 || len(doc.Sections) != 3 {
		t.Fatalf("expected three headings and sections: nodes=%d sections=%d", len(doc.Nodes), len(doc.Sections))
	}
	if doc.Nodes[1].ParentID == nil || *doc.Nodes[1].ParentID != doc.Nodes[0].ID {
		t.Fatalf("expected level-two heading under chapter: %+v", doc.Nodes[1])
	}
	if doc.Nodes[1].ID == doc.Nodes[2].ID {
		t.Fatal("repeated headings must receive distinct deterministic ids")
	}
	if got := string(content[doc.Sections[1].Span.SourceStart:doc.Sections[1].Span.SourceEnd]); got != doc.Sections[1].SourceText {
		t.Fatalf("source span did not map exactly: %q != %q", got, doc.Sections[1].SourceText)
	}
	if doc.Sections[1].NormalizedText == "" {
		t.Fatal("expected normalized Unicode section text")
	}

	again, err := adapter.Extract("edition-1", content)
	if err != nil {
		t.Fatalf("second extract: %v", err)
	}
	if again.Nodes[2].ID != doc.Nodes[2].ID {
		t.Fatal("identical extraction must preserve structural ids")
	}
}

func TestMarkdownRejectsMissingEdition(t *testing.T) {
	_, err := (MarkdownAdapter{ParserVersion: "markdown-v1", NormalizationVersion: "text-v1"}).Extract("", []byte("# Chapter"))
	if err == nil {
		t.Fatal("expected missing edition identity to fail")
	}
}
