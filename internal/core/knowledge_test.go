package core

import "testing"

func TestKnowledgeProvenanceSeparatesAttributionFormAndDerivation(t *testing.T) {
	provenance := KnowledgeProvenance{
		Attribution: Attribution{Kind: AttributionAuthor, SubjectID: "author-1", DisplayName: "Author"},
		Form:        KnowledgeClaim,
		Derivation:  DerivationExtracted,
		CitationIDs: []string{"citation-1"},
	}
	if err := provenance.Validate(); err != nil {
		t.Fatalf("expected valid author claim provenance: %v", err)
	}

	if !IsAttributionKind(AttributionReader) || !IsKnowledgeForm(KnowledgeQuestion) || !IsDerivationKind(DerivationDiscussed) {
		t.Fatal("expected attribution, knowledge form, and derivation enums to validate independently")
	}
}

func TestKnowledgeProvenanceRejectsEpistemicallyInvalidCombinations(t *testing.T) {
	tests := []struct {
		name       string
		provenance KnowledgeProvenance
	}{
		{
			name: "direct quote without citation",
			provenance: KnowledgeProvenance{
				Attribution: Attribution{Kind: AttributionAuthor, SubjectID: "author-1"},
				Form:        KnowledgeQuote,
				Derivation:  DerivationExtracted,
			},
		},
		{
			name: "direct quote attributed to an agent interpretation",
			provenance: KnowledgeProvenance{
				Attribution: Attribution{Kind: AttributionAgent, SubjectID: "agent-1"},
				Form:        KnowledgeQuote,
				Derivation:  DerivationInterpreted,
				CitationIDs: []string{"citation-1"},
			},
		},
		{
			name: "author claim without citation",
			provenance: KnowledgeProvenance{
				Attribution: Attribution{Kind: AttributionAuthor, SubjectID: "author-1"},
				Form:        KnowledgeClaim,
				Derivation:  DerivationExtracted,
			},
		},
		{
			name: "synthesis without derivation",
			provenance: KnowledgeProvenance{
				Attribution: Attribution{Kind: AttributionAgent, SubjectID: "agent-1"},
				Form:        KnowledgeSynthesis,
				Derivation:  DerivationConsolidated,
			},
		},
		{
			name: "reader recollection with wrong derivation",
			provenance: KnowledgeProvenance{
				Attribution: Attribution{Kind: AttributionReader, SubjectID: "reader-1"},
				Form:        KnowledgeRecollection,
				Derivation:  DerivationExtracted,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.provenance.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCitationValidate(t *testing.T) {
	valid := Citation{
		ID:            "citation-1",
		EditionID:     "edition-1",
		SourceAssetID: "asset-1",
		PassageID:     "passage-1",
		Locator: SourceLocator{
			Kind:                 LocatorMarkdown,
			Display:              "Chapter 1 > The Problem",
			ParserVersion:        "markdown-v1",
			NormalizationVersion: "text-v1",
			Text: &TextLocator{
				HeadingPath:     []string{"Chapter 1", "The Problem"},
				SourceStart:     10,
				SourceEnd:       45,
				NormalizedStart: 12,
				NormalizedEnd:   42,
			},
		},
		PassageFingerprint: "sha256:abc",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid citation: %v", err)
	}

	invalid := valid
	invalid.EditionID = ""
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected edition identity to be required")
	}

	invalid = valid
	invalid.Locator.Text.NormalizedEnd = 3
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected invalid locator range to be rejected")
	}
}

func TestSourceLocatorRequiresFormatSpecificCoordinates(t *testing.T) {
	valid := []SourceLocator{
		{Kind: LocatorPDF, Display: "p. 61", ParserVersion: "pdf-v1", NormalizationVersion: "text-v1", PDF: &PDFLocator{Page: 61, StartOffset: 10, EndOffset: 30}},
		{Kind: LocatorEPUB, Display: "Chapter 3", ParserVersion: "epub-v1", NormalizationVersion: "text-v1", EPUB: &EPUBLocator{SpineItem: "chapter-3.xhtml", CFI: "epubcfi(/6/8)"}},
		{Kind: LocatorWeb, Display: "Section 2", ParserVersion: "web-v1", NormalizationVersion: "text-v1", Web: &WebLocator{CaptureID: "capture-1", CanonicalURL: "https://example.test/book", Selector: "#section-2"}},
	}
	for _, locator := range valid {
		if err := locator.Validate(); err != nil {
			t.Fatalf("expected %q locator to be valid: %v", locator.Kind, err)
		}
	}

	invalid := SourceLocator{Kind: LocatorPDF, Display: "Chapter 3", ParserVersion: "pdf-v1", NormalizationVersion: "text-v1"}
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected display-only PDF locator to fail")
	}
}
