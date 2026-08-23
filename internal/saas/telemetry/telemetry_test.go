package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

func TestEvidenceHandlerReturnsExactRecentContentFreeObservation(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	observer := newWithClock("api", slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })
	routes := http.NewServeMux()
	routes.Handle("GET /health/ready", observer.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	routes.Handle("GET /internal/evidence/requests/{request_id}", observer.EvidenceHandler())

	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set("X-Request-ID", "probe-request-123")
	request.Header.Set("X-Trace-ID", "probe-trace-123")
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Header().Get("X-Request-ID") != "probe-request-123" || response.Header().Get("X-Trace-ID") != "probe-trace-123" {
		t.Fatalf("correlation headers were not echoed: %v", response.Header())
	}

	lookup := httptest.NewRecorder()
	routes.ServeHTTP(lookup, httptest.NewRequest(http.MethodGet, "/internal/evidence/requests/probe-request-123", nil))
	if lookup.Code != http.StatusOK {
		t.Fatalf("lookup status=%d body=%s", lookup.Code, lookup.Body.String())
	}
	for _, required := range []string{"probe-request-123", "probe-trace-123", "GET:/health/ready", "success"} {
		if !strings.Contains(lookup.Body.String(), required) {
			t.Fatalf("observation missing %q: %s", required, lookup.Body.String())
		}
	}
	for _, forbidden := range []string{"tenant", "body", "payload", "customer", "authorization"} {
		if strings.Contains(strings.ToLower(lookup.Body.String()), forbidden) {
			t.Fatalf("observation leaked forbidden field %q: %s", forbidden, lookup.Body.String())
		}
	}
}

func TestEvidenceObservationsExpireAndRemainCapacityBounded(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	observer := newWithClock("api", slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })
	handler := observer.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for index := 0; index < maximumEvidenceObservations+1; index++ {
		request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		request.Header.Set("X-Request-ID", fmt.Sprintf("probe-%04d", index))
		request.Header.Set("X-Trace-ID", fmt.Sprintf("trace-%04d", index))
		handler.ServeHTTP(httptest.NewRecorder(), request)
		now = now.Add(time.Millisecond)
	}
	if observer.evidenceCount() != maximumEvidenceObservations {
		t.Fatalf("observation count=%d, want %d", observer.evidenceCount(), maximumEvidenceObservations)
	}
	if observer.observation("probe-0000").RequestID != "" {
		t.Fatal("oldest observation was not evicted")
	}
	now = now.Add(evidenceObservationTTL + time.Second)
	if observer.observation(fmt.Sprintf("probe-%04d", maximumEvidenceObservations)).RequestID != "" || observer.evidenceCount() != 0 {
		t.Fatal("expired observations remained available")
	}
}

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

func TestMiddlewareSuppressesSuccessfulHealthRequestLogsButKeepsFailures(t *testing.T) {
	var logs bytes.Buffer
	observer := New("api", slog.New(slog.NewJSONHandler(&logs, nil)))
	healthy := observer.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	healthy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	healthy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if logs.Len() != 0 {
		t.Fatalf("successful health checks wrote request logs: %s", logs.String())
	}

	unhealthy := observer.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	unhealthy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if !strings.Contains(logs.String(), `"status":503`) {
		t.Fatalf("failed health check was not logged: %s", logs.String())
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
