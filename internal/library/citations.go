package library

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type Passage struct {
	ID               string             `json:"id"`
	EditionID        string             `json:"edition_id"`
	SourceAssetID    string             `json:"source_asset_id"`
	StructuralNodeID string             `json:"structural_node_id"`
	Text             string             `json:"text"`
	Locator          core.SourceLocator `json:"locator"`
	Fingerprint      string             `json:"fingerprint"`
}

func (p Passage) Validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.EditionID) == "" || strings.TrimSpace(p.SourceAssetID) == "" ||
		strings.TrimSpace(p.StructuralNodeID) == "" || strings.TrimSpace(p.Text) == "" || strings.TrimSpace(p.Fingerprint) == "" {
		return errors.New("passage identity, source, text, and fingerprint are required")
	}
	return p.Locator.Validate()
}

type CitationRepository interface {
	GetPassage(context.Context, string) (Passage, error)
	GetSourceAsset(context.Context, string) (SourceAsset, error)
	PutCitation(context.Context, core.Citation) error
	GetCitation(context.Context, string) (core.Citation, error)
}

type CitationService struct {
	repository CitationRepository
}

type ResolvedCitation struct {
	Citation core.Citation `json:"citation"`
	Passage  Passage       `json:"passage"`
}

func NewCitationService(repository CitationRepository) *CitationService {
	return &CitationService{repository: repository}
}

func (s *CitationService) CitePassage(ctx context.Context, passageID, shortQuote string) (core.Citation, error) {
	if s == nil || s.repository == nil {
		return core.Citation{}, errors.New("citation repository is required")
	}
	passage, err := s.repository.GetPassage(ctx, passageID)
	if err != nil {
		return core.Citation{}, err
	}
	if err := passage.Validate(); err != nil {
		return core.Citation{}, err
	}
	asset, err := s.repository.GetSourceAsset(ctx, passage.SourceAssetID)
	if err != nil {
		return core.Citation{}, err
	}
	shortQuote = strings.TrimSpace(shortQuote)
	if shortQuote != "" && (!strings.Contains(passage.Text, shortQuote) || !asset.Policy.CanQuote(shortQuote)) {
		return core.Citation{}, errors.New("quote is not an exact permitted passage substring")
	}
	citation := core.Citation{
		ID:        "citation_" + strings.TrimPrefix(core.FingerprintText(passage.ID+"\x00"+shortQuote), "sha256:")[:24],
		EditionID: passage.EditionID, SourceAssetID: passage.SourceAssetID, PassageID: passage.ID,
		StructuralNodeID: passage.StructuralNodeID, Locator: passage.Locator,
		PassageFingerprint: passage.Fingerprint, ShortQuote: shortQuote,
	}
	if err := citation.Validate(); err != nil {
		return core.Citation{}, err
	}
	if err := s.repository.PutCitation(ctx, citation); err != nil {
		return core.Citation{}, err
	}
	return citation, nil
}

func (s *CitationService) Resolve(ctx context.Context, citationID string) (ResolvedCitation, error) {
	if s == nil || s.repository == nil {
		return ResolvedCitation{}, errors.New("citation repository is required")
	}
	citation, err := s.repository.GetCitation(ctx, citationID)
	if err != nil {
		return ResolvedCitation{}, err
	}
	passage, err := s.repository.GetPassage(ctx, citation.PassageID)
	if err != nil {
		return ResolvedCitation{}, err
	}
	if passage.Fingerprint != citation.PassageFingerprint {
		return ResolvedCitation{}, fmt.Errorf("citation %s is stale: passage fingerprint changed", citation.ID)
	}
	return ResolvedCitation{Citation: citation, Passage: passage}, nil
}
