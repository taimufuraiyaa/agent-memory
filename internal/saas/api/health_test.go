package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadinessProbeFailsClosedWithoutLeakingDependencyDetails(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	recorder := httptest.NewRecorder()
	readyProbe(func(context.Context) error { return errors.New("postgres password and endpoint") })(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "postgres") || strings.Contains(recorder.Body.String(), "password") {
		t.Fatalf("dependency detail leaked: %s", recorder.Body.String())
	}
}

func TestReadinessProbeBoundsChecksAndReportsSuccess(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	recorder := httptest.NewRecorder()
	deadlineSeen := false
	readyProbe(func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		deadlineSeen = ok && time.Until(deadline) <= 3*time.Second
		return nil
	})(recorder, request)
	if recorder.Code != http.StatusOK || !deadlineSeen || !strings.Contains(recorder.Body.String(), `"status":"ok"`) {
		t.Fatalf("status=%d deadline=%v body=%s", recorder.Code, deadlineSeen, recorder.Body.String())
	}
}

func TestOperationalRoutesObserveReadinessAndKeepEvidenceLookupInternal(t *testing.T) {
	observer := &operationalObserver{}
	routes := http.NewServeMux()
	registerOperationalRoutes(routes, func(context.Context) error { return nil }, observer)

	ready := httptest.NewRecorder()
	routes.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusOK || ready.Header().Get("X-Test-Observed") != "true" {
		t.Fatalf("readiness was not observed: status=%d headers=%v", ready.Code, ready.Header())
	}

	evidence := httptest.NewRecorder()
	routes.ServeHTTP(evidence, httptest.NewRequest(http.MethodGet, "/internal/evidence/requests/probe-123", nil))
	if evidence.Code != http.StatusNoContent || observer.evidenceCalls != 1 {
		t.Fatalf("evidence route status=%d calls=%d", evidence.Code, observer.evidenceCalls)
	}
}

type operationalObserver struct {
	evidenceCalls int
}

func (observer *operationalObserver) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Observed", "true")
		next.ServeHTTP(w, r)
	})
}

func (observer *operationalObserver) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func (observer *operationalObserver) EvidenceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		observer.evidenceCalls++
		w.WriteHeader(http.StatusNoContent)
	})
}
