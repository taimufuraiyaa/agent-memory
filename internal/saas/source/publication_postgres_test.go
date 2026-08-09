package source

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/attestationstore"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	searchservice "github.com/taimufuraiyaa/agent-memory/internal/saas/search"
)

func TestSourcePublicationIsAtomicVersionedAndIdempotent(t *testing.T) {
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
	now := time.Date(2026, 8, 5, 20, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(),
		ExternalSubject: "provider|publication", VerifiedEmail: "publication@example.test", RequestID: uuid.NewString(), OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := auth.RequestContext{AccountID: account.AccountID, TenantID: account.TenantID, RequestID: uuid.NewString(), TraceID: uuid.NewString()}
	attestations := attestation.NewService(attestationstore.NewPostgresStore(pool), attestation.WithClock(func() time.Time { return now }))
	policy := attestation.CurrentPolicy()
	statementIDs := make([]string, 0, len(policy.Statements))
	for _, statement := range policy.Statements {
		statementIDs = append(statementIDs, statement.ID)
	}
	receipt, err := attestations.Accept(auth.WithRequestContext(ctx, request), account.AccountID, attestation.AcceptCommand{PolicyVersion: policy.Version, AcceptedStatementIDs: statementIDs, RequestID: request.RequestID})
	if err != nil {
		t.Fatal(err)
	}

	repository := NewPostgresRepository(pool)
	firstSource := uuid.NewString()
	insertProcessingSource(t, ctx, pool, account.TenantID, account.WorkspaceID, receipt.Receipt.ID, firstSource, "same-content", now)
	claim, err := repository.ClaimExtraction(ctx, account.TenantID, now, time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	firstExtraction := markdownExtraction(t, firstSource, "# One\nAtomic publication content.")
	if err := repository.PublishExtraction(ctx, *claim, firstExtraction, now); err != nil {
		t.Fatal(err)
	}
	if err := repository.PublishExtraction(ctx, *claim, firstExtraction, now.Add(time.Second)); err != nil {
		t.Fatalf("identical retry was not idempotent: %v", err)
	}
	assertPublishedCorpus(t, ctx, pool, account.TenantID, firstSource, len(firstExtraction.Nodes), len(firstExtraction.Passages))

	secondSource := uuid.NewString()
	insertProcessingSource(t, ctx, pool, account.TenantID, account.WorkspaceID, receipt.Receipt.ID, secondSource, "same-content", now)
	secondClaim, err := repository.ClaimExtraction(ctx, account.TenantID, now, time.Minute)
	if err != nil || secondClaim == nil {
		t.Fatalf("duplicate content claim=%+v err=%v", secondClaim, err)
	}
	secondExtraction := markdownExtraction(t, secondSource, "# One\nAtomic publication content.")
	if err := repository.PublishExtraction(ctx, *secondClaim, secondExtraction, now); err != nil {
		t.Fatalf("tenant-local re-import failed: %v", err)
	}
	assertPublishedCorpus(t, ctx, pool, account.TenantID, secondSource, len(secondExtraction.Nodes), len(secondExtraction.Passages))
	projector, err := searchservice.NewFullTextProjector(searchservice.NewPostgresRepository(pool), func() time.Time { return now.Add(2 * time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if count, err := projector.ProcessOnce(ctx); err != nil || count != 1 {
			t.Fatalf("projection count=%d err=%v", count, err)
		}
	}
	if count, err := projector.ProcessOnce(ctx); err != nil || count != 0 {
		t.Fatalf("idempotent projection count=%d err=%v", count, err)
	}
	stats, err := searchservice.NewPostgresRepository(pool).FullTextProjectionStats(ctx, account.TenantID, searchservice.FullTextProjectionVersion)
	if err != nil || stats.Ready != 2 || stats.Pending != 0 || stats.Stale != 0 {
		t.Fatalf("projection stats=%+v err=%v", stats, err)
	}
	if err := projector.Rebuild(ctx, account.TenantID, firstSource); err != nil {
		t.Fatal(err)
	}
	if count, err := projector.ProcessOnce(ctx); err != nil || count != 1 {
		t.Fatalf("rebuild projection count=%d err=%v", count, err)
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `INSERT INTO saas_source_versions
		(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,vault_encryption_version,published_at,created_at)
		SELECT tenant_id,source_id,2,content_sha256||'-v2',media_type,parser_version,normalization_version,vault_object_key,vault_encryption_version,$2,$2
		FROM saas_source_versions WHERE source_id=$1 AND version=1`, firstSource, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `INSERT INTO saas_source_nodes
		(tenant_id,source_id,source_version,id,parent_id,kind,ordinal,title,start_offset,end_offset,explicit)
		SELECT tenant_id,source_id,2,id,parent_id,kind,ordinal,title,start_offset,end_offset,explicit FROM saas_source_nodes WHERE source_id=$1 AND source_version=1`, firstSource); err != nil {
		t.Fatal(err)
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `INSERT INTO saas_source_passages
		(tenant_id,source_id,source_version,id,structural_node_id,text_content,fingerprint,locator)
		SELECT tenant_id,source_id,2,id,structural_node_id,text_content,fingerprint,locator FROM saas_source_passages WHERE source_id=$1 AND source_version=1`, firstSource); err != nil {
		t.Fatal(err)
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `UPDATE saas_sources SET active_version=2,state='indexing' WHERE id=$1`, firstSource); err != nil {
		t.Fatal(err)
	}
	if count, err := projector.ProcessOnce(ctx); err != nil || count != 1 {
		t.Fatalf("stale-version replacement count=%d err=%v", count, err)
	}
	var minVersion, maxVersion int64
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT min(source_version),max(source_version) FROM saas_fulltext_documents WHERE source_id=$1`, firstSource).Scan(&minVersion, &maxVersion); err != nil || minVersion != 2 || maxVersion != 2 {
		t.Fatalf("stale projection survived min=%d max=%d err=%v", minVersion, maxVersion, err)
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `UPDATE saas_sources SET state='disabled' WHERE id=$1`, firstSource); err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var firstDocuments int
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT count(*) FROM saas_fulltext_documents WHERE source_id=$1`, firstSource).Scan(&firstDocuments); err != nil || firstDocuments != 0 {
		t.Fatalf("disabled source documents=%d err=%v", firstDocuments, err)
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `UPDATE saas_sources SET state='indexing' WHERE id=$1`, firstSource); err != nil {
		t.Fatal(err)
	}
	if err := projector.Rebuild(ctx, account.TenantID, firstSource); err != nil {
		t.Fatal(err)
	}
	if count, err := projector.ProcessOnce(ctx); err != nil || count != 1 {
		t.Fatalf("pre-deletion rebuild count=%d err=%v", count, err)
	}
	if _, err := tenantExec(ctx, pool, account.TenantID, `UPDATE saas_sources SET state='deleting' WHERE id=$1`, firstSource); err != nil {
		t.Fatal(err)
	}
	if _, err := projector.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT count(*) FROM saas_fulltext_documents WHERE source_id=$1`, firstSource).Scan(&firstDocuments); err != nil || firstDocuments != 0 {
		t.Fatalf("deleting source documents=%d err=%v", firstDocuments, err)
	}

	failedSource := uuid.NewString()
	insertProcessingSource(t, ctx, pool, account.TenantID, account.WorkspaceID, receipt.Receipt.ID, failedSource, "failed-content", now)
	failedClaim, err := repository.ClaimExtraction(ctx, account.TenantID, now, time.Minute)
	if err != nil || failedClaim == nil {
		t.Fatalf("failure claim=%+v err=%v", failedClaim, err)
	}
	invalid := markdownExtraction(t, failedSource, "# Broken\nMust never become visible.")
	invalid.Passages[0].StructuralNodeID = "missing-node"
	if err := repository.PublishExtraction(ctx, *failedClaim, invalid, now); err == nil {
		t.Fatal("publication with invalid citation structure unexpectedly succeeded")
	}
	var nodes, passages, citations int
	var publishedAt *time.Time
	if err := tenantQuery(ctx, pool, account.TenantID, `SELECT
		(SELECT count(*) FROM saas_source_nodes WHERE source_id=$1),
		(SELECT count(*) FROM saas_source_passages WHERE source_id=$1),
		(SELECT count(*) FROM saas_source_citations WHERE source_id=$1),
		(SELECT published_at FROM saas_source_versions WHERE source_id=$1 AND version=1)`, failedSource).Scan(&nodes, &passages, &citations, &publishedAt); err != nil {
		t.Fatal(err)
	}
	if nodes != 0 || passages != 0 || citations != 0 || publishedAt != nil {
		t.Fatalf("partial corpus escaped rollback nodes=%d passages=%d citations=%d published=%v", nodes, passages, citations, publishedAt)
	}
}

func insertProcessingSource(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, tenantID, workspaceID, receiptID, sourceID, hash string, at time.Time) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,active_version,created_at,updated_at)
		VALUES($1,$2,$3,'processing','owned_copy',$4,1,$5,$5)`, tenantID, sourceID, workspaceID, receiptID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,vault_encryption_version,created_at)
		VALUES($1,$2,1,$3,'text/markdown','pending','pending',$4,'aes-256-gcm-v1',$5)`, tenantID, sourceID, hash, "vault/"+tenantID+"/"+sourceID+"/1.aesgcm", at); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func markdownExtraction(t *testing.T, sourceID, content string) ingestion.BookExtraction {
	t.Helper()
	editionID := sourceID + ":v1"
	value, err := (ingestion.MarkdownBookExtractor{Adapter: ingestion.MarkdownAdapter{ParserVersion: ParserMarkdownV1, NormalizationVersion: NormalizationTextV1}}).Extract(context.Background(), editionID, sourceID, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertPublishedCorpus(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, tenantID, sourceID string, wantNodes, wantPassages int) {
	t.Helper()
	var state, parserVersion, normalizationVersion string
	var nodes, passages, citations, indexingEvents int
	err := tenantQuery(ctx, pool, tenantID, `SELECT s.state,v.parser_version,v.normalization_version,
		(SELECT count(*) FROM saas_source_nodes WHERE source_id=s.id),
		(SELECT count(*) FROM saas_source_passages WHERE source_id=s.id),
		(SELECT count(*) FROM saas_source_citations WHERE source_id=s.id),
		(SELECT count(*) FROM saas_outbox WHERE aggregate_id=s.id AND event_type='source.indexing_requested')
		FROM saas_sources s JOIN saas_source_versions v ON v.tenant_id=s.tenant_id AND v.source_id=s.id AND v.version=1 WHERE s.id=$1`, sourceID).
		Scan(&state, &parserVersion, &normalizationVersion, &nodes, &passages, &citations, &indexingEvents)
	if err != nil {
		t.Fatal(err)
	}
	if state != "indexing" || parserVersion != ParserMarkdownV1 || normalizationVersion != NormalizationTextV1 || nodes != wantNodes || passages != wantPassages || citations != wantPassages || indexingEvents != 1 {
		t.Fatalf("state=%s parser=%s normalization=%s nodes=%d passages=%d citations=%d events=%d", state, parserVersion, normalizationVersion, nodes, passages, citations, indexingEvents)
	}
}
