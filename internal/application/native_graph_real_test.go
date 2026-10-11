package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

func mustNativeBuildDigest(t *testing.T, cfg config.GraphConfig) string {
	t.Helper()
	digest, err := nativeGraphBuildDigest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
func containsNativeIncrementalMode(data []byte) bool {
	var manifest struct {
		Mode string `json:"mode"`
	}
	return json.Unmarshal(data, &manifest) == nil && manifest.Mode == "incremental"
}

// Explicitly opted-in integration proof: real packaged Python GraphRAG and
// local model calls, isolated synthetic canonical data, full and incremental.
func TestNativeGraphRealLocalAdapter(t *testing.T) {
	if os.Getenv("AGENT_MEMORY_REAL_GRAPH_SMOKE") != "1" {
		t.Skip("opt-in real local GraphRAG integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	store, cfg, _ := nativeWorkerFixture(t)
	loaded, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	runtime := loaded.Graph
	runtime.Enabled = true
	runtime.JobRoot = filepath.Join(t.TempDir(), "graphrag-jobs")
	worker := NewNativeGraphWorker(runtime)
	worker.batch = 1
	first := nativeWorkerMemory(t, store, cfg.Scope.WorkspaceID, "Project Atlas pricing depends on benchmark throughput. Atlas runs on the Cedar runtime. Cedar capacity is 1200 transactions per second. Cedar runtime cost determines the Atlas price per transaction.")
	second := nativeWorkerMemory(t, store, cfg.Scope.WorkspaceID, "Cedar is the runtime for Project Atlas. Cedar uses Juniper servers. Juniper server operating cost increases with server count, and Cedar throughput determines how many Juniper servers Atlas needs.")
	if err = worker.RunWorkspace(ctx, store, cfg.Scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	full := nativeWorkerStatus(t, store, cfg)
	if !full.Fresh || full.ActiveRevisionID == "" {
		t.Fatalf("full index not active: %+v", full)
	}
	t.Logf("full index active: %s, pending=%d", full.ActiveRevisionID, full.PendingRecords)
	third := nativeWorkerMemory(t, store, cfg.Scope.WorkspaceID, "Project Atlas has a new runtime decision: Cedar uses Willow storage. Willow storage cost is part of Cedar operating cost and therefore affects Atlas pricing.")
	if err = worker.RunWorkspace(ctx, store, cfg.Scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	incremental := nativeWorkerStatus(t, store, cfg)
	if !incremental.Fresh || incremental.ActiveRevisionID == full.ActiveRevisionID {
		t.Fatalf("update not active: %+v", incremental)
	}
	manifest, err := readNativeGraphFile(worker.statePath(cfg.Scope, incremental.ActiveRevisionID), 4<<20)
	if err != nil || len(manifest) == 0 {
		t.Fatalf("incremental state unavailable: %v", err)
	}
	revision, err := store.GetGraphRevision(ctx, cfg.Scope, cfg.ID, incremental.ActiveRevisionID)
	if err != nil || revision.BaseRevisionID != full.ActiveRevisionID {
		t.Fatalf("update did not retain base revision: %+v %v", revision, err)
	}
	stateDir, err := worker.loadState(cfg.Scope, incremental.ActiveRevisionID, mustNativeBuildDigest(t, runtime))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := readNativeGraphFile(filepath.Join(stateDir, "output", "normalized", "artifact-manifest.json"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !containsNativeIncrementalMode(artifact) {
		t.Fatalf("real adapter did not perform incremental update: %s", artifact)
	}
	provider := &hybridSearchEmbeddingProvider{vectors: map[string][]float32{first.Content: {1, 0}, second.Content: {0.2, 0.98}, third.Content: {0.4, 0.9}}}
	for _, m := range []core.MemoryEntry{first, second, third} {
		if err = store.UpsertMemoryVector(ctx, m.ID, m.Workspace, provider.Name(), provider.ModelVersion(), provider.vectors[m.Content]); err != nil {
			t.Fatal(err)
		}
	}
	service := NewMemoryService(store, nil, engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider)))
	result, err := service.SearchHybrid(ctx, GraphSearchOptions{Retrieval: engine.RetrievalOptions{Workspace: cfg.Scope.WorkspaceID, Query: "How does Atlas pricing depend on the Cedar runtime?", TopK: 8, Mode: engine.ModeSearch}, GraphMode: graphretrieval.GraphQueryLocal, GraphRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.GraphContext == nil || result.GraphContext.Local == nil || len(result.GraphContext.Local.Paths) == 0 {
		t.Fatalf("real graph produced no relationship paths: %+v", result.GraphContext)
	}
	assertHybridSearchHasMemories(t, result.Hits, first.ID, second.ID, third.ID)
	t.Logf("incremental index active: %s, nodes/paths=%d, original canonical memories=%d", incremental.ActiveRevisionID, len(result.GraphContext.Local.Paths), len(result.Hits))
}
