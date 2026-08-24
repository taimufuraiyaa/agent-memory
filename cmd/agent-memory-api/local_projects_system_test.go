package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestLocalProjectSystemReadsRegisteredLifecycleAndRegularSkillsOnly(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agents", "skills", "safe-release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agents", "skills", "linked-secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agents", "skills", "safe-release", "SKILL.md"), []byte("---\nname: Safe Release\ndescription: Verify a release\n---\nRun the checks."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(projectRoot, ".agents", "skills", "linked-secret", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(baseDir, "project.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSchedulerRunRecord(ctx, sqlite.SchedulerRunRecord{ID: "run-1", Workspace: "agent-memory", Result: "success", Promoted: 2}, 30); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	registry, err := json.Marshal(workspace.Registry{Projects: []workspace.Project{{Name: "agent-memory", DBPath: dbPath, WorkspaceRoot: projectRoot}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "workspaces.json"), registry, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	clients, err := clientprofile.Open(baseDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := &localProjectService{manager: manager, clients: clients}

	lifecycle, err := service.Lifecycle(ctx, "agent-memory", 100)
	if err != nil || len(lifecycle.History) != 1 || lifecycle.History[0].Promoted != 2 {
		t.Fatalf("unexpected lifecycle: result=%+v err=%v", lifecycle, err)
	}
	skills, err := service.Skills(ctx, "agent-memory")
	if err != nil || len(skills) != 1 {
		t.Fatalf("unexpected skills: result=%+v err=%v", skills, err)
	}
	if skills[0].Path != ".agents/skills/safe-release/SKILL.md" || strings.Contains(skills[0].Path, projectRoot) || strings.Contains(skills[0].Content, "secret") {
		t.Fatalf("skill path or content escaped the registered root: %+v", skills[0])
	}
}
