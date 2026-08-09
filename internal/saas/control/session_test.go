package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type recordingSessionRepository struct {
	started Session
	valid   Session
	err     error
}

func (r *recordingSessionRepository) StartSession(_ context.Context, session Session) (Session, error) {
	r.started = session
	return session, r.err
}
func (r *recordingSessionRepository) ValidateSession(context.Context, string, string, time.Time) (Session, error) {
	return r.valid, r.err
}
func (r *recordingSessionRepository) RevokeSession(context.Context, string, string, string, time.Time) error {
	return r.err
}
func (r *recordingSessionRepository) ChangeVerifiedEmail(context.Context, string, string, string, string, time.Time) error {
	return r.err
}
func (r *recordingSessionRepository) RecoverAccount(context.Context, Session) (Session, error) {
	return r.started, r.err
}

func TestSessionServiceStartsBoundedLogin(t *testing.T) {
	now := time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC)
	repository := &recordingSessionRepository{}
	service := NewSessionService(repository, func() time.Time { return now })

	session, err := service.Login(context.Background(), PersonalAccount{AccountID: "account-1", TenantID: "tenant-1"}, "provider-session", now.Add(time.Hour), "request-1")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.ID == "" || session.AccountID != "account-1" || session.TenantID != "tenant-1" || session.ProviderSessionID != "provider-session" {
		t.Fatalf("invalid session: %+v", session)
	}
}

func TestSessionServiceRejectsExpiredLogin(t *testing.T) {
	now := time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC)
	service := NewSessionService(&recordingSessionRepository{}, func() time.Time { return now })
	_, err := service.Login(context.Background(), PersonalAccount{AccountID: "account-1", TenantID: "tenant-1"}, "provider-session", now, "request")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Login() error = %v, want ErrSessionExpired", err)
	}
}

func TestSessionServiceRequiresVerifiedEmailChange(t *testing.T) {
	repository := &recordingSessionRepository{}
	service := NewSessionService(repository, time.Now)
	err := service.ChangeEmail(context.Background(), auth.RequestContext{AccountID: "account-1", TenantID: "tenant-1"}, "new@example.test", false)
	if !errors.Is(err, ErrVerifiedIdentityRequired) {
		t.Fatalf("ChangeEmail() error = %v, want ErrVerifiedIdentityRequired", err)
	}
}
