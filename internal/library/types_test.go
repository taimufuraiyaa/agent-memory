package library

import (
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestBookWorkEditionAndSourceAssetValidation(t *testing.T) {
	work := BookWork{ID: "work-1", Title: "The Book", NormalizedTitle: "the book"}
	if err := work.Validate(); err != nil {
		t.Fatalf("valid work: %v", err)
	}
	edition := BookEdition{ID: "edition-1", WorkID: work.ID, Label: "First edition", Language: "en", ContentFingerprint: "sha256:content"}
	if err := edition.Validate(); err != nil {
		t.Fatalf("valid edition: %v", err)
	}
	asset := SourceAsset{
		ID:                    "asset-1",
		EditionID:             edition.ID,
		Format:                FormatMarkdown,
		ByteFingerprint:       "sha256:bytes",
		NormalizedFingerprint: "sha256:content",
		ParserVersion:         "markdown-v1",
		Policy: core.SourcePolicy{
			Retention:       core.RetentionRetained,
			StoreOriginal:   true,
			StoreNormalized: true,
			AllowSearch:     true,
		},
		ImportedAt: time.Now().UTC(),
	}
	if err := asset.Validate(); err != nil {
		t.Fatalf("valid source asset: %v", err)
	}
	if work.ID == edition.ID {
		t.Fatal("work and edition identities must be distinct")
	}
}

func TestValidateStructureRequiresOrderedAcyclicHierarchy(t *testing.T) {
	rootID := "chapter-1"
	nodes := []StructuralNode{
		{ID: rootID, EditionID: "edition-1", Kind: NodeChapter, Ordinal: 0, Title: "Chapter 1"},
		{ID: "section-1", EditionID: "edition-1", ParentID: &rootID, Kind: NodeSection, Ordinal: 0, Title: "First section"},
	}
	if err := ValidateStructure(nodes); err != nil {
		t.Fatalf("expected valid structure: %v", err)
	}

	missing := "missing"
	nodes[1].ParentID = &missing
	if err := ValidateStructure(nodes); err == nil {
		t.Fatal("expected unknown parent to fail")
	}
}
