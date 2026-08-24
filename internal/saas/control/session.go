package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

var (
	ErrSessionExpired = errors.New("session has expired")
	ErrSessionRevoked = errors.New("session has been revoked")
)

type Session struct {
	ID                string
	TenantID          string
	AccountID         string
	ProviderSessionID string
	ExpiresAt         time.Time
	RevokedAt         *time.Time
	RequestID         string
	OccurredAt        time.Time
}

type SessionRepository interface {
	StartSession(context.Context, Session) (Session, error)
	ValidateSession(context.Context, string, string, time.Time) (Session, error)
	RevokeSession(context.Context, string, string, string, time.Time) error
	ChangeVerifiedEmail(context.Context, string, string, string, string, time.Time) error
	RecoverAccount(context.Context, Session) (Session, error)
}

type SessionService struct {
	repository SessionRepository
	now        func() time.Time
}

func NewSessionService(repository SessionRepository, now func() time.Time) *SessionService {
	if now == nil {
		now = time.Now
	}
	return &SessionService{repository: repository, now: now}
}

func (s *SessionService) Login(ctx context.Context, account PersonalAccount, providerSessionID string, expiresAt time.Time, requestID string) (Session, error) {
	if err := s.configured(); err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	if strings.TrimSpace(account.AccountID) == "" || strings.TrimSpace(account.TenantID) == "" || strings.TrimSpace(providerSessionID) == "" {
		return Session{}, errors.New("account, tenant, and provider session are required")
	}
	if !expiresAt.UTC().After(now) {
		return Session{}, ErrSessionExpired
	}
	return s.repository.StartSession(ctx, Session{
		ID: uuid.NewString(), TenantID: account.TenantID, AccountID: account.AccountID,
		ProviderSessionID: strings.TrimSpace(providerSessionID), ExpiresAt: expiresAt.UTC(),
		RequestID: strings.TrimSpace(requestID), OccurredAt: now,
	})
}

func (s *SessionService) Validate(ctx context.Context, request auth.RequestContext) (Session, error) {
	if err := s.configured(); err != nil {
		return Session{}, err
	}
	if request.TenantID == "" || request.SessionID == "" {
		return Session{}, errors.New("tenant and session context are required")
	}
	return s.repository.ValidateSession(ctx, request.TenantID, request.SessionID, s.now().UTC())
}

func (s *SessionService) Logout(ctx context.Context, request auth.RequestContext) error {
	if err := s.configured(); err != nil {
		return err
	}
	if request.TenantID == "" || request.SessionID == "" || request.RequestID == "" {
		return errors.New("tenant, session, and request context are required")
	}
	return s.repository.RevokeSession(ctx, request.TenantID, request.SessionID, request.RequestID, s.now().UTC())
}

func (s *SessionService) ChangeEmail(ctx context.Context, request auth.RequestContext, email string, verified bool) error {
	if err := s.configured(); err != nil {
		return err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !verified || email == "" || !strings.Contains(email, "@") {
		return ErrVerifiedIdentityRequired
	}
	if request.TenantID == "" || request.AccountID == "" || request.RequestID == "" {
		return errors.New("authenticated account, tenant, and request context are required")
	}
	return s.repository.ChangeVerifiedEmail(ctx, request.TenantID, request.AccountID, email, request.RequestID, s.now().UTC())
}

func (s *SessionService) Recover(ctx context.Context, account PersonalAccount, providerSessionID string, expiresAt time.Time, requestID string) (Session, error) {
	if err := s.configured(); err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	if account.AccountID == "" || account.TenantID == "" || strings.TrimSpace(providerSessionID) == "" || !expiresAt.UTC().After(now) {
		return Session{}, fmt.Errorf("%w: valid account and future provider session are required", ErrSessionExpired)
	}
	return s.repository.RecoverAccount(ctx, Session{
		ID: uuid.NewString(), TenantID: account.TenantID, AccountID: account.AccountID,
		ProviderSessionID: strings.TrimSpace(providerSessionID), ExpiresAt: expiresAt.UTC(),
		RequestID: strings.TrimSpace(requestID), OccurredAt: now,
	})
}

func (s *SessionService) configured() error {
	if s == nil || s.repository == nil {
		return errors.New("session repository is not configured")
	}
	return nil
}
