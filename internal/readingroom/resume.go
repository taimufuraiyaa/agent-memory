package readingroom

import (
	"context"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

type SessionResumeRepository interface {
	StudySessionRepository
	GetReadingProgress(context.Context, string, string) (library.ReadingProgress, error)
	ListAcceptedBookMemoryProposals(context.Context, string, string) ([]core.BookMemoryProposal, error)
}

type ResumeItemKind string

const (
	ResumeConversation      ResumeItemKind = "conversation"
	ResumeAcceptedKnowledge ResumeItemKind = "accepted_knowledge"
	ResumeOpenQuestion      ResumeItemKind = "open_question"
)

type ResumeItem struct {
	Kind      ResumeItemKind  `json:"kind"`
	Content   string          `json:"content"`
	Principal *core.Principal `json:"principal,omitempty"`
	Citations []core.Citation `json:"citations,omitempty"`
}
type SessionResumeContext struct {
	Session         StudySession              `json:"session"`
	Progress        []library.ReadingProgress `json:"progress"`
	Items           []ResumeItem              `json:"items"`
	EstimatedTokens int                       `json:"estimated_tokens"`
}

type SessionResumeAssembler struct{ repository SessionResumeRepository }

func NewSessionResumeAssembler(repository SessionResumeRepository) *SessionResumeAssembler {
	return &SessionResumeAssembler{repository: repository}
}
func (a *SessionResumeAssembler) Build(ctx context.Context, scope core.AuthorizationScope, sessionID string, tokenBudget int) (SessionResumeContext, error) {
	if a == nil || a.repository == nil {
		return SessionResumeContext{}, errors.New("resume repository is required")
	}
	if tokenBudget <= 0 {
		return SessionResumeContext{}, errors.New("positive token budget is required")
	}
	service := NewStudySessionService(a.repository)
	session, err := service.Get(ctx, scope, sessionID)
	if err != nil {
		return SessionResumeContext{}, err
	}
	turns, err := service.Turns(ctx, scope, sessionID)
	if err != nil {
		return SessionResumeContext{}, err
	}
	out := SessionResumeContext{Session: session, Progress: []library.ReadingProgress{}, Items: []ResumeItem{}}
	for _, editionID := range session.Scope.EditionIDs {
		if progress, e := a.repository.GetReadingProgress(ctx, scope.Principal.ID, editionID); e == nil {
			out.Progress = append(out.Progress, progress)
		}
	}
	proposals, err := a.repository.ListAcceptedBookMemoryProposals(ctx, session.Workspace, scope.Principal.ID)
	if err != nil {
		return SessionResumeContext{}, err
	}
	candidates := []ResumeItem{}
	for _, proposal := range proposals {
		candidates = append(candidates, ResumeItem{Kind: ResumeAcceptedKnowledge, Content: proposal.Content, Citations: append([]core.Citation(nil), proposal.Citations...)})
	}
	for index := len(turns) - 1; index >= 0; index-- {
		turn := turns[index]
		kind := ResumeConversation
		if strings.HasSuffix(strings.TrimSpace(turn.Content), "?") {
			kind = ResumeOpenQuestion
		}
		principal := turn.Principal
		candidates = append(candidates, ResumeItem{Kind: kind, Content: turn.Content, Principal: &principal})
	}
	used := 0
	for _, item := range candidates {
		cost := (len([]rune(item.Content))+3)/4 + 8
		if used+cost > tokenBudget {
			continue
		}
		out.Items = append(out.Items, item)
		used += cost
	}
	out.EstimatedTokens = used
	return out, nil
}
