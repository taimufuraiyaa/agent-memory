package control

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

var ErrInvalidLocalOwnerSignup = errors.New("local owner signup is invalid")

type LocalOwnerSignup struct {
	DisplayName                  string `json:"display_name"`
	Email                        string `json:"email"`
	PrivateInstallationConfirmed bool   `json:"private_installation_confirmed"`
}

type LocalOwnerStatus struct {
	State   string          `json:"state"`
	Account PersonalAccount `json:"account,omitempty"`
}

type LocalOwnerStore interface {
	AccountRepository
	FindPersonalAccount(context.Context, string) (PersonalAccount, error)
}

type LocalOwnerInitializer interface {
	InitializeLocalOwner(context.Context, PersonalAccount) error
}

type LocalOwnerService struct {
	store       LocalOwnerStore
	signup      *SignupService
	initializer LocalOwnerInitializer
	subject     string
}

func NewLocalOwnerService(store LocalOwnerStore, subject string, now func() time.Time) (*LocalOwnerService, error) {
	return NewLocalOwnerServiceWithInitializer(store, subject, nil, now)
}

func NewLocalOwnerServiceWithInitializer(store LocalOwnerStore, subject string, initializer LocalOwnerInitializer, now func() time.Time) (*LocalOwnerService, error) {
	subject = strings.TrimSpace(subject)
	if store == nil || subject == "" {
		return nil, errors.New("local owner service is incomplete")
	}
	return &LocalOwnerService{store: store, signup: NewSignupService(store, now), initializer: initializer, subject: subject}, nil
}

func (s *LocalOwnerService) Status(ctx context.Context) (LocalOwnerStatus, error) {
	account, err := s.store.FindPersonalAccount(ctx, s.subject)
	if errors.Is(err, auth.ErrTenantUnavailable) {
		return LocalOwnerStatus{State: "signup_required"}, nil
	}
	if err != nil {
		return LocalOwnerStatus{}, err
	}
	if err := s.initialize(ctx, account); err != nil {
		return LocalOwnerStatus{}, err
	}
	return LocalOwnerStatus{State: "authenticated", Account: account}, nil
}

func (s *LocalOwnerService) Signup(ctx context.Context, input LocalOwnerSignup) (PersonalAccount, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	parsed, err := mail.ParseAddress(input.Email)
	if !input.PrivateInstallationConfirmed || input.DisplayName == "" || len(input.DisplayName) > 120 || err != nil || parsed.Address != input.Email || len(input.Email) > 254 {
		return PersonalAccount{}, ErrInvalidLocalOwnerSignup
	}
	account, err := s.signup.Signup(ctx, VerifiedIdentity{ExternalSubject: s.subject, Email: input.Email, EmailVerified: true, DisplayName: input.DisplayName})
	if err != nil {
		return PersonalAccount{}, err
	}
	if err := s.initialize(ctx, account); err != nil {
		return PersonalAccount{}, err
	}
	return account, nil
}

func (s *LocalOwnerService) initialize(ctx context.Context, account PersonalAccount) error {
	if s.initializer == nil {
		return nil
	}
	return s.initializer.InitializeLocalOwner(ctx, account)
}
