package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestTUIBackendLoadsOnlySelectedWorkspace(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "tui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider := naturalTestProvider{}
	pipeline := engine.NewWritePipelineWithEmbedder(store, provider)
	for _, item := range []struct {
		workspace string
		typeName  core.MemoryType
		content   string
		pinned    bool
	}{
		{"selected", core.SemanticMemory, "selected database architecture", true},
		{"selected", core.ProceduralMemory, "selected deployment workflow", false},
		{"other", core.OutcomeMemory, "private other workspace content", false},
	} {
		result, writeErr := pipeline.Write(ctx, engine.WriteInput{Workspace: item.workspace, Type: item.typeName, Content: item.content, Source: core.MemorySource{Type: core.SourceUserInput}, Mode: engine.ExtractFast})
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if item.pinned {
			if _, pinErr := store.SetPinned(ctx, result.ID, true); pinErr != nil {
				t.Fatal(pinErr)
			}
		}
	}

	backend := newLocalTUIBackend(store, provider, "selected")
	overview, err := backend.LoadOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Workspace != "selected" || overview.Total != 2 || overview.Pinned != 1 || overview.TypeCounts["outcome"] != 0 {
		t.Fatalf("overview=%+v", overview)
	}
	recent, err := backend.ListRecent(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Content == "private other workspace content" {
		t.Fatalf("recent=%+v", recent)
	}
	results, err := backend.Search(ctx, "selected architecture", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Score == nil {
		t.Fatalf("search results=%+v", results)
	}
	for _, result := range results {
		if result.Content == "private other workspace content" {
			t.Fatalf("cross-workspace result=%+v", result)
		}
	}
}
