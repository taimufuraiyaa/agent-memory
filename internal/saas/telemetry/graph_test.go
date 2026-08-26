package telemetry

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	baseobservability "github.com/taimufuraiyaa/agent-memory/internal/observability"
)

func TestHostedGraphTelemetryExportsBoundedMetricsWithoutContent(t *testing.T) {
	observer := New("graph-worker", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := observer.RecordGraph(baseobservability.GraphObservation{Stage: "index", Mode: "incremental", Route: "none", Outcome: "completed", Duration: time.Second, Records: 10, Entities: 3, Relationships: 2, CostMicroUSD: 42, AdapterStateBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	if err := observer.RecordGraph(baseobservability.GraphObservation{Stage: "index", Mode: "incremental", Route: "private report text", Outcome: "completed"}); err == nil {
		t.Fatal("content-shaped telemetry label accepted")
	}
	response := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "agent_memory_graph_cost_microusd_total") || strings.Contains(response.Body.String(), "private report text") {
		t.Fatalf("unsafe graph metrics: %d %s", response.Code, response.Body.String())
	}
}
