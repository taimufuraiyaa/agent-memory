package application

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestMemoryServiceWriteAndSearchShareCanonicalPath(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	modelDir := t.TempDir()
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	writer := engine.NewWritePipelineWithEmbedder(store, provider)
	retrieval := engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider))
	service := NewMemoryService(store, writer, retrieval)

	written, err := service.Write(ctx, engine.WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "orders emit order.created events",
		Source:    core.MemorySource{Type: core.SourceUserInput},
		Mode:      engine.ExtractFast,
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if written.ID == "" {
		t.Fatal("expected durable memory ID")
	}

	result, err := service.Search(ctx, engine.RetrievalOptions{
		Workspace: "ws",
		Query:     "order events",
		TopK:      5,
		Mode:      engine.ModeSearch,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.RequestID == "" {
		t.Fatal("expected request ID")
	}
	if len(result.Hits) != 1 || result.Hits[0].Memory.ID != written.ID {
		t.Fatalf("unexpected hits: %+v", result.Hits)
	}
}

func TestMemoryServiceFeedbackAppliesRetrievalAndReconsolidation(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider, err := embeddings.NewLocalProvider(t.TempDir())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	writer := engine.NewWritePipelineWithEmbedder(store, provider)
	service := NewMemoryService(store, writer, engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider)))
	written, err := service.Write(ctx, engine.WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "feedback target memory", Source: core.MemorySource{Type: core.SourceUserInput}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	updated, err := service.Feedback(ctx, FeedbackInput{MemoryID: written.ID, Outcome: core.FeedbackHelpful, OccurredAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("feedback: %v", err)
	}
	if updated.UsefulCount != 1 {
		t.Fatalf("expected useful count 1, got %d", updated.UsefulCount)
	}
}

func TestMemoryServiceRecallReturnsBudgetedContextAndRequestID(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider, err := embeddings.NewLocalProvider(t.TempDir())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	writer := engine.NewWritePipelineWithEmbedder(store, provider)
	retrieval := engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider))
	service := NewMemoryService(store, writer, retrieval)
	_, err = service.Write(ctx, engine.WriteInput{
		Workspace: "ws",
		Type:      core.ProceduralMemory,
		Content:   "inspect the retry dead letter queue before restarting workers",
		Source:    core.MemorySource{Type: core.SourceUserInput},
		Mode:      engine.ExtractFast,
	})
	if err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	result, err := service.Recall(ctx, RecallOptions{
		Workspace: "ws",
		Task:      "continue investigating worker retry failures",
		TopK:      5,
		Budget:    80,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if result.RequestID == "" || result.ContextBlock == "" {
		t.Fatalf("expected request and context, got %+v", result)
	}
	if result.Clip.UsedTokens > 80 {
		t.Fatalf("recall exceeded budget: %+v", result.Clip)
	}
}
