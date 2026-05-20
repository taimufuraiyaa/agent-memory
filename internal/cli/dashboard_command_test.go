package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildDashboardProcessArgsOmitsWorkspaceWhenEmpty(t *testing.T) {
	cfg := runtimeConfig{
		dbPath:   "/tmp/dashboard.db",
		modelDir: "/tmp/models",
	}
	args := buildDashboardProcessArgs(cfg, ":3210", "", "")
	for i := 0; i < len(args); i++ {
		if args[i] == "--workspace" {
			t.Fatalf("expected dashboard start args to omit --workspace when empty: %v", args)
		}
	}
}

func TestResolveDashboardRuntimeAllowsMissingWorkspace(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("MEMORY_WORKSPACE", "")

	cfg, err := resolveDashboardRuntime(commonFlags{})
	if err != nil {
		t.Fatalf("resolveDashboardRuntime: %v", err)
	}
	if cfg.workspace != "" {
		t.Fatalf("expected empty workspace fallback, got %q", cfg.workspace)
	}
	wantDir := filepath.Join(home, ".agent-memory")
	if got := filepath.Dir(cfg.dbPath); got != wantDir {
		t.Fatalf("expected dashboard base dir %q, got %q", wantDir, got)
	}
	if got := filepath.Base(cfg.dbPath); got != ".dashboard-placeholder.db" {
		t.Fatalf("expected placeholder db path, got %q", got)
	}
}
