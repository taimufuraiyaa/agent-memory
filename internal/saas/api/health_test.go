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
