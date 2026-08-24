package memory

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresMemoryWriteIsTenantScopedTransactionalAndIdempotent(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 14, 0, 0, 0, time.UTC)
	store := control.NewPostgresStore(pool)
	account1 := provisionMemoryAccount(t, ctx, store, "provider|memory-1", now)
	account2 := provisionMemoryAccount(t, ctx, store, "provider|memory-2", now)
	workspace1 := insertWorkspace(t, ctx, pool, account1.TenantID, now)
	workspace2 := insertWorkspace(t, ctx, pool, account2.TenantID, now)
	service := NewService(NewPostgresRepository(pool), func() time.Time { return now })
	requestContext := func(account control.PersonalAccount) context.Context {
		return auth.WithRequestContext(ctx, auth.RequestContext{
			AccountID: account.AccountID, TenantID: account.TenantID, RequestID: uuid.NewString(),
			Capabilities: map[string]struct{}{"memory:write": {}},
		})
	}
	command := Command{
		WorkspaceID: workspace1, Type: core.SemanticMemory, Content: "The original text must never enter audit metadata.",
		Source: core.MemorySource{Type: core.SourceUserInput}, IdempotencyKey: "memory-write-key-0001",
		Keywords: []core.MemoryTerm{{Term: "audit", Source: core.TermSourceExplicit}},
	}
	first, duplicate, err := service.Write(requestContext(account1), command)
	if err != nil || duplicate {
		t.Fatalf("first Write() = %+v duplicate=%v error=%v", first, duplicate, err)
	}
	second, duplicate, err := service.Write(requestContext(account1), command)
	if err != nil || !duplicate || second.ID != first.ID {
		t.Fatalf("retry Write() = %+v duplicate=%v error=%v", second, duplicate, err)
	}
	conflict := command
	conflict.Content = "Different input under the same idempotency key."
	if _, _, err := service.Write(requestContext(account1), conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting Write() error = %v", err)
	}
	duplicateContent := command
	duplicateContent.IdempotencyKey = "memory-write-key-0002"
	if existing, duplicate, err := service.Write(requestContext(account1), duplicateContent); err != nil || !duplicate || existing.ID != first.ID {
		t.Fatalf("content duplicate = %+v duplicate=%v error=%v", existing, duplicate, err)
	}
	crossTenant := command
	crossTenant.WorkspaceID = workspace2
	crossTenant.IdempotencyKey = "memory-write-key-0003"
	if _, _, err := service.Write(requestContext(account1), crossTenant); !errors.Is(err, auth.ErrTenantUnavailable) {
		t.Fatalf("cross-tenant Write() error = %v", err)
	}
	for table, want := range map[string]int{"saas_memories": 1, "saas_outbox": 2, "saas_audit_events": 2} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", account1.TenantID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		// Provisioning contributes one outbox and one audit event.
		if count != want {
			t.Errorf("%s count = %d, want %d", table, count, want)
		}
	}
	var leaked bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM saas_outbox WHERE payload::text LIKE '%original text%'
		UNION ALL
		SELECT 1 FROM saas_audit_events WHERE safe_metadata::text LIKE '%original text%'
	)`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Fatal("memory content leaked into outbox or audit metadata")
	}
}

func provisionMemoryAccount(t *testing.T, ctx context.Context, store *control.PostgresStore, subject string, now time.Time) control.PersonalAccount {
	t.Helper()
	account, err := store.ProvisionPersonalAccount(ctx, control.ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), ExternalSubject: subject,
		VerifiedEmail: uuid.NewString() + "@example.test", RequestID: uuid.NewString(), OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func insertWorkspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string, now time.Time) string {
	t.Helper()
	workspaceID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_workspaces (tenant_id,id,name,state,created_at,updated_at)
		VALUES ($1,$2,$3,'active',$4,$4)`, tenantID, workspaceID, "workspace-"+workspaceID, now); err != nil {
		t.Fatal(err)
	}
	return workspaceID
}
