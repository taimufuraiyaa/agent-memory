package readingroom

import (
	"errors"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type EvidenceState string

const (
	EvidenceSupported      EvidenceState = "supported"
	EvidenceInterpretation EvidenceState = "interpretation"
	EvidenceUnresolved     EvidenceState = "unresolved"
	EvidenceContradicted   EvidenceState = "contradicted"
)

type AnswerStatement struct {
	ID            string                      `json:"id"`
	Text          string                      `json:"text"`
	EvidenceState EvidenceState               `json:"evidence_state"`
	Provenance    core.KnowledgeProvenance    `json:"provenance"`
	Citations     []core.Citation             `json:"citations,omitempty"`
	Verifications []core.EvidenceVerification `json:"verifications,omitempty"`
	Confidence    float64                     `json:"confidence"`
}

func (s AnswerStatement) Validate() error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Text) == "" {
		return errors.New("answer statement requires id and text")
	}
	if err := s.Provenance.Validate(); err != nil {
		return err
	}
	if s.Confidence < 0 || s.Confidence > 1 {
		return errors.New("answer statement confidence must be between 0 and 1")
	}
	switch s.EvidenceState {
	case EvidenceSupported, EvidenceInterpretation, EvidenceUnresolved, EvidenceContradicted:
	default:
		return errors.New("invalid answer evidence state")
	}
	for _, citation := range s.Citations {
		if err := citation.Validate(); err != nil {
			return err
		}
	}
	for _, verification := range s.Verifications {
		if err := verification.Validate(); err != nil {
			return err
		}
	}
	if !citationsResolve(s.Provenance.CitationIDs, s.Citations) || !verificationsResolve(s.ID, s.Citations, s.Verifications) {
		return errors.New("answer evidence references do not resolve")
	}
	if s.Provenance.Attribution.Kind == core.AttributionAuthor && s.Provenance.Form == core.KnowledgeClaim {
		if s.EvidenceState != EvidenceSupported || !hasSupportingVerification(s.Text, s.Citations, s.Verifications, core.VerificationEntailment) {
			return errors.New("author claim requires supporting entailment verification")
		}
	}
	if s.Provenance.Form == core.KnowledgeQuote && !hasVerifiedExactQuote(s.Text, s.Citations, s.Verifications) {
		return errors.New("quote requires exact source verification")
	}
	return nil
}

type GroundedAnswer struct {
	Question   string            `json:"question"`
	Statements []AnswerStatement `json:"statements"`
}

func (a GroundedAnswer) Validate() error {
	if strings.TrimSpace(a.Question) == "" || len(a.Statements) == 0 {
		return errors.New("grounded answer requires question and statements")
	}
	seen := map[string]bool{}
	for _, statement := range a.Statements {
		if seen[statement.ID] {
			return fmt.Errorf("duplicate answer statement id %s", statement.ID)
		}
		seen[statement.ID] = true
		if err := statement.Validate(); err != nil {
			return fmt.Errorf("invalid answer statement %s: %w", statement.ID, err)
		}
	}
	return nil
}
