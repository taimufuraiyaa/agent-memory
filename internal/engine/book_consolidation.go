package engine

import (
	"context"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"strings"
	"time"
)

type BookReconsolidation = core.BookReconsolidation
type BookReconsolidationRepository interface {
	GetBookMemoryLineage(context.Context, string) (core.BookMemoryLineage, error)
	PutBookMemoryLineage(context.Context, core.BookMemoryLineage) error
	PutBookReconsolidation(context.Context, BookReconsolidation) error
}
type BookReconsolidator struct {
	writer     BookMemoryWriter
	repository BookReconsolidationRepository
}

func NewBookReconsolidator(w BookMemoryWriter, r BookReconsolidationRepository) *BookReconsolidator {
	return &BookReconsolidator{writer: w, repository: r}
}

type BookReconsolidationInput struct {
	ID, Workspace, PreviousMemoryID, Content string
	MemoryType                               core.MemoryType
	Action                                   core.ReconsolidationAction
	AdditionalCitationIDs                    []string
	CreatedAt                                time.Time
}

func (s *BookReconsolidator) Run(ctx context.Context, input BookReconsolidationInput) (BookReconsolidation, error) {
	if s == nil || s.writer == nil || s.repository == nil || strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Content) == "" || input.CreatedAt.IsZero() {
		return BookReconsolidation{}, errors.New("reconsolidation dependencies, identity, content, and time are required")
	}
	switch input.Action {
	case core.ReconsolidateClarified, core.ReconsolidateContradicted, core.ReconsolidateSuperseded:
	default:
		return BookReconsolidation{}, errors.New("book reconsolidation must clarify, contradict, or supersede")
	}
	prior, err := s.repository.GetBookMemoryLineage(ctx, input.PreviousMemoryID)
	if err != nil {
		return BookReconsolidation{}, err
	}
	written, err := s.writer.Write(ctx, WriteInput{Workspace: input.Workspace, Type: input.MemoryType, Content: input.Content, Source: core.MemorySource{Type: core.SourceReflection}, Mode: ExtractFast})
	if err != nil {
		return BookReconsolidation{}, err
	}
	if written.Rejected {
		return BookReconsolidation{}, errors.New(written.RejectReason)
	}
	citations := uniqueStrings(append(append([]string{}, prior.CitationIDs...), input.AdditionalCitationIDs...))
	provenance := prior.Provenance
	provenance.CitationIDs = citations
	provenance.Derivation = core.DerivationConsolidated
	provenance.DerivedFrom = uniqueStrings(append(provenance.DerivedFrom, input.PreviousMemoryID))
	lineage := core.BookMemoryLineage{MemoryID: written.ID, ProposalID: prior.ProposalID, Provenance: provenance, CitationIDs: citations, CreatedAt: input.CreatedAt}
	if err := s.repository.PutBookMemoryLineage(ctx, lineage); err != nil {
		return BookReconsolidation{}, err
	}
	record := BookReconsolidation{ID: input.ID, PreviousMemoryID: input.PreviousMemoryID, NewMemoryID: written.ID, Action: input.Action, CitationIDs: citations, CreatedAt: input.CreatedAt}
	if err := s.repository.PutBookReconsolidation(ctx, record); err != nil {
		return BookReconsolidation{}, err
	}
	return record, nil
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
