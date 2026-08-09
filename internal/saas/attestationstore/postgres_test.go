package attestationstore

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresStoreMatchesAttestationLifecycle(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), ExternalSubject: "provider|attestation",
		VerifiedEmail: "attestation@example.test", RequestID: uuid.NewString(), OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticated := auth.WithRequestContext(ctx, auth.RequestContext{
		AccountID: account.AccountID, SubjectID: "provider|attestation", TenantID: account.TenantID,
		RequestID: uuid.NewString(), TraceID: uuid.NewString(),
	})
	store := NewPostgresStore(pool)
	service := attestation.NewService(store, attestation.WithClock(func() time.Time { return now }))
	status, err := service.Status(authenticated, account.AccountID)
	if err != nil || status.State != attestation.StatusRequired {
		t.Fatalf("initial Status() = %+v, %v", status, err)
	}
	statementIDs := make([]string, 0, len(status.Policy.Statements))
	for _, statement := range status.Policy.Statements {
		statementIDs = append(statementIDs, statement.ID)
	}
	status, err = service.Accept(authenticated, account.AccountID, attestation.AcceptCommand{
		PolicyVersion: status.Policy.Version, AcceptedStatementIDs: statementIDs, RequestID: "request-accept",
	})
	if err != nil || status.State != attestation.StatusActive || status.Receipt == nil {
		t.Fatalf("Accept() = %+v, %v", status, err)
	}
	service = attestation.NewService(store, attestation.WithClock(func() time.Time { return now.Add(31 * 24 * time.Hour) }))
	status, err = service.Status(authenticated, account.AccountID)
	if err != nil || status.State != attestation.StatusExpired {
		t.Fatalf("expired Status() = %+v, %v", status, err)
	}
	status, err = service.Accept(authenticated, account.AccountID, attestation.AcceptCommand{
		PolicyVersion: attestation.CurrentPolicy().Version, AcceptedStatementIDs: statementIDs, RequestID: "request-accept",
	})
	if err != nil || status.State != attestation.StatusActive || !status.Receipt.AcceptedAt.Equal(now.Add(31*24*time.Hour)) {
		t.Fatalf("renewed Accept() = %+v, %v", status, err)
	}
	if _, err := service.Accept(authenticated, account.AccountID, attestation.AcceptCommand{
		PolicyVersion: attestation.CurrentPolicy().Version, AcceptedStatementIDs: statementIDs, RequestID: "request-accept",
	}); err != nil {
		t.Fatalf("idempotent renewal: %v", err)
	}
	changed := attestation.CurrentPolicy()
	changed.Version = "v-next"
	service = attestation.NewService(store, attestation.WithClock(func() time.Time { return now }), attestation.WithPolicy(changed))
	status, err = service.Status(authenticated, account.AccountID)
	if err != nil || status.Reason != attestation.ReasonPolicyChanged {
		t.Fatalf("changed-policy Status() = %+v, %v", status, err)
	}
	if _, err := store.LatestReceipt(authenticated, uuid.NewString()); err == nil {
		t.Fatal("cross-account attestation read must fail")
	}
	var auditCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM saas_attestation_audit_events WHERE tenant_id=$1", account.TenantID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("attestation audit count = %d, want 2", auditCount)
	}
}

func TestPostgresStoreRequiresAuthenticatedContext(t *testing.T) {
	store := NewPostgresStore(nil)
	_, err := store.LatestReceipt(context.Background(), "account")
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("LatestReceipt() error = %v, want authentication-context error", err)
	}
}
