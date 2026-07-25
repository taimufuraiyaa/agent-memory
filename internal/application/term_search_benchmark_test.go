package application

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func BenchmarkTermSearchPaths(b *testing.B) {
	b.Run("gate_negative", func(b *testing.B) {
		service, store := benchmarkTermService(b)
		b.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := service.SearchTerms(context.Background(), TermSearchOptions{Workspace: "ws", Query: "absent"}); err != nil {
				b.Fatal(err)
			}
		}
		_ = store
	})

	b.Run("canonical_hit", func(b *testing.B) {
		service, _ := benchmarkTermService(b)
		b.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := service.SearchTerms(context.Background(), TermSearchOptions{Workspace: "ws", Query: "bloom"}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("false_positive", func(b *testing.B) {
		service, store := benchmarkTermService(b)
		state, err := store.GetTermIndexState(context.Background(), "ws")
		if err != nil || state == nil {
			b.Fatalf("load state: state=%#v err=%v", state, err)
		}
		for i := range state.Bitmap {
			state.Bitmap[i] = 0xff
		}
		state.Checksum = engine.TermBloomChecksum(state.Bitmap)
		if err := store.UpsertTermIndexState(context.Background(), *state); err != nil {
			b.Fatal(err)
		}
		b.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := service.SearchTerms(context.Background(), TermSearchOptions{Workspace: "ws", Query: "absent"}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("dirty_bypass", func(b *testing.B) {
		service, store := benchmarkTermService(b)
		if err := store.UpsertMemory(context.Background(), benchmarkMemory("memory-dirty", "dirtyterm")); err != nil {
			b.Fatal(err)
		}
		b.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := service.SearchTerms(context.Background(), TermSearchOptions{Workspace: "ws", Query: "dirtyterm"}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkRebuildTermIndex(b *testing.B) {
	service, _ := benchmarkTermService(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := service.RebuildTermIndex(context.Background(), RebuildTermIndexOptions{Workspace: "ws"}); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkTermService(b *testing.B) (*MemoryService, *sqlite.Store) {
	b.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(b.TempDir(), "terms.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertMemory(context.Background(), benchmarkMemory("memory-a", "bloom")); err != nil {
		b.Fatal(err)
	}
	service := NewMemoryService(store, nil, nil)
	if _, err := service.RebuildTermIndex(context.Background(), RebuildTermIndexOptions{Workspace: "ws"}); err != nil {
		b.Fatal(err)
	}
	return service, store
}

func benchmarkMemory(id, term string) *core.MemoryEntry {
	return &core.MemoryEntry{
		ID: id, Type: core.SemanticMemory, Content: term, Workspace: "ws",
		Source: core.MemorySource{Type: core.SourceCodeAnalysis}, StorageTier: core.TierVector, Confidence: 0.9,
		Keywords: []core.MemoryTerm{{Term: term, Source: core.TermSourceExplicit, NormalizationVersion: "locator-v1", ExtractorVersion: "deterministic-v1"}},
	}
}
