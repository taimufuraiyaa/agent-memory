package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneDashboardRuntimeManifest(t *testing.T) {
	handler := NewMux(&Service{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard/runtime.json", nil))
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
	if manifest.Schema != "agent-memory-dashboard-runtime-v1" || manifest.Mode != "standalone" || manifest.APIPrefix != "/api/v1" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected headers: %v", response.Header())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/dashboard/runtime.json", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST runtime status=%d", response.Code)
	}
}

func TestServiceFixedWorkspaceUsesExactDBPath(t *testing.T) {
	exact := filepath.Join(t.TempDir(), "custom-name.db")
	service := &Service{Workspace: "workspace-name", BaseDir: filepath.Dir(exact), DBPath: exact}
	defer func() { _ = service.Close() }()
	assets, err := service.resolve(context.Background(), "workspace-name")
	if err != nil {
		t.Fatal(err)
	}
	if assets.DBPath != exact {
		t.Fatalf("resolved %q, want exact database %q", assets.DBPath, exact)
	}
}

func TestWorkspaceRouteServesEmbeddedSPAShell(t *testing.T) {
	server := httptest.NewServer(NewMux(&Service{}))
	defer server.Close()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, _ := http.NewRequest(method, server.URL+"/w/agent-memory/knowledge/history", nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s workspace route returned %d", method, res.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/w/agent-memory/knowledge/history", strings.NewReader("{}"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("workspace route mutation returned %d", res.StatusCode)
	}
}
