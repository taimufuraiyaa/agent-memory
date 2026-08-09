package control

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingAccountRepository struct {
	command ProvisionCommand
	account PersonalAccount
	err     error
	calls   int
}

func (r *recordingAccountRepository) ProvisionPersonalAccount(_ context.Context, command ProvisionCommand) (PersonalAccount, error) {
	r.calls++
	r.command = command
	return r.account, r.err
}

func TestSignupProvisionsVerifiedPersonalTenant(t *testing.T) {
	repository := &recordingAccountRepository{account: PersonalAccount{AccountID: "account-1", TenantID: "tenant-1", Role: "owner"}}
	service := NewSignupService(repository, func() time.Time { return time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC) })

	account, err := service.Signup(context.Background(), VerifiedIdentity{
		ExternalSubject: "provider|subject-1",
		Email:           "  MEMBER@Example.COM ",
		EmailVerified:   true,
		DisplayName:     " Member ",
	})
	if err != nil {
		t.Fatalf("Signup() error = %v", err)
	}
	if account.TenantID != "tenant-1" || repository.calls != 1 {
		t.Fatalf("unexpected provision result: account=%+v calls=%d", account, repository.calls)
	}
	if repository.command.VerifiedEmail != "member@example.com" || repository.command.DisplayName != "Member" {
		t.Fatalf("identity was not normalized: %+v", repository.command)
	}
	if repository.command.AccountID == "" || repository.command.TenantID == "" || repository.command.RequestID == "" {
		t.Fatalf("server IDs were not generated: %+v", repository.command)
	}
}

func TestSignupRejectsUnverifiedEmailBeforePersistence(t *testing.T) {
	repository := &recordingAccountRepository{}
	service := NewSignupService(repository, time.Now)

	_, err := service.Signup(context.Background(), VerifiedIdentity{ExternalSubject: "subject", Email: "member@example.com"})
	if !errors.Is(err, ErrVerifiedIdentityRequired) {
		t.Fatalf("Signup() error = %v, want ErrVerifiedIdentityRequired", err)
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d, want 0", repository.calls)
	}
}

func TestSignupRequiresRepository(t *testing.T) {
	service := NewSignupService(nil, time.Now)
	_, err := service.Signup(context.Background(), VerifiedIdentity{ExternalSubject: "subject", Email: "member@example.com", EmailVerified: true})
	if err == nil {
		t.Fatal("Signup() error = nil, want configuration error")
	}
}
