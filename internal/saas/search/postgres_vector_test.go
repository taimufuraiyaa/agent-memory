package search

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresVectorProjectionIsTenantFilteredRebuildableAndVersionSafe(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 23, 0, 0, 0, time.UTC)
	one := provisionVectorTenant(t, ctx, pool, "one", now)
	two := provisionVectorTenant(t, ctx, pool, "two", now)
	oneSource := publishVectorSource(t, ctx, pool, one, "one", now)
	twoSource := publishVectorSource(t, ctx, pool, two, "two", now)

	provider := &providerFixture384{}
	gatewayProvider, err := modelgateway.NewEmbeddingProvider(provider, "local-only")
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := modelgateway.New(modelgateway.Config{
		Providers: []modelgateway.Provider{gatewayProvider},
		Policies:  []modelgateway.ProviderPolicy{{Provider: provider.Name(), Models: []string{provider.ModelVersion()}, RetentionPolicies: []string{"local-only"}, MaxInputTokens: 1000, Timeout: time.Second}},
	}, modelgateway.NewPostgresUsageSink(pool), modelgateway.RedactorFunc(func(value string) string { return value }), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository := NewPostgresRepository(pool)
	projector, err := NewVectorProjector(repository, gateway, provider.Name(), provider.ModelVersion(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	processed, err := projector.ProcessOnce(ctx)
	if err != nil || processed != 2 {
		t.Fatalf("initial vector projection processed=%d err=%v", processed, err)
	}
	query := make([]float32, VectorDimensions)
	query[0] = 1
	oneHits, err := repository.SearchVectors(ctx, one.TenantID, []string{oneSource}, query, 10)
	if err != nil || len(oneHits) != 1 || oneHits[0].SourceID != oneSource || oneHits[0].PassageID != "passage-one" {
		t.Fatalf("tenant one hits=%+v err=%v", oneHits, err)
	}
	crossTenant, err := repository.SearchVectors(ctx, one.TenantID, []string{twoSource}, query, 10)
	if err != nil || len(crossTenant) != 0 {
		t.Fatalf("cross-tenant hits=%+v err=%v", crossTenant, err)
	}
	if _, err := repository.SearchVectors(ctx, one.TenantID, nil, query, 10); err == nil {
		t.Fatal("vector query without an authorization filter was accepted")
	}

	if err := projector.Rebuild(ctx, one.TenantID, oneSource); err != nil {
		t.Fatal(err)
	}
	processed, err = projector.ProcessOnce(ctx)
	if err != nil || processed != 1 {
		t.Fatalf("rebuild processed=%d err=%v", processed, err)
	}
	var passageID string
	if err := vectorTenantRow(ctx, pool, one.TenantID, `SELECT id FROM saas_source_passages WHERE source_id=$1 AND source_version=1`, oneSource).Scan(&passageID); err != nil || passageID != "passage-one" {
		t.Fatalf("authoritative passage after rebuild=%q err=%v", passageID, err)
	}

	if err := projector.Rebuild(ctx, one.TenantID, oneSource); err != nil {
		t.Fatal(err)
	}
	claim, err := repository.ClaimNextVector(ctx, one.TenantID, vectorProjectionVersion(provider.Name(), provider.ModelVersion(), VectorDimensions), now, time.Minute)
	if err != nil || claim == nil || claim.SourceVersion != 1 {
		t.Fatalf("stale test claim=%+v err=%v", claim, err)
	}
	publishSecondVectorVersion(t, ctx, pool, one.TenantID, oneSource, now.Add(time.Minute))
	record := VectorRecord{PassageID: claim.Passages[0].ID, StructuralNodeID: claim.Passages[0].StructuralNodeID, Embedding: query}
	if err := repository.CompleteVectorProjection(ctx, *claim, provider.Name(), provider.ModelVersion(), VectorDimensions, []VectorRecord{record}, now.Add(time.Minute)); !errors.Is(err, ErrStaleVectorClaim) {
		t.Fatalf("stale completion error=%v", err)
	}
	processed, err = projector.ProcessOnce(ctx)
	if err != nil || processed != 1 {
		t.Fatalf("new-version projection processed=%d err=%v", processed, err)
	}
	newHits, err := repository.SearchVectors(ctx, one.TenantID, []string{oneSource}, query, 10)
	if err != nil || len(newHits) != 1 || newHits[0].SourceVersion != 2 || newHits[0].PassageID != "passage-one-v2" {
		t.Fatalf("new-version hits=%+v err=%v", newHits, err)
	}
	var usageCount int
	if err := vectorTenantRow(ctx, pool, one.TenantID, `SELECT count(*) FROM saas_model_usage WHERE operation='embed' AND source_id=$1`, oneSource).Scan(&usageCount); err != nil || usageCount < 2 {
		t.Fatalf("model usage count=%d err=%v", usageCount, err)
	}
}

func TestSourceReadinessIsIndependentOfProjectionCompletionOrder(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 23, 30, 0, 0, time.UTC)
	account := provisionVectorTenant(t, ctx, pool, "order", now)
	sourceID := publishVectorSource(t, ctx, pool, account, "order", now)
	tx := beginVectorTenant(t, ctx, pool, account.TenantID)
	if _, err := tx.Exec(ctx, `DELETE FROM saas_source_projections WHERE source_id=$1 AND projection_kind='fulltext'`, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	provider := &providerFixture384{}
	gatewayProvider, _ := modelgateway.NewEmbeddingProvider(provider, "local-only")
	gateway, err := modelgateway.New(modelgateway.Config{Providers: []modelgateway.Provider{gatewayProvider}, Policies: []modelgateway.ProviderPolicy{{Provider: provider.Name(), Models: []string{provider.ModelVersion()}, RetentionPolicies: []string{"local-only"}, MaxInputTokens: 1000, Timeout: time.Second}}}, modelgateway.NewPostgresUsageSink(pool), modelgateway.RedactorFunc(func(value string) string { return value }), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository := NewPostgresRepository(pool)
	vectorProjector, _ := NewVectorProjector(repository, gateway, provider.Name(), provider.ModelVersion(), func() time.Time { return now })
	if processed, err := vectorProjector.ProcessOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("vector-first processed=%d err=%v", processed, err)
	}
	var state string
	if err := vectorTenantRow(ctx, pool, account.TenantID, `SELECT state FROM saas_sources WHERE id=$1`, sourceID).Scan(&state); err != nil || state != "indexing" {
		t.Fatalf("state before fulltext=%s err=%v", state, err)
	}
	fullTextProjector, _ := NewFullTextProjector(repository, func() time.Time { return now.Add(time.Second) })
	if processed, err := fullTextProjector.ProcessOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("fulltext-second processed=%d err=%v", processed, err)
	}
	if err := vectorTenantRow(ctx, pool, account.TenantID, `SELECT state FROM saas_sources WHERE id=$1`, sourceID).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("final state=%s err=%v", state, err)
	}
}

type providerFixture384 struct{}

func (*providerFixture384) Name() string         { return "local-test" }
func (*providerFixture384) ModelVersion() string { return "embed-v1" }
func (*providerFixture384) Dimension() int       { return VectorDimensions }
func (*providerFixture384) Embed(context.Context, string) ([]float32, error) {
	value := make([]float32, VectorDimensions)
	value[0] = 1
	return value, nil
}
func (p *providerFixture384) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	values := make([][]float32, len(texts))
	for index := range texts {
		values[index], _ = p.Embed(ctx, texts[index])
	}
	return values, nil
}

func provisionVectorTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string, at time.Time) control.PersonalAccount {
	t.Helper()
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "vector|" + label, VerifiedEmail: label + "@example.test", RequestID: uuid.NewString(), OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func publishVectorSource(t *testing.T, ctx context.Context, pool *pgxpool.Pool, account control.PersonalAccount, label string, at time.Time) string {
	t.Helper()
	sourceID, receiptID := uuid.NewString(), uuid.NewString()
	tx := beginVectorTenant(t, ctx, pool, account.TenantID)
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_attestation_receipts(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent)
		VALUES($1,$2,$3,'v1',$4,'[]',$5,$6,$7,'test')`, account.TenantID, receiptID, account.AccountID, strings.Repeat("a", 64), at, at.Add(30*24*time.Hour), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,active_version,created_at,updated_at)
		VALUES($1,$2,$3,'indexing','lawfully_acquired_private_use',$4,1,$5,$5)`, account.TenantID, sourceID, account.WorkspaceID, receiptID, at); err != nil {
		t.Fatal(err)
	}
	insertVectorCorpus(t, ctx, tx, account.TenantID, sourceID, 1, "passage-"+label, at)
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_projections(tenant_id,source_id,source_version,projection_kind,projection_version,state,document_count,projected_at)
		VALUES($1,$2,1,'fulltext',$3,'ready',1,$4)`, account.TenantID, sourceID, FullTextProjectionVersion, at); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return sourceID
}

func publishSecondVectorVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, sourceID string, at time.Time) {
	t.Helper()
	tx := beginVectorTenant(t, ctx, pool, tenantID)
	defer func() { _ = tx.Rollback(ctx) }()
	insertVectorCorpus(t, ctx, tx, tenantID, sourceID, 2, "passage-one-v2", at)
	if _, err := tx.Exec(ctx, `UPDATE saas_sources SET active_version=2,state='indexing',updated_at=$3 WHERE tenant_id=$1 AND id=$2`, tenantID, sourceID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_projections(tenant_id,source_id,source_version,projection_kind,projection_version,state,document_count,projected_at)
		VALUES($1,$2,2,'fulltext',$3,'ready',1,$4)`, tenantID, sourceID, FullTextProjectionVersion, at); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func insertVectorCorpus(t *testing.T, ctx context.Context, tx pgx.Tx, tenantID, sourceID string, version int64, passageID string, at time.Time) {
	t.Helper()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,published_at,created_at)
		VALUES($1,$2,$3,$4,'text/markdown','markdown-v1','text-v1',$5,$6,$6)`, tenantID, sourceID, version, strings.Repeat(strconvDigit(version), 64), "vault/"+sourceID+"/"+strconvDigit(version), at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_nodes(tenant_id,source_id,source_version,id,kind,ordinal,title,start_offset,end_offset,explicit)
		VALUES($1,$2,$3,'node','section',0,'Section',0,20,true)`, tenantID, sourceID, version); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_passages(tenant_id,source_id,source_version,id,structural_node_id,text_content,fingerprint,locator)
		VALUES($1,$2,$3,$4,'node',$5,$6,'{}')`, tenantID, sourceID, version, passageID, "private text "+passageID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
}

func strconvDigit(value int64) string {
	if value == 2 {
		return "2"
	}
	return "1"
}

func beginVectorTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	return tx
}

func vectorTenantRow(ctx context.Context, pool *pgxpool.Pool, tenantID, query string, args ...any) interface{ Scan(...any) error } {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return vectorErrorRow{err}
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return vectorErrorRow{err}
	}
	return &vectorTxRow{Row: tx.QueryRow(ctx, query, args...), tx: tx, ctx: ctx}
}

type vectorTxRow struct {
	pgx.Row
	tx  pgx.Tx
	ctx context.Context
}

func (r *vectorTxRow) Scan(values ...any) error {
	err := r.Row.Scan(values...)
	_ = r.tx.Rollback(r.ctx)
	return err
}

type vectorErrorRow struct{ err error }

func (r vectorErrorRow) Scan(...any) error { return r.err }
