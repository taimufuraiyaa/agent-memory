package core

import (
	"errors"
	"strings"
	"time"
)

type ProposalStatus string

const (
	ProposalSuggested ProposalStatus = "suggested"
	ProposalAccepted  ProposalStatus = "accepted"
	ProposalRejected  ProposalStatus = "rejected"
)

type BookMemoryProposal struct {
	ID            string                 `json:"id"`
	Workspace     string                 `json:"workspace"`
	RequestedBy   Principal              `json:"requested_by"`
	MemoryType    MemoryType             `json:"memory_type"`
	Content       string                 `json:"content"`
	Provenance    KnowledgeProvenance    `json:"provenance"`
	Citations     []Citation             `json:"citations,omitempty"`
	Verifications []EvidenceVerification `json:"verifications,omitempty"`
	Confidence    float64                `json:"confidence"`
	Status        ProposalStatus         `json:"status"`
	MemoryID      string                 `json:"memory_id,omitempty"`
	ReviewedBy    *Principal             `json:"reviewed_by,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	ReviewedAt    *time.Time             `json:"reviewed_at,omitempty"`
}

func (p BookMemoryProposal) Validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Workspace) == "" || strings.TrimSpace(p.Content) == "" || p.CreatedAt.IsZero() {
		return errors.New("book memory proposal identity, workspace, content, and creation time are required")
	}
	if err := p.RequestedBy.Validate(); err != nil {
		return err
	}
	if !IsMemoryType(p.MemoryType) {
		return errors.New("invalid proposed memory type")
	}
	if err := p.Provenance.Validate(); err != nil {
		return err
	}
	if p.Confidence < 0 || p.Confidence > 1 || p.Status != ProposalSuggested {
		return errors.New("new proposal requires valid confidence and suggested status")
	}
	return nil
}

type BookMemoryLineage struct {
	MemoryID    string              `json:"memory_id"`
	ProposalID  string              `json:"proposal_id"`
	Provenance  KnowledgeProvenance `json:"provenance"`
	CitationIDs []string            `json:"citation_ids,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
}
