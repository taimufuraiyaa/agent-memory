// Package telemetry provides content-free, bounded-cardinality observability
// for the hosted product boundary.
package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	maximumEvidenceObservations = 1024
	evidenceObservationTTL      = 10 * time.Minute
)

var evidenceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

// Observer deliberately accepts only operational metadata. Request bodies,
// response bodies, source text, prompts, and model output cannot enter it.
type Observer struct {
	service           string
	logger            *slog.Logger
	registry          *prometheus.Registry
	requests          *prometheus.CounterVec
	duration          *prometheus.HistogramVec
	inFlight          prometheus.Gauge
	componentOps      *prometheus.CounterVec
	componentDuration *prometheus.HistogramVec
	costMicroUSD      *prometheus.CounterVec
	evidenceMu        sync.Mutex
	evidence          map[string]EvidenceObservation
	now               func() time.Time
}

type EvidenceObservation struct {
	RequestID  string    `json:"request_id"`
	TraceID    string    `json:"trace_id"`
	Service    string    `json:"service"`
	Operation  string    `json:"operation"`
	Status     int       `json:"status"`
	Outcome    string    `json:"outcome"`
	ObservedAt time.Time `json:"observed_at"`
}

func New(service string, logger *slog.Logger) *Observer {
	return newWithClock(service, logger, time.Now)
}

func newWithClock(service string, logger *slog.Logger, now func() time.Time) *Observer {
	service = bounded(service, "unknown")
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	registry := prometheus.NewRegistry()
	o := &Observer{
		service:           service,
		logger:            logger,
		registry:          registry,
		requests:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_saas_http_requests_total", Help: "Hosted HTTP requests by bounded operation and outcome."}, []string{"service", "method", "operation", "outcome"}),
		duration:          prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_saas_http_request_duration_seconds", Help: "Hosted HTTP request latency by bounded operation.", Buckets: prometheus.DefBuckets}, []string{"service", "method", "operation"}),
		inFlight:          prometheus.NewGauge(prometheus.GaugeOpts{Name: "agent_memory_saas_http_requests_in_flight", Help: "Hosted HTTP requests currently executing."}),
		componentOps:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_saas_component_operations_total", Help: "Content-free operation outcomes across hosted components."}, []string{"service", "component", "operation", "outcome"}),
		componentDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_saas_component_operation_duration_seconds", Help: "Content-free operation latency across hosted components.", Buckets: prometheus.DefBuckets}, []string{"service", "component", "operation"}),
		costMicroUSD:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_saas_cost_microusd_total", Help: "Attributed hosted cost in integer millionths of a US dollar."}, []string{"service", "component"}),
		evidence:          make(map[string]EvidenceObservation, maximumEvidenceObservations),
		now:               now,
	}
	registry.MustRegister(o.requests, o.duration, o.inFlight, o.componentOps, o.componentDuration, o.costMicroUSD)
	return o
}

func (o *Observer) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(o.registry, promhttp.HandlerOpts{})
}

func (o *Observer) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		o.inFlight.Inc()
		defer o.inFlight.Dec()

		request, authenticated := auth.FromContext(r.Context())
		if !authenticated {
			request.TenantID = "public"
			request.RequestID = bounded(r.Header.Get("X-Request-ID"), uuid.NewString())
			request.TraceID = bounded(r.Header.Get("X-Trace-ID"), uuid.NewString())
		}
		w.Header().Set("X-Request-ID", request.RequestID)
		w.Header().Set("X-Trace-ID", request.TraceID)
		operation := operationName(r)
		ctx, span := otel.Tracer("agent-memory/saas").Start(r.Context(), "http."+operation,
			trace.WithAttributes(
				attribute.String("service.name", o.service),
				attribute.String("agent_memory.operation", operation),
				attribute.String("agent_memory.tenant_id", request.TenantID),
				attribute.String("agent_memory.request_id", request.RequestID),
				attribute.String("agent_memory.trace_id", request.TraceID),
			))
		defer span.End()

		response := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(response, r.WithContext(ctx))
		outcome := outcomeFor(response.status)
		elapsed := time.Since(started)
		o.requests.WithLabelValues(o.service, r.Method, operation, outcome).Inc()
		o.duration.WithLabelValues(o.service, r.Method, operation).Observe(elapsed.Seconds())
		o.recordEvidence(EvidenceObservation{
			RequestID: request.RequestID, TraceID: request.TraceID, Service: o.service,
			Operation: operation, Status: response.status, Outcome: outcome, ObservedAt: o.now().UTC(),
		})
		span.SetAttributes(attribute.Int("http.response.status_code", response.status), attribute.String("agent_memory.outcome", outcome))
		if response.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, "server_error")
		} else {
			span.SetStatus(codes.Ok, "")
		}
		if shouldLogRequest(r, response.status) {
			o.logger.InfoContext(ctx, "request completed",
				"request_id", request.RequestID,
				"trace_id", request.TraceID,
				"tenant_id", request.TenantID,
				"service", o.service,
				"operation", operation,
				"method", r.Method,
				"status", response.status,
				"outcome", outcome,
				"duration_ms", elapsed.Milliseconds(),
			)
		}
	})
}

func shouldLogRequest(r *http.Request, status int) bool {
	isHealthCheck := r.Method == http.MethodGet && (r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/health/"))
	return !isHealthCheck || status >= http.StatusBadRequest
}

func (o *Observer) EvidenceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.PathValue("request_id"))
		if !evidenceIDPattern.MatchString(requestID) {
			http.NotFound(w, r)
			return
		}
		observation := o.observation(requestID)
		if observation.RequestID == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(observation)
	})
}

func (o *Observer) recordEvidence(observation EvidenceObservation) {
	if !evidenceIDPattern.MatchString(observation.RequestID) || !evidenceIDPattern.MatchString(observation.TraceID) {
		return
	}
	o.evidenceMu.Lock()
	defer o.evidenceMu.Unlock()
	o.expireEvidenceLocked(o.now().UTC())
	if _, exists := o.evidence[observation.RequestID]; !exists && len(o.evidence) >= maximumEvidenceObservations {
		oldestID := ""
		var oldest time.Time
		for requestID, candidate := range o.evidence {
			if oldestID == "" || candidate.ObservedAt.Before(oldest) {
				oldestID, oldest = requestID, candidate.ObservedAt
			}
		}
		delete(o.evidence, oldestID)
	}
	o.evidence[observation.RequestID] = observation
}

func (o *Observer) observation(requestID string) EvidenceObservation {
	o.evidenceMu.Lock()
	defer o.evidenceMu.Unlock()
	o.expireEvidenceLocked(o.now().UTC())
	return o.evidence[requestID]
}

func (o *Observer) evidenceCount() int {
	o.evidenceMu.Lock()
	defer o.evidenceMu.Unlock()
	o.expireEvidenceLocked(o.now().UTC())
	return len(o.evidence)
}

func (o *Observer) expireEvidenceLocked(now time.Time) {
	cutoff := now.Add(-evidenceObservationTTL)
	for requestID, observation := range o.evidence {
		if observation.ObservedAt.Before(cutoff) {
			delete(o.evidence, requestID)
		}
	}
}

// RecordComponent records only bounded component metadata. costMicroUSD may be
// zero and is kept integer-valued so floating-point currency is never logged.
func (o *Observer) RecordComponent(component, operation, outcome string, costMicroUSD int64) {
	component = bounded(component, "unknown")
	operation = bounded(operation, "unknown")
	outcome = bounded(outcome, "unknown")
	o.componentOps.WithLabelValues(o.service, component, operation, outcome).Inc()
	if costMicroUSD > 0 {
		o.costMicroUSD.WithLabelValues(o.service, component).Add(float64(costMicroUSD))
	}
	_, span := otel.Tracer("agent-memory/saas").Start(context.Background(), component+"."+operation,
		trace.WithAttributes(
			attribute.String("service.name", o.service),
			attribute.String("agent_memory.component", component),
			attribute.String("agent_memory.operation", operation),
			attribute.String("agent_memory.outcome", outcome),
			attribute.Int64("agent_memory.cost_microusd", costMicroUSD),
		))
	if outcome == "error" {
		span.SetStatus(codes.Error, "operation_error")
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

func (o *Observer) ObserveComponentDuration(component, operation string, elapsed time.Duration) {
	if elapsed >= 0 {
		o.componentDuration.WithLabelValues(o.service, bounded(component, "unknown"), bounded(operation, "unknown")).Observe(elapsed.Seconds())
	}
}

func operationName(r *http.Request) string {
	if pattern := strings.TrimSpace(r.Pattern); pattern != "" {
		return bounded(strings.ReplaceAll(pattern, " ", ":"), "unmatched")
	}
	return "unmatched"
}

func outcomeFor(status int) string {
	switch {
	case status >= 500:
		return "error"
	case status >= 400:
		return "rejected"
	default:
		return "success"
	}
}

func bounded(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if len(value) > 96 {
		return value[:96]
	}
	return value
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (o *Observer) ServeMetrics(ctx context.Context, address string) error {
	server := &http.Server{Addr: address, Handler: o.MetricsHandler(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("serve telemetry: %w", err)
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func StatusOutcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

// ErrorClass converts arbitrary provider/parser/storage errors into a fixed,
// content-free diagnostic category suitable for logs and traces.
func ErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return "operation_failed"
}

func HTTPStatusLabel(status int) string { return strconv.Itoa(status) }
