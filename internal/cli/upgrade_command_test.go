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

func TestUpgradeAllProjects(t *testing.T) {
	tempBase := t.TempDir()

	proj1Root := filepath.Join(tempBase, "proj1")
	proj2Root := filepath.Join(tempBase, "proj2")

	err := os.MkdirAll(filepath.Join(proj1Root, ".agents"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll(filepath.Join(proj2Root, ".agents"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tempBase)

	dataDir := filepath.Join(tempBase, ".agent-memory")
	err = os.MkdirAll(dataDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	regDataMocked := `{
  "projects": [
    {
      "name": "proj1",
      "db_path": "` + filepath.Join(dataDir, "proj1.db") + `",
      "workspace_root": "` + proj1Root + `",
      "created_at": "2026-05-10T05:56:45Z",
      "last_used_at": "2026-05-10T08:04:01Z"
    },
    {
      "name": "proj2",
      "db_path": "` + filepath.Join(dataDir, "proj2.db") + `",
      "workspace_root": "` + proj2Root + `",
      "created_at": "2026-05-10T08:09:17Z",
      "last_used_at": "2026-05-10T08:46:18Z"
    }
  ]
}`
	err = os.WriteFile(filepath.Join(dataDir, "workspaces.json"), []byte(regDataMocked), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cmd := newUpgradeCommand()
	cmd.SetArgs([]string{"--hooks-only", "--all", "--yes", "--format", "json"})

	err = cmd.Execute()
	if err != nil {
		t.Fatalf("cmd.Execute() failed: %v", err)
	}

	proj1AgentsDir := filepath.Join(proj1Root, ".agents", "rules")
	proj2AgentsDir := filepath.Join(proj2Root, ".agents", "rules")

	_, err1 := os.Stat(filepath.Join(proj1AgentsDir, "agent-memory.md"))
	_, err2 := os.Stat(filepath.Join(proj2AgentsDir, "agent-memory.md"))

	if err1 != nil {
		t.Errorf("proj1 rules not written: %v", err1)
	}
	if err2 != nil {
		t.Errorf("proj2 rules not written: %v", err2)
	}
}
