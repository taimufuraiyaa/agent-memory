package retrieval

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

func TestPostgresRetrievalAppliesTenantAuthorizationAndDurableFeedback(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 14, 0, 0, 0, time.UTC)
	one := provisionRetrievalTenant(t, ctx, pool, "one", now)
	two := provisionRetrievalTenant(t, ctx, pool, "two", now)
	oneSource := seedRetrievalSource(t, ctx, pool, one, "private alpha evidence", now)
	twoSource := seedRetrievalSource(t, ctx, pool, two, "private alpha from another tenant", now)
	repository := NewPostgresRepository(pool)

	candidates, err := repository.LexicalCandidates(ctx, one.TenantID, []string{oneSource}, "alpha", 10)
	if err != nil || len(candidates) != 1 || candidates[0].SourceID != oneSource || candidates[0].PassageID != "shared-passage" || candidates[0].Breakdown.FullText <= 0 {
		t.Fatalf("tenant candidates=%+v err=%v", candidates, err)
	}
	crossTenant, err := repository.LexicalCandidates(ctx, one.TenantID, []string{twoSource}, "alpha", 10)
	if err != nil || len(crossTenant) != 0 {
		t.Fatalf("cross-tenant candidates=%+v err=%v", crossTenant, err)
	}
	hydrated, err := repository.EvidenceByPassageIDs(ctx, one.TenantID, []string{oneSource}, []EvidenceKey{{SourceID: oneSource, PassageID: "shared-passage"}, {SourceID: twoSource, PassageID: "shared-passage"}})
	if err != nil || len(hydrated) != 1 || hydrated[0].SourceID != oneSource {
		t.Fatalf("reauthorized hydration=%+v err=%v", hydrated, err)
	}
	if err := repository.RecordPassageFeedback(ctx, one.TenantID, EvidenceKey{SourceID: oneSource, PassageID: "shared-passage"}, 1, "helpful", one.AccountID, uuid.NewString(), now); err != nil {
		t.Fatal(err)
	}
	candidates, err = repository.LexicalCandidates(ctx, one.TenantID, []string{oneSource}, "alpha", 10)
	if err != nil || len(candidates) != 1 || candidates[0].UsefulCount != 1 || candidates[0].LastHelpfulAt == nil {
		t.Fatalf("feedback candidates=%+v err=%v", candidates, err)
	}
	if err := repository.RecordPassageFeedback(ctx, one.TenantID, EvidenceKey{SourceID: twoSource, PassageID: "shared-passage"}, 1, "helpful", one.AccountID, uuid.NewString(), now); err == nil {
		t.Fatal("cross-tenant passage feedback was accepted")
	}
}

func provisionRetrievalTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string, at time.Time) control.PersonalAccount {
	t.Helper()
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "retrieval|" + label, VerifiedEmail: label + "@example.test", RequestID: uuid.NewString(), OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func seedRetrievalSource(t *testing.T, ctx context.Context, pool *pgxpool.Pool, account control.PersonalAccount, text string, at time.Time) string {
	t.Helper()
	sourceID, receiptID := uuid.NewString(), uuid.NewString()
	tx := beginRetrievalTenant(t, ctx, pool, account.TenantID)
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_attestation_receipts(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent)
		VALUES($1,$2,$3,'v1',$4,'[]',$5,$6,$7,'test')`, account.TenantID, receiptID, account.AccountID, strings.Repeat("a", 64), at, at.Add(30*24*time.Hour), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,active_version,created_at,updated_at)
		VALUES($1,$2,$3,'ready','lawfully_acquired_private_use',$4,1,$5,$5)`, account.TenantID, sourceID, account.WorkspaceID, receiptID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,published_at,created_at)
		VALUES($1,$2,1,$3,'text/plain','text-v1','text-v1',$4,$5,$5)`, account.TenantID, sourceID, strings.ReplaceAll(sourceID, "-", "")+strings.Repeat("0", 32), "vault/"+sourceID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_nodes(tenant_id,source_id,source_version,id,kind,ordinal,title,start_offset,end_offset,explicit)
		VALUES($1,$2,1,'node','section',0,'Section',0,$3,true)`, account.TenantID, sourceID, len(text)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_passages(tenant_id,source_id,source_version,id,structural_node_id,text_content,fingerprint,locator)
		VALUES($1,$2,1,'shared-passage','node',$3,$4,'{"page":1}')`, account.TenantID, sourceID, text, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_citations(tenant_id,source_id,source_version,id,passage_id,structural_node_id,passage_fingerprint,locator)
		VALUES($1,$2,1,'shared-citation','shared-passage','node',$3,'{"page":1}')`, account.TenantID, sourceID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_fulltext_documents(tenant_id,source_id,source_version,passage_id,structural_node_id,text_content,locator,projected_at)
		VALUES($1,$2,1,'shared-passage','node',$3,'{"page":1}',$4)`, account.TenantID, sourceID, text, at); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return sourceID
}

func beginRetrievalTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) pgx.Tx {
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
