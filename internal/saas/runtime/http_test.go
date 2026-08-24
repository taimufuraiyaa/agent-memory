package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeHandlerExposesContentFreeLivenessAndReadiness(t *testing.T) {
	handler := ProbeHandler("api", func() bool { return true })

	for _, path := range []string{"/health/live", "/health/ready"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, recorder.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s response: %v", path, err)
		}
		if body["service"] != "api" || body["status"] != "ok" {
			t.Fatalf("unexpected %s response: %#v", path, body)
		}
	}
}

func TestProbeHandlerFailsReadinessWithoutLeakingDetails(t *testing.T) {
	handler := ProbeHandler("worker", func() bool { return false })
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if recorder.Body.String() != "{\"service\":\"worker\",\"status\":\"unavailable\"}\n" {
		t.Fatalf("unexpected content-bearing readiness response: %s", recorder.Body.String())
	}
}
