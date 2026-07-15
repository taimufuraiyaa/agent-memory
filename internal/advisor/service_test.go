package advisor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type fakeSource struct {
	memories []core.MemoryEntry
	requests []core.RetrievalRequestLog
	metrics  []sqlite.TokenMetricOperationTotals
	errAt    string
}

func (f fakeSource) ListMemoriesByWorkspace(context.Context, string) ([]core.MemoryEntry, error) {
	if f.errAt == "memories" {
		return nil, errors.New("memory read failed")
	}
	return f.memories, nil
}

func (f fakeSource) ListRetrievalRequests(context.Context, string) ([]core.RetrievalRequestLog, error) {
	if f.errAt == "requests" {
		return nil, errors.New("request read failed")
	}
	return f.requests, nil
}

func (f fakeSource) AggregateTokenMetricsByOperation(context.Context, string) ([]sqlite.TokenMetricOperationTotals, error) {
	if f.errAt == "metrics" {
		return nil, errors.New("metric read failed")
	}
	return f.metrics, nil
}

func TestBuildReportLoadsWorkspaceEvidence(t *testing.T) {
	source := fakeSource{
		memories: []core.MemoryEntry{healthyMemory("safe", 0)},
		requests: []core.RetrievalRequestLog{{Score: 5}, {Score: 5}, {Score: 5}},
		metrics: []sqlite.TokenMetricOperationTotals{{
			Operation:         "recall",
			TokenMetricTotals: sqlite.TokenMetricTotals{Records: 3, BaselineTokens: 100, SavedTokens: 50},
		}},
	}

	report, err := BuildReport(context.Background(), source, "client-workspace")
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if report.Workspace != "client-workspace" || report.Evidence.MemoryCount != 1 || report.Evidence.ScoredRequestCount != 3 {
		t.Fatalf("unexpected assembled report: %+v", report)
	}
}

func TestBuildReportAddsContextToReadFailures(t *testing.T) {
	for _, stage := range []string{"memories", "requests", "metrics"} {
		t.Run(stage, func(t *testing.T) {
			_, err := BuildReport(context.Background(), fakeSource{errAt: stage}, "ws")
			if err == nil || !strings.Contains(err.Error(), "advisor: load "+stage) {
				t.Fatalf("expected contextual %s error, got %v", stage, err)
			}
		})
	}
}
