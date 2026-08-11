package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostedDashboardRuntimeManifest(t *testing.T) {
	response := httptest.NewRecorder()
	dashboardRuntime("hosted", "/v1").ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard/runtime.json", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", response.Code, response.Body.String())
	}
	var manifest struct {
		Schema    string   `json:"schema"`
		Mode      string   `json:"mode"`
		APIPrefix string   `json:"api_prefix"`
		Features  []string `json:"features"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != "agent-memory-dashboard-runtime-v1" || manifest.Mode != "hosted" || manifest.APIPrefix != "/v1" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected headers: %v", response.Header())
	}

	response = httptest.NewRecorder()
	dashboardRuntime("hosted", "/v1").ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/dashboard/runtime.json", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST runtime status=%d", response.Code)
	}
}
