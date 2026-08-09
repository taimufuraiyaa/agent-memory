package deletion

import (
	"context"
	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAccountDeletionCoolingOffRevocationBillingDetachAndPseudonymization(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "delete|account", VerifiedEmail: "delete@example.test", RequestID: uuid.NewString(), OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	session, credential := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_sessions(tenant_id,id,account_id,provider_session_id,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6)`, account.TenantID, session, account.AccountID, "provider-session", now.Add(24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_api_credentials(tenant_id,id,account_id,verifier_hash,label,scopes,expires_at,created_at) VALUES($1,$2,$3,$4,'test','{}',$5,$6)`, account.TenantID, credential, account.AccountID, []byte("hash"), now.Add(24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	registry := retention.NewRegistry(pool)
	sourceRepo := NewPostgresRepository(pool, registry)
	clock := now
	service := NewAccountService(pool, sourceRepo, registry, func() time.Time { return clock })
	requestCtx := auth.WithRequestContext(ctx, auth.RequestContext{TenantID: account.TenantID, AccountID: account.AccountID, RequestID: uuid.NewString(), TraceID: uuid.NewString(), Capabilities: map[string]struct{}{"account:manage": {}}})
	op, duplicate, err := service.Request(requestCtx, "delete-account-0001")
	if err != nil || duplicate {
		t.Fatalf("Request op=%+v duplicate=%v err=%v", op, duplicate, err)
	}
	var tenantState, subscriptionState string
	var revokedSessions, revokedCredentials int
	if err := pool.QueryRow(ctx, `SELECT state FROM saas_tenants WHERE id=$1`, account.TenantID).Scan(&tenantState); err != nil || tenantState != "deleting" {
		t.Fatalf("tenant state=%s err=%v", tenantState, err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM saas_subscriptions WHERE tenant_id=$1`, account.TenantID).Scan(&subscriptionState); err != nil || subscriptionState != "canceled" {
		t.Fatalf("subscription state=%s err=%v", subscriptionState, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_sessions WHERE tenant_id=$1 AND revoked_at IS NOT NULL`, account.TenantID).Scan(&revokedSessions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM saas_api_credentials WHERE tenant_id=$1 AND revoked_at IS NOT NULL`, account.TenantID).Scan(&revokedCredentials); err != nil || revokedSessions != 1 || revokedCredentials != 1 {
		t.Fatalf("revoked sessions=%d credentials=%d err=%v", revokedSessions, revokedCredentials, err)
	}
	if processed, err := service.RunOnce(ctx, account.TenantID); err != nil || processed {
		t.Fatalf("cooling run processed=%v err=%v", processed, err)
	}
	clock = clock.Add(8 * 24 * time.Hour)
	if processed, err := service.RunOnce(ctx, account.TenantID); err != nil || !processed {
		t.Fatalf("final run processed=%v err=%v", processed, err)
	}
	var accountState, external string
	if err := pool.QueryRow(ctx, `SELECT state,external_subject FROM saas_accounts WHERE id=$1`, account.AccountID).Scan(&accountState, &external); err != nil || accountState != "deleted" || external == "delete|account" {
		t.Fatalf("account state=%s external=%s err=%v", accountState, external, err)
	}
	events, err := audit.NewPostgresRepository(pool).Search(ctx, account.TenantID, audit.Filter{Operation: "deletion.account.request", Limit: 10})
	if err != nil || len(events) != 1 || events[0].ActorID == account.AccountID {
		t.Fatalf("pseudonymized events=%+v err=%v", events, err)
	}
	var receipt string
	if err := pool.QueryRow(ctx, `SELECT receipt_sha256 FROM saas_deletion_operations WHERE tenant_id=$1 AND id=$2`, account.TenantID, op.ID).Scan(&receipt); err != nil || len(receipt) != 64 {
		t.Fatalf("receipt=%s err=%v", receipt, err)
	}
}
