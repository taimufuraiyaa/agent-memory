package review

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresMemoryProposalRequiresEditAndReviewWithLineage(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 15, 0, 0, 0, time.UTC)
	one := provisionReviewTenant(t, ctx, pool, "one", now)
	two := provisionReviewTenant(t, ctx, pool, "two", now)
	rawText := "Raw exact book sentence that should not become memory without transformation."
	oneSource := seedReviewSource(t, ctx, pool, one, rawText, now)
	twoSource := seedReviewSource(t, ctx, pool, two, "Other tenant private sentence.", now)
	request := auth.RequestContext{TenantID: one.TenantID, AccountID: one.AccountID, RequestID: uuid.NewString(), TraceID: uuid.NewString(), Capabilities: map[string]struct{}{"memory:write": {}}}
	authenticated := auth.WithRequestContext(ctx, request)
	service := NewService(NewPostgresRepository(pool), func() time.Time { return now })
	evidence := []EvidenceRef{{SourceID: oneSource, SourceVersion: 1, PassageID: "passage", CitationID: "citation"}}

	if _, err := service.Create(authenticated, CreateCommand{WorkspaceID: one.WorkspaceID, MemoryType: core.SemanticMemory, Content: rawText, Transformation: "summary", Evidence: evidence}); err == nil {
		t.Fatal("raw source text was silently accepted as memory")
	}
	proposal, err := service.Create(authenticated, CreateCommand{WorkspaceID: one.WorkspaceID, MemoryType: core.SemanticMemory, Content: "The reader interprets the chapter as warning against direct copying.", Transformation: "interpretation", Evidence: evidence})
	if err != nil || proposal.Status != core.ProposalSuggested || proposal.MemoryID != "" {
		t.Fatalf("proposal=%+v err=%v", proposal, err)
	}
	if count := reviewMemoryCount(t, ctx, pool, one.TenantID); count != 0 {
		t.Fatalf("suggested proposal wrote %d memories", count)
	}
	proposal, err = service.Update(authenticated, proposal.ID, UpdateCommand{Content: "My reviewed interpretation is that durable memory must remain distinct from quoted source text.", Transformation: "user_edit"})
	if err != nil || proposal.Transformation != "user_edit" {
		t.Fatalf("updated proposal=%+v err=%v", proposal, err)
	}
	accepted, err := service.Accept(authenticated, proposal.ID)
	if err != nil || accepted.Status != core.ProposalAccepted || accepted.MemoryID == "" {
		t.Fatalf("accepted proposal=%+v err=%v", accepted, err)
	}
	var content, transformation string
	var lineageCount int
	if err := reviewTenantRow(ctx, pool, one.TenantID, `SELECT m.content,l.transformation,count(*) OVER() FROM saas_memories m JOIN saas_lineage_edges l ON l.tenant_id=m.tenant_id AND l.to_id=m.id WHERE m.id=$1`, accepted.MemoryID).Scan(&content, &transformation, &lineageCount); err != nil {
		t.Fatal(err)
	}
	if content != proposal.Content || transformation != "user_edit" || lineageCount != 1 {
		t.Fatalf("content=%q transformation=%s lineage=%d", content, transformation, lineageCount)
	}
	if _, err := service.Accept(authenticated, proposal.ID); err == nil {
		t.Fatal("accepted proposal was reviewed twice")
	}

	rejected, err := service.Create(authenticated, CreateCommand{WorkspaceID: one.WorkspaceID, MemoryType: core.SemanticMemory, Content: "A second distinct interpretation for explicit rejection.", Transformation: "interpretation", Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err = service.Reject(authenticated, rejected.ID)
	if err != nil || rejected.Status != core.ProposalRejected || reviewMemoryCount(t, ctx, pool, one.TenantID) != 1 {
		t.Fatalf("rejected=%+v count=%d err=%v", rejected, reviewMemoryCount(t, ctx, pool, one.TenantID), err)
	}
	if _, err := service.Create(authenticated, CreateCommand{WorkspaceID: one.WorkspaceID, MemoryType: core.SemanticMemory, Content: "Attempted cross tenant interpretation.", Transformation: "interpretation", Evidence: []EvidenceRef{{SourceID: twoSource, SourceVersion: 1, PassageID: "passage", CitationID: "citation"}}}); err == nil {
		t.Fatal("cross-tenant proposal evidence was accepted")
	}
}

func provisionReviewTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string, at time.Time) control.PersonalAccount {
	t.Helper()
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "review|" + label, VerifiedEmail: label + "@example.test", RequestID: uuid.NewString(), OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func seedReviewSource(t *testing.T, ctx context.Context, pool *pgxpool.Pool, account control.PersonalAccount, text string, at time.Time) string {
	t.Helper()
	sourceID, receiptID := uuid.NewString(), uuid.NewString()
	tx := beginReviewTenant(t, ctx, pool, account.TenantID)
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_attestation_receipts(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent)
		VALUES($1,$2,$3,'v1',$4,'[]',$5,$6,$7,'test')`, account.TenantID, receiptID, account.AccountID, strings.Repeat("a", 64), at, at.Add(30*24*time.Hour), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,active_version,created_at,updated_at)
		VALUES($1,$2,$3,'ready','lawfully_acquired_private_use',$4,1,$5,$5)`, account.TenantID, sourceID, account.WorkspaceID, receiptID, at); err != nil {
		t.Fatal(err)
	}
	checksum := strings.ReplaceAll(sourceID, "-", "") + strings.Repeat("0", 32)
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,published_at,created_at)
		VALUES($1,$2,1,$3,'text/plain','text-v1','text-v1',$4,$5,$5)`, account.TenantID, sourceID, checksum, "vault/"+sourceID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_nodes(tenant_id,source_id,source_version,id,kind,ordinal,title,start_offset,end_offset,explicit)
		VALUES($1,$2,1,'node','section',0,'Section',0,$3,true)`, account.TenantID, sourceID, len(text)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_passages(tenant_id,source_id,source_version,id,structural_node_id,text_content,fingerprint,locator)
		VALUES($1,$2,1,'passage','node',$3,$4,'{}')`, account.TenantID, sourceID, text, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_citations(tenant_id,source_id,source_version,id,passage_id,structural_node_id,passage_fingerprint,locator)
		VALUES($1,$2,1,'citation','passage','node',$3,'{}')`, account.TenantID, sourceID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return sourceID
}

func beginReviewTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) pgx.Tx {
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

func reviewMemoryCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) int {
	t.Helper()
	var count int
	if err := reviewTenantRow(ctx, pool, tenantID, `SELECT count(*) FROM saas_memories`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func reviewTenantRow(ctx context.Context, pool *pgxpool.Pool, tenantID, query string, args ...any) interface{ Scan(...any) error } {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return reviewErrorRow{err}
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return reviewErrorRow{err}
	}
	return &reviewTxRow{Row: tx.QueryRow(ctx, query, args...), tx: tx, ctx: ctx}
}

type reviewTxRow struct {
	pgx.Row
	tx  pgx.Tx
	ctx context.Context
}

func (r *reviewTxRow) Scan(values ...any) error {
	err := r.Row.Scan(values...)
	_ = r.tx.Rollback(r.ctx)
	return err
}

type reviewErrorRow struct{ err error }

func (r reviewErrorRow) Scan(...any) error { return r.err }
