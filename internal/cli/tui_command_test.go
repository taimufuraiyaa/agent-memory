package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
)

func TestNewTUICommandIsRegisteredAndRequiresExplicitWorkspace(t *testing.T) {
	root := NewRootCommand()
	command, _, err := root.Find([]string{"tui"})
	if err != nil || command == nil || command.Name() != "tui" {
		t.Fatalf("tui command not registered: command=%v err=%v", command, err)
	}
	for _, flagName := range []string{"workspace", "db", "model-dir", "api"} {
		if command.Flags().Lookup(flagName) == nil {
			t.Fatalf("tui command missing --%s", flagName)
		}
	}
	root.SetArgs([]string{"tui"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "explicit --workspace is required") {
		t.Fatalf("missing workspace error=%v", err)
	}
}

func TestTUICommandRejectsAPIAndNonTerminalUseBeforeOpeningDependencies(t *testing.T) {
	openCalls := 0
	dependencies := tuiCommandDependencies{
		isTerminal: func(any) bool { return false },
		open: func(context.Context, runtimeConfig) (*sqlite.Store, embeddings.Provider, error) {
			openCalls++
			return nil, nil, errors.New("must not open")
		},
	}
	command := newTUICommandWithDependencies(dependencies)
	command.SetArgs([]string{"--workspace", "test", "--api", "http://127.0.0.1:9999"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "local workspaces") {
		t.Fatalf("api rejection error=%v", err)
	}
	command = newTUICommandWithDependencies(dependencies)
	command.SetArgs([]string{"--workspace", "test"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("non-terminal rejection error=%v", err)
	}
	if openCalls != 0 {
		t.Fatalf("dependencies opened %d times before validation", openCalls)
	}
}

func TestTUICommandRunsSelectedWorkspaceAndClosesStore(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "tui-command.db"))
	if err != nil {
		t.Fatal(err)
	}
	var openedWorkspace string
	var rendered string
	dependencies := tuiCommandDependencies{
		isTerminal: func(any) bool { return true },
		open: func(_ context.Context, config runtimeConfig) (*sqlite.Store, embeddings.Provider, error) {
			openedWorkspace = config.workspace
			return store, naturalTestProvider{}, nil
		},
		run: func(_ context.Context, _ io.Reader, _ io.Writer, model memorytui.Model) (tea.Model, error) {
			updated, _ := model.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
			rendered = updated.(memorytui.Model).View().Content
			return updated, nil
		},
	}
	command := newTUICommandWithDependencies(dependencies)
	command.SetIn(&bytes.Buffer{})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"--workspace", "selected"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if openedWorkspace != "selected" || !strings.Contains(rendered, "selected") {
		t.Fatalf("workspace=%q rendered=%q", openedWorkspace, rendered)
	}
	if _, err := store.CountMemories(ctx); err == nil {
		t.Fatal("workspace store remained open after TUI exit")
	}
}
