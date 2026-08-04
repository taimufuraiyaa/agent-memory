package sqlite

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestMemoryTermsRoundTripReplaceAndProjectIsolation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "terms.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	seedTermMemory(t, store, "memory-a", "project-a")
	seedTermMemory(t, store, "memory-b", "project-b")

	termsA := []core.MemoryTerm{
		{Term: "bloomfilter", Display: "#BloomFilter", Source: core.TermSourceExplicit, Ordinal: 0, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
		{Term: "sqlite", Display: "SQLite", Source: core.TermSourceEntity, Ordinal: 1, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}
	if err := store.ReplaceMemoryTerms(ctx, "project-a", "memory-a", termsA); err != nil {
		t.Fatalf("replace project-a terms: %v", err)
	}
	if err := store.ReplaceMemoryTerms(ctx, "project-b", "memory-b", []core.MemoryTerm{
		{Term: "kafka", Display: "Kafka", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}); err != nil {
		t.Fatalf("replace project-b terms: %v", err)
	}

	gotA, err := store.ListMemoryTerms(ctx, "project-a", "memory-a")
	if err != nil {
		t.Fatalf("list project-a terms: %v", err)
	}
	if len(gotA) != 2 || gotA[0].Term != "bloomfilter" || gotA[1].Term != "sqlite" {
		t.Fatalf("unexpected project-a terms: %#v", gotA)
	}

	if err := store.ReplaceMemoryTerms(ctx, "project-a", "memory-a", termsA[:1]); err != nil {
		t.Fatalf("replace existing terms: %v", err)
	}
	gotA, err = store.ListMemoryTerms(ctx, "project-a", "memory-a")
	if err != nil {
		t.Fatalf("list replaced terms: %v", err)
	}
	if len(gotA) != 1 || gotA[0].Term != "bloomfilter" {
		t.Fatalf("replacement did not remove stale terms: %#v", gotA)
	}

	gotB, err := store.ListMemoryTerms(ctx, "project-b", "memory-b")
	if err != nil {
		t.Fatalf("list project-b terms: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Term != "kafka" {
		t.Fatalf("project terms leaked or changed: %#v", gotB)
	}
}

func TestMemoryTermsCascadeWhenMemoryIsDeleted(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "term-cascade.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	seedTermMemory(t, store, "memory-a", "project-a")
	if err := store.ReplaceMemoryTerms(ctx, "project-a", "memory-a", []core.MemoryTerm{
		{Term: "sqlite", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}); err != nil {
		t.Fatalf("replace terms: %v", err)
	}
	if err := store.DeleteByIDs(ctx, []string{"memory-a"}); err != nil {
		t.Fatalf("delete memory: %v", err)
	}
	got, err := store.ListMemoryTerms(ctx, "project-a", "memory-a")
	if err != nil {
		t.Fatalf("list terms after delete: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected cascade deletion, got %#v", got)
	}
}

func TestTermIndexStateRoundTripAndMissingState(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "term-state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	missing, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil {
		t.Fatalf("get missing state: %v", err)
	}
	if missing != nil {
		t.Fatalf("missing state = %#v, want nil", missing)
	}

	now := time.Date(2026, 7, 19, 1, 2, 3, 0, time.UTC)
	want := TermIndexState{
		Workspace:            "project-a",
		Bitmap:               []byte{0x01, 0x02, 0x03},
		State:                TermIndexReady,
		FormatVersion:        "bloom-v1",
		NormalizationVersion: "locator-v1",
		HashVersion:          "sha256-double-v1",
		BitCount:             24,
		HashCount:            7,
		DistinctItemCount:    2,
		PlannedCapacity:      100,
		EstimatedFPP:         0.01,
		CorpusGeneration:     4,
		FilterGeneration:     4,
		Checksum:             "checksum",
		BuiltAt:              now,
		UpdatedAt:            now,
	}
	if err := store.UpsertTermIndexState(ctx, want); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	got, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got == nil || got.State != want.State || got.CorpusGeneration != 4 || got.FilterGeneration != 4 {
		t.Fatalf("unexpected state: %#v", got)
	}
	if !bytes.Equal(got.Bitmap, want.Bitmap) || !got.BuiltAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("state payload did not round trip: %#v", got)
	}
}

func TestInsertMemoryByHashRollsBackWhenTermsAreInvalid(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "term-atomic.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	entry := &core.MemoryEntry{
		ID:          "memory-a",
		Type:        core.SemanticMemory,
		Content:     "atomic term write",
		Workspace:   "project-a",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}
	err = store.InsertMemoryByHash(ctx, entry, "hash-a", []core.MemoryTerm{
		{Term: "invalid-without-versions", Source: core.TermSourceExplicit},
	})
	if err == nil {
		t.Fatal("expected invalid term write to fail")
	}
	if _, err := store.GetMemory(ctx, entry.ID); err == nil {
		t.Fatal("memory row remained after term transaction failed")
	}
}

func TestCascadeDeleteDirtiesTermIndexGeneration(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "term-delete-generation.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	seedTermMemory(t, store, "memory-a", "project-a")
	if err := store.ReplaceMemoryTerms(ctx, "project-a", "memory-a", []core.MemoryTerm{
		{Term: "sqlite", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}); err != nil {
		t.Fatalf("replace terms: %v", err)
	}
	before, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil || before == nil {
		t.Fatalf("get state before delete: state=%#v err=%v", before, err)
	}

	if err := store.DeleteByIDs(ctx, []string{"memory-a"}); err != nil {
		t.Fatalf("delete memory: %v", err)
	}
	after, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil || after == nil {
		t.Fatalf("get state after delete: state=%#v err=%v", after, err)
	}
	if after.State != TermIndexDirty || after.CorpusGeneration <= before.CorpusGeneration {
		t.Fatalf("cascade deletion did not advance dirty generation: before=%#v after=%#v", before, after)
	}
	if after.StaleDeleteCount != before.StaleDeleteCount+1 {
		t.Fatalf("cascade deletion did not record stale-delete pressure: before=%#v after=%#v", before, after)
	}
}

func TestSearchMemoryTermsSupportsAndOrAndProjectIsolation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "term-search.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	seedTermMemory(t, store, "memory-a", "project-a")
	seedTermMemory(t, store, "memory-b", "project-a")
	seedTermMemory(t, store, "memory-c", "project-b")
	if err := store.ReplaceMemoryTerms(ctx, "project-a", "memory-a", []core.MemoryTerm{
		{Term: "bloom", Source: core.TermSourceExplicit, Ordinal: 0, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
		{Term: "sqlite", Source: core.TermSourceEntity, Ordinal: 1, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}); err != nil {
		t.Fatalf("terms memory-a: %v", err)
	}
	if err := store.ReplaceMemoryTerms(ctx, "project-a", "memory-b", []core.MemoryTerm{
		{Term: "bloom", Source: core.TermSourceTag, Ordinal: 0, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}); err != nil {
		t.Fatalf("terms memory-b: %v", err)
	}
	if err := store.ReplaceMemoryTerms(ctx, "project-b", "memory-c", []core.MemoryTerm{
		{Term: "sqlite", Source: core.TermSourceExplicit, Ordinal: 0, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}); err != nil {
		t.Fatalf("terms memory-c: %v", err)
	}

	andMatches, err := store.SearchMemoryTerms(ctx, TermSearchQuery{
		Workspace:            "project-a",
		Terms:                []string{"bloom", "sqlite"},
		Operator:             TermOperatorAND,
		NormalizationVersion: "locator-v1",
		Limit:                10,
	})
	if err != nil {
		t.Fatalf("AND search: %v", err)
	}
	if len(andMatches) != 1 || andMatches[0].MemoryID != "memory-a" || andMatches[0].MatchCount != 2 {
		t.Fatalf("unexpected AND matches: %#v", andMatches)
	}

	orMatches, err := store.SearchMemoryTerms(ctx, TermSearchQuery{
		Workspace:            "project-a",
		Terms:                []string{"bloom", "sqlite"},
		Operator:             TermOperatorOR,
		NormalizationVersion: "locator-v1",
		Limit:                10,
	})
	if err != nil {
		t.Fatalf("OR search: %v", err)
	}
	if len(orMatches) != 2 || orMatches[0].MemoryID != "memory-a" || orMatches[1].MemoryID != "memory-b" {
		t.Fatalf("unexpected OR ordering or project leakage: %#v", orMatches)
	}
}

func seedTermMemory(t *testing.T, store *Store, id, workspace string) {
	t.Helper()
	if err := store.UpsertMemory(context.Background(), &core.MemoryEntry{
		ID:          id,
		Type:        core.SemanticMemory,
		Content:     "term index seed " + id,
		Workspace:   workspace,
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}); err != nil {
		t.Fatalf("seed memory %s: %v", id, err)
	}
}

// TestRoutineTermReplacementDoesNotInflateStaleDeleteCount proves that
// routine DELETE+INSERT term replacement dirties the snapshot but must not
// count as eviction pressure; only a real memories DELETE may increment
// stale_delete_count.
func TestRoutineTermReplacementDoesNotInflateStaleDeleteCount(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "term-routine-replace.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	seedTermMemory(t, store, "memory-a", "project-a")

	// Seeding alone creates no term_index_state row (no insert trigger on
	// memories); establish a baseline via the term insert trigger.
	if err := store.ReplaceMemoryTerms(ctx, "project-a", "memory-a", []core.MemoryTerm{
		{Term: "baseline", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}); err != nil {
		t.Fatalf("seed baseline terms: %v", err)
	}
	before, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil || before == nil {
		t.Fatalf("get state before replacement: state=%#v err=%v", before, err)
	}

	// Routine replacement is DELETE+INSERT; it must not count as an eviction.
	if err := store.ReplaceMemoryTerms(ctx, "project-a", "memory-a", []core.MemoryTerm{
		{Term: "replacement", Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"},
	}); err != nil {
		t.Fatalf("replace terms: %v", err)
	}
	after, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil || after == nil {
		t.Fatalf("get state after replacement: state=%#v err=%v", after, err)
	}
	if after.State != TermIndexDirty {
		t.Fatalf("expected dirty state after replacement, got %q", after.State)
	}
	if after.CorpusGeneration <= before.CorpusGeneration {
		t.Fatalf("routine replacement did not invalidate snapshot: before=%d after=%d", before.CorpusGeneration, after.CorpusGeneration)
	}
	if after.StaleDeleteCount != before.StaleDeleteCount {
		t.Fatalf("routine replacement inflated stale-delete pressure: before=%d after=%d", before.StaleDeleteCount, after.StaleDeleteCount)
	}

	// A real eviction must count exactly once.
	if err := store.DeleteByIDs(ctx, []string{"memory-a"}); err != nil {
		t.Fatalf("delete memory: %v", err)
	}
	final, err := store.GetTermIndexState(ctx, "project-a")
	if err != nil || final == nil {
		t.Fatalf("get state after delete: state=%#v err=%v", final, err)
	}
	if final.State != TermIndexDirty {
		t.Fatalf("expected state still dirty after delete, got %q", final.State)
	}
	if final.StaleDeleteCount != before.StaleDeleteCount+1 {
		t.Fatalf("delete did not count exactly one stale delete: before=%d after=%d", before.StaleDeleteCount, final.StaleDeleteCount)
	}
}
