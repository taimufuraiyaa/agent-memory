package control

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresSessionLoginLogoutExpiryEmailAndRecovery(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := saaspostgres.Open(ctx, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := saaspostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE saas_accounts CASCADE"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	store := NewPostgresStore(pool)
	account, err := store.ProvisionPersonalAccount(ctx, ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), ExternalSubject: "provider|sessions",
		VerifiedEmail: "old@example.test", RequestID: uuid.NewString(), OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewSessionService(store, func() time.Time { return now })
	session, err := service.Login(ctx, account, "provider-session-1", now.Add(time.Hour), "login-request")
	if err != nil {
		t.Fatal(err)
	}
	request := auth.RequestContext{AccountID: account.AccountID, TenantID: account.TenantID, SessionID: session.ID, RequestID: "logout-request"}
	if _, err := service.Validate(ctx, request); err != nil {
		t.Fatalf("validate active session: %v", err)
	}
	expiredService := NewSessionService(store, func() time.Time { return now.Add(time.Hour) })
	if _, err := expiredService.Validate(ctx, request); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired Validate() error = %v", err)
	}
	if err := service.ChangeEmail(ctx, request, "NEW@Example.Test", true); err != nil {
		t.Fatalf("ChangeEmail() error = %v", err)
	}
	var email string
	if err := pool.QueryRow(ctx, "SELECT verified_email FROM saas_accounts WHERE id=$1", account.AccountID).Scan(&email); err != nil || email != "new@example.test" {
		t.Fatalf("verified email = %q, error %v", email, err)
	}
	if err := service.Logout(ctx, request); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if err := service.Logout(ctx, request); err != nil {
		t.Fatalf("idempotent Logout() error = %v", err)
	}
	if _, err := service.Validate(ctx, request); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("revoked Validate() error = %v", err)
	}
	oldSession, err := service.Login(ctx, account, "provider-session-2", now.Add(2*time.Hour), "login-request-2")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := service.Recover(ctx, account, "provider-session-recovery", now.Add(2*time.Hour), "recovery-request")
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if _, err := service.Validate(ctx, auth.RequestContext{TenantID: account.TenantID, SessionID: oldSession.ID}); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("old session after recovery error = %v", err)
	}
	if _, err := service.Validate(ctx, auth.RequestContext{TenantID: account.TenantID, SessionID: recovered.ID}); err != nil {
		t.Fatalf("recovery session validation: %v", err)
	}
	var recoveryAudits int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM saas_audit_events WHERE tenant_id=$1 AND operation='account.recovered'", account.TenantID).Scan(&recoveryAudits); err != nil || recoveryAudits != 1 {
		t.Fatalf("recovery audit count = %d, error %v", recoveryAudits, err)
	}
}
