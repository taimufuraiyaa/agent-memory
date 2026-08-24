package operator

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestOperatorInspectionAndTimeBoundIndependentElevation(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := saaspostgres.Open(ctx, url)
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
	now := time.Date(2026, 8, 5, 5, 0, 0, 0, time.UTC)
	account, tenant, workspace, source := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at) VALUES($1,$2,$3,'active',$4,$4)`, account, account, account+"@example.test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at) VALUES($1,'personal','active',$2,$3,$3)`, tenant, account, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_workspaces(tenant_id,id,name,state,created_at,updated_at) VALUES($1,$2,'private','active',$3,$3)`, tenant, workspace, now); err != nil {
		t.Fatal(err)
	}
	receipt := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_attestation_receipts(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent) VALUES($1,$2,$3,'rights-v1','digest','[]',$4,$5,'request','test')`, tenant, receipt, account, now, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,created_at,updated_at) VALUES($1,$2,$3,'pending','licensed',$4,$5,$5)`, tenant, source, workspace, receipt, now); err != nil {
		t.Fatal(err)
	}
	clock := now
	repository := NewRepository(pool, func() time.Time { return clock })
	if err := repository.GrantRole(ctx, tenant, "support-a", "support", "bootstrap"); err != nil {
		t.Fatal(err)
	}
	if err := repository.GrantRole(ctx, tenant, "admin-b", "security_admin", "bootstrap"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.Inspect(ctx, tenant, "support-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Audit) < 3 {
		t.Fatalf("audited snapshot events=%d", len(snapshot.Audit))
	}
	elevation, err := repository.RequestElevation(ctx, tenant, "support-a", source, "ticket-123", "diagnose_parser_failure", 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ApproveElevation(ctx, tenant, elevation, "support-a", true); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("self approval err=%v", err)
	}
	if err := repository.ApproveElevation(ctx, tenant, elevation, "admin-b", true); err != nil {
		t.Fatal(err)
	}
	if err := repository.AuthorizeSourceAccess(ctx, tenant, "support-a", source); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(31 * time.Minute)
	if err := repository.AuthorizeSourceAccess(ctx, tenant, "support-a", source); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("expired access err=%v", err)
	}
}
