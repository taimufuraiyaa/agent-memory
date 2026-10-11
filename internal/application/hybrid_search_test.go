package application

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/locator"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestHybridSearchRetainsFreshUnindexedDirectMemoryAlongsideGraphDiscoveries(t *testing.T) {
	f := newHybridSearchFixture(t)
	pricing := f.memory("pricing", "Pricing decision uses benchmark assumptions", core.SemanticMemory, 0.9, []float32{1, 0})
	expense := f.memory("expense", "Fleet expense grows with instance count", core.SemanticMemory, 0.9, []float32{0.2, float32(math.Sqrt(0.96))})
	f.activate(pricing, expense)
	fresh := f.memory("fresh", "Pricing latest throughput correction published after indexing", core.SemanticMemory, 0.9, []float32{0.95, float32(math.Sqrt(1 - 0.95*0.95))})

	result, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{
		Retrieval: f.options("How does pricing depend on operating assumptions?", 8),
		GraphMode: graphretrieval.GraphQueryLocal, GraphAllowStale: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHybridSearchHasMemories(t, result.Hits, pricing.ID, expense.ID, fresh.ID)
	if result.GraphRoute.SelectedMode != graphretrieval.GraphQueryLocal || result.GraphRoute.Fallback {
		t.Fatalf("graph discovery did not run alongside direct search: %+v", result.GraphRoute)
	}
	if result.GraphContext == nil || result.GraphContext.Fresh || result.GraphContext.Local == nil || len(result.GraphContext.Local.Paths) == 0 {
		t.Fatalf("expected a stale indexed graph with connected evidence: %+v", result.GraphContext)
	}
	if result.RequestID == "" {
		t.Fatal("hybrid search did not expose a retrieval request ID")
	}
}

func TestHybridSearchResolvesGraphOnlyEvidenceBelowSemanticFloor(t *testing.T) {
	f := newHybridSearchFixture(t)
	pricing := f.memory("pricing", "Pricing decision depends on measured capacity", core.SemanticMemory, 0.9, []float32{1, 0})
	expense := f.memory("expense", "Fleet expense grows with instance count", core.SemanticMemory, 0.9, []float32{0.2, float32(math.Sqrt(0.96))})
	f.activate(pricing, expense)
	options := f.options("pricing", 5)
	floor := 0.5
	options.Policy.MinSemanticScore = &floor
	basic, err := f.service.Search(f.ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if hybridSearchHit(basic.Hits, expense.ID) != nil {
		t.Fatal("fixture did not isolate graph-only evidence from basic semantic search")
	}

	result, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{Retrieval: options, GraphMode: graphretrieval.GraphQueryLocal, GraphRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	assertHybridSearchHasMemories(t, result.Hits, pricing.ID, expense.ID)
	hit := hybridSearchHit(result.Hits, expense.ID)
	if hit.Memory.Content != expense.Content {
		t.Fatalf("related evidence was not fetched as original canonical content: %+v", hit.Memory)
	}
	if math.Abs(hit.Breakdown.Semantic-0.2) > 0.01 {
		t.Fatalf("related evidence must use actual query similarity despite its semantic floor: %+v", hit.Breakdown)
	}
	if result.GraphContext == nil || result.GraphContext.RevisionID != f.revisionID || !result.GraphContext.Fresh || result.GraphContext.Local == nil || len(result.GraphContext.Local.Paths) == 0 {
		t.Fatalf("expected revision-bound graph evidence and paths: %+v", result.GraphContext)
	}
}

func TestHybridSearchRetainsIndependentExactTermCandidate(t *testing.T) {
	f := newHybridSearchFixture(t)
	pricing := f.memory("pricing", "Pricing decision depends on measured capacity", core.SemanticMemory, 0.9, []float32{1, 0})
	expense := f.memory("expense", "Fleet expense grows with instance count", core.SemanticMemory, 0.9, []float32{0.2, float32(math.Sqrt(0.96))})
	termOnly := f.memory("term-only", "Pricing exception approved for the ledger migration", core.SemanticMemory, 0.9, []float32{0, 1})
	termOnly.Keywords = []core.MemoryTerm{{Term: "pricing", Display: "pricing", Source: core.TermSourceExplicit, NormalizationVersion: locator.NormalizationVersion, ExtractorVersion: locator.ExtractorVersion}}
	if err := f.store.UpsertMemory(f.ctx, &termOnly); err != nil {
		t.Fatal(err)
	}
	f.activate(pricing, expense)

	result, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{Retrieval: f.options("pricing", 8), GraphMode: graphretrieval.GraphQueryLocal})
	if err != nil {
		t.Fatal(err)
	}
	assertHybridSearchHasMemories(t, result.Hits, pricing.ID, expense.ID, termOnly.ID)
	if result.GraphRoute.SelectedMode != graphretrieval.GraphQueryLocal || result.GraphRoute.Fallback {
		t.Fatalf("expected exact terms to supplement a working graph: %+v", result.GraphRoute)
	}
}

func TestHybridSearchRejectsInvalidCanonicalGraphEvidence(t *testing.T) {
	for _, mutation := range []string{"changed", "deleted", "superseded", "cross-workspace", "suppressed", "cold"} {
		t.Run(mutation, func(t *testing.T) {
			f := newHybridSearchFixture(t)
			pricing := f.memory("pricing", "Pricing decision depends on measured capacity", core.SemanticMemory, 0.9, []float32{1, 0})
			valid := f.memory("valid", "Fleet expense grows with instance count", core.SemanticMemory, 0.9, []float32{0.2, float32(math.Sqrt(0.96))})
			invalid := f.memory("invalid", "Unrelated historical sizing assumption", core.SemanticMemory, 0.9, []float32{0, 1})
			if mutation == "cross-workspace" {
				invalid.Workspace = "other-workspace"
				if err := f.store.UpsertMemory(f.ctx, &invalid); err != nil {
					t.Fatal(err)
				}
			}
			f.activate(pricing, valid, invalid)
			switch mutation {
			case "changed":
				invalid.Content = "Revised historical sizing assumption"
				if err := f.store.UpsertMemory(f.ctx, &invalid); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := f.store.DeleteByIDs(f.ctx, []string{invalid.ID}); err != nil {
					t.Fatal(err)
				}
			case "superseded":
				successor := valid.ID
				invalid.SupersededBy = &successor
				if err := f.store.UpsertMemory(f.ctx, &invalid); err != nil {
					t.Fatal(err)
				}
			case "suppressed":
				if _, err := f.store.ApplyRetrievalFeedback(f.ctx, invalid.ID, core.FeedbackHarmful, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			case "cold":
				invalid.StorageTier = core.TierCold
				if err := f.store.UpsertMemory(f.ctx, &invalid); err != nil {
					t.Fatal(err)
				}
			}

			result, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{
				Retrieval: f.options("pricing", 8), GraphMode: graphretrieval.GraphQueryLocal, GraphAllowStale: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertHybridSearchHasMemories(t, result.Hits, pricing.ID, valid.ID)
			if hybridSearchHit(result.Hits, invalid.ID) != nil {
				t.Fatalf("%s graph reference resurrected unauthorized memory: %+v", mutation, result.Hits)
			}
			if result.GraphContext == nil || result.GraphContext.Local == nil {
				t.Fatalf("valid graph discoveries disappeared with an invalid reference: %+v", result.GraphContext)
			}
			for _, id := range result.GraphContext.CanonicalIDs {
				if id == invalid.ID {
					t.Fatalf("%s memory remained in graph canonical evidence: %+v", mutation, result.GraphContext)
				}
			}
			for _, path := range result.GraphContext.Local.Paths {
				for _, evidence := range path.Evidence {
					if evidence.CanonicalID == invalid.ID {
						t.Fatalf("%s memory remained in a returned evidence path: %+v", mutation, path)
					}
				}
			}
		})
	}
}

func TestHybridSearchGlobalDoesNotReturnReportAfterAnyEvidenceBecomesInvalid(t *testing.T) {
	store, cfg, runtime := nativeWorkerFixture(t)
	ctx := context.Background()
	valid := nativeWorkerMemory(t, store, cfg.Scope.WorkspaceID, "Pricing depends on runtime")
	invalid := nativeWorkerMemory(t, store, cfg.Scope.WorkspaceID, "Historical cost evidence")
	worker := nativeTestWorker(runtime, &nativeArtifactRunner{root: runtime.JobRoot})
	if err := worker.RunWorkspace(ctx, store, cfg.Scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteByIDs(ctx, []string{invalid.ID}); err != nil {
		t.Fatal(err)
	}
	provider := &hybridSearchEmbeddingProvider{vectors: map[string][]float32{valid.Content: {1, 0}}}
	service := NewMemoryService(store, nil, engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider)))
	result, err := service.SearchHybrid(ctx, GraphSearchOptions{Retrieval: engine.RetrievalOptions{Workspace: cfg.Scope.WorkspaceID, Query: "pricing across all memories", TopK: 4, Mode: engine.ModeSearch}, GraphMode: graphretrieval.GraphQueryGlobal, GraphAllowStale: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.GraphContext != nil && result.GraphContext.Global != nil && len(result.GraphContext.Global.Communities) > 0 {
		t.Fatalf("partially invalid report remained visible: %+v", result.GraphContext.Global)
	}
	assertHybridSearchHasMemories(t, result.Hits, valid.ID)
}

func TestHybridSearchAppliesFiltersToGraphDiscoveriesAndHonorsTopK(t *testing.T) {
	f := newHybridSearchFixture(t)
	pricing := f.memory("pricing", "Pricing decision depends on measured capacity", core.SemanticMemory, 0.9, []float32{1, 0})
	expense := f.memory("expense", "Fleet expense grows with instance count", core.SemanticMemory, 0.9, []float32{0.2, float32(math.Sqrt(0.96))})
	wrongType := f.memory("procedure", "Restart the capacity worker", core.ProceduralMemory, 0.95, []float32{0, 1})
	lowConfidence := f.memory("uncertain", "Unverified instance sizing assumption", core.SemanticMemory, 0.1, []float32{0, 1})
	f.activate(pricing, expense, wrongType, lowConfidence)
	options := f.options("pricing", 2)
	confidence := 0.8
	options.Filters = engine.RetrievalFilters{Types: []core.MemoryType{core.SemanticMemory}, MinConfidence: &confidence}

	result, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{Retrieval: options, GraphMode: graphretrieval.GraphQueryLocal})
	if err != nil {
		t.Fatal(err)
	}
	assertHybridSearchHasMemories(t, result.Hits, pricing.ID, expense.ID)
	if len(result.Hits) != 2 {
		t.Fatalf("hybrid union did not enforce top-k: %+v", result.Hits)
	}
	for _, hit := range result.Hits {
		if hit.Memory.Type != core.SemanticMemory || hit.Memory.Confidence < confidence {
			t.Fatalf("graph-discovered memory bypassed caller filters: %+v", hit.Memory)
		}
	}
	options.TopK = 1
	limited, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{Retrieval: options, GraphMode: graphretrieval.GraphQueryLocal})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Hits) != 1 {
		t.Fatalf("top-k=1 retained %d merged hits", len(limited.Hits))
	}
}

func TestHybridSearchOptionalFallbackAndRequiredGraphFailure(t *testing.T) {
	for _, state := range []string{"unavailable", "stale"} {
		t.Run(state, func(t *testing.T) {
			f := newHybridSearchFixture(t)
			pricing := f.memory("pricing", "Pricing decision depends on measured capacity", core.SemanticMemory, 0.9, []float32{1, 0})
			expense := f.memory("expense", "Fleet expense grows with instance count", core.SemanticMemory, 0.9, []float32{0.2, float32(math.Sqrt(0.96))})
			wantReason := graphretrieval.GraphReasonIndexUnavailable
			if state == "stale" {
				f.activate(pricing, expense)
				f.memory("fresh", "Pricing correction after indexing", core.SemanticMemory, 0.9, []float32{1, 0})
				wantReason = graphretrieval.GraphReasonIndexStale
			}
			options := GraphSearchOptions{Retrieval: f.options("pricing", 8), GraphMode: graphretrieval.GraphQueryLocal}

			result, err := f.service.SearchHybrid(f.ctx, options)
			if err != nil {
				t.Fatalf("optional graph prevented usable direct retrieval: %v", err)
			}
			assertHybridSearchHasMemories(t, result.Hits, pricing.ID)
			if result.GraphRoute.SelectedMode != graphretrieval.GraphQueryBasic || !result.GraphRoute.Fallback || !result.GraphRoute.Degraded || result.GraphRoute.ReasonCode != wantReason {
				t.Fatalf("%s graph fallback was not visible: %+v", state, result.GraphRoute)
			}
			if hybridSearchHit(result.Hits, expense.ID) != nil {
				t.Fatal("fallback used graph-only evidence without stale permission")
			}
			options.GraphRequired = true
			_, err = f.service.SearchHybrid(f.ctx, options)
			if !errors.Is(err, graphretrieval.ErrGraphRouteRequired) {
				t.Fatalf("required %s graph did not fail clearly: %v", state, err)
			}
		})
	}
}

func TestHybridSearchAutoUsesGraphForRelationshipsAndBasicForDirectLookup(t *testing.T) {
	f := newHybridSearchFixture(t)
	pricing := f.memory("pricing", "Pricing decision depends on measured capacity", core.SemanticMemory, 0.9, []float32{1, 0})
	expense := f.memory("expense", "Fleet expense grows with instance count", core.SemanticMemory, 0.9, []float32{0.2, float32(math.Sqrt(0.96))})
	f.activate(pricing, expense)

	related, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{Retrieval: f.options("How does pricing depend on operating assumptions?", 8), GraphMode: graphretrieval.GraphQueryAuto})
	if err != nil {
		t.Fatal(err)
	}
	if related.GraphRoute.SelectedMode != graphretrieval.GraphQueryLocal {
		t.Fatalf("relational question did not select graph discovery: %+v", related.GraphRoute)
	}
	assertHybridSearchHasMemories(t, related.Hits, expense.ID)
	direct, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{Retrieval: f.options("pricing", 8), GraphMode: graphretrieval.GraphQueryAuto})
	if err != nil {
		t.Fatal(err)
	}
	if direct.GraphRoute.SelectedMode != graphretrieval.GraphQueryBasic || direct.GraphRoute.Fallback {
		t.Fatalf("direct lookup was not a normal basic route: %+v", direct.GraphRoute)
	}
	assertHybridSearchHasMemories(t, direct.Hits, pricing.ID)
	if hybridSearchHit(direct.Hits, expense.ID) != nil {
		t.Fatal("direct auto lookup unexpectedly traversed graph-only evidence")
	}
}

func TestHybridSearchReportsFallbackWhenGraphFindsNoRelationshipEvidence(t *testing.T) {
	f := newHybridSearchFixture(t)
	pricing := f.memory("pricing", "Pricing decision", core.SemanticMemory, 0.9, []float32{1, 0})
	f.activate(pricing)
	options := GraphSearchOptions{Retrieval: f.options("How does pricing depend on runtime?", 4), GraphMode: graphretrieval.GraphQueryLocal}
	result, err := f.service.SearchHybrid(f.ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	assertHybridSearchHasMemories(t, result.Hits, pricing.ID)
	if !result.GraphRoute.Fallback || result.GraphRoute.SelectedMode != graphretrieval.GraphQueryBasic || result.GraphRoute.ReasonCode != "graph_no_evidence" {
		t.Fatalf("empty graph discovery was not visible: %+v", result.GraphRoute)
	}
	options.GraphRequired = true
	if _, err = f.service.SearchHybrid(f.ctx, options); !errors.Is(err, graphretrieval.ErrGraphRouteRequired) {
		t.Fatalf("required empty graph did not fail: %v", err)
	}
}

type hybridSearchFixture struct {
	t          *testing.T
	ctx        context.Context
	store      *sqlite.Store
	service    *MemoryService
	provider   *hybridSearchEmbeddingProvider
	scope      core.GraphScope
	revisionID string
}

func newHybridSearchFixture(t *testing.T) *hybridSearchFixture {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "hybrid-search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider := &hybridSearchEmbeddingProvider{vectors: map[string][]float32{}}
	service := NewMemoryService(store, nil, engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider)))
	return &hybridSearchFixture{t: t, ctx: ctx, store: store, service: service, provider: provider, scope: core.GraphScope{WorkspaceID: "hybrid-workspace"}, revisionID: "hybrid-revision"}
}

func (f *hybridSearchFixture) memory(id, content string, kind core.MemoryType, confidence float64, vector []float32) core.MemoryEntry {
	f.t.Helper()
	now := time.Now().UTC()
	memory := core.MemoryEntry{
		ID: id, Workspace: f.scope.WorkspaceID, Type: kind, Content: content, Confidence: confidence,
		StorageTier: core.TierVector, Source: core.MemorySource{Type: core.SourceUserInput}, CreatedAt: now, UpdatedAt: now,
	}
	if err := f.store.UpsertMemory(f.ctx, &memory); err != nil {
		f.t.Fatal(err)
	}
	f.provider.vectors[content] = vector
	if err := f.store.UpsertMemoryVector(f.ctx, memory.ID, memory.Workspace, f.provider.Name(), f.provider.ModelVersion(), vector); err != nil {
		f.t.Fatal(err)
	}
	return memory
}

func (f *hybridSearchFixture) options(query string, topK int) engine.RetrievalOptions {
	return engine.RetrievalOptions{Workspace: f.scope.WorkspaceID, Query: query, TopK: topK, Mode: engine.ModeSearch}
}

// activate persists the same evidence-bound normalized entities and typed edges
// used by graph recall, with the first memory connected to every related memory.
func (f *hybridSearchFixture) activate(memories ...core.MemoryEntry) {
	f.t.Helper()
	now := time.Now().UTC()
	configuration := core.GraphConfiguration{
		ID: "hybrid-configuration", Scope: f.scope, Version: 1, Enabled: true,
		AdapterName: "agent-memory-graphrag", AdapterVersion: "0.1.0", IndexMethod: core.GraphIndexStandard,
		ProjectionVersion: "projection-v1", ArtifactSchemaVersion: "graph-artifact/v1",
		PromptFingerprint: "sha256:prompts", ModelRoute: "index-text-primary", CreatedAt: now, UpdatedAt: now,
	}
	if err := f.store.UpsertGraphConfiguration(f.ctx, configuration); err != nil {
		f.t.Fatal(err)
	}
	revision := core.GraphRevision{
		ID: f.revisionID, Scope: f.scope, ConfigurationID: configuration.ID, State: core.GraphRevisionReady,
		Cutoff: core.GraphWatermark{Sequence: 1, EventTime: now, Digest: "sha256:cutoff"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := f.store.CreateGraphRevision(f.ctx, revision); err != nil {
		f.t.Fatal(err)
	}
	evidence := func(memory core.MemoryEntry) core.GraphEvidence {
		return core.GraphEvidence{ID: "evidence-" + memory.ID, Scope: f.scope, CanonicalKind: "memory", CanonicalID: memory.ID, CanonicalFingerprint: core.FingerprintText(memory.Content), OccurrenceCount: 1}
	}
	for index, memory := range memories {
		entity := core.GraphEntity{ID: "entity-" + memory.ID, Scope: f.scope, Trust: core.GraphTrustApproved, FirstRevisionID: f.revisionID, LastRevisionID: f.revisionID, CreatedAt: now, UpdatedAt: now}
		version := core.GraphEntityVersion{EntityID: entity.ID, RevisionID: f.revisionID, ExternalID: "external-" + memory.ID, Name: memory.Content, EntityType: "decision", OccurrenceCount: 1}
		if err := f.store.ImportGraphEntity(f.ctx, entity, version, []core.GraphEvidence{evidence(memory)}); err != nil {
			f.t.Fatal(err)
		}
		if index == 0 {
			continue
		}
		edge := core.GraphEdge{
			ID: "edge-" + memory.ID, Scope: f.scope, SourceEntityID: "entity-" + memories[0].ID, TargetEntityID: entity.ID,
			NormalizedKind: string(core.GraphRelationshipSupports), Trust: core.GraphTrustApproved,
			FirstRevisionID: f.revisionID, LastRevisionID: f.revisionID, CreatedAt: now, UpdatedAt: now,
		}
		edgeVersion := core.GraphEdgeVersion{EdgeID: edge.ID, RevisionID: f.revisionID, Weight: 0.9}
		edgeEvidence := evidence(memory)
		edgeEvidence.ID = "edge-evidence-" + memory.ID
		if err := f.store.ImportGraphEdge(f.ctx, edge, edgeVersion, []core.GraphEvidence{edgeEvidence}); err != nil {
			f.t.Fatal(err)
		}
	}
	if err := f.store.ActivateGraphRevision(f.ctx, core.GraphActivation{Scope: f.scope, ConfigurationID: configuration.ID, CandidateRevision: f.revisionID}); err != nil {
		f.t.Fatal(err)
	}
	snapshot, err := f.store.LoadActiveGraphSnapshot(f.ctx, f.scope, 32, 32, 0)
	if err != nil || snapshot.RevisionID != f.revisionID || !snapshot.Fresh || len(snapshot.Nodes) != len(memories) {
		f.t.Fatalf("normalized graph fixture is not fresh and active: snapshot=%+v err=%v", snapshot, err)
	}
}

func assertHybridSearchHasMemories(t *testing.T, hits []engine.RetrievalHit, ids ...string) {
	t.Helper()
	seen := make(map[string]bool, len(hits))
	for _, hit := range hits {
		if seen[hit.Memory.ID] {
			t.Fatalf("canonical memory %q was returned twice: %+v", hit.Memory.ID, hits)
		}
		seen[hit.Memory.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatalf("expected canonical memory %q in hybrid results: %+v", id, hits)
		}
	}
}

func hybridSearchHit(hits []engine.RetrievalHit, id string) *engine.RetrievalHit {
	for index := range hits {
		if hits[index].Memory.ID == id {
			return &hits[index]
		}
	}
	return nil
}

// The embedding boundary is deterministic; SQLite, canonical retrieval, graph
// imports, graph traversal and filtering all use their real implementations.
type hybridSearchEmbeddingProvider struct {
	vectors map[string][]float32
}

func (p *hybridSearchEmbeddingProvider) Name() string         { return "hybrid-test" }
func (p *hybridSearchEmbeddingProvider) ModelVersion() string { return "hybrid-test-v1" }
func (p *hybridSearchEmbeddingProvider) Dimension() int       { return 2 }
func (p *hybridSearchEmbeddingProvider) Embed(_ context.Context, text string) ([]float32, error) {
	if vector, ok := p.vectors[text]; ok {
		return append([]float32(nil), vector...), nil
	}
	return []float32{1, 0}, nil
}
func (p *hybridSearchEmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vector, err := p.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		result = append(result, vector)
	}
	return result, nil
}
