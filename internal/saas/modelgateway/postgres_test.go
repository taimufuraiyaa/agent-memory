package modelgateway

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresUsageIsTenantScopedContentFreeAndDurable(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	rlsRole := "saas_gateway_rls_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedRLSRole := pgx.Identifier{rlsRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+quotedRLSRole+" NOLOGIN NOBYPASSRLS"); err != nil {
		t.Fatalf("create gateway RLS role: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "DROP OWNED BY "+quotedRLSRole)
		_, _ = pool.Exec(context.Background(), "DROP ROLE "+quotedRLSRole)
	}()
	if _, err := pool.Exec(ctx, "GRANT USAGE ON SCHEMA public TO "+quotedRLSRole+"; GRANT SELECT ON saas_model_usage TO "+quotedRLSRole); err != nil {
		t.Fatalf("grant gateway RLS role: %v", err)
	}
	now := time.Date(2026, 8, 5, 22, 0, 0, 0, time.UTC)
	one := provisionGatewayTenant(t, ctx, pool, "one", now)
	two := provisionGatewayTenant(t, ctx, pool, "two", now)
	provider := &providerFixture{name: "private-model", model: "embed-v1", retention: "zero-retention", dimension: 3}
	gateway, err := New(Config{Providers: []Provider{provider}, Policies: []ProviderPolicy{{Provider: "private-model", Models: []string{"embed-v1"}, RetentionPolicies: []string{"zero-retention"}, MaxInputTokens: 100, Timeout: time.Second, InputCostPerMillion: 2}}}, NewPostgresUsageSink(pool), redactorFixture{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	secret := "content that must never enter the usage ledger"
	if _, err := gateway.Embed(ctx, EmbedRequest{TenantID: one.TenantID, SourceID: uuid.NewString(), SourceVersion: 1, Provider: "private-model", Model: "embed-v1", Texts: []string{secret}}); err != nil {
		t.Fatal(err)
	}
	var count int
	var operation, providerName, model, outcome string
	var inputTokens, dimensions int
	if err := gatewayTenantRow(ctx, pool, quotedRLSRole, one.TenantID, `SELECT count(*),min(operation),min(provider),min(model),min(outcome),min(input_tokens),min(dimensions) FROM saas_model_usage`).Scan(&count, &operation, &providerName, &model, &outcome, &inputTokens, &dimensions); err != nil {
		t.Fatal(err)
	}
	if count != 1 || operation != "embed" || providerName != "private-model" || model != "embed-v1" || outcome != "success" || inputTokens == 0 || dimensions != 3 {
		t.Fatalf("usage count=%d operation=%s provider=%s model=%s outcome=%s tokens=%d dimensions=%d", count, operation, providerName, model, outcome, inputTokens, dimensions)
	}
	if err := gatewayTenantRow(ctx, pool, quotedRLSRole, two.TenantID, `SELECT count(*) FROM saas_model_usage`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-tenant usage count=%d err=%v", count, err)
	}
	var contentColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='saas_model_usage' AND column_name IN('prompt','response','text','content')`).Scan(&contentColumns); err != nil || contentColumns != 0 {
		t.Fatalf("usage content columns=%d err=%v", contentColumns, err)
	}
}

func provisionGatewayTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string, at time.Time) control.PersonalAccount {
	t.Helper()
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "gateway|" + label, VerifiedEmail: label + "@example.test", RequestID: uuid.NewString(), OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

type gatewayRow interface{ Scan(...any) error }

func gatewayTenantRow(ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, quotedRole, tenantID, query string, args ...any) gatewayRow {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return gatewayErrorRow{err}
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+quotedRole); err != nil {
		_ = tx.Rollback(ctx)
		return gatewayErrorRow{err}
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return gatewayErrorRow{err}
	}
	return &gatewayTxRow{Row: tx.QueryRow(ctx, query, args...), tx: tx, ctx: ctx}
}

type gatewayTxRow struct {
	pgx.Row
	tx  pgx.Tx
	ctx context.Context
}

func (r *gatewayTxRow) Scan(values ...any) error {
	err := r.Row.Scan(values...)
	_ = r.tx.Rollback(r.ctx)
	return err
}

type gatewayErrorRow struct{ err error }

func (r gatewayErrorRow) Scan(...any) error { return r.err }
