package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestE2ELifecycleFlow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	pipe := NewWritePipeline(store)
	_, _ = pipe.Write(ctx, WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "orders service emits order.created event",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	})
	_, _ = pipe.Write(ctx, WriteInput{
		Workspace: "ws",
		Type:      core.ProceduralMemory,
		Content:   "run migrations before deploying API",
		Source:    core.MemorySource{Type: core.SourceAgentObservation},
	})

	extractor := NewSessionEndExtractor(pipe)
	_, err = extractor.ExtractAndStore(ctx, "ws", "retry with backoff fixed timeout issue\nresult was success")
	if err != nil {
		t.Fatalf("session-end extract: %v", err)
	}

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := ensureEmbeddingsDir(modelDir); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	retrieval := NewRetrievalEngine(NewVectorSearcher(store, provider))
	hits, err := retrieval.Retrieve(ctx, RetrievalOptions{
		Workspace: "ws",
		Query:     "how to deploy safely",
		TopK:      6,
		Mode:      ModeRecall,
	})
	if err != nil {
		t.Fatalf("retrieve recall: %v", err)
	}
	if len(hits.Hits) == 0 {
		t.Fatalf("expected non-empty recall hits")
	}

	clipper := NewTokenClipper(nil)
	included, meta := clipper.Clip(hits.Hits, 30)
	if len(included) == 0 || meta.UsedTokens <= 0 {
		t.Fatalf("expected clipped recall content")
	}

	lifecycle := NewLifecycleManager(store, pipe)
	metrics, err := lifecycle.Run(ctx, "ws")
	if err != nil {
		t.Fatalf("lifecycle run: %v", err)
	}
	if metrics.DecayUpdated == 0 {
		t.Fatalf("expected lifecycle decay updates")
	}
}

func TestWritePipelineConcurrentLoad(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "load.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	pipe := NewWritePipeline(store)

	const workers = 40
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := pipe.Write(ctx, WriteInput{
				Workspace: "ws",
				Type:      core.SemanticMemory,
				Content:   fmt.Sprintf("concurrent note %d: kafka consumer retries", i),
				Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
			})
			if err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent write failed: %v", err)
	}
	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	if len(memories) < workers/2 {
		t.Fatalf("expected substantial persisted writes under load, got %d", len(memories))
	}
}
