package advisor

import (
	"context"
	"fmt"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type Source interface {
	ListMemoriesByWorkspace(context.Context, string) ([]core.MemoryEntry, error)
	ListRetrievalRequests(context.Context, string) ([]core.RetrievalRequestLog, error)
	AggregateTokenMetricsByOperation(context.Context, string) ([]sqlite.TokenMetricOperationTotals, error)
}

func BuildReport(ctx context.Context, source Source, workspace string) (Report, error) {
	memories, err := source.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil {
		return Report{}, fmt.Errorf("advisor: load memories: %w", err)
	}
	requests, err := source.ListRetrievalRequests(ctx, workspace)
	if err != nil {
		return Report{}, fmt.Errorf("advisor: load requests: %w", err)
	}
	metrics, err := source.AggregateTokenMetricsByOperation(ctx, workspace)
	if err != nil {
		return Report{}, fmt.Errorf("advisor: load metrics: %w", err)
	}
	return Analyze(Snapshot{
		Workspace:               workspace,
		Memories:                memories,
		Requests:                requests,
		TokenMetricsByOperation: metrics,
	}), nil
}
