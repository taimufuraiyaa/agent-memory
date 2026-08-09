package billing

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestWebhookOrderingEntitlementsAndUsageReconciliation(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "billing|one", VerifiedEmail: "billing@example.test", RequestID: uuid.NewString(), OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(pool, func() time.Time { return now.Add(4 * time.Hour) })
	paid := Webhook{Provider: "sandbox", EventID: "event-paid", TenantID: account.TenantID, EventType: "subscription.updated", PlanID: "individual", State: "active", CustomerRef: "customer", SubscriptionRef: "subscription", ProviderCreatedAt: now.Add(3 * time.Hour), PeriodEndsAt: now.AddDate(0, 1, 0)}
	if applied, err := repository.ApplyVerifiedWebhook(ctx, paid); err != nil || !applied {
		t.Fatalf("paid applied=%v err=%v", applied, err)
	}
	if applied, err := repository.ApplyVerifiedWebhook(ctx, paid); err != nil || applied {
		t.Fatalf("duplicate applied=%v err=%v", applied, err)
	}
	older := paid
	older.EventID = "event-old"
	older.State = "canceled"
	older.ProviderCreatedAt = now.Add(2 * time.Hour)
	if applied, err := repository.ApplyVerifiedWebhook(ctx, older); err != nil || applied {
		t.Fatalf("older applied=%v err=%v", applied, err)
	}
	pastDue := paid
	pastDue.EventID = "event-past-due"
	pastDue.State = "past_due"
	pastDue.ProviderCreatedAt = now.Add(4 * time.Hour)
	if applied, err := repository.ApplyVerifiedWebhook(ctx, pastDue); err != nil || !applied {
		t.Fatalf("past due applied=%v err=%v", applied, err)
	}
	entitlements, err := repository.Entitlements(ctx, account.TenantID)
	if err != nil || entitlements.PlanID != "individual" || entitlements.BillingState != "past_due" || !entitlements.SourceUploadEnabled {
		t.Fatalf("entitlements=%+v err=%v", entitlements, err)
	}
	var tenantState string
	if err := pool.QueryRow(ctx, `SELECT state FROM saas_tenants WHERE id=$1`, account.TenantID).Scan(&tenantState); err != nil || tenantState != "active" {
		t.Fatalf("payment failure tenant state=%s err=%v", tenantState, err)
	}
	jobID, exportID, usageID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_jobs(tenant_id,id,job_type,subject_type,subject_id,deterministic_key,state,available_at,created_at,updated_at) VALUES($1,$2,'test','tenant',$1,$3,'succeeded',$4,$4,$4)`, account.TenantID, jobID, jobID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_exports(tenant_id,id,account_id,state,requested_at) VALUES($1,$2,$3,'ready',$4)`, account.TenantID, exportID, account.AccountID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_model_usage(tenant_id,id,operation,provider,model,dimensions,input_tokens,output_tokens,estimated_cost_micros,outcome,occurred_at) VALUES($1,$2,'embed','sandbox','embed-v1',384,10,0,0,'success',$3)`, account.TenantID, usageID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	totals, err := repository.ReconcileUsage(ctx, account.TenantID, now)
	if err != nil {
		t.Fatal(err)
	}
	if totals["jobs"] != 1 || totals["exports"] != 1 || totals["embedding_tokens"] != 10 {
		t.Fatalf("usage totals=%v", totals)
	}
	replayed, err := repository.ReconcileUsage(ctx, account.TenantID, now)
	if err != nil || replayed["jobs"] != 1 || replayed["embedding_tokens"] != 10 {
		t.Fatalf("replayed totals=%v err=%v", replayed, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_usage_events(tenant_id,id,usage_key,metric,quantity,source_type,source_id,occurred_at,safe_metadata) VALUES($1,$2,'unsafe','jobs',1,'job','id',$3,'{"content":"private"}')`, account.TenantID, uuid.NewString(), now); err == nil {
		t.Fatal("billing metadata accepted content")
	}
	authenticated := auth.WithRequestContext(ctx, auth.RequestContext{AccountID: account.AccountID, TenantID: account.TenantID, Capabilities: map[string]struct{}{"billing:read": {}}})
	economics, err := NewEconomicsService(pool, UnitCosts{EmbeddingTokenMicros: 1, JobMicros: 2, ExportMicros: 3}).Report(authenticated, now, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if economics.TotalCostMicros != 15 || economics.ActiveMembers != 1 || economics.ModelCalls != 1 || economics.CostPerActiveMemberMicros != 15 || economics.CostPerModelCallMicros != 15 {
		t.Fatalf("economics=%+v", economics)
	}
	worstCase, err := NewEconomicsService(pool, UnitCosts{GenerationTokenMicros: 1, EmbeddingTokenMicros: 1, APIRequestMicros: 1, PassageMicros: 1, JobMicros: 1}).WorstCase(authenticated, 20_000_000)
	if err != nil || !worstCase.Bounded || worstCase.MaximumMonthlyRequests == 0 {
		t.Fatalf("worst-case estimate=%+v err=%v", worstCase, err)
	}
}
