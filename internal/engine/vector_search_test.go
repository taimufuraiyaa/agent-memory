package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestVectorSearcherReturnsRankedHits(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	writePipeline := NewWritePipeline(store)
	entries := []string{
		"orders service publishes order.created event",
		"payment service retries charge on network timeout",
		"frontend uses react query for caching",
	}
	for _, content := range entries {
		_, err := writePipeline.Write(context.Background(), WriteInput{
			Workspace: "ws",
			Type:      core.SemanticMemory,
			Content:   content,
			Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
		})
		if err != nil {
			t.Fatalf("seed write failed: %v", err)
		}
	}

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := ensureEmbeddingsDir(modelDir); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	searcher := NewVectorSearcher(store, provider)
	hits, err := searcher.Search(context.Background(), "ws", "orders event publisher", 2)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Score < hits[1].Score {
		t.Fatalf("expected sorted desc scores")
	}
	if hits[0].Memory.Content != "orders service publishes order.created event" {
		t.Fatalf("expected most relevant orders memory on top, got %q", hits[0].Memory.Content)
	}
}

func TestVectorSearcherFiltersByTypeAndTier(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memory-filter.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	writePipeline := NewWritePipeline(store)
	_, err = writePipeline.Write(context.Background(), WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "orders service emits order.created",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	})
	if err != nil {
		t.Fatalf("seed semantic write failed: %v", err)
	}
	_, err = writePipeline.Write(context.Background(), WriteInput{
		Workspace: "ws",
		Type:      core.ProceduralMemory,
		Content:   "always run smoke tests before deployment",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	})
	if err != nil {
		t.Fatalf("seed procedural write failed: %v", err)
	}

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := ensureEmbeddingsDir(modelDir); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	searcher := NewVectorSearcher(store, provider)
	hits, err := searcher.SearchWithOptions(context.Background(), VectorSearchOptions{
		Workspace: "ws",
		Query:     "deployment checklist",
		TopK:      5,
		Types:     []core.MemoryType{core.ProceduralMemory},
		Tiers:     []core.StorageTier{core.TierMarkdown},
	})
	if err != nil {
		t.Fatalf("search with filters failed: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected one filtered hit, got %d", len(hits))
	}
	if hits[0].Memory.Type != core.ProceduralMemory {
		t.Fatalf("expected procedural hit, got %s", hits[0].Memory.Type)
	}
	if hits[0].Memory.StorageTier != core.TierMarkdown {
		t.Fatalf("expected markdown tier hit, got %s", hits[0].Memory.StorageTier)
	}
}

func ensureEmbeddingsDir(path string) error {
	return os.MkdirAll(path, 0o755)
}
