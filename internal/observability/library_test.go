package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLibraryMetricsExcludeRestrictedSourceText(t *testing.T) {
	metric := LibraryOperationMetric{Format: "epub", Workflow: "seminar", Outcome: "partial", Latency: time.Second, InputTokens: 10, OutputTokens: 20, SourceText: "restricted full passage", Quote: "short quote"}
	if err := metric.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(metric)
	if strings.Contains(string(encoded), "restricted") || strings.Contains(string(encoded), "short quote") {
		t.Fatalf("restricted content entered metric trace: %s", encoded)
	}
	if len(metric.SafeLabels()) != 3 {
		t.Fatal("unexpected metric labels")
	}
}
