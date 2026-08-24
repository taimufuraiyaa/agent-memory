package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// BenchmarkWritePipeline benchmarks the full write pipeline
func BenchmarkWritePipeline(b *testing.B) {
	ctx := context.Background()
	store, provider := setupBenchmarkDeps(b)
	defer store.Close()

	pipeline := NewWritePipelineWithEmbedder(store, provider)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := pipeline.Write(ctx, WriteInput{
			Workspace: "bench",
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Benchmark memory content %d with details", i),
			Entities:  []string{"entity1", "entity2"},
			Tags:      []string{"benchmark", "test"},
			Source:    core.MemorySource{Type: core.SourceUserInput},
			Mode:      ExtractFast,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWritePipelineParallel benchmarks concurrent writes
func BenchmarkWritePipelineParallel(b *testing.B) {
	ctx := context.Background()
	store, provider := setupBenchmarkDeps(b)
	defer store.Close()

	pipeline := NewWritePipelineWithEmbedder(store, provider)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, err := pipeline.Write(ctx, WriteInput{
				Workspace: "bench",
				Type:      core.SemanticMemory,
				Content:   fmt.Sprintf("Concurrent memory %d", i),
				Source:    core.MemorySource{Type: core.SourceUserInput},
				Mode:      ExtractFast,
			})
			if err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}

// BenchmarkRetrievalSearch benchmarks semantic search
func BenchmarkRetrievalSearch(b *testing.B) {
	ctx := context.Background()
	store, provider := setupBenchmarkDeps(b)
	defer store.Close()

	// Seed with test data
	seedBenchmarkMemories(b, ctx, store, provider, 100)

	engine := NewRetrievalEngine(NewVectorSearcher(store, provider))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Retrieve(ctx, RetrievalOptions{
			Workspace: "bench",
			Query:     "test query for semantic search",
			TopK:      10,
			Mode:      ModeSearch,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRetrievalRecall benchmarks recall mode
func BenchmarkRetrievalRecall(b *testing.B) {
	ctx := context.Background()
	store, provider := setupBenchmarkDeps(b)
	defer store.Close()

	seedBenchmarkMemories(b, ctx, store, provider, 100)

	engine := NewRetrievalEngine(NewVectorSearcher(store, provider))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Retrieve(ctx, RetrievalOptions{
			Workspace: "bench",
			Query:     "continue previous work",
			TopK:      50,
			Mode:      ModeRecall,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVectorSearcher benchmarks vector search directly
func BenchmarkVectorSearcher(b *testing.B) {
	ctx := context.Background()
	store, provider := setupBenchmarkDeps(b)
	defer store.Close()

	seedBenchmarkMemories(b, ctx, store, provider, 200)

	searcher := NewVectorSearcher(store, provider)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := searcher.Search(ctx, "bench", "semantic search query", 20)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTokenClipper benchmarks token clipping
func BenchmarkTokenClipper(b *testing.B) {
	clipper := NewTokenClipper(nil)

	// Create sample hits
	hits := make([]RetrievalHit, 50)
	for i := range hits {
		hits[i] = RetrievalHit{
			Memory: core.MemoryEntry{
				ID:        fmt.Sprintf("mem-%d", i),
				Content:   fmt.Sprintf("Sample memory content %d with repeated text data", i),
				Type:      core.SemanticMemory,
				Workspace: "bench",
			},
			Score: float64(50-i) / 50.0,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = clipper.Clip(hits, 2000)
	}
}

// BenchmarkRebalanceRecallHits benchmarks hit rebalancing
func BenchmarkRebalanceRecallHits(b *testing.B) {
	hits := make([]RetrievalHit, 100)
	for i := range hits {
		hits[i] = RetrievalHit{
			Memory: core.MemoryEntry{
				ID:        fmt.Sprintf("mem-%d", i),
				Content:   fmt.Sprintf("Memory content %d", i),
				Type:      core.SemanticMemory,
				Workspace: "bench",
			},
			Score: float64(100-i) / 100.0,
		}
	}

	query := "task description for rebalancing test"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RebalanceRecallHits(query, hits)
	}
}

// BenchmarkAssembleRecallSections benchmarks context assembly
func BenchmarkAssembleRecallSections(b *testing.B) {
	hits := make([]RetrievalHit, 20)
	for i := range hits {
		hits[i] = RetrievalHit{
			Memory: core.MemoryEntry{
				ID:      fmt.Sprintf("mem-%d", i),
				Type:    core.SemanticMemory,
				Content: fmt.Sprintf("Memory content %d with information", i),
			},
			Score: float64(20-i) / 20.0,
		}
	}

	task := "Complete the implementation task"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AssembleRecallSections(task, hits)
	}
}

// Benchmark helpers

func setupBenchmarkDeps(b *testing.B) (*sqlite.Store, embeddings.Provider) {
	b.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(b.TempDir(), "bench.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	// Use mock provider for benchmarks (faster)
	provider := &mockEmbeddingProvider{}

	return store, provider
}

func seedBenchmarkMemories(b *testing.B, ctx context.Context, store *sqlite.Store, provider embeddings.Provider, count int) {
	b.Helper()

	pipeline := NewWritePipelineWithEmbedder(store, provider)

	for i := 0; i < count; i++ {
		_, err := pipeline.Write(ctx, WriteInput{
			Workspace: "bench",
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Seeded memory %d with content for testing", i),
			Entities:  []string{fmt.Sprintf("entity-%d", i%10)},
			Tags:      []string{"seed", "benchmark"},
			Source:    core.MemorySource{Type: core.SourceUserInput},
			Mode:      ExtractFast,
		})
		if err != nil {
			b.Fatalf("seed memory: %v", err)
		}
	}
}

// mockEmbeddingProvider provides fast fake embeddings
type mockEmbeddingProvider struct{}

func (m *mockEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	// Return fixed-size fake embedding
	embedding := make([]float32, 384)
	for i := range embedding {
		embedding[i] = float32(i) / 384.0
	}
	return embedding, nil
}

func (m *mockEmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i := range texts {
		emb, err := m.Embed(ctx, texts[i])
		if err != nil {
			return nil, err
		}
		results[i] = emb
	}
	return results, nil
}

func (m *mockEmbeddingProvider) Dimension() int {
	return 384
}

func (m *mockEmbeddingProvider) Name() string {
	return "mock-embeddings-v1"
}

func (m *mockEmbeddingProvider) ModelVersion() string {
	return "mock-v1"
}
