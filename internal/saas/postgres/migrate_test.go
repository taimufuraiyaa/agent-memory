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

func TestMemorySearchMigrationMaintainsGINProjectionAndRollback(t *testing.T) {
	migrations := mustMigrations(t)
	var search *Migration
	for index := range migrations {
		if migrations[index].Version == "0026_memory_search" {
			search = &migrations[index]
			break
		}
	}
	if search == nil {
		t.Fatal("memory search migration is missing")
	}
	for _, required := range []string{
		"ADD COLUMN search_document tsvector",
		"CREATE TRIGGER saas_memories_search_document",
		"BEFORE INSERT OR UPDATE OF content, memory_type, source_kind, entities, tags, keywords",
		"to_tsvector(",
		"ALTER COLUMN search_document SET NOT NULL",
		"USING gin(search_document)",
	} {
		if !strings.Contains(search.Up, required) {
			t.Errorf("memory search up migration missing %q", required)
		}
	}
	for _, required := range []string{
		"DROP INDEX IF EXISTS saas_memories_search_document_gin",
		"DROP TRIGGER IF EXISTS saas_memories_search_document ON saas_memories",
		"DROP FUNCTION IF EXISTS saas_refresh_memory_search_document()",
		"DROP COLUMN IF EXISTS search_document",
	} {
		if !strings.Contains(search.Down, required) {
			t.Errorf("memory search down migration missing %q", required)
		}
	}
}

func TestRetentionPurposeMigrationCompletesReviewableInventory(t *testing.T) {
	migrations := mustMigrations(t)
	var purpose *Migration
	for index := range migrations {
		if migrations[index].Version == "0027_retention_purpose" {
			purpose = &migrations[index]
			break
		}
	}
	if purpose == nil {
		t.Fatal("retention purpose migration is missing")
	}
	for _, required := range []string{
		"ADD COLUMN purpose text",
		"ALTER COLUMN purpose SET NOT NULL",
		"saas_retention_policy_purpose",
		"char_length(purpose) BETWEEN 1 AND 512",
	} {
		if !strings.Contains(purpose.Up, required) {
			t.Errorf("retention purpose up migration missing %q", required)
		}
	}
	for _, dataClass := range []string{"account_identity", "sessions_credentials", "memory_content", "source_originals", "source_derived", "exports", "model_usage", "audit_events", "security_cases", "billing_records", "backups", "analytics"} {
		if !strings.Contains(purpose.Up, "'"+dataClass+"'") {
			t.Errorf("retention purpose migration missing purpose for %q", dataClass)
		}
	}
	for _, required := range []string{"DROP CONSTRAINT IF EXISTS saas_retention_policy_purpose", "DROP COLUMN IF EXISTS purpose"} {
		if !strings.Contains(purpose.Down, required) {
			t.Errorf("retention purpose down migration missing %q", required)
		}
	}
}

func TestLaunchPolicyMigrationsDefaultInternalAlphaSignupClosed(t *testing.T) {
	migrations := mustMigrations(t)
	var launchReadiness, safeDefault *Migration
	for index := range migrations {
		switch migrations[index].Version {
		case "0024_launch_readiness":
			launchReadiness = &migrations[index]
		case "0028_launch_policy_safe_default":
			safeDefault = &migrations[index]
		}
	}
	if launchReadiness == nil {
		t.Fatal("launch readiness migration is missing")
	}
	if safeDefault == nil {
		t.Fatal("safe launch-policy default migration is missing")
	}
	if !strings.Contains(launchReadiness.Up, "VALUES(true,'internal_alpha',false,true") {
		t.Error("fresh installations must seed internal-alpha signup disabled and invitations required")
	}
	for _, required := range []string{
		"UPDATE saas_launch_policy",
		"signup_enabled = false",
		"invitation_required = true",
		"updated_by = 'migration'",
		"reason_code = 'safe_platform_default_closed'",
		"WHERE singleton = true",
		"AND phase = 'internal_alpha'",
		"AND (signup_enabled = true OR invitation_required = false)",
	} {
		if !strings.Contains(safeDefault.Up, required) {
			t.Errorf("safe-default migration missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(safeDefault.Down), "signup_enabled = true") {
		t.Error("rolling back the safe-default migration must not reopen signup")
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
	if _, err := pool.Exec(ctx, `UPDATE saas_launch_policy SET signup_enabled=true, invitation_required=false, updated_by='migration-test', reason_code='simulate_pre_0028', updated_at=clock_timestamp() WHERE singleton=true`); err != nil {
		t.Fatalf("simulate already-migrated unsafe launch policy: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM saas_schema_migrations WHERE version='0028_launch_policy_safe_default'`); err != nil {
		t.Fatalf("rewind safe-default migration ledger: %v", err)
	}
	if err := Apply(ctx, pool); err != nil {
		t.Fatalf("apply safe default to existing installation: %v", err)
	}
	var phase string
	var signupEnabled, invitationRequired bool
	if err := pool.QueryRow(ctx, `SELECT phase, signup_enabled, invitation_required FROM saas_launch_policy WHERE singleton=true`).Scan(&phase, &signupEnabled, &invitationRequired); err != nil {
		t.Fatalf("read installed launch policy: %v", err)
	}
	if phase != "internal_alpha" || signupEnabled || !invitationRequired {
		t.Fatalf("installed launch policy = phase %q signup %v invitation %v, want fail-closed internal alpha", phase, signupEnabled, invitationRequired)
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
