package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDashboardNPMCIRetriesAfterCleanup(t *testing.T) {
	dst := t.TempDir()
	t.Setenv("PATH", fakeNPMScriptDir(t, `#!/bin/sh
set -eu
marker=".npm-ci-attempt"
if [ ! -f "$marker" ]; then
  touch "$marker"
  mkdir -p node_modules/esbuild
  echo "simulated failure" >&2
  exit 1
fi
if [ -d node_modules ]; then
  echo "node_modules should have been removed before retry" >&2
  exit 1
fi
touch npm-ci-success
`)+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := runDashboardNPMCI(dst); err != nil {
		t.Fatalf("runDashboardNPMCI: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "npm-ci-success")); err != nil {
		t.Fatalf("expected retry success marker: %v", err)
	}
}

func TestRunDashboardNPMCIReportsRetryFailure(t *testing.T) {
	dst := t.TempDir()
	t.Setenv("PATH", fakeNPMScriptDir(t, `#!/bin/sh
echo "still broken" >&2
exit 1
`)+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := runDashboardNPMCI(dst)
	if err == nil {
		t.Fatalf("expected retry failure")
	}
	if !strings.Contains(err.Error(), "npm ci failed after clean retry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func fakeNPMScriptDir(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "npm")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	return dir
}
