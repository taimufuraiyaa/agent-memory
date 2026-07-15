package doctor

import (
	"context"
	"path/filepath"
	"testing"
)

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
