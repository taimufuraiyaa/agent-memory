package dashboard

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	shareddashboard "github.com/taimufuraiyaa/agent-memory/internal/api/dashboard"
)

func TestHostedDashboardUsesSharedReactDistribution(t *testing.T) {
	hosted := httptest.NewRecorder()
	Handler().ServeHTTP(hosted, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	shared := httptest.NewRecorder()
	shareddashboard.GetEmbeddedHandler().ServeHTTP(shared, httptest.NewRequest(http.MethodGet, "/", nil))
	if hosted.Code != http.StatusOK || shared.Code != http.StatusOK {
		t.Fatalf("hosted status=%d shared status=%d", hosted.Code, shared.Code)
	}
	if !bytes.Equal(hosted.Body.Bytes(), shared.Body.Bytes()) {
		t.Fatal("hosted dashboard index differs from the shared React distribution")
	}
	if hosted.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("hosted cache control=%q", hosted.Header().Get("Cache-Control"))
	}
}

func TestHostedDashboardServesSharedReactAssets(t *testing.T) {
	server := httptest.NewServer(Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/dashboard/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, required := range []string{`id="root"`, `src="./assets/app.js"`, `href="./assets/app.css"`} {
		if !strings.Contains(page, required) {
			t.Fatalf("dashboard is missing %q", required)
		}
	}
	for _, asset := range []string{"/dashboard/assets/app.js", "/dashboard/assets/app.css"} {
		assetResponse, err := http.Get(server.URL + asset)
		if err != nil {
			t.Fatal(err)
		}
		if assetResponse.StatusCode != http.StatusOK {
			t.Fatalf("asset %s status=%d", asset, assetResponse.StatusCode)
		}
		assetBody, err := io.ReadAll(assetResponse.Body)
		_ = assetResponse.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if asset == "/dashboard/assets/app.js" {
			script := string(assetBody)
			for _, required := range []string{"NotebookWorkspace", "Human note", "Write what matters."} {
				if !strings.Contains(script, required) {
					t.Fatalf("shared dashboard script is missing %q", required)
				}
			}
		}
	}
}

func TestHostedDashboardDoesNotFallbackForUnknownAssets(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/dashboard/not-found.js", nil)
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown asset status=%d", response.Code)
	}
}
