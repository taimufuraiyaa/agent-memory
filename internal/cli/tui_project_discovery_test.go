package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestRuntimeUsesRegisteredDatabaseUnlessOverriddenOrRemote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MEMORY_API_URL", "")
	registered := filepath.Join(t.TempDir(), "custom.db")
	writeTUIProjectRegistry(t, home, []workspace.Project{{Name: "selected", DBPath: registered}})
	config, err := resolveRuntime(commonFlags{workspace: "selected"})
	if err != nil || config.dbPath != registered {
		t.Fatalf("registered config=%+v err=%v", config, err)
	}
	for _, flags := range []commonFlags{
		{workspace: "selected", dbPath: "explicit.db"},
		{workspace: "selected", apiURL: "http://remote.example"},
		{workspace: "unregistered"},
	} {
		config, err := resolveRuntime(flags)
		if err != nil {
			t.Fatal(err)
		}
		want := flags.dbPath
		if want == "" {
			want = filepath.Join(home, ".agent-memory", flags.workspace+".db")
		}
		if config.dbPath != want {
			t.Fatalf("override/fallback config=%+v want=%q", config, want)
		}
	}
}

func TestRuntimeDoesNotHideInvalidLocalRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MEMORY_API_URL", "")
	writeTUIProjectRegistry(t, home, nil)
	if err := os.WriteFile(filepath.Join(home, ".agent-memory/workspaces.json"), []byte("invalid JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRuntime(commonFlags{workspace: "selected"}); err == nil {
		t.Fatal("invalid registry silently fell back to an empty default database")
	}
	if _, err := resolveRuntime(commonFlags{workspace: "selected", apiURL: "http://remote.example"}); err != nil {
		t.Fatalf("remote routing depended on the local registry: %v", err)
	}
}

func TestLocalTUIFindsAllProjectsOutsideSelectedDatabaseDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_MEMORY_DATA_DIR", filepath.Join(home, ".agent-memory"))
	databaseDir := t.TempDir()
	writeTUIProjectRegistry(t, home, []workspace.Project{
		{Name: "selected", DBPath: filepath.Join(databaseDir, "selected-custom.db")},
		{Name: "other", DBPath: filepath.Join(databaseDir, "other-custom.db")},
	})
	backend, closeConsole, err := newLocalTUIConsole(context.Background(), runtimeConfig{workspace: "selected", dbPath: filepath.Join(databaseDir, "selected-custom.db")}, naturalTestProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeConsole()
	raw, err := backend.Execute(context.Background(), memorytui.Request{Method: "GET", Path: "/api/v1/projects/list"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Projects []workspace.ListItem `json:"projects"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Projects) != 2 {
		t.Fatalf("registered projects=%s err=%v", raw, err)
	}
	selected, err := backend.(memorytui.WorkspaceSelector).SelectWorkspace(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if overview, err := selected.LoadOverview(context.Background()); err != nil || overview.Workspace != "other" {
		t.Fatalf("switched overview=%+v err=%v", overview, err)
	}
}

func writeTUIProjectRegistry(t *testing.T, home string, projects []workspace.Project) {
	t.Helper()
	base := filepath.Join(home, ".agent-memory")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(workspace.Registry{Projects: projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "workspaces.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
