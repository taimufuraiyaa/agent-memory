package isolation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/search"
)

const (
	localSearchTarget = 800 * time.Millisecond
	timingDeltaLimit  = 100 * time.Millisecond
	loadConcurrency   = 8
	queriesPerWorker  = 25
)

type corpusTenant struct {
	account  control.PersonalAccount
	sourceID string
	marker   string
}

type generationDisabledModels struct {
	embedCalls    atomic.Int64
	generateCalls atomic.Int64
}

func (m *generationDisabledModels) Embed(context.Context, modelgateway.EmbedRequest) (modelgateway.EmbedResponse, error) {
	m.embedCalls.Add(1)
	return modelgateway.EmbedResponse{}, errors.New("external embedding is disabled for local isolation load")
}

func (m *generationDisabledModels) Generate(context.Context, modelgateway.GenerateRequest) (modelgateway.GenerateResponse, error) {
	m.generateCalls.Add(1)
	return modelgateway.GenerateResponse{}, errors.New("generation is disabled for local isolation load")
}

type noVectorSearch struct{}

func (noVectorSearch) SearchVectors(context.Context, string, []string, []float32, int) ([]search.VectorHit, error) {
	return nil, errors.New("vector search must not run when local embedding is disabled")
}

func TestTwoTenantAdversarialAndBoundedRetrievalLoad(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

	at := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	one := seedCorpusTenant(t, ctx, pool, "atlasmarker", 128, at)
	two := seedCorpusTenant(t, ctx, pool, "nebulamarker", 384, at)
	models := &generationDisabledModels{}
	service, err := retrieval.NewService(retrieval.NewPostgresRepository(pool), noVectorSearch{}, models, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}

	assertOwnEvidence(t, ctx, service, one)
	assertOwnEvidence(t, ctx, service, two)
	if _, err := runQuery(ctx, service, one.account.TenantID, two.sourceID, one.marker); err == nil {
		t.Fatal("cross-tenant source substitution was accepted")
	}
	assertNoEvidence(t, ctx, service, one, two.marker)
	for range 16 {
		assertOwnEvidence(t, ctx, service, two)
		assertNoEvidence(t, ctx, service, one, two.marker)
	}

	presentElsewhere := make([]time.Duration, 0, 40)
	absentEverywhere := make([]time.Duration, 0, 40)
	for index := range 40 {
		if index%2 == 0 {
			presentElsewhere = append(presentElsewhere, timedMiss(t, ctx, service, one, two.marker))
			absentEverywhere = append(absentEverywhere, timedMiss(t, ctx, service, one, "globallyabsentmarker"))
		} else {
			absentEverywhere = append(absentEverywhere, timedMiss(t, ctx, service, one, "globallyabsentmarker"))
			presentElsewhere = append(presentElsewhere, timedMiss(t, ctx, service, one, two.marker))
		}
	}
	presentP95 := percentile95(presentElsewhere)
	absentP95 := percentile95(absentEverywhere)
	delta := absoluteDuration(presentP95 - absentP95)
	if presentP95 > localSearchTarget || absentP95 > localSearchTarget || delta > timingDeltaLimit {
		t.Fatal("local cross-tenant miss timing exceeded the regression envelope")
	}
	t.Logf("two_tenant_adversarial timing_samples=%d cross_tenant_results=0 cache_leaks=0 timing_delta_ms=%.3f", len(presentElsewhere)+len(absentEverywhere), milliseconds(delta))

	durations, loadErrors := runLoad(ctx, service, one, two)
	loadP95 := percentile95(durations)
	if loadErrors != 0 {
		t.Fatal("bounded retrieval load returned errors or invalid evidence")
	}
	if len(durations) != loadConcurrency*queriesPerWorker || loadP95 > localSearchTarget {
		t.Fatal("bounded retrieval load exceeded its request or latency target")
	}
	if models.generateCalls.Load() != 0 {
		t.Fatal("generation ran during the generation-disabled load rehearsal")
	}
	t.Logf("bounded_load queries=%d concurrency=%d errors=0 p95_ms=%.3f generation_calls=0", len(durations), loadConcurrency, milliseconds(loadP95))
}

func runLoad(ctx context.Context, service *retrieval.Service, one, two corpusTenant) ([]time.Duration, int) {
	durations := make(chan time.Duration, loadConcurrency*queriesPerWorker)
	errorsFound := make(chan struct{}, loadConcurrency*queriesPerWorker)
	var workers sync.WaitGroup
	for worker := range loadConcurrency {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for request := range queriesPerWorker {
				tenant := one
				if (worker+request)%2 == 1 {
					tenant = two
				}
				started := time.Now()
				result, err := runQuery(ctx, service, tenant.account.TenantID, tenant.sourceID, tenant.marker)
				durations <- time.Since(started)
				if err != nil || !validEvidence(result, tenant.sourceID) {
					errorsFound <- struct{}{}
				}
			}
		}(worker)
	}
	workers.Wait()
	close(durations)
	close(errorsFound)
	values := make([]time.Duration, 0, loadConcurrency*queriesPerWorker)
	for duration := range durations {
		values = append(values, duration)
	}
	return values, len(errorsFound)
}

func assertOwnEvidence(t *testing.T, ctx context.Context, service *retrieval.Service, tenant corpusTenant) {
	t.Helper()
	result, err := runQuery(ctx, service, tenant.account.TenantID, tenant.sourceID, tenant.marker)
	if err != nil || !validEvidence(result, tenant.sourceID) {
		t.Fatal("own-tenant retrieval did not return only authorized evidence")
	}
}

func assertNoEvidence(t *testing.T, ctx context.Context, service *retrieval.Service, tenant corpusTenant, marker string) {
	t.Helper()
	result, err := runQuery(ctx, service, tenant.account.TenantID, tenant.sourceID, marker)
	if err != nil || result.Answerable || len(result.Evidence) != 0 || len(result.Context.IncludedIDs) != 0 {
		t.Fatal("cross-tenant marker changed the public result or count")
	}
}

func timedMiss(t *testing.T, ctx context.Context, service *retrieval.Service, tenant corpusTenant, marker string) time.Duration {
	t.Helper()
	started := time.Now()
	assertNoEvidence(t, ctx, service, tenant, marker)
	return time.Since(started)
}

func runQuery(ctx context.Context, service *retrieval.Service, tenantID, sourceID, marker string) (retrieval.Result, error) {
	return service.Query(ctx, retrieval.Query{
		TenantID: tenantID, AuthorizedSourceIDs: []string{sourceID}, Text: marker,
		Limit: 10, ContextTokenBudget: 400, Generate: false, Provider: "local-disabled", Model: "local-disabled",
	})
}

func validEvidence(result retrieval.Result, sourceID string) bool {
	if !result.Answerable || len(result.Evidence) == 0 || len(result.Evidence) != len(result.Context.IncludedIDs) {
		return false
	}
	for _, evidence := range result.Evidence {
		if evidence.SourceID != sourceID {
			return false
		}
	}
	return true
}

func seedCorpusTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, marker string, passages int, at time.Time) corpusTenant {
	t.Helper()
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{
		AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(),
		ExternalSubject: "isolation|" + marker, VerifiedEmail: marker + "@example.test", RequestID: uuid.NewString(), OccurredAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceID, receiptID := uuid.NewString(), uuid.NewString()
	tx := beginTenant(t, ctx, pool, account.TenantID)
	defer func() { _ = tx.Rollback(ctx) }()
	contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(marker)))
	if _, err := tx.Exec(ctx, `INSERT INTO saas_attestation_receipts(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent)
		VALUES($1,$2,$3,'isolation-v1',$4,'[]',$5,$6,$7,'local-evaluation')`, account.TenantID, receiptID, account.AccountID, contentHash, at, at.Add(24*time.Hour), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,active_version,created_at,updated_at)
		VALUES($1,$2,$3,'ready','lawfully_acquired_private_use',$4,1,$5,$5)`, account.TenantID, sourceID, account.WorkspaceID, receiptID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,published_at,created_at)
		VALUES($1,$2,1,$3,'text/plain','isolation-v1','isolation-v1',$4,$5,$5)`, account.TenantID, sourceID, contentHash, "isolated/"+sourceID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_source_nodes(tenant_id,source_id,source_version,id,kind,ordinal,title,start_offset,end_offset,explicit)
		VALUES($1,$2,1,'shared-node','section',0,'Section',0,$3,true)`, account.TenantID, sourceID, passages*64); err != nil {
		t.Fatal(err)
	}
	batch := &pgx.Batch{}
	for index := range passages {
		passageID := fmt.Sprintf("shared-passage-%04d", index)
		citationID := fmt.Sprintf("shared-citation-%04d", index)
		text := fmt.Sprintf("%s authorized private evidence item %04d", marker, index)
		fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		batch.Queue(`INSERT INTO saas_source_passages(tenant_id,source_id,source_version,id,structural_node_id,text_content,fingerprint,locator)
			VALUES($1,$2,1,$3,'shared-node',$4,$5,'{"ordinal":0}')`, account.TenantID, sourceID, passageID, text, fingerprint)
		batch.Queue(`INSERT INTO saas_source_citations(tenant_id,source_id,source_version,id,passage_id,structural_node_id,passage_fingerprint,locator)
			VALUES($1,$2,1,$3,$4,'shared-node',$5,'{"ordinal":0}')`, account.TenantID, sourceID, citationID, passageID, fingerprint)
		batch.Queue(`INSERT INTO saas_fulltext_documents(tenant_id,source_id,source_version,passage_id,structural_node_id,text_content,locator,projected_at)
			VALUES($1,$2,1,$3,'shared-node',$4,'{"ordinal":0}',$5)`, account.TenantID, sourceID, passageID, text, at)
	}
	results := tx.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return corpusTenant{account: account, sourceID: sourceID, marker: marker}
}

func beginTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) pgx.Tx {
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

func percentile95(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return ordered[(len(ordered)-1)*95/100]
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func milliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}
