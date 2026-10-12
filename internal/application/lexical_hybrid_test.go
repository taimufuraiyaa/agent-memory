package application

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func lexicalHybridSearch(t *testing.T, f *hybridSearchFixture, query string, topK int) *GraphSearchResult {
	t.Helper()
	result, err := f.service.SearchHybrid(f.ctx, GraphSearchOptions{Retrieval: f.options(query, topK), GraphMode: graphretrieval.GraphQueryBasic})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHybridSearchLexicalOffKeepsBaselineResults(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LEXICAL_MODE", "off")
	f := newHybridSearchFixture(t)
	f.memory("semantic", "Capacity planning guidance for the fleet", core.SemanticMemory, 0.9, []float32{1, 0})
	f.memory("rare", "Incident ticket zxqv9 timeout in ledger service", core.SemanticMemory, 0.9, []float32{0, 1})

	result := lexicalHybridSearch(t, f, "zxqv9", 5)
	if hybridSearchHit(result.Hits, "rare") != nil {
		t.Fatalf("lexical stream must be inert when off: %+v", result.Hits)
	}
	for _, hit := range result.Hits {
		if hit.Breakdown.Lexical != 0 {
			t.Fatalf("lexical signal leaked while off: %+v", hit.Breakdown)
		}
	}
	if result.Weights.Lexical != 0 {
		t.Fatalf("lexical weight reported while off: %+v", result.Weights)
	}
}

func TestHybridSearchLexicalDiscoversRareTokenBelowSemanticFloor(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LEXICAL_MODE", "on")
	t.Setenv("AGENT_MEMORY_LEXICAL_WEIGHT", "")
	f := newHybridSearchFixture(t)
	semantic := f.memory("semantic", "Capacity planning guidance for the fleet", core.SemanticMemory, 0.9, []float32{1, 0})
	rare := f.memory("rare", "Incident ticket zxqv9 timeout in ledger service", core.SemanticMemory, 0.9, []float32{0, 1})

	result := lexicalHybridSearch(t, f, "zxqv9", 5)
	assertHybridSearchHasMemories(t, result.Hits, semantic.ID, rare.ID)
	hit := hybridSearchHit(result.Hits, rare.ID)
	if hit.Breakdown.Lexical <= 0 || hit.Breakdown.Lexical > 1 {
		t.Fatalf("lexical signal not explained: %+v", hit.Breakdown)
	}
	if result.Weights.Lexical != engine.DefaultLexicalWeight {
		t.Fatalf("lexical weight not reported: %+v", result.Weights)
	}
	if hybridSearchHit(result.Hits, semantic.ID).Breakdown.Lexical != 0 {
		t.Fatal("non-matching memory must not receive a lexical signal")
	}
}

func TestHybridSearchLexicalSignalRanksLexicalMatchAboveEqualSemanticPeer(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LEXICAL_MODE", "on")
	t.Setenv("AGENT_MEMORY_LEXICAL_WEIGHT", "0.5")
	f := newHybridSearchFixture(t)
	f.memory("plain", "General notes about queue handling", core.SemanticMemory, 0.9, []float32{1, 0})
	f.memory("match", "Queue handling uses the ERR7731 retry budget", core.SemanticMemory, 0.9, []float32{1, 0})

	result := lexicalHybridSearch(t, f, "ERR7731 queue", 5)
	if len(result.Hits) < 2 || result.Hits[0].Memory.ID != "match" {
		t.Fatalf("lexical match should rank first: %+v", result.Hits)
	}
}

func TestHybridSearchLexicalExcludesSupersededDeletedAndCrossWorkspace(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LEXICAL_MODE", "on")
	f := newHybridSearchFixture(t)
	f.memory("keep", "Baseline memory without rare tokens", core.SemanticMemory, 0.9, []float32{1, 0})
	superseded := f.memory("superseded", "Superseded note with ticketzz token", core.SemanticMemory, 0.9, []float32{0, 1})
	deleted := f.memory("deleted", "Deleted note with ticketzz token", core.SemanticMemory, 0.9, []float32{0, 1})
	other := f.memory("other", "Other workspace note with ticketzz token", core.SemanticMemory, 0.9, []float32{0, 1})
	other.Workspace = "another-workspace"
	if err := f.store.UpsertMemory(f.ctx, &other); err != nil {
		t.Fatal(err)
	}
	successor := "keep"
	superseded.SupersededBy = &successor
	if err := f.store.UpsertMemory(f.ctx, &superseded); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteByIDs(f.ctx, []string{deleted.ID}); err != nil {
		t.Fatal(err)
	}

	result := lexicalHybridSearch(t, f, "ticketzz", 5)
	for _, id := range []string{"superseded", "deleted", "other"} {
		if hybridSearchHit(result.Hits, id) != nil {
			t.Fatalf("%s must not be returned: %+v", id, result.Hits)
		}
	}
}

func TestHybridSearchLexicalFailsOpenWhenIndexIsBroken(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LEXICAL_MODE", "on")
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lexical-broken.db")
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider := &hybridSearchEmbeddingProvider{vectors: map[string][]float32{}}
	service := NewMemoryService(store, nil, engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider)))
	now := time.Now().UTC()
	memory := core.MemoryEntry{ID: "m1", Workspace: "ws", Type: core.SemanticMemory, Content: "Plain capacity note", Confidence: 0.9,
		StorageTier: core.TierVector, Source: core.MemorySource{Type: core.SourceUserInput}, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertMemory(ctx, &memory); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMemoryVector(ctx, "m1", "ws", provider.Name(), provider.ModelVersion(), []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the index out-of-band: the FTS table disappears while the
	// store still believes the lexical index is available.
	if !store.LexicalIndexAvailable(ctx) {
		t.Fatal("expected lexical index to be available after migration")
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TRIGGER trg_memory_lexical_insert`, `DROP TRIGGER trg_memory_lexical_update`, `DROP TRIGGER trg_memory_lexical_workspace`, `DROP TRIGGER trg_memory_lexical_delete`,
		`DROP TABLE memory_lexical_fts`,
	} {
		if _, err := raw.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	_ = raw.Close()

	result, err := service.SearchHybrid(ctx, GraphSearchOptions{
		Retrieval: engine.RetrievalOptions{Workspace: "ws", Query: "capacity", TopK: 5, Mode: engine.ModeSearch},
		GraphMode: graphretrieval.GraphQueryBasic,
	})
	if err != nil {
		t.Fatalf("broken lexical index must not fail search: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Memory.ID != "m1" {
		t.Fatalf("baseline results lost: %+v", result.Hits)
	}
}
