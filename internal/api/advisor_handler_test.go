package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdvisorEndpointReturnsWorkspaceReport(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	server := httptest.NewServer(NewMux(svc))
	defer server.Close()
	defer func() { _ = svc.Close() }()

	data := getJSON(t, server.URL+"/api/v1/advisor?workspace=ws")
	if data["workspace"] != "ws" || data["grade"] != "N/A" || data["neutral"] != true {
		t.Fatalf("unexpected advisor report: %+v", data)
	}
	if _, ok := data["dimensions"].([]any); !ok {
		t.Fatalf("expected advisor dimensions, got %+v", data["dimensions"])
	}
	if _, ok := data["recommendations"].([]any); !ok {
		t.Fatalf("expected advisor recommendations, got %+v", data["recommendations"])
	}
}

func TestAdvisorEndpointRejectsUnsupportedMethod(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/advisor?workspace=ws", nil)
	rec := httptest.NewRecorder()

	NewMux(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceStatsIncludesAdvisorReport(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats?workspace=ws", nil)
	rec := httptest.NewRecorder()

	NewMux(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode stats response: %v", err)
	}
	var advisorReport struct {
		Workspace string `json:"workspace"`
		Grade     string `json:"grade"`
	}
	if err := json.Unmarshal(payload.Data["advisor"], &advisorReport); err != nil {
		t.Fatalf("decode stats advisor: %v raw=%s", err, payload.Data["advisor"])
	}
	if advisorReport.Workspace != "ws" || advisorReport.Grade != "N/A" {
		t.Fatalf("unexpected stats advisor: %+v", advisorReport)
	}
}
