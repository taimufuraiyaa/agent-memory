package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostedDashboardServesAccessibleSourceProgressSurface(t *testing.T) {
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
	for _, required := range []string{
		`<main`, `data-service-mode="hosted"`, `Agent Memory / Hosted Private Vault`, `aria-live="polite"`, `aria-label="Source state legend"`,
		"Uploading", "Validating", "Processing", "Indexing", "Ready", "Failed", "Disabled", "Deleting",
		"Ask your ready sources", "Review a derived memory", "Accept into memory", "Reject",
		`src="/dashboard/app.js"`, `href="/dashboard/styles.css"`,
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("dashboard is missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(page), "localstorage") {
		t.Fatal("dashboard must not persist bearer credentials in browser storage")
	}
	for _, asset := range []string{"/dashboard/app.js", "/dashboard/styles.css"} {
		assetResponse, err := http.Get(server.URL + asset)
		if err != nil {
			t.Fatal(err)
		}
		if assetResponse.StatusCode != http.StatusOK {
			t.Fatalf("asset %s status=%d", asset, assetResponse.StatusCode)
		}
		_ = assetResponse.Body.Close()
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
