package observability

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

func TestGraphPreflightRejectsWorkspaceLimitBeforeModelCall(t *testing.T) {
	limits := GraphWorkspaceLimits{MaxPendingRecords: 100, MaxInputTokens: 10_000, MaxCostMicroUSD: 50_000, MaxArtifactBytes: 1 << 20}
	if err := CheckGraphPreflight(limits, GraphPreflight{PendingRecords: 101, InputTokens: 1, CostMicroUSD: 1, ArtifactBytes: 1}); !errors.Is(err, ErrGraphLimitExceeded) {
		t.Fatalf("pending limit was not fail-closed: %v", err)
	}
	if err := CheckGraphPreflight(limits, GraphPreflight{PendingRecords: 10, InputTokens: 1000, CostMicroUSD: 1000, ArtifactBytes: 1000}); err != nil {
		t.Fatal(err)
	}
}

func TestGraphMetricsAreContentFreeAndBounded(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewGraphMetrics(registry)
	if err := metrics.Observe(GraphObservation{Stage: "query", Mode: "local_graph", Route: "local_graph", Outcome: "completed", Duration: 20 * time.Millisecond, QueueAge: time.Second, RevisionAge: time.Minute, Records: 2, Entities: 1, Relationships: 1, InputTokens: 20, CostMicroUSD: 3, CacheHit: true, CacheObserved: true, FeedbackOutcome: "helpful"}); err != nil {
		t.Fatal(err)
	}
	if err := metrics.Observe(GraphObservation{Stage: "query", Mode: "local_graph", Route: "Book A private title", Outcome: "completed"}); err == nil {
		t.Fatal("content-shaped route label was accepted")
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	encoder := expfmt.NewEncoder(&output, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(output.String(), "Book A") || !strings.Contains(output.String(), "agent_memory_graph_duration_seconds") {
		t.Fatalf("unsafe or absent graph metrics:\n%s", output.String())
	}
}

func TestGraphMetricsRejectInvalidConditionalLabelsBeforeMutation(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewGraphMetrics(registry)
	if err := metrics.Observe(GraphObservation{Stage: "query", Mode: "local_graph", Route: "local_graph", Outcome: "completed", Fallback: true, Reason: "private source title"}); err == nil {
		t.Fatal("content-shaped fallback reason was accepted")
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "agent_memory_graph_operations_total" && len(family.Metric) != 0 {
			t.Fatal("rejected graph observation partially mutated metrics")
		}
	}
}
