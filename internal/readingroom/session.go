package readingroom

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SessionRetention string

const (
	SessionRetentionRaw     SessionRetention = "raw"
	SessionRetentionSummary SessionRetention = "summary_only"
	SessionRetentionDeleted SessionRetention = "deleted"
)

type StudyScope struct {
	LibraryID         string   `json:"library_id"`
	EditionIDs        []string `json:"edition_ids,omitempty"`
	StructuralNodeIDs []string `json:"structural_node_ids,omitempty"`
}

type StudySession struct {
	ID        string            `json:"id"`
	Workspace string            `json:"workspace"`
	Owner     core.Principal    `json:"owner"`
	Scope     StudyScope        `json:"scope"`
	Policy    core.AccessPolicy `json:"policy"`
	Retention SessionRetention  `json:"retention"`
	CreatedAt time.Time         `json:"created_at"`
	EndedAt   *time.Time        `json:"ended_at,omitempty"`
}

func (s StudySession) Validate() error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Workspace) == "" || strings.TrimSpace(s.Scope.LibraryID) == "" || s.CreatedAt.IsZero() {
		return errors.New("study session identity, workspace, library scope, and creation time are required")
	}
	if err := s.Owner.Validate(); err != nil {
		return err
	}
	if err := s.Policy.Validate(); err != nil {
		return err
	}
	switch s.Retention {
	case SessionRetentionRaw, SessionRetentionSummary, SessionRetentionDeleted:
	default:
		return errors.New("invalid study session retention")
	}
	return nil
}

type StudyTurn struct {
	ID                        string         `json:"id"`
	SessionID                 string         `json:"session_id"`
	Principal                 core.Principal `json:"principal"`
	Content                   string         `json:"content"`
	EvidencePacketFingerprint string         `json:"evidence_packet_fingerprint,omitempty"`
	CreatedAt                 time.Time      `json:"created_at"`
}

func (t StudyTurn) Validate() error {
	if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.SessionID) == "" || strings.TrimSpace(t.Content) == "" || t.CreatedAt.IsZero() {
		return errors.New("study turn identity, session, content, and creation time are required")
	}
	return t.Principal.Validate()
}

type StudySessionRepository interface {
	PutStudySession(context.Context, StudySession) error
	PutStudyTurn(context.Context, StudyTurn) error
	GetStudySession(context.Context, string) (StudySession, error)
	ListStudyTurns(context.Context, string) ([]StudyTurn, error)
}

type StudySessionService struct{ repository StudySessionRepository }

func NewStudySessionService(repository StudySessionRepository) *StudySessionService {
	return &StudySessionService{repository: repository}
}
func (s *StudySessionService) Start(ctx context.Context, session StudySession) error {
	if s == nil || s.repository == nil {
		return errors.New("study session repository is required")
	}
	if err := session.Validate(); err != nil {
		return err
	}
	return s.repository.PutStudySession(ctx, session)
}
func (s *StudySessionService) AddTurn(ctx context.Context, scope core.AuthorizationScope, turn StudyTurn) error {
	session, err := s.Get(ctx, scope, turn.SessionID)
	if err != nil {
		return err
	}
	if session.Retention != SessionRetentionRaw {
		return errors.New("session does not retain raw turns")
	}
	return s.repository.PutStudyTurn(ctx, turn)
}
func (s *StudySessionService) Get(ctx context.Context, scope core.AuthorizationScope, id string) (StudySession, error) {
	if s == nil || s.repository == nil {
		return StudySession{}, errors.New("study session repository is required")
	}
	session, err := s.repository.GetStudySession(ctx, id)
	if err != nil || !core.Authorize(scope, session.Policy, core.CapabilityDiscuss).Allowed {
		return StudySession{}, errors.New("study session not found")
	}
	return session, nil
}
func (s *StudySessionService) Turns(ctx context.Context, scope core.AuthorizationScope, id string) ([]StudyTurn, error) {
	if _, err := s.Get(ctx, scope, id); err != nil {
		return nil, err
	}
	return s.repository.ListStudyTurns(ctx, id)
}
