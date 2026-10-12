package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
)

func TestTerminalLaunchCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"start", "restart"} {
		root := NewRootCommand()
		command, _, err := root.Find([]string{name})
		if err != nil || command == nil || command.Name() != name {
			t.Fatalf("%s is not registered", name)
		}
		if commandRequiresExplicitWorkspace(command) {
			t.Fatalf("%s cannot reach the interactive workspace picker", name)
		}
	}
}

func TestTerminalStartUsesExplicitPickerSelection(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "selected.db"))
	if err != nil {
		t.Fatal(err)
	}
	var opened string
	dependencies := terminalLaunchDependencies{
		tui: tuiCommandDependencies{
			isTerminal: func(any) bool { return true },
			open: func(_ context.Context, config runtimeConfig) (*sqlite.Store, embeddings.Provider, error) {
				opened = config.workspace
				return store, naturalTestProvider{}, nil
			},
			run: func(_ context.Context, _ io.Reader, _ io.Writer, model memorytui.Model) (tea.Model, error) {
				return model, nil
			},
		},
		names: func() ([]string, error) { return []string{"first", "chosen"}, nil },
		pick:  func(_ *cobra.Command, _ []string) (string, error) { return "chosen", nil },
	}
	command := newTerminalLaunchCommand(false, dependencies)
	command.SetIn(&bytes.Buffer{})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if opened != "chosen" || !command.Flags().Changed("workspace") {
		t.Fatalf("workspace=%q, selected flag=%v", opened, command.Flags().Changed("workspace"))
	}
}

func TestTerminalRestartRejectsNonTerminalBeforeRebuild(t *testing.T) {
	dependencies := terminalLaunchDependencies{
		tui: tuiCommandDependencies{isTerminal: func(any) bool { return false }},
		rebuild: func(context.Context, string) (string, error) {
			t.Fatal("rebuild before terminal validation")
			return "", nil
		},
	}
	command := newTerminalLaunchCommand(true, dependencies)
	command.SetArgs([]string{"--workspace", "chosen"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("error=%v", err)
	}
}

func TestTerminalRestartFailureDoesNotLaunch(t *testing.T) {
	dependencies := terminalLaunchDependencies{
		tui:     tuiCommandDependencies{isTerminal: func(any) bool { return true }},
		rebuild: func(context.Context, string) (string, error) { return "", errors.New("build failed") },
		launch:  func(*cobra.Command, string, []string) error { t.Fatal("launched stale build"); return nil },
	}
	command := newTerminalLaunchCommand(true, dependencies)
	command.SetArgs([]string{"--workspace", "chosen"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("error=%v", err)
	}
}

func TestTerminalRestartLaunchesFreshBinaryWithOriginalFlags(t *testing.T) {
	var launched string
	var arguments []string
	dependencies := terminalLaunchDependencies{
		tui: tuiCommandDependencies{isTerminal: func(any) bool { return true }},
		rebuild: func(_ context.Context, source string) (string, error) {
			if source != "/source checkout" {
				t.Fatalf("source=%q", source)
			}
			return "/installed/agent-memory", nil
		},
		launch: func(_ *cobra.Command, binary string, args []string) error {
			launched, arguments = binary, args
			return nil
		},
	}
	command := newTerminalLaunchCommand(true, dependencies)
	command.SetArgs([]string{"--workspace", "chosen", "--db", "/data exact.db", "--model-dir", "/models", "--api", "http://127.0.0.1:3211", "--source-dir", "/source checkout"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, "|")
	if launched != "/installed/agent-memory" || !strings.HasPrefix(joined, "tui|--workspace|chosen") || !strings.Contains(joined, "--db|/data exact.db") || !strings.Contains(joined, "--model-dir|/models") || !strings.Contains(joined, "--api|http://127.0.0.1:3211") || strings.Contains(joined, "source-dir") {
		t.Fatalf("binary=%q arguments=%v", launched, arguments)
	}
}

func TestTerminalSelectionCancellationDoesNotRebuild(t *testing.T) {
	dependencies := terminalLaunchDependencies{
		tui:     tuiCommandDependencies{isTerminal: func(any) bool { return true }},
		names:   func() ([]string, error) { return []string{"chosen"}, nil },
		pick:    func(*cobra.Command, []string) (string, error) { return "", errWorkspaceSelectionCancelled },
		rebuild: func(context.Context, string) (string, error) { t.Fatal("cancelled selection rebuilt"); return "", nil },
	}
	if err := newTerminalLaunchCommand(true, dependencies).Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalStartAPIModeRequiresWorkspaceBeforeSelection(t *testing.T) {
	dependencies := terminalLaunchDependencies{
		tui:   tuiCommandDependencies{isTerminal: func(any) bool { return true }},
		names: func() ([]string, error) { t.Fatal("remote launcher used local registry"); return nil, nil },
	}
	command := newTerminalLaunchCommand(false, dependencies)
	command.SetArgs([]string{"--api", "http://127.0.0.1:3211"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "--workspace is required") {
		t.Fatalf("error=%v", err)
	}
}

func TestTerminalStartEmptyRegistryDoesNotRebuild(t *testing.T) {
	dependencies := terminalLaunchDependencies{
		tui:     tuiCommandDependencies{isTerminal: func(any) bool { return true }},
		names:   func() ([]string, error) { return nil, nil },
		rebuild: func(context.Context, string) (string, error) { t.Fatal("empty registry rebuilt"); return "", nil },
	}
	if err := newTerminalLaunchCommand(true, dependencies).Execute(); err == nil || !strings.Contains(err.Error(), "no registered workspaces") {
		t.Fatalf("error=%v", err)
	}
}

func TestTerminalWorkspacePickerSelectionCancellationAndBounds(t *testing.T) {
	picker := terminalWorkspacePicker{names: []string{"first", "chosen", "last"}, width: 12, height: 5}
	model, _ := picker.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.(terminalWorkspacePicker).selected != "chosen" || command == nil {
		t.Fatal("picker did not select the highlighted workspace")
	}
	model, command = picker.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.(terminalWorkspacePicker).selected != "" || command == nil {
		t.Fatal("picker cancel selected a workspace")
	}
	for _, height := range []int{1, 3, 5, 12} {
		picker.height = height
		view := picker.View().Content
		if len(strings.Split(view, "\n")) > height {
			t.Fatalf("height=%d view=%q", height, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > picker.width {
				t.Fatalf("unbounded line %q", line)
			}
		}
	}
}

func TestTerminalBinaryPublicationUpdatesBothCopies(t *testing.T) {
	directory := t.TempDir()
	canonical := filepath.Join(directory, binNameWithExt("agent-memory"))
	built := filepath.Join(t.TempDir(), "fresh")
	if err := os.WriteFile(canonical, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("fresh build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := publishTerminalBinaries(canonical, built, replaceFileAtomic); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-memory", "am"} {
		path := filepath.Join(directory, binNameWithExt(name))
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "fresh build" {
			t.Fatalf("%s=%q error=%v", name, data, err)
		}
	}
	artifacts, _ := filepath.Glob(filepath.Join(directory, ".agent-memory-restart-*"))
	if len(artifacts) != 0 {
		t.Fatalf("left temporary files: %v", artifacts)
	}
}

func TestTerminalBinaryPublicationRestoresCanonicalOnAliasFailure(t *testing.T) {
	directory := t.TempDir()
	canonical := filepath.Join(directory, binNameWithExt("agent-memory"))
	alias := filepath.Join(directory, binNameWithExt("am"))
	built := filepath.Join(t.TempDir(), "fresh")
	for path, data := range map[string]string{canonical: "old canonical", alias: "old alias", built: "new build"} {
		if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	err := publishTerminalBinaries(canonical, built, func(destination, source string) error {
		if destination == alias {
			return errors.New("alias is read only")
		}
		return replaceFileAtomic(destination, source)
	})
	if err == nil {
		t.Fatal("publication failure was ignored")
	}
	for path, wanted := range map[string]string{canonical: "old canonical", alias: "old alias"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != wanted {
			t.Fatalf("%s=%q error=%v", path, data, err)
		}
	}
}
