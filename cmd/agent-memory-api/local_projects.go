package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	api "github.com/taimufuraiyaa/agent-memory/internal/saas/api"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type localProjectService struct {
	manager  *workspace.Manager
	modelDir string
}

func newLocalProjectService() (*localProjectService, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	baseDir := strings.TrimSpace(os.Getenv("AGENT_MEMORY_LOCAL_PROJECTS_DIR"))
	if baseDir == "" {
		baseDir = filepath.Join(home, ".agent-memory")
	}
	manager, err := workspace.NewManager(baseDir)
	if err != nil {
		return nil, err
	}
	modelDir := strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_DIR"))
	if modelDir == "" {
		modelDir = embeddings.DefaultModelDir(home)
	}
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return nil, err
	}
	return &localProjectService{manager: manager, modelDir: modelDir}, nil
}

func (service *localProjectService) List(ctx context.Context) ([]workspace.ListItem, error) {
	return service.manager.List(ctx)
}

func (service *localProjectService) Study(ctx context.Context, input api.LocalProjectStudyInput) (*engine.StudyResult, error) {
	project, err := service.manager.Project(input.Workspace)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(project.WorkspaceRoot) == "" {
		return nil, errors.New("project has no registered root; re-register it before studying")
	}
	provider, err := embeddings.NewLocalProvider(service.modelDir)
	if err != nil {
		return nil, err
	}
	store, err := sqlite.Open(ctx, project.DBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	writer := engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{Embedder: provider})
	return engine.NewStudyEngine(writer).IngestWithOptions(ctx, engine.StudyOptions{
		Workspace: input.Workspace,
		Sources:   workspace.DefaultStudySources(project.WorkspaceRoot),
		Depth:     input.Depth,
		DryRun:    input.DryRun,
		MaxFiles:  input.MaxFiles,
		Offset:    input.Offset,
	})
}

func (service *localProjectService) ListFeedback(ctx context.Context, workspaceName string) ([]core.RetrievalRequestLog, error) {
	store, err := service.openProjectStore(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ListRetrievalRequests(ctx, workspaceName)
}

func (service *localProjectService) RecordFeedback(ctx context.Context, input api.LocalProjectFeedbackInput) error {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return err
	}
	defer store.Close()
	useful, total := -1, -1
	if input.UsefulCount != nil {
		useful = *input.UsefulCount
	}
	if input.TotalCount != nil {
		total = *input.TotalCount
	}
	return store.RecordRequestFeedback(ctx, input.RequestID, input.Score, input.Reason, useful, total)
}

func (service *localProjectService) Search(ctx context.Context, input api.LocalProjectSearchInput) ([]api.LocalProjectMemoryResult, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	provider, err := embeddings.NewLocalProvider(service.modelDir)
	if err != nil {
		return nil, err
	}
	topK := input.Offset + input.Limit
	hits, err := engine.NewVectorSearcher(store, provider).Search(ctx, input.Workspace, input.Query, topK)
	if err != nil {
		return nil, err
	}
	if input.Offset >= len(hits) {
		return []api.LocalProjectMemoryResult{}, nil
	}
	hits = hits[input.Offset:]
	if len(hits) > input.Limit {
		hits = hits[:input.Limit]
	}
	results := make([]api.LocalProjectMemoryResult, 0, len(hits))
	for _, hit := range hits {
		results = append(results, api.LocalProjectMemoryResult{Memory: hit.Memory, Score: hit.Score, Explanation: "semantic similarity"})
	}
	return results, nil
}

func (service *localProjectService) Browse(ctx context.Context, input api.LocalProjectBrowseInput) ([]core.MemoryEntry, error) {
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	memories, err := store.ListRecentMemoriesByWorkspace(ctx, input.Workspace, input.Offset+input.Limit+1)
	if err != nil {
		return nil, err
	}
	if input.Mode == "pinned" {
		filtered := memories[:0]
		for _, memory := range memories {
			if memory.Pinned {
				filtered = append(filtered, memory)
			}
		}
		memories = filtered
	}
	if input.Offset >= len(memories) {
		return []core.MemoryEntry{}, nil
	}
	memories = memories[input.Offset:]
	if len(memories) > input.Limit {
		memories = memories[:input.Limit]
	}
	return memories, nil
}

func (service *localProjectService) GetMemory(ctx context.Context, workspaceName, memoryID string) (*core.MemoryEntry, error) {
	store, err := service.openProjectStore(ctx, workspaceName)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	memory, err := store.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	if memory.Workspace != workspaceName {
		return nil, errors.New("memory is outside the requested workspace")
	}
	return memory, nil
}

func (service *localProjectService) openProjectStore(ctx context.Context, workspaceName string) (*sqlite.Store, error) {
	project, err := service.manager.Project(workspaceName)
	if err != nil {
		return nil, err
	}
	return sqlite.Open(ctx, project.DBPath)
}
