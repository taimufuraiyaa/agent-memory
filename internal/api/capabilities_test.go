package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCapabilitiesReportsStableCoreContract(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil)
	rec := httptest.NewRecorder()

	capabilitiesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var got envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.OK || got.Version != envelopeVersion {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	data, ok := got.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected object data, got %T", got.Data)
	}
	if data["contract_version"] != "v1" {
		t.Fatalf("unexpected contract version: %v", data["contract_version"])
	}
	operations, ok := data["operations"].([]any)
	if !ok || len(operations) != 7 {
		t.Fatalf("expected seven core operations, got %#v", data["operations"])
	}
	formats, ok := data["result_formats"].([]any)
	if !ok || len(formats) != 2 || formats[0] != "compact" || formats[1] != "full" {
		t.Fatalf("unexpected result formats: %#v", data["result_formats"])
	}
	limits, ok := data["limits"].(map[string]any)
	if !ok || limits["max_request_bytes"] == nil || limits["max_response_bytes"] == nil {
		t.Fatalf("missing bounded limits: %#v", data["limits"])
	}
}

func TestCapabilitiesRejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/capabilities", nil)
	rec := httptest.NewRecorder()

	capabilitiesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}
}
