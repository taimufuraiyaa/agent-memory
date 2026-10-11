package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestDefaultChecksWarnWhenExecutableDirectoryIsMissingFromPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", "/opt/homebrew/bin/fish")
	results := NewRunner(DefaultChecks(Options{
		Root:       t.TempDir(),
		DataDir:    t.TempDir(),
		Workspace:  "missing",
		ServiceURL: "http://127.0.0.1:1",
		ModelDir:   t.TempDir(),
	})...).Run(context.Background())

	byName := map[string]Result{}
	for _, result := range results {
		byName[result.Name] = result
	}
	binary := byName["binary"]
	if binary.Status != StatusWarning {
		t.Fatalf("expected PATH warning, got %+v", binary)
	}
	if !strings.Contains(strings.ToLower(binary.NextAction), "path") {
		t.Fatalf("expected actionable PATH guidance, got %+v", binary)
	}
	if !strings.Contains(binary.NextAction, "fish_add_path") {
		t.Fatalf("expected Fish-specific PATH guidance, got %+v", binary)
	}
}

func TestDefaultChecksUseRegisteredDatabasePath(t *testing.T) {
	dataDir := t.TempDir()
	customDB := filepath.Join(t.TempDir(), "custom.db")
	if err := os.WriteFile(customDB, []byte("sqlite placeholder"), 0o600); err != nil {
		t.Fatalf("write custom database: %v", err)
	}
	registry := `{"projects":[{"name":"registered","db_path":` + quotedJSON(customDB) + `}]}`
	if err := os.WriteFile(filepath.Join(dataDir, "workspaces.json"), []byte(registry), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	results := NewRunner(DefaultChecks(Options{
		Root:       t.TempDir(),
		DataDir:    dataDir,
		Workspace:  "registered",
		ServiceURL: "http://127.0.0.1:1",
		ModelDir:   t.TempDir(),
	})...).Run(context.Background())
	byName := map[string]Result{}
	for _, result := range results {
		byName[result.Name] = result
	}
	if byName["workspace_registry"].Status != StatusPass {
		t.Fatalf("expected registered workspace pass, got %+v", byName["workspace_registry"])
	}
	if byName["database"].Status != StatusPass || !strings.Contains(byName["database"].Evidence, customDB) {
		t.Fatalf("expected custom database path, got %+v", byName["database"])
	}
}

func quotedJSON(value string) string {
	return `"` + strings.ReplaceAll(value, `\`, `\\`) + `"`
}

func TestDefaultChecksReportMissingRuntimeArtifactsIndependently(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	runner := NewRunner(DefaultChecks(Options{
		Root:       root,
		DataDir:    dataDir,
		Workspace:  "missing",
		ServiceURL: "http://127.0.0.1:1",
		ModelDir:   filepath.Join(root, "missing-model"),
	})...)

	results := runner.Run(context.Background())
	byName := map[string]Result{}
	for _, result := range results {
		byName[result.Name] = result
	}
	for _, name := range []string{"binary", "workspace_registry", "database", "embedding_model", "service", "port", "writable_root", "hooks", "mcp"} {
		if _, exists := byName[name]; !exists {
			t.Fatalf("missing check %q in %+v", name, results)
		}
	}
	if byName["database"].Status != StatusFail || byName["embedding_model"].Status != StatusFail || byName["service"].Status != StatusFail {
		t.Fatalf("expected actionable failures: %+v", byName)
	}
}

func TestServiceHealthRequiresMultiWorkspaceDaemon(t *testing.T) {
	if result := serviceHealthResult([]byte(`{"ok":true,"data":{"service_mode":"fixed_workspace"}}`)); result.Status != StatusFail {
		t.Fatalf("legacy service should fail: %+v", result)
	}
	if result := serviceHealthResult([]byte(`{"ok":true,"data":{"service_mode":"multi_workspace","registered_workspaces":8}}`)); result.Status != StatusPass {
		t.Fatalf("multi-workspace daemon should pass: %+v", result)
	}
}

func TestMemoryContractCheckRejectsStaleDetectedClientAndReportsEnforcement(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".cursor", "rules", "agent-memory.mdc"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := memoryContractCheck(root)
	if result.Status != StatusFail || result.Err == nil || !strings.Contains(result.Err.Error(), "cursor") {
		t.Fatalf("expected stale Cursor failure, got %+v", result)
	}

	current := workspace.MemoryContractMarker
	if err := os.WriteFile(filepath.Join(root, ".cursor", "rules", "agent-memory.mdc"), []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	result = memoryContractCheck(root)
	if result.Status != StatusPass || !strings.Contains(result.Evidence, "instruction-enforced=cursor") {
		t.Fatalf("expected truthful Cursor pass, got %+v", result)
	}
}

func TestMemoryContractCheckReportsRuleOnlyClaudeTruthfully(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(workspace.MemoryContractMarker), 0o644); err != nil {
		t.Fatal(err)
	}
	result := memoryContractCheck(root)
	if result.Status != StatusPass || !strings.Contains(result.Evidence, "instruction-enforced=claude") {
		t.Fatalf("expected rule-only Claude evidence, got %+v", result)
	}
}
