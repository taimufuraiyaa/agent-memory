package engine

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
)

type BookMemoryWriter interface {
	Write(context.Context, WriteInput) (*WriteResult, error)
}

type BookMemoryRepository interface {
	PutBookMemoryProposal(context.Context, core.BookMemoryProposal) error
	GetBookMemoryProposal(context.Context, string) (core.BookMemoryProposal, error)
	UpdateBookMemoryProposal(context.Context, core.BookMemoryProposal) error
	PutBookMemoryLineage(context.Context, core.BookMemoryLineage) error
}

type BookMemoryProposalInput struct {
	ID          string
	Workspace   string
	RequestedBy core.Principal
	MemoryType  core.MemoryType
	Statement   readingroom.AnswerStatement
	CreatedAt   time.Time
}

type BookMemoryService struct {
	writer     BookMemoryWriter
	repository BookMemoryRepository
}

func NewBookMemoryService(writer BookMemoryWriter, repository BookMemoryRepository) *BookMemoryService {
	return &BookMemoryService{writer: writer, repository: repository}
}

func (s *BookMemoryService) Propose(ctx context.Context, input BookMemoryProposalInput) (core.BookMemoryProposal, error) {
	if s == nil || s.writer == nil || s.repository == nil {
		return core.BookMemoryProposal{}, errors.New("book memory service dependencies are required")
	}
	if err := input.Statement.Validate(); err != nil {
		return core.BookMemoryProposal{}, err
	}
	proposal := core.BookMemoryProposal{
		ID: input.ID, Workspace: input.Workspace, RequestedBy: input.RequestedBy, MemoryType: input.MemoryType,
		Content: input.Statement.Text, Provenance: input.Statement.Provenance,
		Citations: input.Statement.Citations, Verifications: input.Statement.Verifications,
		Confidence: input.Statement.Confidence, Status: core.ProposalSuggested, CreatedAt: input.CreatedAt,
	}
	if err := proposal.Validate(); err != nil {
		return core.BookMemoryProposal{}, err
	}
	if err := s.repository.PutBookMemoryProposal(ctx, proposal); err != nil {
		return core.BookMemoryProposal{}, err
	}
	return proposal, nil
}

func (s *BookMemoryService) Accept(ctx context.Context, id string, reviewer core.Principal) (core.BookMemoryProposal, error) {
	proposal, err := s.repository.GetBookMemoryProposal(ctx, id)
	if err != nil {
		return core.BookMemoryProposal{}, err
	}
	if proposal.Status != core.ProposalSuggested || reviewer != proposal.RequestedBy {
		return core.BookMemoryProposal{}, errors.New("proposal is not reviewable by principal")
	}
	result, err := s.writer.Write(ctx, WriteInput{
		Workspace: proposal.Workspace, Type: proposal.MemoryType, Content: proposal.Content,
		Source: core.MemorySource{Type: core.SourceReflection}, Mode: ExtractFast,
	})
	if err != nil || result.Rejected {
		if err != nil {
			return core.BookMemoryProposal{}, err
		}
		return core.BookMemoryProposal{}, errors.New(result.RejectReason)
	}
	now := time.Now().UTC()
	proposal.Status, proposal.MemoryID, proposal.ReviewedBy, proposal.ReviewedAt = core.ProposalAccepted, result.ID, &reviewer, &now
	if err := s.repository.UpdateBookMemoryProposal(ctx, proposal); err != nil {
		return core.BookMemoryProposal{}, err
	}
	lineage := core.BookMemoryLineage{
		MemoryID: result.ID, ProposalID: proposal.ID, Provenance: proposal.Provenance,
		CitationIDs: proposal.Provenance.CitationIDs, CreatedAt: now,
	}
	if err := s.repository.PutBookMemoryLineage(ctx, lineage); err != nil {
		return core.BookMemoryProposal{}, err
	}
	return proposal, nil
}

func (s *BookMemoryService) Reject(ctx context.Context, id string, reviewer core.Principal) (core.BookMemoryProposal, error) {
	proposal, err := s.repository.GetBookMemoryProposal(ctx, id)
	if err != nil {
		return core.BookMemoryProposal{}, err
	}
	if proposal.Status != core.ProposalSuggested || reviewer != proposal.RequestedBy {
		return core.BookMemoryProposal{}, errors.New("proposal is not reviewable by principal")
	}
	now := time.Now().UTC()
	proposal.Status, proposal.ReviewedBy, proposal.ReviewedAt = core.ProposalRejected, &reviewer, &now
	if err := s.repository.UpdateBookMemoryProposal(ctx, proposal); err != nil {
		return core.BookMemoryProposal{}, err
	}
	return proposal, nil
}
