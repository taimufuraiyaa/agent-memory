package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// BenchmarkUpsertMemory benchmarks memory upsert operations
func BenchmarkUpsertMemory(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mem := &core.MemoryEntry{
			ID:        fmt.Sprintf("bench-mem-%d", i),
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Benchmark memory %d", i),
			Workspace: "bench",
		}
		err := store.UpsertMemory(ctx, mem)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUpsertMemoryWithEmbedding benchmarks upsert with embedding
func BenchmarkUpsertMemoryWithEmbedding(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	embedding := make([]float32, 384)
	for i := range embedding {
		embedding[i] = float32(i) / 384.0
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mem := &core.MemoryEntry{
			ID:        fmt.Sprintf("bench-embed-%d", i),
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Memory with embedding %d", i),
			Workspace: "bench",
		}
		err := store.UpsertMemory(ctx, mem)
		if err != nil {
			b.Fatal(err)
		}
		
		err = store.UpsertMemoryVector(ctx, mem.ID, "bench", "test-provider", "test-v1", embedding)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListMemoryVectors benchmarks vector listing
func BenchmarkListMemoryVectors(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	// Seed with memories
	seedStoreMemories(b, ctx, store, 500)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.ListMemoryVectorsByWorkspace(ctx, "bench")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListRecentMemories benchmarks recent memory listing
func BenchmarkListRecentMemories(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	seedStoreMemories(b, ctx, store, 500)
	
	limits := []int{10, 25, 50, 100}
	
	for _, limit := range limits {
		b.Run(fmt.Sprintf("limit_%d", limit), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := store.ListRecentMemoriesByWorkspace(ctx, "bench", limit)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkListMemoriesByWorkspace benchmarks memory listing
func BenchmarkListMemoriesByWorkspace(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	seedStoreMemories(b, ctx, store, 1000)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.ListMemoriesByWorkspace(ctx, "bench")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetMemory benchmarks single memory retrieval
func BenchmarkGetMemory(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	// Create a memory to retrieve
	mem := &core.MemoryEntry{
		ID:        "bench-getmem-1",
		Type:      core.SemanticMemory,
		Content:   "Test memory for retrieval",
		Workspace: "bench",
	}
	err := store.UpsertMemory(ctx, mem)
	if err != nil {
		b.Fatal(err)
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.GetMemory(ctx, mem.ID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSetPinned benchmarks pin/unpin operations
func BenchmarkSetPinned(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	mem := &core.MemoryEntry{
		ID:        "bench-pin-1",
		Type:      core.SemanticMemory,
		Content:   "Pin test memory",
		Workspace: "bench",
	}
	err := store.UpsertMemory(ctx, mem)
	if err != nil {
		b.Fatal(err)
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pinned := i%2 == 0
		_, err := store.SetPinned(ctx, mem.ID, pinned)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMarkAccessed benchmarks access tracking
func BenchmarkMarkAccessed(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	// Create memories to mark as accessed
	ids := make([]string, 10)
	for i := range ids {
		mem := &core.MemoryEntry{
			ID:        fmt.Sprintf("bench-access-%d", i),
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Access memory %d", i),
			Workspace: "bench",
		}
		err := store.UpsertMemory(ctx, mem)
		if err != nil {
			b.Fatal(err)
		}
		ids[i] = mem.ID
	}
	
	now := time.Now().UTC()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := store.MarkAccessed(ctx, ids, now)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDeleteByIDs benchmarks bulk deletion
func BenchmarkDeleteByIDs(b *testing.B) {
	ctx := context.Background()
	
	deleteSizes := []int{1, 5, 10, 50}
	
	for _, size := range deleteSizes {
		b.Run(fmt.Sprintf("delete_%d", size), func(b *testing.B) {
			store := setupBenchStore(b)
			defer store.Close()
			
			// Seed enough memories
			seedStoreMemories(b, ctx, store, b.N*size)
			
			// Get IDs to delete
			memories, err := store.ListRecentMemoriesByWorkspace(ctx, "bench", b.N*size)
			if err != nil {
				b.Fatal(err)
			}
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := i * size
				end := start + size
				if end > len(memories) {
					break
				}
				
				ids := make([]string, size)
				for j := 0; j < size; j++ {
					ids[j] = memories[start+j].ID
				}
				
				err := store.DeleteByIDs(ctx, ids)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAddTokenMetricV2 benchmarks token metric recording
func BenchmarkAddTokenMetricV2(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := store.AddTokenMetricV2(ctx, "bench", "search", 100, 80, "run-001", true)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConcurrentReads benchmarks concurrent read operations
func BenchmarkConcurrentReads(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	seedStoreMemories(b, ctx, store, 500)
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := store.ListRecentMemoriesByWorkspace(ctx, "bench", 25)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkConcurrentWrites benchmarks concurrent write operations
func BenchmarkConcurrentWrites(b *testing.B) {
	ctx := context.Background()
	store := setupBenchStore(b)
	defer store.Close()
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			mem := &core.MemoryEntry{
				ID:        fmt.Sprintf("bench-concurrent-%d-%d", i, time.Now().UnixNano()),
				Type:      core.SemanticMemory,
				Content:   fmt.Sprintf("Concurrent write %d", i),
				Workspace: "bench",
			}
			err := store.UpsertMemory(ctx, mem)
			if err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}

// Benchmark helpers

func setupBenchStore(b *testing.B) *Store {
	b.Helper()
	ctx := context.Background()
	
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	
	return store
}

func seedStoreMemories(b *testing.B, ctx context.Context, store *Store, count int) {
	b.Helper()
	
	embedding := make([]float32, 384)
	for i := range embedding {
		embedding[i] = float32(i) / 384.0
	}
	
	for i := 0; i < count; i++ {
		mem := &core.MemoryEntry{
			ID:        fmt.Sprintf("bench-seed-%d", i),
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Seeded memory %d for benchmark", i),
			Workspace: "bench",
		}
		err := store.UpsertMemory(ctx, mem)
		if err != nil {
			b.Fatalf("seed memory: %v", err)
		}
		
		// Add embedding for half of them
		if i%2 == 0 {
			err = store.UpsertMemoryVector(ctx, mem.ID, "bench", "test-provider", "test-v1", embedding)
			if err != nil {
				b.Fatalf("seed embedding: %v", err)
			}
		}
	}
}
