package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	api "github.com/taimufuraiyaa/agent-memory/internal/saas/api"
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

func TestLocalProjectOrchestrationStatusStaysInsideRegisteredWorkspace(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	projects := make([]workspace.Project, 0, 2)
	for _, name := range []string{"project-a", "project-b"} {
		dbPath := filepath.Join(baseDir, name+".db")
		store, err := sqlite.Open(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		workflow := localProjectTestWorkflow(name, "workflow-shared", "origin-"+name)
		if _, _, err := store.CreateSkillWorkflow(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		projects = append(projects, workspace.Project{Name: name, DBPath: dbPath, WorkspaceRoot: t.TempDir()})
	}
	registry, err := json.Marshal(workspace.Registry{Projects: projects})
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
	service := &localProjectService{manager: manager}
	for _, name := range []string{"project-a", "project-b"} {
		status, err := service.StatusSkillOrchestration(ctx, api.LocalProjectSkillOrchestrationStatusInput{
			Workspace: name, WorkflowID: "workflow-shared", Limit: 20, TenantID: "tenant-1", AccountID: "account-1", Actor: "owner-1",
		})
		if err != nil || status.Workflow.Scope.WorkspaceID != name || status.Workflow.OriginID != "origin-"+name {
			t.Fatalf("workspace status escaped partition: workspace=%s status=%+v err=%v", name, status, err)
		}
	}
	if _, err := service.StatusSkillOrchestration(ctx, api.LocalProjectSkillOrchestrationStatusInput{Workspace: "missing", WorkflowID: "workflow-shared", Limit: 20, TenantID: "tenant-1", AccountID: "account-1", Actor: "owner-1"}); err == nil {
		t.Fatal("unregistered workspace was accepted")
	}
}

func localProjectTestWorkflow(workspaceName, id, originID string) core.SkillWorkflow {
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	return core.SkillWorkflow{
		ID: id, Scope: core.SkillOrchestratorScope{WorkspaceID: workspaceName, Environment: "local"},
		OriginKind: core.SkillWorkflowOriginOperator, OriginID: originID, Kind: core.SkillWorkflowAutomaticRevision,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: "sha256:" + strings.Repeat("a", 64),
		State: core.SkillWorkflowOpen, CurrentStage: core.SkillStageDetect, Generation: 1, ConfigurationVersion: 1,
		PolicyDigest: "sha256:" + strings.Repeat("b", 64), CreatedAt: now, UpdatedAt: now,
	}
}
