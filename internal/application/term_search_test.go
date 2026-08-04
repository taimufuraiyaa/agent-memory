package application

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestSearchTermsNormalizesAndReturnsExactHitsWithoutRetrievalEngine(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-terms.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	memory := &core.MemoryEntry{
		ID:          "memory-a",
		Type:        core.SemanticMemory,
		Content:     "Bloom terms live in SQLite",
		Workspace:   "project-a",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
		Keywords: []core.MemoryTerm{
			{Term: "bloom", Display: "Bloom", Source: core.TermSourceExplicit, Ordinal: 0, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
			{Term: "sqlite", Display: "SQLite", Source: core.TermSourceExplicit, Ordinal: 1, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
		},
	}
	if err := store.UpsertMemory(ctx, memory); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}

	service := NewMemoryService(store, nil, nil)
	result, err := service.SearchTerms(ctx, TermSearchOptions{
		Workspace: "project-a",
		Query:     "#BLOOM SQLite",
		Operator:  TermOperatorAND,
		TopK:      10,
	})
	if err != nil {
		t.Fatalf("SearchTerms: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Memory.ID != memory.ID {
		t.Fatalf("unexpected exact hits: %#v", result.Hits)
	}
	if len(result.Terms) != 2 || result.Terms[0] != "bloom" || result.Terms[1] != "sqlite" {
		t.Fatalf("query normalization was not preserved: %#v", result.Terms)
	}
	if len(result.Hits[0].Memory.Keywords) != 2 {
		t.Fatalf("canonical keywords missing from result: %#v", result.Hits[0].Memory)
	}
}

func TestRebuildTermIndexBackfillsAndPublishesReadySnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-rebuild.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.UpsertMemory(ctx, &core.MemoryEntry{
		ID:          "memory-a",
		Type:        core.SemanticMemory,
		Content:     "The #HotPath serves Orders.API",
		Workspace:   "project-a",
		Entities:    []string{"Payment Gateway"},
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}

	service := NewMemoryService(store, nil, nil)
	report, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a", TargetFPP: 0.01})
	if err != nil {
		t.Fatalf("RebuildTermIndex: %v", err)
	}
	if report.Scanned != 1 || report.Indexed != 1 || report.DistinctTerms != 3 {
		t.Fatalf("unexpected rebuild report: %#v", report)
	}
	terms, err := store.ListMemoryTerms(ctx, "project-a", "memory-a")
	if err != nil {
		t.Fatalf("list backfilled terms: %v", err)
	}
	if len(terms) != 3 || terms[0].Term != "hotpath" {
		t.Fatalf("unexpected backfilled terms: %#v", terms)
	}
	state, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil {
		t.Fatalf("get ready state: %v", err)
	}
	if state == nil || state.State != sqlite.TermIndexReady || state.FilterGeneration != state.CorpusGeneration {
		t.Fatalf("rebuild did not publish a ready matching generation: %#v", state)
	}
	if engine.TermBloomChecksum(state.Bitmap) != state.Checksum {
		t.Fatalf("persisted checksum mismatch: %#v", state)
	}
	filter, err := engine.LoadTermBloom(state.Bitmap, state.BitCount, state.HashCount)
	if err != nil {
		t.Fatalf("load persisted filter: %v", err)
	}
	for _, term := range terms {
		if !filter.MightContain(term.Term) {
			t.Fatalf("ready filter lost canonical term %q", term.Term)
		}
	}
}

func TestPlannedTermBloomCapacityKeepsGrowthHeadroom(t *testing.T) {
	if got := plannedTermBloomCapacity(0); got != minimumTermBloomCapacity {
		t.Fatalf("empty capacity = %d, want %d", got, minimumTermBloomCapacity)
	}
	if got := plannedTermBloomCapacity(2_000); got <= 2_000 {
		t.Fatalf("large rebuilt index has no growth headroom: %d", got)
	}
}

func TestSearchTermsRejectsMoreThanThreeTerms(t *testing.T) {
	service := NewMemoryService(nil, nil, nil)
	if _, err := service.SearchTerms(context.Background(), TermSearchOptions{
		Workspace: "project-a",
		Query:     "one two three four",
	}); err == nil {
		t.Fatal("expected over-three term query to fail")
	}
}

func TestSearchTermsShadowProbesReadyBloomWithoutSuppressingResults(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-shadow.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	memory := &core.MemoryEntry{
		ID:          "memory-a",
		Type:        core.SemanticMemory,
		Content:     "Bloom and SQLite",
		Workspace:   "project-a",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
		Keywords: []core.MemoryTerm{
			{Term: "bloom", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
		},
	}
	if err := store.UpsertMemory(ctx, memory); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	maybe, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "bloom"})
	if err != nil {
		t.Fatalf("search present term: %v", err)
	}
	if !maybe.Prefilter.Consulted || maybe.Prefilter.Decision != "maybe" || len(maybe.Hits) != 1 {
		t.Fatalf("unexpected maybe probe: %#v", maybe)
	}

	negative, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "missing"})
	if err != nil {
		t.Fatalf("search absent term: %v", err)
	}
	if !negative.Prefilter.Consulted || negative.Prefilter.Decision != "negative" || !negative.Prefilter.Shadow || len(negative.Hits) != 0 {
		t.Fatalf("unexpected shadow negative: %#v", negative)
	}
}

func TestSearchTermsShadowDetectsBloomNegativeWithCanonicalMatch(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-shadow-mismatch.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	memory := &core.MemoryEntry{
		ID:          "memory-a",
		Type:        core.SemanticMemory,
		Content:     "Bloom",
		Workspace:   "project-a",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
		Keywords: []core.MemoryTerm{
			{Term: "bloom", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
		},
	}
	if err := store.UpsertMemory(ctx, memory); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	state, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil || state == nil {
		t.Fatalf("get ready state: state=%#v err=%v", state, err)
	}
	state.Bitmap = make([]byte, len(state.Bitmap))
	state.Checksum = engine.TermBloomChecksum(state.Bitmap)
	if err := store.UpsertTermIndexState(ctx, *state); err != nil {
		t.Fatalf("publish intentionally incorrect snapshot: %v", err)
	}

	result, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "bloom"})
	if err != nil {
		t.Fatalf("shadow mismatch search: %v", err)
	}
	if len(result.Hits) != 1 || !result.Prefilter.ShadowMismatch || result.Prefilter.Decision != "negative" {
		t.Fatalf("shadow mode failed to preserve and flag canonical match: %#v", result)
	}
}

func TestSearchTermsBloomModeOffFailsOpen(t *testing.T) {
	t.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "off")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-bloom-off.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	memory := &core.MemoryEntry{
		ID:          "memory-a",
		Type:        core.SemanticMemory,
		Content:     "Bloom",
		Workspace:   "project-a",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
		Keywords: []core.MemoryTerm{
			{Term: "bloom", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
		},
	}
	if err := store.UpsertMemory(ctx, memory); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	result, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "bloom"})
	if err != nil {
		t.Fatalf("search with Bloom off: %v", err)
	}
	if len(result.Hits) != 1 || result.Prefilter.Consulted || result.Prefilter.Reason != "disabled" {
		t.Fatalf("Bloom off did not fail open: %#v", result)
	}
}

func TestSearchTermsLabelsObservedBloomFalsePositive(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-false-positive.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild empty index: %v", err)
	}
	state, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil || state == nil {
		t.Fatalf("get ready state: state=%#v err=%v", state, err)
	}
	for i := range state.Bitmap {
		state.Bitmap[i] = 0xff
	}
	state.Checksum = engine.TermBloomChecksum(state.Bitmap)
	if err := store.UpsertTermIndexState(ctx, *state); err != nil {
		t.Fatalf("publish saturated fixture: %v", err)
	}
	result, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "absent"})
	if err != nil {
		t.Fatalf("search saturated filter: %v", err)
	}
	if result.Prefilter.Decision != "maybe" || result.Prefilter.Reason != "observed_false_positive" || len(result.Hits) != 0 {
		t.Fatalf("false positive was not identified: %#v", result)
	}
}

func TestCommittedTermAfterRebuildForcesDirtyFailOpen(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-dirty-write.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild empty index: %v", err)
	}
	if err := store.UpsertMemory(ctx, &core.MemoryEntry{
		ID:          "memory-new",
		Type:        core.SemanticMemory,
		Content:     "new locator",
		Workspace:   "project-a",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
		Keywords: []core.MemoryTerm{
			{Term: "newlocator", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
		},
	}); err != nil {
		t.Fatalf("commit new term: %v", err)
	}
	result, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "newlocator"})
	if err != nil {
		t.Fatalf("search dirty corpus: %v", err)
	}
	// After R3 (incremental Bloom), the state is ready and the term is in the bitmap.
	// The search should find the match normally (not via dirty fail-open).
	if result.Prefilter.ShortCircuited || len(result.Hits) != 1 {
		t.Fatalf("expected canonical match via ready Bloom (incremental update), got: %#v", result)
	}
}

func TestSearchTermsGateSkipsCanonicalSQLForHealthyDefiniteMiss(t *testing.T) {
	t.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "application-gate-skip.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild empty index: %v", err)
	}
	faultDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fault connection: %v", err)
	}
	defer func() { _ = faultDB.Close() }()
	if _, err := faultDB.ExecContext(ctx, `DROP TABLE memory_terms`); err != nil {
		t.Fatalf("remove canonical table fault fixture: %v", err)
	}

	result, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "absent"})
	if err != nil {
		t.Fatalf("healthy definite miss should skip unavailable canonical SQL: %v", err)
	}
	if !result.Prefilter.ShortCircuited || result.Prefilter.Shadow || result.Prefilter.Decision != "negative" || len(result.Hits) != 0 {
		t.Fatalf("healthy negative was not gated: %#v", result)
	}
}

func TestSearchTermsGateUsesOperatorSpecificNegativeLogic(t *testing.T) {
	t.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-gate-operators.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.UpsertMemory(ctx, &core.MemoryEntry{
		ID: "memory-a", Type: core.SemanticMemory, Content: "Bloom", Workspace: "project-a",
		Source: core.MemorySource{Type: core.SourceCodeAnalysis}, StorageTier: core.TierVector, Confidence: 0.9,
		Keywords: []core.MemoryTerm{{Term: "bloom", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"}},
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	andResult, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "bloom absent", Operator: TermOperatorAND})
	if err != nil {
		t.Fatalf("AND search: %v", err)
	}
	if !andResult.Prefilter.ShortCircuited || len(andResult.Hits) != 0 {
		t.Fatalf("AND should gate when any term is absent: %#v", andResult)
	}

	orResult, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "bloom absent", Operator: TermOperatorOR})
	if err != nil {
		t.Fatalf("OR search: %v", err)
	}
	if orResult.Prefilter.ShortCircuited || orResult.Prefilter.Decision != "maybe" || len(orResult.Hits) != 1 {
		t.Fatalf("OR should continue when any term may be present: %#v", orResult)
	}
}

func TestSearchTermsGateFailsOpenWhenIndexIsDirty(t *testing.T) {
	t.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-gate-dirty.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if err := store.UpsertMemory(ctx, &core.MemoryEntry{
		ID: "memory-new", Type: core.SemanticMemory, Content: "New locator", Workspace: "project-a",
		Source: core.MemorySource{Type: core.SourceCodeAnalysis}, StorageTier: core.TierVector, Confidence: 0.9,
		Keywords: []core.MemoryTerm{{Term: "newlocator", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"}},
	}); err != nil {
		t.Fatalf("commit new term: %v", err)
	}

	result, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "newlocator"})
	if err != nil {
		t.Fatalf("dirty fail-open search: %v", err)
	}
	// After R3 (incremental Bloom), the state is ready and the term is in the bitmap.
	// Gate mode consults the Bloom ("maybe"), runs canonical, and finds the match.
	if result.Prefilter.ShortCircuited || len(result.Hits) != 1 {
		t.Fatalf("expected canonical match via ready Bloom (incremental update), got: %#v", result)
	}
}

func TestTermIndexStatusExplainsRebuildHealthWithoutRawBitmap(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-term-status.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: "project-a"}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	status, err := service.TermIndexStatus(ctx, "project-a")
	if err != nil {
		t.Fatalf("term index status: %v", err)
	}
	if !status.Ready || !status.GatingEligible || status.RebuildRequired || status.BitmapBytes == 0 || !status.ChecksumValid || !status.VersionsValid {
		t.Fatalf("unexpected healthy status: %#v", status)
	}
	if status.Workspace != "project-a" || status.CorpusGeneration != status.FilterGeneration {
		t.Fatalf("status omitted project generations: %#v", status)
	}
}

func TestTermIndexStatusRequestsRebuildForCapacityFPPAndDeletePressure(t *testing.T) {
	state := sqlite.TermIndexState{
		Workspace: "project-a", State: sqlite.TermIndexReady,
		FormatVersion: engine.TermBloomFormatVersion, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1", HashVersion: engine.TermBloomHashVersion,
		Bitmap: []byte{1}, BitCount: 8, HashCount: 1, DistinctItemCount: 100, PlannedCapacity: 100,
		EstimatedFPP: 0.02, CorpusGeneration: 7, FilterGeneration: 7, StaleDeleteCount: 101,
	}
	state.Checksum = engine.TermBloomChecksum(state.Bitmap)
	status := evaluateTermIndexStatus(&state)
	if !status.RebuildRequired || status.RebuildReason != "capacity_exhausted" || status.GatingEligible {
		t.Fatalf("capacity should be the first rebuild reason: %#v", status)
	}

	state.PlannedCapacity = 500
	status = evaluateTermIndexStatus(&state)
	if status.RebuildReason != "fpp_exceeded" {
		t.Fatalf("high estimated FPP should request rebuild: %#v", status)
	}

	state.EstimatedFPP = 0.001
	status = evaluateTermIndexStatus(&state)
	if status.RebuildReason != "stale_delete_pressure" {
		t.Fatalf("stale deletes should request rebuild: %#v", status)
	}
}

func TestSearchTermsGateKeepsProjectSnapshotsIsolated(t *testing.T) {
	t.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "application-gate-isolation.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	for _, memory := range []*core.MemoryEntry{
		{ID: "memory-a", Type: core.SemanticMemory, Content: "Alpha", Workspace: "project-a", Source: core.MemorySource{Type: core.SourceCodeAnalysis}, StorageTier: core.TierVector, Confidence: 0.9, Keywords: []core.MemoryTerm{{Term: "alpha", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"}}},
		{ID: "memory-b", Type: core.SemanticMemory, Content: "Shared locator", Workspace: "project-b", Source: core.MemorySource{Type: core.SourceCodeAnalysis}, StorageTier: core.TierVector, Confidence: 0.9, Keywords: []core.MemoryTerm{{Term: "shared", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"}}},
	} {
		if err := store.UpsertMemory(ctx, memory); err != nil {
			t.Fatalf("upsert %s: %v", memory.ID, err)
		}
	}
	service := NewMemoryService(store, nil, nil)
	for _, workspace := range []string{"project-a", "project-b"} {
		if _, err := service.RebuildTermIndex(ctx, RebuildTermIndexOptions{Workspace: workspace}); err != nil {
			t.Fatalf("rebuild %s: %v", workspace, err)
		}
	}

	a, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-a", Query: "shared"})
	if err != nil || !a.Prefilter.ShortCircuited || len(a.Hits) != 0 {
		t.Fatalf("project-a should have a definite miss: result=%#v err=%v", a, err)
	}
	b, err := service.SearchTerms(ctx, TermSearchOptions{Workspace: "project-b", Query: "shared"})
	if err != nil || b.Prefilter.ShortCircuited || len(b.Hits) != 1 || b.Hits[0].Memory.ID != "memory-b" {
		t.Fatalf("project-b snapshot was contaminated: result=%#v err=%v", b, err)
	}
}
