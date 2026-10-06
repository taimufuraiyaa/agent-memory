package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestInitFormatHelpMatchesSupportedOutput(t *testing.T) {
	cmd := newInitCommand()
	formatFlag := cmd.Flags().Lookup("format")
	if formatFlag == nil {
		t.Fatal("init format flag is missing")
	}
	if got, want := formatFlag.Usage, "Output format: json"; got != want {
		t.Fatalf("format help = %q, want %q", got, want)
	}
}

func TestReinstallCommandPropagatesCodexPermissionConflict(t *testing.T) {
	baseDir := t.TempDir()
	projectRoot := t.TempDir()
	manager, err := workspace.NewManager(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Init(context.Background(), workspace.InitOptions{
		CWD:         projectRoot,
		ProjectName: "codex-conflict",
		NoRule:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(projectRoot, ".codex", "config.toml")
	seed := "sandbox_mode = \"workspace-write\"\nmodel = \"gpt-test\"\n"
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatal(err)
	}

	cmd := newReinstallCommand()
	cmd.SetArgs([]string{"--base-dir", baseDir, "--project-name", "codex-conflict", "--ide", "codex"})
	err = cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sandbox_mode") || !strings.Contains(err.Error(), "default_permissions") {
		t.Fatalf("expected actionable Codex permission conflict, got %v", err)
	}
	contents, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(contents) != seed {
		t.Fatalf("reinstall modified conflicting config: %s", contents)
	}
}
