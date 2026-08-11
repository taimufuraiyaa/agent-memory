package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type fakeLocalOwnerStore struct {
	account PersonalAccount
}

type fakeLocalOwnerInitializer struct {
	accounts []PersonalAccount
	err      error
}

func (f *fakeLocalOwnerInitializer) InitializeLocalOwner(_ context.Context, account PersonalAccount) error {
	f.accounts = append(f.accounts, account)
	return f.err
}

func (s *fakeLocalOwnerStore) ProvisionPersonalAccount(_ context.Context, command ProvisionCommand) (PersonalAccount, error) {
	if s.account.AccountID == "" {
		s.account = PersonalAccount{AccountID: command.AccountID, TenantID: command.TenantID, WorkspaceID: command.WorkspaceID, Role: "owner", State: "active", CreatedAt: command.OccurredAt}
	}
	return s.account, nil
}

func (s *fakeLocalOwnerStore) FindPersonalAccount(_ context.Context, _ string) (PersonalAccount, error) {
	if s.account.AccountID == "" {
		return PersonalAccount{}, auth.ErrTenantUnavailable
	}
	return s.account, nil
}

func TestLocalOwnerServiceReportsSignupThenReturnsIdempotentOwner(t *testing.T) {
	store := &fakeLocalOwnerStore{}
	service, err := NewLocalOwnerService(store, "development|member", func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil || status.State != "signup_required" {
		t.Fatalf("initial status=%+v err=%v", status, err)
	}
	first, err := service.Signup(context.Background(), LocalOwnerSignup{DisplayName: "Private Owner", Email: "OWNER@EXAMPLE.TEST", PrivateInstallationConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Signup(context.Background(), LocalOwnerSignup{DisplayName: "Private Owner", Email: "owner@example.test", PrivateInstallationConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.TenantID == "" || first.WorkspaceID == "" || first != second {
		t.Fatalf("signup was not idempotent: first=%+v second=%+v", first, second)
	}
	status, err = service.Status(context.Background())
	if err != nil || status.State != "authenticated" || status.Account != first {
		t.Fatalf("resumed status=%+v err=%v", status, err)
	}
}

func TestLocalOwnerServiceRejectsUnconfirmedOrInvalidSignup(t *testing.T) {
	service, err := NewLocalOwnerService(&fakeLocalOwnerStore{}, "development|member", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []LocalOwnerSignup{
		{DisplayName: "Owner", Email: "owner@example.test"},
		{DisplayName: "", Email: "owner@example.test", PrivateInstallationConfirmed: true},
		{DisplayName: "Owner", Email: "not-an-email", PrivateInstallationConfirmed: true},
	} {
		if _, err := service.Signup(context.Background(), input); !errors.Is(err, ErrInvalidLocalOwnerSignup) {
			t.Fatalf("Signup(%+v) error=%v, want validation failure", input, err)
		}
	}
}

func TestLocalOwnerServiceInitializesNewAndResumedOwner(t *testing.T) {
	store := &fakeLocalOwnerStore{}
	initializer := &fakeLocalOwnerInitializer{}
	service, err := NewLocalOwnerServiceWithInitializer(store, "development|member", initializer, func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	account, err := service.Signup(context.Background(), LocalOwnerSignup{DisplayName: "Owner", Email: "owner@example.test", PrivateInstallationConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil || status.State != "authenticated" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if len(initializer.accounts) != 2 || initializer.accounts[0] != account || initializer.accounts[1] != account {
		t.Fatalf("initializer accounts=%+v want repeated account=%+v", initializer.accounts, account)
	}
}

func TestLocalOwnerServiceFailsClosedWhenInitializationFails(t *testing.T) {
	store := &fakeLocalOwnerStore{account: PersonalAccount{AccountID: "account", TenantID: "tenant", WorkspaceID: "workspace", State: "active"}}
	initializer := &fakeLocalOwnerInitializer{err: errors.New("initialize failed")}
	service, err := NewLocalOwnerServiceWithInitializer(store, "development|member", initializer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Status(context.Background()); err == nil {
		t.Fatal("Status() succeeded without tenant initialization")
	}
}
