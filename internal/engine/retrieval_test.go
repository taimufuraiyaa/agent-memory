package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestRetrievalEngineExplainAndModes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)
	_, _ = pipe.Write(context.Background(), WriteInput{
		Workspace: "ws",
		Type:      core.OutcomeMemory,
		Content:   "Retry with exponential backoff fixed payment timeout",
		Source:    core.MemorySource{Type: core.SourceAgentObservation},
		Outcome:   &core.Outcome{Result: core.OutcomeSuccess},
	})
	_, _ = pipe.Write(context.Background(), WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "Orders service publishes order.created event",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	})

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := ensureEmbeddingsDir(modelDir); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	searcher := NewVectorSearcher(store, provider)
	engine := NewRetrievalEngine(searcher)
	engine.clock = func() time.Time { return time.Now().UTC() }

	outcomesRes, err := engine.Retrieve(context.Background(), RetrievalOptions{
		Workspace: "ws",
		Query:     "payment timeout workaround",
		TopK:      2,
		Mode:      ModeOutcomes,
	})
	if err != nil {
		t.Fatalf("retrieve outcomes: %v", err)
	}
	if len(outcomesRes.Hits) == 0 {
		t.Fatalf("expected hits")
	}
	if outcomesRes.Hits[0].Breakdown.Total == 0 {
		t.Fatalf("expected explainable total score")
	}
	if outcomesRes.Weights.Outcome <= outcomesRes.Weights.Recency {
		t.Fatalf("expected outcome mode to prioritize outcome weight")
	}
}

