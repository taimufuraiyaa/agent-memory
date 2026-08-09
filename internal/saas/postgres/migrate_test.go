package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationCoversTenantAuthoritativeTables(t *testing.T) {
	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations() error = %v", err)
	}
	if len(migrations) < 2 {
		t.Fatalf("migration count = %d, want at least 2", len(migrations))
	}
	var up string
	for _, migration := range migrations {
		up += migration.Up
	}
	for _, table := range []string{
		"saas_workspaces", "saas_notes", "saas_memories", "saas_feedback",
		"saas_sessions_memory", "saas_sources", "saas_jobs", "saas_lineage_edges",
		"saas_outbox", "saas_deletion_operations",
	} {
		if !strings.Contains(up, "CREATE TABLE "+table) {
			t.Errorf("migration missing table %s", table)
		}
		if !strings.Contains(up, "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY") {
			t.Errorf("migration missing forced RLS for %s", table)
		}
	}
}

func TestApplyRollbackAndTenantRLS(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Open(ctx, connectionURL)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer pool.Close()

	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS saas_schema_migrations CASCADE")
	migrations := mustMigrations(t)
	for index := len(migrations) - 1; index >= 0; index-- {
		_, _ = pool.Exec(ctx, migrations[index].Down)
	}
	if err := Apply(ctx, pool); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := Apply(ctx, pool); err != nil {
		t.Fatalf("second Apply() must be idempotent: %v", err)
	}

	account1, account2 := uuid.New(), uuid.New()
	tenant1, tenant2 := uuid.New(), uuid.New()
	now := time.Now().UTC()
	for _, values := range []struct{ account, tenant uuid.UUID }{{account1, tenant1}, {account2, tenant2}} {
		if _, err := pool.Exec(ctx, `INSERT INTO saas_accounts (id, external_subject, verified_email, state, created_at, updated_at) VALUES ($1,$2,$3,'active',$4,$4)`, values.account, values.account.String(), values.account.String()+"@example.test", now); err != nil {
			t.Fatalf("insert account: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO saas_tenants (id, kind, state, personal_owner_account_id, created_at, updated_at) VALUES ($1,'personal','active',$2,$3,$3)`, values.tenant, values.account, now); err != nil {
			t.Fatalf("insert tenant: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO saas_workspaces (tenant_id,id,name,state,created_at,updated_at) VALUES ($1,$2,'private','active',$3,$3)`, values.tenant, uuid.New(), now); err != nil {
			t.Fatalf("insert workspace: %v", err)
		}
	}
	dropRLSTestRole(t, ctx, pool)
	if _, err := pool.Exec(ctx, "CREATE ROLE saas_rls_test NOLOGIN"); err != nil {
		t.Fatalf("create RLS test role: %v", err)
	}
	defer func() { dropRLSTestRole(t, context.Background(), pool) }()
	if _, err := pool.Exec(ctx, "GRANT USAGE ON SCHEMA public TO saas_rls_test; GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO saas_rls_test"); err != nil {
		t.Fatalf("grant RLS test role: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE saas_rls_test"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenant1.String()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM saas_workspaces").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("tenant-scoped workspace count = %d, want 1", count)
	}
	if tag, err := tx.Exec(ctx, `DELETE FROM saas_workspaces WHERE tenant_id = $1`, tenant2); err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("cross-tenant delete affected %d rows, error %v", tag.RowsAffected(), err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := RollbackLatest(ctx, pool); err != nil {
		t.Fatalf("RollbackLatest() error = %v", err)
	}
	if err := Apply(ctx, pool); err != nil {
		t.Fatalf("re-apply after rollback error = %v", err)
	}
}

func mustMigrations(t *testing.T) []Migration {
	t.Helper()
	migrations, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	return migrations
}

func dropRLSTestRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='saas_rls_test')").Scan(&exists); err != nil {
		t.Fatalf("check RLS test role: %v", err)
	}
	if !exists {
		return
	}
	if _, err := pool.Exec(ctx, "DROP OWNED BY saas_rls_test"); err != nil {
		t.Fatalf("drop RLS test role privileges: %v", err)
	}
	if _, err := pool.Exec(ctx, "DROP ROLE saas_rls_test"); err != nil {
		t.Fatalf("drop RLS test role: %v", err)
	}
}
