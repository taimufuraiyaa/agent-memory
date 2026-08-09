package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type NoteCreate struct {
	Input          core.CreateNoteInput
	IdempotencyKey string
}
type NoteUpdate struct{ Input core.UpdateNoteInput }
type FeedbackCommand struct {
	MemoryID, RequestID string
	Outcome             core.RetrievalFeedback
	ReasonCategory      string
}
type SessionEndCommand struct{ SessionID, WorkspaceID, Transcript, IdempotencyKey string }

type WorkflowRepository interface {
	CreateNote(context.Context, auth.RequestContext, NoteCreate, time.Time) (*core.Note, bool, error)
	UpdateNote(context.Context, auth.RequestContext, NoteUpdate, time.Time) (*core.Note, bool, error)
	RecordFeedback(context.Context, auth.RequestContext, FeedbackCommand, time.Time) (bool, error)
	EndSession(context.Context, auth.RequestContext, SessionEndCommand, string, time.Time) (bool, error)
}

type WorkflowService struct {
	repository WorkflowRepository
	now        func() time.Time
}

func NewWorkflowService(repository WorkflowRepository, now func() time.Time) *WorkflowService {
	if now == nil {
		now = time.Now
	}
	return &WorkflowService{repository: repository, now: now}
}

func (s *WorkflowService) CreateNote(ctx context.Context, command NoteCreate) (*core.Note, bool, error) {
	request, err := workflowRequest(ctx)
	if err != nil {
		return nil, false, err
	}
	command.Input.Workspace = strings.TrimSpace(command.Input.Workspace)
	command.Input.Path = strings.TrimSpace(command.Input.Path)
	command.Input.Title = core.NormalizeNoteTitle(command.Input.Title, command.Input.Path)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if command.Input.Workspace == "" || command.Input.Path == "" || len(command.IdempotencyKey) < 16 || len(command.Input.Body) > 1<<20 {
		return nil, false, errors.New("invalid note create command")
	}
	return s.repository.CreateNote(ctx, request, command, s.now().UTC())
}
func (s *WorkflowService) UpdateNote(ctx context.Context, command NoteUpdate) (*core.Note, bool, error) {
	request, err := workflowRequest(ctx)
	if err != nil {
		return nil, false, err
	}
	if command.Input.ExpectedRevision < 1 || strings.TrimSpace(command.Input.NoteID) == "" {
		return nil, false, errors.New("invalid note update command")
	}
	return s.repository.UpdateNote(ctx, request, command, s.now().UTC())
}
func (s *WorkflowService) Feedback(ctx context.Context, command FeedbackCommand) (bool, error) {
	request, err := workflowRequest(ctx)
	if err != nil {
		return false, err
	}
	switch command.Outcome {
	case core.FeedbackHelpful, core.FeedbackIgnored, core.FeedbackRejected, core.FeedbackHarmful:
	default:
		return false, errors.New("invalid feedback")
	}
	if strings.TrimSpace(command.MemoryID) == "" || strings.TrimSpace(command.RequestID) == "" {
		return false, errors.New("invalid feedback")
	}
	return s.repository.RecordFeedback(ctx, request, command, s.now().UTC())
}
func (s *WorkflowService) EndSession(ctx context.Context, command SessionEndCommand) (bool, error) {
	request, err := workflowRequest(ctx)
	if err != nil {
		return false, err
	}
	command.Transcript = strings.TrimSpace(command.Transcript)
	if command.WorkspaceID == "" || command.SessionID == "" || len(command.IdempotencyKey) < 16 || command.Transcript == "" || len(command.Transcript) > 1<<20 {
		return false, errors.New("invalid session end")
	}
	sum := sha256.Sum256([]byte(command.Transcript))
	return s.repository.EndSession(ctx, request, command, hex.EncodeToString(sum[:]), s.now().UTC())
}
func workflowRequest(ctx context.Context) (auth.RequestContext, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || request.TenantID == "" || request.AccountID == "" || !request.Can("memory:write") {
		return auth.RequestContext{}, ErrForbidden
	}
	return request, nil
}
