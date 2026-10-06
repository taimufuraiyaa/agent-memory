package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
)

type localTUIBackend struct {
	store     *sqlite.Store
	service   *application.MemoryService
	workspace string
}

func newLocalTUIBackend(store *sqlite.Store, provider embeddings.Provider, workspace string) *localTUIBackend {
	var service *application.MemoryService
	if store != nil && provider != nil {
		searcher := engine.NewVectorSearcher(store, provider)
		service = application.NewMemoryService(store, nil, engine.NewRetrievalEngine(searcher))
	}
	return &localTUIBackend{
		store:     store,
		service:   service,
		workspace: strings.TrimSpace(workspace),
	}
}

func (backend *localTUIBackend) LoadOverview(ctx context.Context) (memorytui.Overview, error) {
	if backend == nil || backend.store == nil || backend.workspace == "" {
		return memorytui.Overview{}, errors.New("TUI backend is not configured")
	}
	memories, err := backend.store.ListMemoriesByWorkspace(ctx, backend.workspace)
	if err != nil {
		return memorytui.Overview{}, err
	}
	overview := memorytui.Overview{
		Workspace:  backend.workspace,
		Total:      len(memories),
		TypeCounts: make(map[string]int),
		TierCounts: make(map[string]int),
	}
	for _, memory := range memories {
		overview.TypeCounts[string(memory.Type)]++
		overview.TierCounts[string(memory.StorageTier)]++
		if memory.Pinned {
			overview.Pinned++
		}
		if memory.Diagram != nil && strings.TrimSpace(memory.Diagram.Code) != "" {
			overview.Diagrams++
		}
		if memory.UpdatedAt.After(overview.UpdatedAt) {
			overview.UpdatedAt = memory.UpdatedAt
		}
	}
	return overview, nil
}

func (backend *localTUIBackend) ListRecent(ctx context.Context, limit int) ([]memorytui.MemoryItem, error) {
	if backend == nil || backend.store == nil || backend.workspace == "" {
		return nil, errors.New("TUI backend is not configured")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	memories, err := backend.store.ListRecentMemoriesByWorkspace(ctx, backend.workspace, limit)
	if err != nil {
		return nil, err
	}
	items := make([]memorytui.MemoryItem, 0, len(memories))
	for _, memory := range memories {
		items = append(items, tuiMemoryItem(memory, nil))
	}
	return items, nil
}

func (backend *localTUIBackend) Search(ctx context.Context, query string, limit int) ([]memorytui.MemoryItem, error) {
	if backend == nil || backend.service == nil || backend.workspace == "" {
		return nil, errors.New("TUI search backend is not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is required")
	}
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	result, err := backend.service.Search(ctx, engine.RetrievalOptions{
		Workspace: backend.workspace,
		Query:     query,
		TopK:      limit,
		Mode:      engine.ModeSearch,
	})
	if err != nil {
		return nil, err
	}
	items := make([]memorytui.MemoryItem, 0, len(result.Hits))
	for _, hit := range result.Hits {
		score := hit.Score
		items = append(items, tuiMemoryItem(hit.Memory, &score))
	}
	return items, nil
}

func tuiMemoryItem(memory core.MemoryEntry, score *float64) memorytui.MemoryItem {
	item := memorytui.MemoryItem{
		ID:           memory.ID,
		Type:         string(memory.Type),
		StorageTier:  string(memory.StorageTier),
		Content:      memory.Content,
		SourceType:   string(memory.Source.Type),
		SourcePath:   memory.Source.FilePath,
		CreatedAt:    memory.CreatedAt,
		UpdatedAt:    memory.UpdatedAt,
		LastAccessed: memory.LastAccessedAt,
		Pinned:       memory.Pinned,
		AccessCount:  memory.AccessCount,
		Score:        score,
	}
	if item.SourcePath == "" {
		item.SourcePath = memory.Source.NotePath
	}
	if memory.Outcome != nil {
		item.Outcome = &memorytui.Outcome{
			Result:   string(memory.Outcome.Result),
			Approach: memory.Outcome.Approach,
			Reason:   memory.Outcome.Reason,
		}
	}
	return item
}
