# Dashboard Asset Embedding Guide

## Overview

This guide documents how to embed pre-built dashboard assets using `go:embed` to make npm optional for users.

## Current State

**Dashboard Architecture:**
- Source: `tools/agent-memory/dashboard/`
- Build tool: Vite + React + TypeScript
- Serving: Development mode uses Vite dev server, production uses built assets
- Assets: HTML, CSS, JS bundles in `dist/` after build

**Current Flow:**
1. User runs `agent-memory dashboard`
2. CLI checks for dashboard source directory
3. If missing, runs installation/upgrade to set up npm dependencies
4. Starts Vite dev server or serves built assets
5. Opens browser to dashboard URL

**Problem:**
- Requires npm to be installed
- Requires building dashboard from source
- Installation can fail if npm/node issues occur
- Increases installation complexity

## Goal

Make npm optional by embedding pre-built dashboard assets:
1. Pre-build dashboard assets during release/CI
2. Embed assets in Go binary using `go:embed`
3. Serve embedded assets as fallback when npm not available
4. Keep development workflow unchanged (npm/Vite for dev)

## Implementation Plan

### Phase 1: Embed Pre-built Assets

**1. Create embedded assets package:**

```go
// internal/api/dashboard/assets.go
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/*
var embeddedAssets embed.FS

// GetEmbeddedFS returns the embedded dashboard assets
func GetEmbeddedFS() (fs.FS, error) {
	return fs.Sub(embeddedAssets, "dist")
}

// GetEmbeddedHandler returns an HTTP handler for embedded assets
func GetEmbeddedHandler() http.Handler {
	fsys, err := GetEmbeddedFS()
	if err != nil {
		// Return error handler
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Dashboard assets not available", http.StatusNotFound)
		})
	}
	return http.FileServer(http.FS(fsys))
}
```

**2. Copy built assets to embeddable location:**

Before building Go binary, ensure dashboard is built:

```bash
# In CI or pre-release script
cd tools/agent-memory/dashboard
npm ci
npm run build

# Copy dist to embeddable location
mkdir -p internal/api/dashboard/dist
cp -r dist/* internal/api/dashboard/dist/
```

**3. Update .gitignore:**

```gitignore
# Don't commit built assets (built in CI)
internal/api/dashboard/dist/

# Or commit them for easier distribution
# (decision depends on repo size concerns)
```

### Phase 2: Fallback Logic

**Update server to use embedded assets as fallback:**

```go
// internal/api/server.go

import (
	"github.com/time/timebooks/agent-memory/internal/api/dashboard"
)

func (s *Service) serveDashboard(mux *http.ServeMux, dashboardDir string) {
	// Try external dashboard directory first (for development)
	if dashboardDir != "" && dirExists(dashboardDir) {
		mux.Handle("/dashboard/", http.StripPrefix("/dashboard/", 
			http.FileServer(http.Dir(dashboardDir))))
		return
	}
	
	// Fallback to embedded assets
	mux.Handle("/dashboard/", http.StripPrefix("/dashboard/",
		dashboard.GetEmbeddedHandler()))
}
```

### Phase 3: CLI Updates

**Update dashboard command to detect embedded assets:**

```go
// internal/cli/commands.go

func newDashboardCommand() *cobra.Command {
	// ... existing code ...
	
	// Check if embedded assets are available
	hasEmbedded := dashboard.HasEmbeddedAssets()
	
	// If embedded assets exist, skip npm requirement
	if hasEmbedded {
		fmt.Fprintf(cmd.ErrOrStderr(), 
			"Using embedded dashboard assets (npm not required)\n")
		// Start API server with embedded assets
		return startAPIServerWithEmbedded(cfg, addr)
	}
	
	// Otherwise, use existing npm-based flow
	// ... existing dashboard directory resolution ...
}
```

### Phase 4: Installation Updates

**Update install.go to make dashboard optional:**

```go
// install.go

func runInstall(cfg config) error {
	// ... existing model and runtime installation ...
	
	// Dashboard installation is now optional
	if cfg.skipDashboard {
		fmt.Fprintln(os.Stderr, "Skipping dashboard (embedded assets will be used)")
		return nil
	}
	
	// Try to install dashboard from source
	if err := bootstrap.EnsureDashboard(cfg.dataDir, cfg.quiet); err != nil {
		fmt.Fprintf(os.Stderr, 
			"Warning: dashboard installation failed: %v\n", err)
		fmt.Fprintln(os.Stderr, 
			"Dashboard will use embedded assets (limited functionality)")
		// Continue installation - not a fatal error
	}
	
	return nil
}
```

### Phase 5: Build Process

**Update Makefile with dashboard build target:**

```makefile
.PHONY: build-dashboard embed-dashboard build-with-dashboard

build-dashboard: ## Build dashboard assets for embedding
	@echo "Building dashboard assets..."
	cd tools/agent-memory/dashboard && npm ci && npm run build
	@echo "Dashboard built successfully"

embed-dashboard: build-dashboard ## Copy dashboard assets for embedding
	@echo "Embedding dashboard assets..."
	mkdir -p internal/api/dashboard/dist
	cp -r tools/agent-memory/dashboard/dist/* internal/api/dashboard/dist/
	@echo "Dashboard assets ready for embedding"

build-with-dashboard: embed-dashboard ## Build agent-memory with embedded dashboard
	@echo "Building agent-memory with embedded dashboard..."
	go build -o bin/agent-memory ./cmd/agent-memory
	@echo "Build complete with embedded dashboard"

release: build-with-dashboard ## Build release with embedded assets
	@echo "Creating release build..."
	go build -ldflags="-s -w" -o bin/agent-memory ./cmd/agent-memory
```

### Phase 6: CI Integration

**Update CI workflow to embed assets:**

```yaml
# .github/workflows/release.yml

- name: Setup Node.js
  uses: actions/setup-node@v3
  with:
    node-version: '20'

- name: Build dashboard assets
  run: |
    cd tools/agent-memory/dashboard
    npm ci
    npm run build

- name: Embed dashboard assets
  run: make embed-dashboard

- name: Build Go binary
  run: go build -ldflags="-s -w" -o agent-memory ./cmd/agent-memory

- name: Create release
  # ... upload binary with embedded assets ...
```

## Benefits

**For Users:**
- ✅ npm becomes optional
- ✅ Simpler installation (no npm errors)
- ✅ Faster startup (no Vite dev server)
- ✅ Works offline after installation
- ✅ Single binary distribution

**For Developers:**
- ✅ Development workflow unchanged (npm/Vite still available)
- ✅ Embedded assets as fallback
- ✅ CI handles asset building
- ✅ No manual pre-build steps needed

## Tradeoffs

**Binary Size:**
- Dashboard assets add ~2-5MB to binary
- Acceptable tradeoff for easier distribution

**Development:**
- Developers still use npm for hot-reload
- Production users get embedded assets
- Best of both worlds

**Maintenance:**
- CI must build dashboard on each release
- Embedded assets lag behind source slightly
- Clear versioning needed

## Migration Path

1. **Phase 1:** Implement embedding (backward compatible)
2. **Phase 2:** Test with both embedded and external assets
3. **Phase 3:** Update CI to build and embed
4. **Phase 4:** Update documentation
5. **Phase 5:** Make npm optional in installation

## Testing

**Test scenarios:**
1. Development mode (npm available)
2. Embedded mode (npm not available)
3. Explicit dashboard directory flag
4. API server with embedded assets
5. Dashboard upgrade with embedded fallback

**Test commands:**
```bash
# Test embedded assets
go:embed enabled, npm not installed
agent-memory dashboard

# Test development mode
npm installed, dashboard source available
agent-memory dashboard --start

# Test fallback
npm installed but dashboard build failed
agent-memory dashboard  # should use embedded
```

## Documentation Updates

**README.md:**
```markdown
## Dashboard

The dashboard is embedded in the agent-memory binary, so npm is optional.

### Using Embedded Dashboard (Recommended)
Just run: `agent-memory dashboard`

### Using Development Dashboard
For hot-reload during development:
1. Install npm dependencies: `cd tools/agent-memory/dashboard && npm ci`
2. Run dashboard: `agent-memory dashboard --dev`
```

**CONTRIBUTING.md:**
```markdown
## Dashboard Development

The dashboard can be developed with hot-reload:

```bash
cd tools/agent-memory/dashboard
npm install
npm run dev
```

Built assets are embedded in the binary during release.
```

## Implementation Checklist

- [ ] Create `internal/api/dashboard/assets.go` with `go:embed`
- [ ] Update server.go to serve embedded assets as fallback
- [ ] Add `build-dashboard` and `embed-dashboard` Makefile targets
- [ ] Update CLI dashboard command to detect embedded assets
- [ ] Make dashboard installation optional in install.go
- [ ] Update CI workflow to build and embed assets
- [ ] Add tests for embedded asset serving
- [ ] Update documentation (README, CONTRIBUTING)
- [ ] Test with npm not installed
- [ ] Test fallback behavior
- [ ] Update release process

## Estimated Effort

**Total: 3-4 hours**
- Embedding setup: 1 hour
- Fallback logic: 1 hour
- CI integration: 30 minutes
- Testing: 1 hour
- Documentation: 30 minutes

## References

- Go embed directive: https://pkg.go.dev/embed
- `http.FileServer` with `embed.FS`: https://pkg.go.dev/net/http#FileServer
- Dashboard source: `tools/agent-memory/dashboard/`
- Current serving: `internal/cli/commands.go:newDashboardCommand`

## Next Steps

1. Review this guide with team
2. Decide on embedded assets commit strategy (commit to repo or build in CI only)
3. Implement Phase 1 (embedding)
4. Test locally
5. Proceed with remaining phases

This implementation will make agent-memory more user-friendly while maintaining developer workflow.
