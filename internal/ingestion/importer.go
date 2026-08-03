package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

type ImportRepository interface {
	PutBookWork(context.Context, library.BookWork) error
	PutBookEdition(context.Context, library.BookEdition) error
	GetBookEdition(context.Context, string) (library.BookEdition, error)
	FindBookEditionByFingerprint(context.Context, string, string) (library.BookEdition, bool, error)
	PutSourceAsset(context.Context, library.SourceAsset) error
	FindSourceAssetByByteFingerprint(context.Context, string) (library.SourceAsset, bool, error)
	ReplaceStructuralNodes(context.Context, string, []library.StructuralNode) error
	ListStructuralNodes(context.Context, string) ([]library.StructuralNode, error)
	PutPassages(context.Context, []library.Passage) error
}

type MarkdownImportInput struct {
	Title        string
	EditionLabel string
	Language     string
	Source       []byte
	Policy       core.SourcePolicy
}

type ImportResult struct {
	WorkID    string `json:"work_id"`
	EditionID string `json:"edition_id"`
	AssetID   string `json:"asset_id"`
	NodeCount int    `json:"node_count"`
	Existing  bool   `json:"existing"`
}

type MarkdownImporter struct {
	repository ImportRepository
	adapter    MarkdownAdapter
}

func NewMarkdownImporter(repository ImportRepository, adapter MarkdownAdapter) *MarkdownImporter {
	return &MarkdownImporter{repository: repository, adapter: adapter}
}

func (i *MarkdownImporter) Import(ctx context.Context, input MarkdownImportInput) (ImportResult, error) {
	if i == nil || i.repository == nil {
		return ImportResult{}, errors.New("import repository is required")
	}
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.EditionLabel) == "" || strings.TrimSpace(input.Language) == "" || len(input.Source) == 0 {
		return ImportResult{}, errors.New("title, edition label, language, and source are required")
	}
	if err := input.Policy.Validate(); err != nil {
		return ImportResult{}, err
	}
	byteFingerprint := core.FingerprintText(string(input.Source))
	if asset, found, err := i.repository.FindSourceAssetByByteFingerprint(ctx, byteFingerprint); err != nil {
		return ImportResult{}, err
	} else if found {
		edition, err := i.repository.GetBookEdition(ctx, asset.EditionID)
		if err != nil {
			return ImportResult{}, err
		}
		nodes, err := i.repository.ListStructuralNodes(ctx, edition.ID)
		if err != nil {
			return ImportResult{}, err
		}
		asset.Policy = input.Policy
		if err := i.repository.PutSourceAsset(ctx, asset); err != nil {
			return ImportResult{}, err
		}
		return ImportResult{WorkID: edition.WorkID, EditionID: edition.ID, AssetID: asset.ID, NodeCount: len(nodes), Existing: true}, nil
	}

	normalizedTitle := strings.ToLower(strings.Join(strings.Fields(input.Title), " "))
	workID := stableImportID("work", normalizedTitle)
	provisional, err := i.adapter.Extract(stableImportID("provisional", byteFingerprint), input.Source)
	if err != nil {
		return ImportResult{}, err
	}
	contentFingerprint := core.FingerprintText(provisional.NormalizedText)
	editionID := stableImportID("edition", workID, contentFingerprint)
	document, err := i.adapter.Extract(editionID, input.Source)
	if err != nil {
		return ImportResult{}, err
	}
	assetID := stableImportID("asset", byteFingerprint)

	work := library.BookWork{ID: workID, Title: strings.TrimSpace(input.Title), NormalizedTitle: normalizedTitle}
	if err := i.repository.PutBookWork(ctx, work); err != nil {
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
	asset := library.SourceAsset{
		ID: assetID, EditionID: edition.ID, Format: library.FormatMarkdown,
		ByteFingerprint: byteFingerprint, NormalizedFingerprint: contentFingerprint,
		ParserVersion: i.adapter.ParserVersion, Policy: input.Policy, ImportedAt: time.Now().UTC(),
	}
	if err := i.repository.PutSourceAsset(ctx, asset); err != nil {
		return ImportResult{}, err
	}
	if err := i.repository.ReplaceStructuralNodes(ctx, edition.ID, document.Nodes); err != nil {
		return ImportResult{}, err
	}
	nodeByID := make(map[string]library.StructuralNode, len(document.Nodes))
	for _, node := range document.Nodes {
		nodeByID[node.ID] = node
	}
	passages := make([]library.Passage, 0, len(document.Sections))
	for _, section := range document.Sections {
		node := nodeByID[section.NodeID]
		passage := library.Passage{
			ID:        stableImportID("passage", edition.ID, assetID, section.NodeID, section.NormalizedText),
			EditionID: edition.ID, SourceAssetID: assetID, StructuralNodeID: section.NodeID,
			Text: section.NormalizedText, Fingerprint: core.FingerprintText(section.NormalizedText),
			Locator: core.SourceLocator{
				Kind: core.LocatorMarkdown, Display: node.Title,
				ParserVersion: i.adapter.ParserVersion, NormalizationVersion: i.adapter.NormalizationVersion,
				Text: &core.TextLocator{
					HeadingPath: []string{node.Title}, SourceStart: section.Span.SourceStart, SourceEnd: section.Span.SourceEnd,
					NormalizedStart: section.Span.NormalizedStart, NormalizedEnd: section.Span.NormalizedEnd,
				},
			},
		}
		passages = append(passages, passage)
	}
	if err := i.repository.PutPassages(ctx, passages); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{WorkID: workID, EditionID: edition.ID, AssetID: assetID, NodeCount: len(document.Nodes)}, nil
}

func stableImportID(prefix string, parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "_" + hex.EncodeToString(digest[:12])
}
