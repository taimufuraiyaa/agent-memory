package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
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
	if _, err := os.Stat(filepath.Join(dataDir, "proj1.db")); !os.IsNotExist(err) {
		t.Fatalf("hooks-only upgrade mutated project database: %v", err)
	}
}

func TestPrepareRegisteredTermIndexesContinuesAfterProjectFailure(t *testing.T) {
	base := t.TempDir()
	goodDB := filepath.Join(base, "good.db")
	store, err := sqlite.Open(context.Background(), goodDB)
	if err != nil {
		t.Fatalf("open good db: %v", err)
	}
	if err := store.UpsertMemory(context.Background(), &core.MemoryEntry{
		ID: "legacy", Type: core.SemanticMemory, Content: "#UpgradeReady", Workspace: "good",
		Source: core.MemorySource{Type: core.SourceCodeAnalysis}, StorageTier: core.TierVector, Confidence: 0.9,
	}); err != nil {
		t.Fatalf("seed legacy memory: %v", err)
	}
	_ = store.Close()
	badDB := filepath.Join(base, "bad.db")
	if err := os.MkdirAll(badDB, 0o755); err != nil {
		t.Fatalf("create invalid db path: %v", err)
	}
	registry := `{"projects":[` +
		`{"name":"good","db_path":"` + goodDB + `","workspace_root":"` + base + `","created_at":"2026-01-01T00:00:00Z","last_used_at":"2026-01-01T00:00:00Z"},` +
		`{"name":"bad","db_path":"` + badDB + `","workspace_root":"` + base + `","created_at":"2026-01-01T00:00:00Z","last_used_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(base, "workspaces.json"), []byte(registry), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	prepared, failures, err := prepareRegisteredTermIndexes(context.Background(), base)
	if err != nil {
		t.Fatalf("prepare registered indexes: %v", err)
	}
	if prepared["good"] == nil || !prepared["good"].Ready || prepared["good"].DistinctTerms == 0 {
		t.Fatalf("healthy project was not prepared: %#v", prepared)
	}
	if failures["bad"] == "" {
		t.Fatalf("invalid project failure was not reported: %#v", failures)
	}
}

func TestUpgradeAddsShadowModeWithoutOverwritingOperatorChoice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envDir := filepath.Join(home, ".agent-memory")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(envDir, "agent-memory.env")
	if err := os.WriteFile(envPath, []byte("export AGENT_MEMORY_ENABLED=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, updated, err := ensureEnvVarIfPresent("AGENT_MEMORY_TERM_BLOOM_MODE", "shadow")
	if err != nil || !updated {
		t.Fatalf("add shadow mode: updated=%v err=%v", updated, err)
	}
	content, _ := os.ReadFile(envPath)
	if !strings.Contains(string(content), `AGENT_MEMORY_TERM_BLOOM_MODE="shadow"`) {
		t.Fatalf("shadow mode not added: %s", content)
	}
	if err := os.WriteFile(envPath, []byte("export AGENT_MEMORY_TERM_BLOOM_MODE=gate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, updated, err = ensureEnvVarIfPresent("AGENT_MEMORY_TERM_BLOOM_MODE", "shadow")
	if err != nil || updated {
		t.Fatalf("operator choice should be preserved: updated=%v err=%v", updated, err)
	}
	content, _ = os.ReadFile(envPath)
	if !strings.Contains(string(content), "AGENT_MEMORY_TERM_BLOOM_MODE=gate") {
		t.Fatalf("gate choice was overwritten: %s", content)
	}
}
