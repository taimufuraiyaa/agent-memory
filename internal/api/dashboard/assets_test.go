package dashboard

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHasEmbeddedAssets(t *testing.T) {
	// This test will pass once assets are actually embedded
	// For now, it validates the API works correctly
	hasAssets := HasEmbeddedAssets()
	t.Logf("HasEmbeddedAssets() = %v", hasAssets)

	// Test should not fail, just report status
	// In production builds with embedded assets, this will be true
	// In development builds without embedded assets, this will be false
}

func TestGetEmbeddedFS(t *testing.T) {
	fsys, err := GetEmbeddedFS()
	if err != nil {
		// This is expected when assets are not embedded
		t.Logf("GetEmbeddedFS() error (expected without embedded assets): %v", err)
		return
	}

	// If we got a filesystem, validate it has the expected structure
	if fsys == nil {
		t.Fatal("GetEmbeddedFS() returned nil filesystem without error")
	}

	// Try to read the root directory
	_, err = fs.ReadDir(fsys, ".")
	if err != nil {
		t.Errorf("ReadDir(\".\") error: %v", err)
	}
}

func TestGetEmbeddedHandler(t *testing.T) {
	handler := GetEmbeddedHandler()
	if handler == nil {
		t.Fatal("GetEmbeddedHandler() returned nil")
	}

	// Test that the handler responds (even if with 404 when assets not embedded)
	req := httptest.NewRequest("GET", "/index.html", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should return some response (200, 301, or 404)
	// 301 is redirect from http.FileServer (e.g., directory -> directory/)
	// 404 if not embedded, 200 if embedded
	if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound && rec.Code != http.StatusMovedPermanently {
		t.Errorf("expected 200, 301, or 404, got %d", rec.Code)
	}

	t.Logf("Handler response status: %d", rec.Code)
}

func TestEmbeddedHandlerPreventsStaleDashboardBundles(t *testing.T) {
	handler := GetEmbeddedHandler()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", got)
	}
}

func TestEmbeddedAssetsStructure(t *testing.T) {
	if !HasEmbeddedAssets() {
		t.Skip("embedded assets not available")
	}

	fsys, err := GetEmbeddedFS()
	if err != nil {
		t.Fatal(err)
	}

	// Check for index.html
	_, err = fs.Stat(fsys, "index.html")
	if err != nil {
		t.Errorf("index.html not found: %v", err)
	}

	// Check for assets directory
	info, err := fs.Stat(fsys, "assets")
	if err != nil {
		t.Errorf("assets directory not found: %v", err)
	} else if !info.IsDir() {
		t.Error("assets is not a directory")
	}
}

func TestEmbeddedDashboardIncludesResponsiveNavigation(t *testing.T) {
	fsys, err := GetEmbeddedFS()
	if err != nil {
		t.Fatal(err)
	}

	stylesheet, err := fs.ReadFile(fsys, "assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte(".navMenuTrigger"),
		[]byte("@media (max-width: 2160px)"),
		[]byte("max-width: calc(100vw - 24px)"),
	} {
		if !bytes.Contains(stylesheet, marker) {
			t.Fatalf("embedded stylesheet is missing responsive navigation marker %q", marker)
		}
	}
}

func TestEmbeddedDashboardIsNotebookFirstAndSelfContained(t *testing.T) {
	fsys, err := GetEmbeddedFS()
	if err != nil {
		t.Fatal(err)
	}
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(index, []byte(`src="./assets/app.js"`)) || !bytes.Contains(index, []byte(`href="./assets/app.css"`)) {
		t.Fatalf("embedded index must use dashboard-relative assets: %s", index)
	}
	if bytes.Contains(index, []byte("fonts.googleapis.com")) || bytes.Contains(index, []byte("fonts.gstatic.com")) {
		t.Fatal("embedded dashboard must not request external font assets")
	}
	app, err := fs.ReadFile(fsys, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{[]byte("NotebookWorkspace"), []byte("Human note"), []byte("Write what matters.")} {
		if !bytes.Contains(app, marker) {
			t.Fatalf("embedded app is missing notebook marker %q", marker)
		}
	}
}

func BenchmarkGetEmbeddedHandler(b *testing.B) {
	req := httptest.NewRequest("GET", "/index.html", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler := GetEmbeddedHandler()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}
