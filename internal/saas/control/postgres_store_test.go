package control

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresProvisionIsTransactionalAndIdempotent(t *testing.T) {
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
	store := NewPostgresStore(pool)
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	firstCommand := ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), ExternalSubject: "provider|stable",
		VerifiedEmail: "member@example.test", DisplayName: "Member", RequestID: uuid.NewString(), OccurredAt: now,
	}
	first, err := store.ProvisionPersonalAccount(ctx, firstCommand)
	if err != nil {
		t.Fatalf("first provision: %v", err)
	}
	secondCommand := firstCommand
	secondCommand.AccountID = uuid.NewString()
	secondCommand.TenantID = uuid.NewString()
	secondCommand.RequestID = uuid.NewString()
	second, err := store.ProvisionPersonalAccount(ctx, secondCommand)
	if err != nil {
		t.Fatalf("idempotent provision: %v", err)
	}
	if first.AccountID != second.AccountID || first.TenantID != second.TenantID {
		t.Fatalf("duplicate identity created new resources: first=%+v second=%+v", first, second)
	}
	for table, want := range map[string]int{
		"saas_accounts": 1, "saas_tenants": 1, "saas_memberships": 1,
		"saas_onboarding_states": 1, "saas_outbox": 1, "saas_audit_events": 1,
	} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s count = %d, want %d", table, count, want)
		}
	}

	membership, err := store.Resolve(ctx, firstCommand.ExternalSubject, first.TenantID)
	if err != nil || membership.TenantID != first.TenantID || membership.Role != "owner" {
		t.Fatalf("Resolve() = %+v, %v", membership, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE saas_tenants SET state='suspended' WHERE id=$1", first.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(ctx, firstCommand.ExternalSubject, first.TenantID); err != auth.ErrTenantUnavailable {
		t.Fatalf("Resolve() error = %v, want ErrTenantUnavailable", err)
	}
}
