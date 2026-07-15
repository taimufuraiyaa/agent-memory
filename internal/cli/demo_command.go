package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func newDemoCommand() *cobra.Command {
	var keep bool
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Prove an isolated write, search, and recall round trip",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := os.MkdirTemp("", "agent-memory-demo-")
			if err != nil {
				return err
			}
			if !keep {
				defer os.RemoveAll(path)
			}
			data, err := runDemo(cmd.Context(), path)
			if err != nil {
				return fmt.Errorf("demo failed (workspace %s): %w", path, err)
			}
			data["path"] = path
			data["kept"] = keep
			return writeSuccessEnvelope(cmd.OutOrStdout(), "demo", data)
		},
	}
	cmd.Flags().BoolVar(&keep, "keep", false, "Keep and print the isolated demo workspace")
	return cmd
}

func runDemo(ctx context.Context, path string) (map[string]any, error) {
	store, err := sqlite.Open(ctx, filepath.Join(path, "demo.db"))
	if err != nil {
		return nil, err
	}
	defer store.Close()
	modelDir := filepath.Join(path, "model")
	if err := os.MkdirAll(modelDir, 0o700); err != nil {
		return nil, err
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		return nil, err
	}
	writer := engine.NewWritePipelineWithEmbedder(store, provider)
	retrieval := engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider))
	service := application.NewMemoryService(store, writer, retrieval)
	memories := []string{
		"JWT authentication uses jose middleware for edge compatibility",
		"The N+1 database query was fixed by preloading order line items",
		"Rate limiting uses a token bucket with per-user keys",
	}
	for _, content := range memories {
		if _, err := service.Write(ctx, engine.WriteInput{Workspace: "demo", Type: core.SemanticMemory, Content: content, Source: core.MemorySource{Type: core.SourceUserInput}, Mode: engine.ExtractFast}); err != nil {
			return nil, err
		}
	}
	search, err := service.Search(ctx, engine.RetrievalOptions{Workspace: "demo", Query: "database query performance", TopK: 3, Mode: engine.ModeSearch})
	if err != nil {
		return nil, err
	}
	recall, err := service.Recall(ctx, application.RecallOptions{Workspace: "demo", Task: "continue improving database query performance", TopK: 3, Budget: 120})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"workspace":         "demo",
		"written_count":     len(memories),
		"search_request_id": search.RequestID,
		"search_hit_count":  len(search.Hits),
		"recall_request_id": recall.RequestID,
		"recall_context":    recall.ContextBlock,
	}, nil
}
