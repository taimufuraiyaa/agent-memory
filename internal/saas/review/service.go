package review

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

var ErrProposalForbidden = errors.New("memory proposal is forbidden")

type EvidenceRef struct {
	SourceID      string `json:"source_id"`
	SourceVersion int64  `json:"source_version"`
	PassageID     string `json:"passage_id"`
	CitationID    string `json:"citation_id"`
}

type Proposal struct {
	ID                    string              `json:"id"`
	WorkspaceID           string              `json:"workspace_id"`
	RequestedBy           string              `json:"requested_by"`
	MemoryType            core.MemoryType     `json:"memory_type"`
	Content               string              `json:"content"`
	Transformation        string              `json:"transformation"`
	TransformationVersion string              `json:"transformation_version"`
	Evidence              []EvidenceRef       `json:"evidence"`
	Status                core.ProposalStatus `json:"status"`
	MemoryID              string              `json:"memory_id,omitempty"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
	ReviewedAt            *time.Time          `json:"reviewed_at,omitempty"`
}

type CreateCommand struct {
	WorkspaceID    string          `json:"workspace_id"`
	MemoryType     core.MemoryType `json:"memory_type"`
	Content        string          `json:"content"`
	Transformation string          `json:"transformation"`
	Evidence       []EvidenceRef   `json:"evidence"`
}

type UpdateCommand struct {
	Content        string `json:"content"`
	Transformation string `json:"transformation"`
}

type Repository interface {
	EvidenceTexts(context.Context, auth.RequestContext, string, []EvidenceRef) ([]string, error)
	Create(context.Context, auth.RequestContext, Proposal) (Proposal, error)
	Get(context.Context, auth.RequestContext, string) (Proposal, error)
	Update(context.Context, auth.RequestContext, string, string, string, time.Time) (Proposal, error)
	Review(context.Context, auth.RequestContext, string, bool, time.Time) (Proposal, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, now: now}
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Proposal, error) {
	request, err := proposalRequest(ctx)
	if err != nil {
		return Proposal{}, err
	}
	command.WorkspaceID = strings.TrimSpace(command.WorkspaceID)
	command.Content = strings.TrimSpace(command.Content)
	command.Transformation = strings.TrimSpace(command.Transformation)
	if command.WorkspaceID == "" || !core.IsMemoryType(command.MemoryType) || command.Content == "" || len(command.Content) > 2000 || !validTransformation(command.Transformation) || len(command.Evidence) == 0 || len(command.Evidence) > 50 {
		return Proposal{}, errors.New("memory proposal is invalid")
	}
	texts, err := s.repository.EvidenceTexts(ctx, request, command.WorkspaceID, command.Evidence)
	if err != nil || len(texts) != len(command.Evidence) {
		return Proposal{}, ErrProposalForbidden
	}
	if rawSourceCopy(command.Content, texts) {
		return Proposal{}, errors.New("raw source text must be transformed before memory review")
	}
	now := s.now().UTC()
	proposal := Proposal{ID: uuid.NewString(), WorkspaceID: command.WorkspaceID, RequestedBy: request.AccountID, MemoryType: command.MemoryType, Content: command.Content, Transformation: command.Transformation, TransformationVersion: "review-v1", Evidence: append([]EvidenceRef(nil), command.Evidence...), Status: core.ProposalSuggested, CreatedAt: now, UpdatedAt: now}
	return s.repository.Create(ctx, request, proposal)
}

func (s *Service) Get(ctx context.Context, id string) (Proposal, error) {
	request, err := proposalRequest(ctx)
	if err != nil {
		return Proposal{}, err
	}
	return s.repository.Get(ctx, request, strings.TrimSpace(id))
}

func (s *Service) Update(ctx context.Context, id string, command UpdateCommand) (Proposal, error) {
	request, err := proposalRequest(ctx)
	if err != nil {
		return Proposal{}, err
	}
	command.Content = strings.TrimSpace(command.Content)
	command.Transformation = strings.TrimSpace(command.Transformation)
	if command.Content == "" || len(command.Content) > 2000 || !validTransformation(command.Transformation) {
		return Proposal{}, errors.New("memory proposal update is invalid")
	}
	proposal, err := s.repository.Get(ctx, request, id)
	if err != nil || proposal.Status != core.ProposalSuggested {
		return Proposal{}, ErrProposalForbidden
	}
	texts, err := s.repository.EvidenceTexts(ctx, request, proposal.WorkspaceID, proposal.Evidence)
	if err != nil || rawSourceCopy(command.Content, texts) {
		return Proposal{}, errors.New("raw source text must be transformed before memory review")
	}
	return s.repository.Update(ctx, request, id, command.Content, command.Transformation, s.now().UTC())
}

func (s *Service) Accept(ctx context.Context, id string) (Proposal, error) {
	request, err := proposalRequest(ctx)
	if err != nil {
		return Proposal{}, err
	}
	return s.repository.Review(ctx, request, strings.TrimSpace(id), true, s.now().UTC())
}

func (s *Service) Reject(ctx context.Context, id string) (Proposal, error) {
	request, err := proposalRequest(ctx)
	if err != nil {
		return Proposal{}, err
	}
	return s.repository.Review(ctx, request, strings.TrimSpace(id), false, s.now().UTC())
}

func proposalRequest(ctx context.Context) (auth.RequestContext, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || request.TenantID == "" || request.AccountID == "" || !request.Can("memory:write") {
		return auth.RequestContext{}, ErrProposalForbidden
	}
	return request, nil
}

func validTransformation(value string) bool {
	switch value {
	case "summary", "interpretation", "synthesis", "user_edit":
		return true
	default:
		return false
	}
}

func rawSourceCopy(content string, evidence []string) bool {
	normalized := normalize(content)
	var combined strings.Builder
	for _, value := range evidence {
		text := normalize(value)
		combined.WriteString(text)
		combined.WriteByte(' ')
		if normalized == text || len(text) >= 80 && strings.Contains(normalized, text) {
			return true
		}
	}
	return normalized == strings.TrimSpace(combined.String())
}

func normalize(value string) string { return strings.Join(strings.Fields(strings.ToLower(value)), " ") }
