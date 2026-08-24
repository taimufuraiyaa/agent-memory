package core

import (
	"errors"
	"strings"
)

// AttributionKind identifies whose position or words a knowledge record represents.
type AttributionKind string

const (
	AttributionAuthor         AttributionKind = "author"
	AttributionReader         AttributionKind = "reader"
	AttributionAgent          AttributionKind = "agent"
	AttributionOrganization   AttributionKind = "organization"
	AttributionExternalSource AttributionKind = "external_source"
)

// KnowledgeForm describes what a record contains independently from who said it.
type KnowledgeForm string

const (
	KnowledgeClaim        KnowledgeForm = "claim"
	KnowledgeQuote        KnowledgeForm = "quote"
	KnowledgeSummary      KnowledgeForm = "summary"
	KnowledgeNote         KnowledgeForm = "note"
	KnowledgeQuestion     KnowledgeForm = "question"
	KnowledgeExplanation  KnowledgeForm = "explanation"
	KnowledgeSynthesis    KnowledgeForm = "synthesis"
	KnowledgeDefinition   KnowledgeForm = "definition"
	KnowledgeRecollection KnowledgeForm = "recollection"
	KnowledgeInsight      KnowledgeForm = "insight"
)

// DerivationKind records how knowledge was produced.
type DerivationKind string

const (
	DerivationExtracted    DerivationKind = "extracted"
	DerivationInterpreted  DerivationKind = "interpreted"
	DerivationDiscussed    DerivationKind = "discussed"
	DerivationConsolidated DerivationKind = "consolidated"
	DerivationApplied      DerivationKind = "applied"
	DerivationRecalled     DerivationKind = "recalled"
)

type Attribution struct {
	Kind        AttributionKind `json:"kind"`
	SubjectID   string          `json:"subject_id"`
	DisplayName string          `json:"display_name,omitempty"`
}

// KnowledgeProvenance keeps attribution, representation, and derivation independent.
type KnowledgeProvenance struct {
	Attribution Attribution    `json:"attribution"`
	Form        KnowledgeForm  `json:"form"`
	Derivation  DerivationKind `json:"derivation"`
	CitationIDs []string       `json:"citation_ids,omitempty"`
	DerivedFrom []string       `json:"derived_from,omitempty"`
}

// LocatorKind identifies the source-specific location scheme used by a citation.
type LocatorKind string

const (
	LocatorPDF      LocatorKind = "pdf"
	LocatorEPUB     LocatorKind = "epub"
	LocatorMarkdown LocatorKind = "markdown"
	LocatorText     LocatorKind = "text"
	LocatorWeb      LocatorKind = "web"
)

type PDFLocator struct {
	Page        int       `json:"page"`
	BlockID     string    `json:"block_id,omitempty"`
	BoundingBox []float64 `json:"bounding_box,omitempty"`
	StartOffset int       `json:"start_offset,omitempty"`
	EndOffset   int       `json:"end_offset,omitempty"`
}

type EPUBLocator struct {
	SpineItem   string `json:"spine_item"`
	CFI         string `json:"cfi,omitempty"`
	StartOffset int    `json:"start_offset,omitempty"`
	EndOffset   int    `json:"end_offset,omitempty"`
}

type TextLocator struct {
	HeadingPath     []string `json:"heading_path,omitempty"`
	SourceStart     int      `json:"source_start"`
	SourceEnd       int      `json:"source_end"`
	NormalizedStart int      `json:"normalized_start"`
	NormalizedEnd   int      `json:"normalized_end"`
}

type WebLocator struct {
	CaptureID    string `json:"capture_id"`
	CanonicalURL string `json:"canonical_url"`
	Selector     string `json:"selector"`
	StartOffset  int    `json:"start_offset,omitempty"`
	EndOffset    int    `json:"end_offset,omitempty"`
}

// SourceLocator combines a display breadcrumb with exactly one format-specific machine locator.
type SourceLocator struct {
	Kind                 LocatorKind  `json:"kind"`
	Display              string       `json:"display"`
	ParserVersion        string       `json:"parser_version"`
	NormalizationVersion string       `json:"normalization_version"`
	PDF                  *PDFLocator  `json:"pdf,omitempty"`
	EPUB                 *EPUBLocator `json:"epub,omitempty"`
	Text                 *TextLocator `json:"text,omitempty"`
	Web                  *WebLocator  `json:"web,omitempty"`
}

// Citation points to evidence in one immutable edition and source asset.
type Citation struct {
	ID                 string        `json:"id"`
	EditionID          string        `json:"edition_id"`
	SourceAssetID      string        `json:"source_asset_id"`
	PassageID          string        `json:"passage_id"`
	StructuralNodeID   string        `json:"structural_node_id,omitempty"`
	Locator            SourceLocator `json:"locator"`
	PassageFingerprint string        `json:"passage_fingerprint"`
	ShortQuote         string        `json:"short_quote,omitempty"`
	VerificationIDs    []string      `json:"verification_ids,omitempty"`
}

func IsAttributionKind(kind AttributionKind) bool {
	switch kind {
	case AttributionAuthor, AttributionReader, AttributionAgent, AttributionOrganization, AttributionExternalSource:
		return true
	default:
		return false
	}
}

func IsKnowledgeForm(form KnowledgeForm) bool {
	switch form {
	case KnowledgeClaim, KnowledgeQuote, KnowledgeSummary, KnowledgeNote, KnowledgeQuestion,
		KnowledgeExplanation, KnowledgeSynthesis, KnowledgeDefinition, KnowledgeRecollection, KnowledgeInsight:
		return true
	default:
		return false
	}
}

func IsDerivationKind(kind DerivationKind) bool {
	switch kind {
	case DerivationExtracted, DerivationInterpreted, DerivationDiscussed,
		DerivationConsolidated, DerivationApplied, DerivationRecalled:
		return true
	default:
		return false
	}
}

func (a Attribution) Validate() error {
	if !IsAttributionKind(a.Kind) {
		return errors.New("invalid attribution kind")
	}
	if strings.TrimSpace(a.SubjectID) == "" {
		return errors.New("attribution subject id is required")
	}
	return nil
}

func (p KnowledgeProvenance) Validate() error {
	if err := p.Attribution.Validate(); err != nil {
		return err
	}
	if !IsKnowledgeForm(p.Form) {
		return errors.New("invalid knowledge form")
	}
	if !IsDerivationKind(p.Derivation) {
		return errors.New("invalid derivation kind")
	}
	if hasBlankID(p.CitationIDs) {
		return errors.New("citation ids cannot be blank")
	}
	if hasBlankID(p.DerivedFrom) {
		return errors.New("derivation ids cannot be blank")
	}

	if p.Form == KnowledgeQuote {
		if len(p.CitationIDs) == 0 {
			return errors.New("direct quote requires a citation")
		}
		if p.Attribution.Kind != AttributionAuthor && p.Attribution.Kind != AttributionExternalSource {
			return errors.New("direct quote requires source attribution")
		}
		if p.Derivation != DerivationExtracted {
			return errors.New("direct quote must be extracted from source evidence")
		}
	}
	if p.Form == KnowledgeClaim && p.Attribution.Kind == AttributionAuthor && len(p.CitationIDs) == 0 {
		return errors.New("author claim requires a citation")
	}
	if p.Form == KnowledgeSynthesis && len(p.DerivedFrom) == 0 {
		return errors.New("synthesis requires derivation links")
	}
	if p.Form == KnowledgeRecollection && (p.Attribution.Kind != AttributionReader || p.Derivation != DerivationRecalled) {
		return errors.New("recollection must be recalled by a reader")
	}
	return nil
}

func hasBlankID(ids []string) bool {
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return true
		}
	}
	return false
}

func IsLocatorKind(kind LocatorKind) bool {
	switch kind {
	case LocatorPDF, LocatorEPUB, LocatorMarkdown, LocatorText, LocatorWeb:
		return true
	default:
		return false
	}
}

func (l SourceLocator) Validate() error {
	if !IsLocatorKind(l.Kind) {
		return errors.New("invalid locator kind")
	}
	if strings.TrimSpace(l.Display) == "" || strings.TrimSpace(l.ParserVersion) == "" || strings.TrimSpace(l.NormalizationVersion) == "" {
		return errors.New("locator requires display, parser version, and normalization version")
	}
	if locatorPayloadCount(l) != 1 {
		return errors.New("locator requires exactly one machine locator")
	}
	switch l.Kind {
	case LocatorPDF:
		if l.PDF == nil || l.PDF.Page < 1 || invalidRange(l.PDF.StartOffset, l.PDF.EndOffset) {
			return errors.New("invalid pdf locator")
		}
	case LocatorEPUB:
		if l.EPUB == nil || strings.TrimSpace(l.EPUB.SpineItem) == "" ||
			(strings.TrimSpace(l.EPUB.CFI) == "" && l.EPUB.EndOffset == 0) || invalidRange(l.EPUB.StartOffset, l.EPUB.EndOffset) {
			return errors.New("invalid epub locator")
		}
	case LocatorMarkdown, LocatorText:
		if l.Text == nil || invalidRequiredRange(l.Text.SourceStart, l.Text.SourceEnd) ||
			invalidRequiredRange(l.Text.NormalizedStart, l.Text.NormalizedEnd) {
			return errors.New("invalid text locator")
		}
	case LocatorWeb:
		if l.Web == nil || strings.TrimSpace(l.Web.CaptureID) == "" || strings.TrimSpace(l.Web.CanonicalURL) == "" ||
			strings.TrimSpace(l.Web.Selector) == "" || invalidRange(l.Web.StartOffset, l.Web.EndOffset) {
			return errors.New("invalid web locator")
		}
	}
	return nil
}

func locatorPayloadCount(locator SourceLocator) int {
	count := 0
	for _, present := range []bool{locator.PDF != nil, locator.EPUB != nil, locator.Text != nil, locator.Web != nil} {
		if present {
			count++
		}
	}
	return count
}

func invalidRange(start, end int) bool {
	return start < 0 || end < 0 || (end > 0 && end < start)
}

func invalidRequiredRange(start, end int) bool {
	return start < 0 || end <= start
}

func (c Citation) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("citation id is required")
	}
	if strings.TrimSpace(c.EditionID) == "" {
		return errors.New("citation edition id is required")
	}
	if strings.TrimSpace(c.SourceAssetID) == "" {
		return errors.New("citation source asset id is required")
	}
	if strings.TrimSpace(c.PassageID) == "" {
		return errors.New("citation passage id is required")
	}
	if strings.TrimSpace(c.PassageFingerprint) == "" {
		return errors.New("citation passage fingerprint is required")
	}
	return c.Locator.Validate()
}
