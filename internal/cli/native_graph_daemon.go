package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// runNativeGraphWorker shares the daemon's registered-workspace resolution,
// with one adapter process at a time to bound local model resource contention.
func (s *serveScheduler) runNativeGraphWorker(ctx context.Context, configuration config.GraphConfig) {
	if !configuration.Enabled {
		return
	}
	worker := application.NewNativeGraphWorker(configuration)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		items, err := s.listManagedWorkspaces(ctx)
		if err != nil {
			_, _ = fmt.Fprintln(s.logOut, "native graph: workspace discovery failed")
		}
		for _, item := range items {
			if ctx.Err() != nil {
				return
			}
			store, openErr := sqlite.Open(ctx, s.workspaceDBPath(item))
			if openErr != nil {
				continue
			}
			err = worker.RunWorkspace(ctx, store, item.Name)
			_ = store.Close()
			if err != nil && ctx.Err() == nil {
				_, _ = fmt.Fprintf(s.logOut, "native graph: workspace %s indexing pass failed; inspect graph-index status and audit\n", item.Name)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
