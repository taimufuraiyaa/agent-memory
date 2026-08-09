// Package control owns hosted account, tenant, membership, and session state.
package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrVerifiedIdentityRequired = errors.New("a verified identity is required")

type VerifiedIdentity struct {
	ExternalSubject string
	Email           string
	EmailVerified   bool
	DisplayName     string
}

type SignupContext struct {
	InvitationToken string
	Country         string
	CountryVerified bool
	AgeConfirmed    bool
	NetworkAddress  string
}

type SignupAdmission interface {
	Reserve(context.Context, VerifiedIdentity, SignupContext) (string, error)
	Commit(context.Context, string, PersonalAccount) error
	Cancel(context.Context, string) error
}

type ProvisionCommand struct {
	AccountID       string
	TenantID        string
	WorkspaceID     string
	ExternalSubject string
	VerifiedEmail   string
	DisplayName     string
	RequestID       string
	OccurredAt      time.Time
}

type PersonalAccount struct {
	AccountID   string    `json:"account_id"`
	TenantID    string    `json:"tenant_id"`
	WorkspaceID string    `json:"workspace_id"`
	Role        string    `json:"role"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
}

type AccountRepository interface {
	ProvisionPersonalAccount(context.Context, ProvisionCommand) (PersonalAccount, error)
}

type SignupService struct {
	repository AccountRepository
	admission  SignupAdmission
	now        func() time.Time
}

func NewSignupService(repository AccountRepository, now func() time.Time) *SignupService {
	if now == nil {
		now = time.Now
	}
	return &SignupService{repository: repository, now: now}
}

func NewSignupServiceWithAdmission(repository AccountRepository, admission SignupAdmission, now func() time.Time) *SignupService {
	service := NewSignupService(repository, now)
	service.admission = admission
	return service
}

func (s *SignupService) Signup(ctx context.Context, identity VerifiedIdentity) (PersonalAccount, error) {
	return s.SignupWithContext(ctx, identity, SignupContext{})
}

func (s *SignupService) SignupWithContext(ctx context.Context, identity VerifiedIdentity, signupContext SignupContext) (PersonalAccount, error) {
	if s == nil || s.repository == nil {
		return PersonalAccount{}, errors.New("account repository is not configured")
	}
	externalSubject := strings.TrimSpace(identity.ExternalSubject)
	email := strings.ToLower(strings.TrimSpace(identity.Email))
	if !identity.EmailVerified || externalSubject == "" || email == "" || !strings.Contains(email, "@") {
		return PersonalAccount{}, ErrVerifiedIdentityRequired
	}
	reservation := ""
	var err error
	if s.admission != nil {
		reservation, err = s.admission.Reserve(ctx, identity, signupContext)
		if err != nil {
			return PersonalAccount{}, err
		}
	}
	account, err := s.repository.ProvisionPersonalAccount(ctx, ProvisionCommand{
		AccountID:       uuid.NewString(),
		TenantID:        uuid.NewString(),
		WorkspaceID:     uuid.NewString(),
		ExternalSubject: externalSubject,
		VerifiedEmail:   email,
		DisplayName:     strings.TrimSpace(identity.DisplayName),
		RequestID:       uuid.NewString(),
		OccurredAt:      s.now().UTC(),
	})
	if err != nil {
		if reservation != "" {
			_ = s.admission.Cancel(ctx, reservation)
		}
		return PersonalAccount{}, fmt.Errorf("provision personal account: %w", err)
	}
	if reservation != "" {
		if err := s.admission.Commit(ctx, reservation, account); err != nil {
			return PersonalAccount{}, fmt.Errorf("commit signup admission: %w", err)
		}
	}
	return account, nil
}
