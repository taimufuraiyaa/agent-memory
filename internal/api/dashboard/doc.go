// Package dashboard provides embedded dashboard assets and HTTP serving utilities.
//
// # Overview
//
// This package embeds pre-built dashboard assets (HTML, CSS, JavaScript) into the
// agent-memory binary using Go's embed directive. This makes npm optional for users
// while maintaining a smooth development workflow.
//
// # Architecture
//
// The dashboard package uses a two-tier serving strategy:
//
//   1. External assets (development): When a dashboard source directory is available,
//      serve from that directory to enable hot-reload and rapid iteration.
//
//   2. Embedded assets (production): When no external directory is available,
//      serve pre-built assets embedded in the binary at compile time.
//
// # Asset Embedding
//
// Dashboard assets are embedded using the //go:embed directive:
//
//	//go:embed dist
//	var embeddedAssets embed.FS
//
// The dist directory contains:
//   - index.html (main dashboard page)
//   - assets/ (CSS, JavaScript bundles)
//
// Assets are built from tools/agent-memory/dashboard/ using Vite and copied
// to internal/api/dashboard/dist/ before the Go build.
//
// # Usage
//
// Check if embedded assets are available:
//
//	if dashboard.HasEmbeddedAssets() {
//	    // Use embedded assets
//	    handler := dashboard.GetEmbeddedHandler()
//	    http.Handle("/dashboard/", handler)
//	}
//
// Get the embedded filesystem directly:
//
//	fsys, err := dashboard.GetEmbeddedFS()
//	if err != nil {
//	    // Handle error
//	}
//	// Use fsys with fs.FS APIs
//
// # Development Workflow
//
// For dashboard development:
//
//	cd tools/agent-memory/dashboard
//	npm install
//	npm run dev
//
// The CLI dashboard command will prefer external assets when available,
// enabling hot-reload during development.
//
// # Build Process
//
// To build agent-memory with embedded dashboard:
//
//	# Build dashboard assets
//	cd tools/agent-memory/dashboard
//	npm ci
//	npm run build
//
//	# Copy to embeddable location
//	mkdir -p internal/api/dashboard/dist
//	cp -r tools/agent-memory/dashboard/dist/* internal/api/dashboard/dist/
//
//	# Build Go binary with embedded assets
//	go build -o agent-memory ./cmd/agent-memory
//
// Or use the Makefile:
//
//	make build-with-dashboard
//
// # Testing
//
// Test embedded asset serving:
//
//	func TestEmbeddedAssets(t *testing.T) {
//	    if !dashboard.HasEmbeddedAssets() {
//	        t.Skip("embedded assets not available")
//	    }
//
//	    handler := dashboard.GetEmbeddedHandler()
//	    req := httptest.NewRequest("GET", "/index.html", nil)
//	    rec := httptest.NewRecorder()
//	    handler.ServeHTTP(rec, req)
//
//	    if rec.Code != http.StatusOK {
//	        t.Errorf("expected 200, got %d", rec.Code)
//	    }
//	}
//
// # Benefits
//
// For Users:
//   - npm becomes optional
//   - Simpler installation (no npm errors)
//   - Faster startup (no Vite dev server)
//   - Works offline after installation
//   - Single binary distribution
//
// For Developers:
//   - Development workflow unchanged (npm/Vite still available)
//   - Embedded assets as fallback
//   - CI handles asset building
//   - No manual pre-build steps needed
//
// # Tradeoffs
//
// Binary Size:
//   - Dashboard assets add ~2-5MB to binary
//   - Acceptable tradeoff for easier distribution
//
// Maintenance:
//   - CI must build dashboard on each release
//   - Embedded assets lag behind source slightly
//   - Clear versioning needed
package dashboard
