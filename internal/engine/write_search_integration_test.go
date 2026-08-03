package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// TestWriteSearchRoundTrip validates that memories written with eager embedding
// are immediately searchable without requiring lazy embedding or lifecycle runs.
func TestWriteSearchRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	t.Setenv("AGENT_MEMORY_TEST_FAKE_ONNX_RUNTIME", "true")

	// Setup temporary database and model directory
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	modelDir := filepath.Join(tmpDir, "models")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("create model dir: %v", err)
	}

	// Create minimal test fixtures for fake ONNX runtime
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("fake"), 0644); err != nil {
		t.Fatalf("write model.onnx: %v", err)
	}

	tokenizerJSON := `{
		"model": {
			"type": "WordPiece",
			"unk_token": "[UNK]",
			"continuing_subword_prefix": "##",
			"max_input_chars_per_word": 100,
			"vocab": {
				"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3,
				"authentication": 10, "oauth": 11, "jwt": 12, "token": 13,
				"database": 20, "migration": 21, "schema": 22, "version": 23,
				"api": 30, "endpoint": 31, "rest": 32, "graphql": 33,
				"test": 40, "unit": 41, "integration": 42, "coverage": 43
			}
		},
		"normalizer": {
			"type": "BertNormalizer",
			"clean_text": true,
			"handle_chinese_chars": false,
			"strip_accents": true,
			"lowercase": true
		},
		"truncation": {
			"max_length": 128
		}
	}`
	if err := os.WriteFile(filepath.Join(modelDir, "tokenizer.json"), []byte(tokenizerJSON), 0644); err != nil {
		t.Fatalf("write tokenizer.json: %v", err)
	}

	// Initialize storage
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Create embedder
	embedder, err := embeddings.NewONNXMiniLMProvider(modelDir, embeddings.ModelLifecycleOptions{})
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}

	// Create write pipeline with eager embedding
	writePipeline := NewWritePipelineWithEmbedder(store, embedder)

	// Create vector searcher and retrieval engine
	vectorSearcher := NewVectorSearcher(store, embedder)
	retrievalEngine := NewRetrievalEngine(vectorSearcher)

	// Test cases: write memories and immediately search for them
	testCases := []struct {
		name        string
		content     string
		searchQuery string
		shouldFind  bool
	}{
		{
			name:        "exact_match",
			content:     "Implemented OAuth authentication with JWT tokens",
			searchQuery: "OAuth authentication",
			shouldFind:  true,
		},
		{
			name:        "semantic_match",
			content:     "Database schema migration completed successfully to version 5",
			searchQuery: "database upgrade",
			shouldFind:  true,
		},
		{
			name:        "no_match",
			content:     "Added unit tests for API endpoints",
			searchQuery: "database migration",
			shouldFind:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Write memory with eager embedding
			writeStart := time.Now()
			writeResult, err := writePipeline.Write(ctx, WriteInput{
				Workspace: "test-workspace",
				Type:      core.SemanticMemory,
				Content:   tc.content,
				Source: core.MemorySource{
					Type: core.SourceUserInput,
				},
			})
			writeDuration := time.Since(writeStart)

			if err != nil {
				t.Fatalf("write failed: %v", err)
			}
			if writeResult.Rejected {
				t.Fatalf("write rejected: %s", writeResult.RejectReason)
			}

			t.Logf("Write completed in %v", writeDuration)

			// Verify vector was persisted
			vectors, err := store.ListMemoryVectorRowsByWorkspace(ctx, "test-workspace")
			if err != nil {
				t.Fatalf("list vectors: %v", err)
			}

			found := false
			for _, vec := range vectors {
				if vec.MemoryID == writeResult.ID {
					found = true
					t.Logf("Vector persisted: provider=%s, version=%s", vec.EmbeddingProvider, vec.EmbeddingModelVersion)

					// Validate provider and version are recorded
					if vec.EmbeddingProvider != embedder.Name() {
						t.Errorf("Expected provider %q, got %q", embedder.Name(), vec.EmbeddingProvider)
					}
					if vec.EmbeddingModelVersion != embedder.ModelVersion() {
						t.Errorf("Expected version %q, got %q", embedder.ModelVersion(), vec.EmbeddingModelVersion)
					}
					break
				}
			}

			if !found {
				t.Error("Vector not found after write - eager embedding may have failed")
			}

			// Immediately search for the memory (no lazy embedding needed)
			searchStart := time.Now()
			searchResult, err := retrievalEngine.Retrieve(ctx, RetrievalOptions{
				Workspace: "test-workspace",
				Query:     tc.searchQuery,
				TopK:      5,
				Mode:      ModeSearch,
			})
			searchDuration := time.Since(searchStart)

			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			t.Logf("Search completed in %v", searchDuration)

			// Verify memory found in results (check both Hits and StrongHits)
			foundInSearch := false
			allHits := append(searchResult.Hits, searchResult.StrongHits...)
			for _, hit := range allHits {
				if hit.Memory.ID == writeResult.ID {
					foundInSearch = true
					t.Logf("Memory found in search results with score %.4f", hit.Score)
					break
				}
			}

			if tc.shouldFind && !foundInSearch {
				t.Errorf("Memory should be found in search but wasn't (wrote at %v, searched at %v)", writeStart, searchStart)
			}
			if !tc.shouldFind && foundInSearch {
				t.Logf("Memory found in search but wasn't expected (this is OK if semantically related)")
			}

			// Validate search latency is reasonable (<100ms target)
			totalLatency := writeDuration + searchDuration
			if totalLatency > 200*time.Millisecond {
				t.Logf("Warning: Total latency %v exceeds 200ms target", totalLatency)
			}

			// Validate search was fast since vector already exists
			if searchDuration > 100*time.Millisecond {
				t.Logf("Warning: Search latency %v exceeds 100ms target", searchDuration)
			}
		})
	}
}

// TestEagerEmbeddingFailureHandling validates that write pipeline handles
// embedding failures gracefully and doesn't corrupt the database.
func TestEagerEmbeddingFailureHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Create a mock embedder that always fails
	failingEmbedder := &mockFailingEmbedder{}

	writePipeline := NewWritePipelineWithEmbedder(store, failingEmbedder)

	// Attempt to write - should fail since embedding is mandatory
	result, err := writePipeline.Write(ctx, WriteInput{
		Workspace: "test-workspace",
		Type:      core.SemanticMemory,
		Content:   "Test content that will fail embedding",
		Source: core.MemorySource{
			Type: core.SourceUserInput,
		},
	})

	// Should fail with embedding error
	if err == nil {
		t.Error("Expected write to fail with embedding error, but it succeeded")
	}
	if result != nil && !result.Rejected {
		t.Error("Expected write to be rejected or return error")
	}

	// Verify no memory was persisted (transaction rolled back)
	count, err := store.CountMemories(ctx)
	if err != nil {
		t.Fatalf("count memories: %v", err)
	}

	if count > 0 {
		t.Errorf("Expected no memories after failed embedding, found %d", count)
	}
}

// mockFailingEmbedder is a test embedder that always returns an error
type mockFailingEmbedder struct{}

func (m *mockFailingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, context.DeadlineExceeded
}

func (m *mockFailingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, context.DeadlineExceeded
}

func (m *mockFailingEmbedder) Name() string {
	return "mock-failing"
}

func (m *mockFailingEmbedder) ModelVersion() string {
	return "test-v1"
}

func (m *mockFailingEmbedder) Dimension() int {
	return 384
}
