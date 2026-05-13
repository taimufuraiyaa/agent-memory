package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspacePrecedence(t *testing.T) {
	t.Setenv("MEMORY_WORKSPACE", "from-env")
	got, err := resolveWorkspace("from-flag")
	if err != nil {
		t.Fatalf("resolveWorkspace returned error: %v", err)
	}
	if got != "from-flag" {
		t.Fatalf("expected flag to win, got %q", got)
	}

	got, err = resolveWorkspace("")
	if err != nil {
		t.Fatalf("resolveWorkspace from env returned error: %v", err)
	}
	if got != "from-env" {
		t.Fatalf("expected env workspace, got %q", got)
	}
}

func TestResolveWorkspaceFromCWD(t *testing.T) {
	t.Setenv("MEMORY_WORKSPACE", "")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	got, err := resolveWorkspace("")
	if err != nil {
		t.Fatalf("resolveWorkspace from cwd returned error: %v", err)
	}
	if got != filepath.Base(dir) {
		t.Fatalf("expected %q, got %q", filepath.Base(dir), got)
	}
}

func TestResolveAPIURL(t *testing.T) {
	t.Setenv("MEMORY_API_URL", "http://env.local:3210/")
	if got := resolveAPIURL("http://flag.local:3210/"); got != "http://flag.local:3210" {
		t.Fatalf("flag api url should win, got %q", got)
	}
	if got := resolveAPIURL(""); got != "http://env.local:3210" {
		t.Fatalf("expected env api url, got %q", got)
	}
}
