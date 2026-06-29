package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
	"github.com/time/timebooks/agent-memory/internal/workspace"
)

func TestDistillCommand(t *testing.T) {
	dataDir := t.TempDir()
	cwd := t.TempDir()

	// Switch working directory to cwd for workspace init testing
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldCwd) }()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	mgr, err := workspace.NewManager(dataDir)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	initOut, err := mgr.Init(context.Background(), workspace.InitOptions{
		CWD:         cwd,
		ProjectName: "cli-distill-proj",
		NoRule:      true,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Insert a semantic memory to verify distillation
	store, err := sqlite.Open(context.Background(), initOut.DBPath)
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	defer func() { _ = store.Close() }()

	m1 := &core.MemoryEntry{
		ID:        "mem-1",
		Workspace: "cli-distill-proj",
		Type:      core.SemanticMemory,
		Content:   "Cli command test fact",
		CreatedAt: time.Now(),
	}
	if err := store.UpsertMemory(context.Background(), m1); err != nil {
		t.Fatalf("upsert semantic: %v", err)
	}

	// Run command
	cmd := newDistillCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	cmd.SetArgs([]string{
		"--name", "cli-skill",
		"--description", "Cli distilled description",
		"--workspace", "cli-distill-proj",
		"--data-dir", dataDir,
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute failed: %v, stderr: %s", err, stderr.String())
	}

	// Verify skill file was created
	skillPath := filepath.Join(cwd, ".agents", "skills", "cli-skill", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected skill file: %v", err)
	}

	b, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "name: cli-skill") {
		t.Fatalf("expected name in skill, got: %s", s)
	}
	if !strings.Contains(s, "Cli command test fact") {
		t.Fatalf("expected distilled memory content, got: %s", s)
	}
}
