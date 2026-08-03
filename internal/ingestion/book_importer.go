package ingestion

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

type BookImportInput struct {
	Title        string
	EditionLabel string
	Language     string
	Format       library.SourceFormat
	Source       []byte
	Policy       core.SourcePolicy
}

type BookExtraction struct {
	NormalizedText       string
	Nodes                []library.StructuralNode
	Passages             []library.Passage
	ParserVersion        string
	NormalizationVersion string
}

type BookExtractor interface {
	Format() library.SourceFormat
	Extract(context.Context, string, string, []byte) (BookExtraction, error)
}

type BookImporter struct {
	repository ImportRepository
	extractors map[library.SourceFormat]BookExtractor
}

func NewBookImporter(repository ImportRepository, extractors ...BookExtractor) *BookImporter {
	registered := make(map[library.SourceFormat]BookExtractor, len(extractors))
	for _, extractor := range extractors {
		if extractor != nil {
			registered[extractor.Format()] = extractor
		}
	}
	return &BookImporter{repository: repository, extractors: registered}
}

func (i *BookImporter) Import(ctx context.Context, input BookImportInput) (ImportResult, error) {
	if i == nil || i.repository == nil {
		return ImportResult{}, errors.New("import repository is required")
	}
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.EditionLabel) == "" || strings.TrimSpace(input.Language) == "" || len(input.Source) == 0 {
		return ImportResult{}, errors.New("title, edition label, language, and source are required")
	}
	if err := input.Policy.Validate(); err != nil {
		return ImportResult{}, err
	}
	extractor, ok := i.extractors[input.Format]
	if !ok {
		return ImportResult{}, fmt.Errorf("unsupported book format %q", input.Format)
	}

	byteFingerprint := core.FingerprintText(string(input.Source))
	if result, found, err := findExistingImport(ctx, i.repository, byteFingerprint, input.Policy); err != nil || found {
		return result, err
	}

	normalizedTitle := strings.ToLower(strings.Join(strings.Fields(input.Title), " "))
	workID := stableImportID("work", normalizedTitle)
	assetID := stableImportID("asset", byteFingerprint)
	provisionalEditionID := stableImportID("provisional", byteFingerprint)
	provisional, err := extractor.Extract(ctx, provisionalEditionID, assetID, input.Source)
	if err != nil {
		return ImportResult{}, err
	}
	if strings.TrimSpace(provisional.NormalizedText) == "" {
		return ImportResult{}, errors.New("book has no searchable native text; OCR is required")
	}
	contentFingerprint := core.FingerprintText(provisional.NormalizedText)
	editionID := stableImportID("edition", workID, contentFingerprint)
	document, err := extractor.Extract(ctx, editionID, assetID, input.Source)
	if err != nil {
		return ImportResult{}, err
	}
	if err := validateBookExtraction(editionID, assetID, document); err != nil {
		return ImportResult{}, err
	}

	if err := i.repository.PutBookWork(ctx, library.BookWork{ID: workID, Title: strings.TrimSpace(input.Title), NormalizedTitle: normalizedTitle}); err != nil {
		return ImportResult{}, err
	}
	edition, found, err := i.repository.FindBookEditionByFingerprint(ctx, workID, contentFingerprint)
	if err != nil {
		return ImportResult{}, err
	}
	if !found {
		edition = library.BookEdition{ID: editionID, WorkID: workID, Label: strings.TrimSpace(input.EditionLabel), Language: strings.TrimSpace(input.Language), ContentFingerprint: contentFingerprint}
		if err := i.repository.PutBookEdition(ctx, edition); err != nil {
			return ImportResult{}, err
		}
	}
	if edition.ID != editionID {
		document, err = extractor.Extract(ctx, edition.ID, assetID, input.Source)
		if err != nil {
			return ImportResult{}, err
		}
		if err := validateBookExtraction(edition.ID, assetID, document); err != nil {
			return ImportResult{}, err
		}
	}
	asset := library.SourceAsset{
		ID: assetID, EditionID: edition.ID, Format: input.Format,
		ByteFingerprint: byteFingerprint, NormalizedFingerprint: contentFingerprint,
		ParserVersion: document.ParserVersion, Policy: input.Policy, ImportedAt: time.Now().UTC(),
	}
	if err := i.repository.PutSourceAsset(ctx, asset); err != nil {
		return ImportResult{}, err
	}
	if err := i.repository.ReplaceStructuralNodes(ctx, edition.ID, document.Nodes); err != nil {
		return ImportResult{}, err
	}
	if err := i.repository.PutPassages(ctx, document.Passages); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{WorkID: workID, EditionID: edition.ID, AssetID: assetID, Format: input.Format, NodeCount: len(document.Nodes), PassageCount: len(document.Passages)}, nil
}

func findExistingImport(ctx context.Context, repository ImportRepository, byteFingerprint string, policy core.SourcePolicy) (ImportResult, bool, error) {
	asset, found, err := repository.FindSourceAssetByByteFingerprint(ctx, byteFingerprint)
	if err != nil || !found {
		return ImportResult{}, false, err
	}
	edition, err := repository.GetBookEdition(ctx, asset.EditionID)
	if err != nil {
		return ImportResult{}, false, err
	}
	nodes, err := repository.ListStructuralNodes(ctx, edition.ID)
	if err != nil {
		return ImportResult{}, false, err
	}
	passages, err := repository.ListPassagesForEditions(ctx, []string{edition.ID})
	if err != nil {
		return ImportResult{}, false, err
	}
	asset.Policy = policy
	if err := repository.PutSourceAsset(ctx, asset); err != nil {
		return ImportResult{}, false, err
	}
	return ImportResult{WorkID: edition.WorkID, EditionID: edition.ID, AssetID: asset.ID, Format: asset.Format, NodeCount: len(nodes), PassageCount: len(passages), Existing: true}, true, nil
}

func validateBookExtraction(editionID, assetID string, extraction BookExtraction) error {
	if strings.TrimSpace(extraction.NormalizedText) == "" || strings.TrimSpace(extraction.ParserVersion) == "" || strings.TrimSpace(extraction.NormalizationVersion) == "" {
		return errors.New("book extraction requires normalized text and parser versions")
	}
	if len(extraction.Nodes) == 0 || len(extraction.Passages) == 0 {
		return errors.New("book extraction requires structure and searchable passages")
	}
	if err := library.ValidateStructure(extraction.Nodes); err != nil {
		return err
	}
	for _, node := range extraction.Nodes {
		if node.EditionID != editionID {
			return errors.New("book structural node edition mismatch")
		}
	}
	for _, passage := range extraction.Passages {
		if passage.EditionID != editionID || passage.SourceAssetID != assetID {
			return errors.New("book passage source identity mismatch")
		}
		if err := passage.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type MarkdownBookExtractor struct {
	Adapter MarkdownAdapter
	AsText  bool
}

func (e MarkdownBookExtractor) Format() library.SourceFormat {
	if e.AsText {
		return library.FormatText
	}
	return library.FormatMarkdown
}

func (e MarkdownBookExtractor) Extract(_ context.Context, editionID, assetID string, source []byte) (BookExtraction, error) {
	document, err := e.Adapter.Extract(editionID, source)
	if err != nil {
		return BookExtraction{}, err
	}
	nodes := make(map[string]library.StructuralNode, len(document.Nodes))
	for _, node := range document.Nodes {
		nodes[node.ID] = node
	}
	passages := make([]library.Passage, 0, len(document.Sections))
	for _, section := range document.Sections {
		node := nodes[section.NodeID]
		passages = append(passages, library.Passage{
			ID:        stableImportID("passage", editionID, assetID, section.NodeID, section.NormalizedText),
			EditionID: editionID, SourceAssetID: assetID, StructuralNodeID: section.NodeID,
			Text: section.NormalizedText, Fingerprint: core.FingerprintText(section.NormalizedText),
			Locator: core.SourceLocator{Kind: core.LocatorMarkdown, Display: node.Title, ParserVersion: e.Adapter.ParserVersion, NormalizationVersion: e.Adapter.NormalizationVersion, Text: &core.TextLocator{HeadingPath: []string{node.Title}, SourceStart: section.Span.SourceStart, SourceEnd: section.Span.SourceEnd, NormalizedStart: section.Span.NormalizedStart, NormalizedEnd: section.Span.NormalizedEnd}},
		})
	}
	return BookExtraction{NormalizedText: document.NormalizedText, Nodes: document.Nodes, Passages: passages, ParserVersion: e.Adapter.ParserVersion, NormalizationVersion: e.Adapter.NormalizationVersion}, nil
}

type EPUBBookExtractor struct{ Adapter EPUBAdapter }

func (e EPUBBookExtractor) Format() library.SourceFormat { return library.FormatEPUB }

func (e EPUBBookExtractor) Extract(_ context.Context, editionID, assetID string, source []byte) (BookExtraction, error) {
	extraction, err := e.Adapter.Extract(editionID, assetID, source)
	if err != nil {
		return BookExtraction{}, err
	}
	return BookExtraction{NormalizedText: extraction.Document.NormalizedText, Nodes: extraction.Document.Nodes, Passages: extraction.Passages, ParserVersion: e.Adapter.ParserVersion, NormalizationVersion: e.Adapter.NormalizationVersion}, nil
}

type PDFBookExtractor struct{ Adapter PDFAdapter }

func (e PDFBookExtractor) Format() library.SourceFormat { return library.FormatPDF }

func (e PDFBookExtractor) Extract(ctx context.Context, editionID, assetID string, source []byte) (BookExtraction, error) {
	document, err := e.Adapter.Extract(ctx, editionID, assetID, source)
	if err != nil {
		return BookExtraction{}, err
	}
	pageText := make(map[int][]string, len(document.Pages))
	for _, passage := range document.Passages {
		if passage.Locator.PDF != nil {
			pageText[passage.Locator.PDF.Page] = append(pageText[passage.Locator.PDF.Page], passage.Text)
		}
	}
	normalizedPages := make([]string, 0, len(document.Pages))
	nodes := make([]library.StructuralNode, 0, len(document.Pages))
	offset := 0
	for ordinal, page := range document.Pages {
		text := strings.Join(pageText[page.Number], "\n")
		title := "Page " + strconv.Itoa(page.Number)
		start, end := 0, 0
		if text != "" {
			start = offset
			end = start + len(text)
			offset = end + 2
			normalizedPages = append(normalizedPages, text)
		} else if page.RequiresOCR {
			title += " (requires OCR)"
		}
		nodes = append(nodes, library.StructuralNode{ID: "page:" + stableImportID("node", editionID, strconv.Itoa(page.Number)), EditionID: editionID, Kind: library.NodeSection, Ordinal: ordinal, Title: title, StartOffset: start, EndOffset: end, Explicit: true})
	}
	return BookExtraction{NormalizedText: strings.Join(normalizedPages, "\n\n"), Nodes: nodes, Passages: document.Passages, ParserVersion: e.Adapter.ParserVersion, NormalizationVersion: e.Adapter.NormalizationVersion}, nil
}
