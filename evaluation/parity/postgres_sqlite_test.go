package parity

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	hostedretrieval "github.com/taimufuraiyaa/agent-memory/internal/saas/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/search"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type parityEmbedding struct{}

func (parityEmbedding) Name() string         { return "parity-v1" }
func (parityEmbedding) ModelVersion() string { return "parity-v1" }
func (parityEmbedding) Dimension() int       { return 2 }
func (parityEmbedding) Embed(_ context.Context, text string) ([]float32, error) {
	text = strings.ToLower(text)
	switch {
	case strings.Contains(text, "obsolete"):
		return []float32{.95, .05}, nil
	case strings.Contains(text, "rollback"):
		return []float32{.8, .2}, nil
	default:
		return []float32{1, 0}, nil
	}
}
func (p parityEmbedding) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for index, text := range texts {
		result[index], _ = p.Embed(ctx, text)
	}
	return result, nil
}

type parityVectors struct{ sourceID string }

func (v parityVectors) SearchVectors(context.Context, string, []string, []float32, int) ([]search.VectorHit, error) {
	return []search.VectorHit{{SourceID: v.sourceID, SourceVersion: 1, PassageID: "a", Score: 1}, {SourceID: v.sourceID, SourceVersion: 1, PassageID: "b", Score: .9701425}, {SourceID: v.sourceID, SourceVersion: 1, PassageID: "c", Score: .9986178}}, nil
}

type parityModel struct{}

func (parityModel) Embed(context.Context, modelgateway.EmbedRequest) (modelgateway.EmbedResponse, error) {
	return modelgateway.EmbedResponse{Vectors: [][]float32{{1, 0}}, Dimensions: 2}, nil
}
func (parityModel) Generate(context.Context, modelgateway.GenerateRequest) (modelgateway.GenerateResponse, error) {
	return modelgateway.GenerateResponse{}, nil
}

func TestApprovedRetrievalDatasetPassesSQLitePostgresParity(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	now := time.Date(2026, 8, 5, 20, 0, 0, 0, time.UTC)
	local := localParityObservation(t, ctx, now)
	hosted := hostedParityObservation(t, ctx, connectionURL, now)
	report, err := Compare("retrieval-parity-v1", "portable-migration-v1", local, hosted, Thresholds{MinimumTopKOverlap: 1, MaximumScoreDelta: .30})
	if err != nil || !report.Passed {
		t.Fatalf("parity report=%+v\nlocal=%+v\nhosted=%+v\nerr=%v", report, local, hosted, err)
	}
	t.Logf("approved parity report=%+v local=%+v hosted=%+v", report, local, hosted)
	if report.MaxScoreDelta == 0 {
		t.Fatal("expected the report to preserve an explained score-model difference")
	}
}

func localParityObservation(t *testing.T, ctx context.Context, now time.Time) Observation {
	t.Helper()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "parity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedLocalCitations(t, ctx, store, now)
	until := now.Add(24 * time.Hour)
	entries := []*core.MemoryEntry{
		{ID: "a", Type: core.SemanticMemory, Content: "quartz deployment checklist requires backups", Workspace: "parity", Source: core.MemorySource{Type: core.SourceImport}, Confidence: .9, StorageTier: core.TierVector, SalienceScore: .2, UsefulCount: 2, LastHelpfulAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "b", Type: core.SemanticMemory, Content: "quartz deployment rollback procedure", Workspace: "parity", Source: core.MemorySource{Type: core.SourceImport}, Confidence: .9, StorageTier: core.TierVector, DecayScore: .7, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "c", Type: core.SemanticMemory, Content: "quartz deployment obsolete wrong order", Workspace: "parity", Source: core.MemorySource{Type: core.SourceImport}, Confidence: .9, StorageTier: core.TierVector, SuppressionScore: .9, RejectedCount: 2, LastRejectedAt: now.Add(-time.Hour), SuppressionUntil: &until, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
	}
	for _, entry := range entries {
		if err := store.UpsertMemory(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	searcher := engine.NewVectorSearcher(store, parityEmbedding{})
	retriever := engine.NewRetrievalEngineWithClock(searcher, func() time.Time { return now })
	low := 0.0
	result, err := retriever.Retrieve(ctx, engine.RetrievalOptions{Workspace: "parity", Query: "quartz deployment", TopK: 5, Mode: engine.ModeSearch, Policy: engine.RetrievalPolicy{MinSemanticScore: &low, MinTotalScore: &low, RelativeScoreCutoff: &low, WeakSemanticScore: &low, WeakTotalScore: &low, WeakRelativeCutoff: &low}})
	if err != nil {
		t.Fatal(err)
	}
	observation := Observation{Backend: "sqlite-local", Order: []string{}, NormalizedScores: map[string]float64{}, ExactTop: "", Suppressed: []string{}, ResolvedCitations: map[string]string{}}
	maximum := 0.0
	for _, hit := range result.Hits {
		observation.Order = append(observation.Order, hit.Memory.ID)
		observation.NormalizedScores[hit.Memory.ID] = hit.Score
		maximum = math.Max(maximum, hit.Score)
	}
	for id, value := range observation.NormalizedScores {
		observation.NormalizedScores[id] = value / maximum
	}
	if len(observation.Order) > 0 {
		observation.ExactTop = observation.Order[0]
	}
	for _, hit := range result.SuppressedHits {
		observation.Suppressed = append(observation.Suppressed, hit.Memory.ID)
	}
	for _, id := range []string{"a", "b"} {
		citation, err := store.GetCitation(ctx, "citation-"+id)
		if err != nil {
			observation.UnresolvedCitation++
			continue
		}
		observation.ResolvedCitations[id] = citation.ID
	}
	observation.FeedbackPreferred = indexOf(observation.Order, "a") < indexOf(observation.Order, "b")
	observation.DecayDemoted = observation.NormalizedScores["b"] < observation.NormalizedScores["a"]
	return observation
}

func seedLocalCitations(t *testing.T, ctx context.Context, store *sqlite.Store, now time.Time) {
	t.Helper()
	if err := store.PutBookWork(ctx, library.BookWork{ID: "work", Title: "Parity", NormalizedTitle: "parity"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBookEdition(ctx, library.BookEdition{ID: "edition", WorkID: "work", Label: "v1", Language: "en", ContentFingerprint: "sha256:edition"}); err != nil {
		t.Fatal(err)
	}
	policy := core.SourcePolicy{Retention: core.RetentionRetained, StoreNormalized: true, AllowSearch: true, AllowQuote: true, MaxQuoteRunes: 100}
	if err := store.PutSourceAsset(ctx, library.SourceAsset{ID: "asset", EditionID: "edition", Format: library.FormatText, ByteFingerprint: "sha256:bytes", NormalizedFingerprint: "sha256:normalized", ParserVersion: "parity-v1", Policy: policy, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceStructuralNodes(ctx, "edition", []library.StructuralNode{{ID: "node", EditionID: "edition", Kind: library.NodeSection, Ordinal: 0, Title: "Parity", EndOffset: 200, Explicit: true}}); err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{"a": "quartz deployment checklist requires backups", "b": "quartz deployment rollback procedure", "c": "quartz deployment obsolete wrong order"}
	for id, text := range texts {
		locator := core.SourceLocator{Kind: core.LocatorText, Display: "Parity " + id, ParserVersion: "parity-v1", NormalizationVersion: "parity-v1", Text: &core.TextLocator{SourceStart: 0, SourceEnd: len(text), NormalizedStart: 0, NormalizedEnd: len(text)}}
		fingerprint := core.FingerprintText(text)
		if err := store.PutPassages(ctx, []library.Passage{{ID: id, EditionID: "edition", SourceAssetID: "asset", StructuralNodeID: "node", Text: text, Locator: locator, Fingerprint: fingerprint}}); err != nil {
			t.Fatal(err)
		}
		if err := store.PutCitation(ctx, core.Citation{ID: "citation-" + id, EditionID: "edition", SourceAssetID: "asset", PassageID: id, StructuralNodeID: "node", Locator: locator, PassageFingerprint: fingerprint}); err != nil {
			t.Fatal(err)
		}
	}
}

func hostedParityObservation(t *testing.T, ctx context.Context, connectionURL string, now time.Time) Observation {
	t.Helper()
	pool, err := saaspostgres.Open(ctx, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = saaspostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "TRUNCATE saas_accounts CASCADE"); err != nil {
		t.Fatal(err)
	}
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "parity|v1", VerifiedEmail: "parity@example.test", RequestID: uuid.NewString(), OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	sourceID := seedHostedParity(t, ctx, pool, account, now)
	service, err := hostedretrieval.NewService(hostedretrieval.NewPostgresRepository(pool), parityVectors{sourceID: sourceID}, parityModel{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(ctx, hostedretrieval.Query{TenantID: account.TenantID, AuthorizedSourceIDs: []string{sourceID}, Text: "quartz deployment", Limit: 5, ContextTokenBudget: 100, Provider: "parity", Model: "parity-v1"})
	if err != nil {
		t.Fatal(err)
	}
	observation := Observation{Backend: "postgres-hosted", Order: []string{}, NormalizedScores: map[string]float64{}, Suppressed: []string{}, ResolvedCitations: map[string]string{}}
	maximum := 0.0
	for _, evidence := range result.Evidence {
		observation.Order = append(observation.Order, evidence.PassageID)
		observation.NormalizedScores[evidence.PassageID] = evidence.Score
		observation.ResolvedCitations[evidence.PassageID] = evidence.CitationID
		maximum = math.Max(maximum, evidence.Score)
	}
	for id, value := range observation.NormalizedScores {
		observation.NormalizedScores[id] = value / maximum
	}
	if len(observation.Order) > 0 {
		observation.ExactTop = observation.Order[0]
	}
	if indexOf(observation.Order, "c") < 0 {
		observation.Suppressed = []string{"c"}
	}
	observation.FeedbackPreferred = indexOf(observation.Order, "a") < indexOf(observation.Order, "b")
	observation.DecayDemoted = observation.NormalizedScores["b"] < observation.NormalizedScores["a"]
	return observation
}

func seedHostedParity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, account control.PersonalAccount, now time.Time) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", account.TenantID); err != nil {
		t.Fatal(err)
	}
	sourceID, receiptID := uuid.NewString(), uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO saas_attestation_receipts(tenant_id,id,subject_id,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at,request_id,user_agent) VALUES($1,$2,$3,'v1',$4,'[]',$5,$6,$7,'parity')`, account.TenantID, receiptID, account.AccountID, strings.Repeat("a", 64), now, now.Add(30*24*time.Hour), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_sources(tenant_id,id,workspace_id,state,rights_basis,attestation_receipt_id,active_version,created_at,updated_at) VALUES($1,$2,$3,'ready','lawfully_acquired_private_use',$4,1,$5,$5)`, account.TenantID, sourceID, account.WorkspaceID, receiptID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_source_versions(tenant_id,source_id,version,content_sha256,media_type,parser_version,normalization_version,vault_object_key,published_at,created_at) VALUES($1,$2,1,$3,'text/plain','parity-v1','parity-v1',$4,$5,$5)`, account.TenantID, sourceID, strings.Repeat("b", 64), "vault/"+sourceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_source_nodes(tenant_id,source_id,source_version,id,kind,ordinal,title,start_offset,end_offset,explicit) VALUES($1,$2,1,'node','section',0,'Parity',0,200,true)`, account.TenantID, sourceID); err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{"a": "quartz deployment checklist requires backups", "b": "quartz deployment rollback procedure", "c": "quartz deployment obsolete wrong order"}
	ids := []string{"a", "b", "c"}
	sort.Strings(ids)
	for _, id := range ids {
		text := texts[id]
		if _, err = tx.Exec(ctx, `INSERT INTO saas_source_passages(tenant_id,source_id,source_version,id,structural_node_id,text_content,fingerprint,locator) VALUES($1,$2,1,$3,'node',$4,$5,'{"page":1}')`, account.TenantID, sourceID, id, text, core.FingerprintText(text)); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO saas_source_citations(tenant_id,source_id,source_version,id,passage_id,structural_node_id,passage_fingerprint,locator) VALUES($1,$2,1,$3,$4,'node',$5,'{"page":1}')`, account.TenantID, sourceID, "citation-"+id, id, core.FingerprintText(text)); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO saas_fulltext_documents(tenant_id,source_id,source_version,passage_id,structural_node_id,text_content,locator,projected_at) VALUES($1,$2,1,$3,'node',$4,'{"page":1}',$5)`, account.TenantID, sourceID, id, text, now); err != nil {
			t.Fatal(err)
		}
	}
	until := now.Add(24 * time.Hour)
	if _, err = tx.Exec(ctx, `INSERT INTO saas_passage_signals(tenant_id,source_id,source_version,passage_id,decay_score,salience_score,suppression_score,useful_count,rejected_count,harmful_count,last_helpful_at,last_rejected_at,suppression_until,updated_at) VALUES
		($1,$2,1,'a',0,.2,0,2,0,0,$3,NULL,NULL,$3),($1,$2,1,'b',.7,0,0,0,0,0,NULL,NULL,NULL,$3),($1,$2,1,'c',0,0,.9,0,2,0,NULL,$3,$4,$3)`, account.TenantID, sourceID, now.Add(-time.Hour), until); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return sourceID
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
