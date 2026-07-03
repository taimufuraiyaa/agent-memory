package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// TestConcurrentReadWrite validates that WAL mode + connection pooling
// allow concurrent reads and writes without SQLITE_BUSY errors.
func TestConcurrentReadWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}

	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "concurrent.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Verify WAL mode is enabled
	var journalMode string
	err = store.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("Expected WAL mode, got %q", journalMode)
	}

	// Test concurrent operations: 10 parallel writes + 10 parallel reads
	const (
		numWriters = 10
		numReaders = 10
		opsPerWriter = 5
		opsPerReader = 10
	)

	var wg sync.WaitGroup
	errors := make(chan error, numWriters+numReaders)

	// Launch concurrent writers
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		writerID := w
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWriter; i++ {
				memory := &core.MemoryEntry{
					ID:        fmt.Sprintf("writer-%d-mem-%d", writerID, i),
					Type:      core.SemanticMemory,
					Content:   fmt.Sprintf("Content from writer %d operation %d", writerID, i),
					Workspace: "concurrent-test",
					Source:    core.MemorySource{Type: core.SourceUserInput},
					StorageTier: core.TierVector,
					UpdatedAt: time.Now(),
					CreatedAt: time.Now(),
				}
				
				hash := fmt.Sprintf("hash-%d-%d", writerID, i)
				if err := store.InsertMemoryByHash(ctx, memory, hash); err != nil {
					errors <- fmt.Errorf("writer %d: %w", writerID, err)
					return
				}
			}
		}()
	}

	// Launch concurrent readers
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		readerID := r
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerReader; i++ {
				count, err := store.CountMemories(ctx)
				if err != nil {
					errors <- fmt.Errorf("reader %d: count: %w", readerID, err)
					return
				}
				if count < 0 {
					errors <- fmt.Errorf("reader %d: invalid count %d", readerID, count)
					return
				}
				
				// Small delay to interleave with writes
				time.Sleep(time.Millisecond)
			}
		}()
	}

	// Wait for all operations to complete
	wg.Wait()
	close(errors)

	// Check for errors
	var errCount int
	for err := range errors {
		t.Errorf("Concurrent operation error: %v", err)
		errCount++
	}

	if errCount > 0 {
		t.Fatalf("Found %d errors during concurrent operations", errCount)
	}

	// Verify all writes succeeded
	finalCount, err := store.CountMemories(ctx)
	if err != nil {
		t.Fatalf("final count: %v", err)
	}

	expectedCount := numWriters * opsPerWriter
	if finalCount != expectedCount {
		t.Errorf("Expected %d memories, got %d", expectedCount, finalCount)
	}

	t.Logf("Successfully completed %d concurrent writes and %d concurrent reads", 
		numWriters*opsPerWriter, numReaders*opsPerReader)
}

// TestConcurrentVectorOperations validates concurrent vector reads/writes
func TestConcurrentVectorOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}

	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "concurrent_vectors.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Pre-populate some memories
	const numMemories = 20
	for i := 0; i < numMemories; i++ {
		memory := &core.MemoryEntry{
			ID:        fmt.Sprintf("mem-%d", i),
			Type:      core.SemanticMemory,
			Content:   fmt.Sprintf("Memory %d", i),
			Workspace: "vector-test",
			Source:    core.MemorySource{Type: core.SourceUserInput},
			StorageTier: core.TierVector,
			UpdatedAt: time.Now(),
			CreatedAt: time.Now(),
		}
		hash := fmt.Sprintf("hash-%d", i)
		if err := store.InsertMemoryByHash(ctx, memory, hash); err != nil {
			t.Fatalf("insert memory %d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	errors := make(chan error, numMemories*2)

	// Concurrent vector upserts
	for i := 0; i < numMemories; i++ {
		wg.Add(1)
		memID := fmt.Sprintf("mem-%d", i)
		go func(id string) {
			defer wg.Done()
			vector := make([]float32, 384)
			for j := range vector {
				vector[j] = 0.01 * float32(j)
			}
			
			err := store.UpsertMemoryVector(ctx, id, "vector-test", "onnx-minilm-l6-v2", "minilm-l6-v2-fp32", vector)
			if err != nil {
				errors <- fmt.Errorf("upsert vector %s: %w", id, err)
			}
		}(memID)
	}

	// Concurrent vector reads
	for i := 0; i < numMemories; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vectors, err := store.ListMemoryVectorRowsByWorkspace(ctx, "vector-test")
			if err != nil {
				errors <- fmt.Errorf("list vectors: %w", err)
				return
			}
			if len(vectors) < 0 {
				errors <- fmt.Errorf("invalid vector count")
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errCount int
	for err := range errors {
		t.Errorf("Concurrent vector operation error: %v", err)
		errCount++
	}

	if errCount > 0 {
		t.Fatalf("Found %d errors during concurrent vector operations", errCount)
	}

	// Verify all vectors were written
	vectors, err := store.ListMemoryVectorRowsByWorkspace(ctx, "vector-test")
	if err != nil {
		t.Fatalf("final vector list: %v", err)
	}

	if len(vectors) != numMemories {
		t.Errorf("Expected %d vectors, got %d", numMemories, len(vectors))
	}

	t.Logf("Successfully completed concurrent vector operations: %d upserts + %d reads", numMemories, numMemories)
}
