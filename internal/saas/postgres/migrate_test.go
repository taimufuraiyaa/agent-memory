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

func TestGraphIndexMigrationIsTenantScopedAndRollbackPreservesCanonicalData(t *testing.T) {
	t.Parallel()
	migrations := mustMigrations(t)
	var graph *Migration
	for index := range migrations {
		if migrations[index].Version == "0029_graph_index" {
			graph = &migrations[index]
			break
		}
	}
	if graph == nil {
		t.Fatal("graph index migration is missing")
	}

	tables := []string{
		"saas_graph_configurations", "saas_graph_revisions", "saas_graph_jobs",
		"saas_graph_entities", "saas_graph_entity_versions", "saas_graph_entity_evidence",
		"saas_graph_edges", "saas_graph_edge_versions", "saas_graph_edge_evidence",
		"saas_graph_communities", "saas_graph_community_members", "saas_graph_reports",
		"saas_graph_reviews", "saas_graph_feedback",
	}
	for _, table := range tables {
		if !strings.Contains(graph.Up, "CREATE TABLE "+table) {
			t.Errorf("graph migration missing table %s", table)
		}
		if !strings.Contains(graph.Up, "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY") {
			t.Errorf("graph migration missing forced RLS for %s", table)
		}
		if !strings.Contains(graph.Down, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("graph rollback missing table %s", table)
		}
	}
	for _, required := range []string{
		"FOREIGN KEY (tenant_id, workspace_id)",
		"current_setting('app.tenant_id', true)::uuid",
		"UNIQUE (tenant_id, workspace_id, configuration_id, idempotency_key)",
		"CHECK (trust IN ('proposed', 'reviewed', 'approved', 'rejected', 'superseded', 'quarantined', 'stale', 'deleted'))",
	} {
		if !strings.Contains(graph.Up, required) {
			t.Errorf("graph migration missing %q", required)
		}
	}
	for _, canonical := range []string{"saas_memories", "saas_sources", "saas_workspaces"} {
		if strings.Contains(graph.Down, "DROP TABLE IF EXISTS "+canonical) {
			t.Errorf("graph rollback must preserve canonical table %s", canonical)
		}
	}
}

func TestSkillOrchestratorMigrationIsTenantWorkspaceScopedAndReversible(t *testing.T) {
	t.Parallel()
	migrations := mustMigrations(t)
	var orchestrator *Migration
	for index := range migrations {
		if migrations[index].Version == "0033_skill_background_orchestrator" {
			orchestrator = &migrations[index]
			break
		}
	}
	if orchestrator == nil {
		t.Fatal("skill background orchestrator migration is missing")
	}

	tables := []string{
		"saas_skill_orchestrator_workflows", "saas_skill_orchestrator_jobs", "saas_skill_orchestrator_job_dependencies",
		"saas_skill_orchestrator_job_attempts", "saas_skill_orchestrator_safety_signals", "saas_skill_orchestrator_configurations",
		"saas_skill_orchestrator_leader_leases", "saas_skill_orchestrator_reconciliation_cursors", "saas_skill_orchestrator_events",
	}
	for _, table := range tables {
		if !strings.Contains(orchestrator.Up, "CREATE TABLE "+table) {
			t.Errorf("orchestrator migration missing table %s", table)
		}
		if !strings.Contains(orchestrator.Up, "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY") {
			t.Errorf("orchestrator migration missing forced RLS for %s", table)
		}
		if !strings.Contains(orchestrator.Down, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("orchestrator rollback missing table %s", table)
		}
	}
	for _, required := range []string{
		"FOREIGN KEY (tenant_id, workspace_id)",
		"current_setting('app.tenant_id', true)::uuid",
		"current_setting('app.workspace_id', true)::uuid",
		"contract_version = 'skill-orchestrator/v1'",
		"saas_skill_orchestrator_jobs_ready",
		"saas_skill_orchestrator_jobs_claim_priority",
		"saas_skill_orchestrator_jobs_expired",
		"saas_skill_orchestrator_jobs_status",
		"jsonb_typeof(result_references) = 'array'",
		"octet_length(result_references::text) <= 8192",
	} {
		if !strings.Contains(orchestrator.Up, required) {
			t.Errorf("orchestrator migration missing %q", required)
		}
	}
	for _, canonical := range []string{"saas_workspaces", "saas_graph_configurations", "saas_memories"} {
		if strings.Contains(orchestrator.Down, "DROP TABLE IF EXISTS "+canonical) {
			t.Errorf("orchestrator rollback must preserve canonical table %s", canonical)
		}
	}
}

func TestSkillReconciliationPartitionMigrationIsScopedBoundedAndReversible(t *testing.T) {
	t.Parallel()
	migrations := mustMigrations(t)
	var partition *Migration
	for index := range migrations {
		if migrations[index].Version == "0034_skill_reconciliation_partitions" {
			partition = &migrations[index]
			break
		}
	}
	if partition == nil {
		t.Fatal("skill reconciliation partition migration is missing")
	}
	for _, required := range []string{
		"PRIMARY KEY (tenant_id, workspace_id, environment)",
		"FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id) ON DELETE CASCADE",
		"idx_skill_reconciliation_partitions_claim",
		"ALTER TABLE saas_skill_orchestrator_reconciliation_partitions FORCE ROW LEVEL SECURITY",
		"current_setting('app.tenant_id', true)::uuid",
		"current_setting('app.workspace_id', true)::uuid",
	} {
		if !strings.Contains(partition.Up, required) {
			t.Errorf("skill reconciliation partition migration missing %q", required)
		}
	}
	if !strings.Contains(partition.Down, "DROP TABLE IF EXISTS saas_skill_orchestrator_reconciliation_partitions") {
		t.Fatal("skill reconciliation partition rollback is missing")
	}
}

func TestSkillRuntimeRoleMigrationIsLeastPrivilegeAndReversible(t *testing.T) {
	t.Parallel()
	migrations := mustMigrations(t)
	var roles *Migration
	for index := range migrations {
		if migrations[index].Version == "0035_skill_runtime_roles" {
			roles = &migrations[index]
			break
		}
	}
	if roles == nil {
		t.Fatal("skill runtime role migration is missing")
	}
	for _, required := range []string{
		"CREATE ROLE agent_memory_skill_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS",
		"CREATE ROLE agent_memory_skill_reconciler LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS",
		"GRANT SELECT, INSERT, UPDATE ON",
		"saas_skill_orchestrator_reconciliation_partitions",
		"GRANT SELECT ON saas_skill_orchestrator_configurations TO agent_memory_skill_worker",
	} {
		if !strings.Contains(roles.Up, required) {
			t.Errorf("skill runtime role migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"SUPERUSER", "BYPASSRLS", "GRANT DELETE", "GRANT ALL"} {
		if forbidden == "SUPERUSER" || forbidden == "BYPASSRLS" {
			continue
		}
		if strings.Contains(roles.Up, forbidden) {
			t.Errorf("skill runtime role migration contains overbroad capability %q", forbidden)
		}
	}
	if !strings.Contains(roles.Down, "DROP ROLE IF EXISTS agent_memory_skill_worker") || !strings.Contains(roles.Down, "DROP ROLE IF EXISTS agent_memory_skill_reconciler") {
		t.Fatal("skill runtime role rollback is incomplete")
	}
}

func TestSkillOrchestratorCustodyMigrationIsScopedAndReversible(t *testing.T) {
	t.Parallel()
	migrations := mustMigrations(t)
	var custody *Migration
	for index := range migrations {
		if migrations[index].Version == "0036_skill_orchestrator_custody" {
			custody = &migrations[index]
			break
		}
	}
	if custody == nil {
		t.Fatal("skill orchestrator custody migration is missing")
	}
	for _, table := range []string{"saas_skill_orchestrator_legal_holds", "saas_skill_orchestrator_tombstones"} {
		if !strings.Contains(custody.Up, "CREATE TABLE "+table) {
			t.Errorf("custody migration missing table %s", table)
		}
		if !strings.Contains(custody.Up, "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY") {
			t.Errorf("custody migration missing forced RLS for %s", table)
		}
		if !strings.Contains(custody.Down, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("custody rollback missing table %s", table)
		}
	}
	for _, required := range []string{
		"PRIMARY KEY (tenant_id,workspace_id,environment,record_kind,record_id)",
		"saas_skill_orchestrator_legal_holds_active",
		"WHERE state='active'",
		"current_setting('app.tenant_id',true)::uuid",
		"current_setting('app.workspace_id',true)::uuid",
		"GRANT SELECT ON saas_skill_orchestrator_tombstones TO agent_memory_skill_worker,agent_memory_skill_reconciler",
	} {
		if !strings.Contains(custody.Up, required) {
			t.Errorf("custody migration missing %q", required)
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

func TestSkillRuntimeDatabaseRolesHaveOnlyDeclaredCapabilities(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Open(ctx, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"agent_memory_skill_worker", "agent_memory_skill_reconciler"} {
		var superuser, createDB, createRole, bypassRLS bool
		if err := pool.QueryRow(ctx, `SELECT rolsuper,rolcreatedb,rolcreaterole,rolbypassrls FROM pg_roles WHERE rolname=$1`, role).Scan(&superuser, &createDB, &createRole, &bypassRLS); err != nil {
			t.Fatal(err)
		}
		if superuser || createDB || createRole || bypassRLS {
			t.Fatalf("role %s has administrative capability", role)
		}
		var canSelect, canUpdate, canDelete bool
		if err := pool.QueryRow(ctx, `SELECT has_table_privilege($1,'saas_skill_orchestrator_jobs','SELECT'),has_table_privilege($1,'saas_skill_orchestrator_jobs','UPDATE'),has_table_privilege($1,'saas_skill_orchestrator_jobs','DELETE')`, role).Scan(&canSelect, &canUpdate, &canDelete); err != nil {
			t.Fatal(err)
		}
		if !canSelect || !canUpdate || canDelete {
			t.Fatalf("role %s jobs capabilities select=%v update=%v delete=%v", role, canSelect, canUpdate, canDelete)
		}
	}
	var workerCanPartition, reconcilerCanPartition bool
	if err := pool.QueryRow(ctx, `SELECT has_table_privilege('agent_memory_skill_worker','saas_skill_orchestrator_reconciliation_partitions','SELECT'),has_table_privilege('agent_memory_skill_reconciler','saas_skill_orchestrator_reconciliation_partitions','UPDATE')`).Scan(&workerCanPartition, &reconcilerCanPartition); err != nil {
		t.Fatal(err)
	}
	if workerCanPartition || !reconcilerCanPartition {
		t.Fatalf("partition capability worker=%v reconciler=%v", workerCanPartition, reconcilerCanPartition)
	}
	var workerCanReadMemories, workerCanRunMigrations bool
	if err := pool.QueryRow(ctx, `SELECT has_table_privilege('agent_memory_skill_worker','saas_memories','SELECT'),has_table_privilege('agent_memory_skill_worker','saas_schema_migrations','UPDATE')`).Scan(&workerCanReadMemories, &workerCanRunMigrations); err != nil {
		t.Fatal(err)
	}
	if workerCanReadMemories || workerCanRunMigrations {
		t.Fatalf("skill worker overlaps API or migration privilege memories=%v migrations=%v", workerCanReadMemories, workerCanRunMigrations)
	}
}

func TestSkillOrchestratorMigrationTenantWorkspaceRLSAndClaimIndex(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Open(ctx, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	account1, account2 := uuid.New(), uuid.New()
	tenant1, tenant2 := uuid.New(), uuid.New()
	workspace1, workspace2 := uuid.New(), uuid.New()
	now := time.Now().UTC()
	for _, values := range []struct{ account, tenant, workspace uuid.UUID }{{account1, tenant1, workspace1}, {account2, tenant2, workspace2}} {
		if _, err := pool.Exec(ctx, `INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`, values.account, values.account.String(), values.account.String()+"@example.test", now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at) VALUES($1,'personal','active',$2,$3,$3)`, values.tenant, values.account, now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO saas_workspaces(tenant_id,id,name,state,created_at,updated_at) VALUES($1,$2,'orchestrator-test','active',$3,$3)`, values.tenant, values.workspace, now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO saas_skill_orchestrator_workflows(
			tenant_id,workspace_id,id,environment,origin_kind,origin_id,workflow_kind,contract_version,input_digest,
			state,current_stage,generation,configuration_version,policy_digest,created_at,updated_at
		) VALUES($1,$2,$3,'production','tool_lesson','lesson-rls','automatic_revision','skill-orchestrator/v1',$4,'open','detect',1,1,$4,$5,$5)`, values.tenant, values.workspace, uuid.New(), "sha256:"+strings.Repeat("a", 64), now); err != nil {
			t.Fatal(err)
		}
	}

	const role = "saas_skill_orchestrator_rls_test"
	_, _ = pool.Exec(ctx, "DROP OWNED BY "+role)
	_, _ = pool.Exec(ctx, "DROP ROLE IF EXISTS "+role)
	if _, err := pool.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "DROP OWNED BY "+role)
		_, _ = pool.Exec(context.Background(), "DROP ROLE IF EXISTS "+role)
	}()
	if _, err := pool.Exec(ctx, "GRANT USAGE ON SCHEMA public TO "+role+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO "+role); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_skill_orchestrator_workflows`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing tenant/workspace context count=%d err=%v", count, err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenant1.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_skill_orchestrator_workflows`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing workspace context count=%d err=%v", count, err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id',$1,true)", workspace1.String()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_skill_orchestrator_workflows`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("tenant/workspace scoped count=%d err=%v", count, err)
	}
	if tag, err := tx.Exec(ctx, `DELETE FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1 AND workspace_id=$2`, tenant2, workspace2); err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("cross-tenant delete affected=%d err=%v", tag.RowsAffected(), err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN SELECT id FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND environment='production' AND state='queued' AND ready_at<=$3 ORDER BY priority DESC,ready_at,created_at LIMIT 10`, tenant1, workspace1, now)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
	}
	rows.Close()
	if !strings.Contains(plan.String(), "saas_skill_orchestrator_jobs_claim_priority") {
		t.Fatalf("claim plan does not use priority-ready index: %s", plan.String())
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
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
