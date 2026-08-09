package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

func TestMiddlewareEmitsContentFreeLogMetricsAndTraceFields(t *testing.T) {
	var logs bytes.Buffer
	observer := New("api", slog.New(slog.NewJSONHandler(&logs, nil)))
	handler := observer.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("accepted"))
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/memories", strings.NewReader("copyrighted customer passage"))
	request = request.WithContext(auth.WithRequestContext(context.Background(), auth.RequestContext{
		TenantID: "tenant-123", RequestID: "request-123", TraceID: "trace-123",
	}))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	entry := logs.String()
	for _, required := range []string{"request-123", "trace-123", "tenant-123", "api", "success"} {
		if !strings.Contains(entry, required) {
			t.Fatalf("log does not contain required field value %q: %s", required, entry)
		}
	}
	if strings.Contains(entry, "copyrighted customer passage") || strings.Contains(entry, "accepted") {
		t.Fatalf("customer request or response content leaked into log: %s", entry)
	}

	metrics := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metrics.Body.String()
	if !strings.Contains(body, `agent_memory_saas_http_requests_total{method="POST",operation="unmatched",outcome="success",service="api"} 1`) {
		t.Fatalf("request metric missing: %s", body)
	}
	if strings.Contains(body, "tenant-123") {
		t.Fatalf("tenant id must not become a metric label: %s", body)
	}
}

func TestErrorClassNeverContainsErrorText(t *testing.T) {
	class := ErrorClass(errors.New("private filename and source fragment"))
	if class != "operation_failed" || strings.Contains(class, "private") {
		t.Fatalf("unsafe error class %q", class)
	}
}

func TestComponentMetricsUseBoundedDimensions(t *testing.T) {
	observer := New("worker", slog.New(slog.NewTextHandler(io.Discard, nil)))
	observer.RecordComponent("queue", "publish", "success", 0)
	observer.RecordComponent("model_gateway", "embed", "error", 1250)

	metrics := httptest.NewRecorder()
	observer.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metrics.Body.String()
	for _, expected := range []string{
		`agent_memory_saas_component_operations_total{component="queue",operation="publish",outcome="success",service="worker"} 1`,
		`agent_memory_saas_cost_microusd_total{component="model_gateway",service="worker"} 1250`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metric %q missing: %s", expected, body)
		}
	}
}
