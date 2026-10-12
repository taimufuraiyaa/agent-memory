package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type projectSwitchTestBackend struct {
	consoleTestBackend
	selected string
}

func (b *projectSwitchTestBackend) SelectWorkspace(_ context.Context, name string) (Backend, error) {
	b.selected = name
	return b, nil
}

func TestWorkspaceProjectsDisplayCountsAndSwitchFilteredProject(t *testing.T) {
	backend := &projectSwitchTestBackend{consoleTestBackend: consoleTestBackend{response: json.RawMessage(`{"projects":[{"name":"empty","memory_count":0,"count_status":"ready"},{"name":"full","memory_count":17,"count_status":"ready","workspace_root":"/projects/full"},{"name":"missing","memory_count":0,"count_status":"unavailable"}]}`)}}
	model := resize(NewModel("empty", backend), 120, 30)
	model, command := updateKey(model, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	model = applyCommand(t, model, command)
	view := ansi.Strip(model.View().Content)
	for _, label := range []string{"All projects", "3 registered", "17 memories", "empty · current", "count unavailable", "Enter Open project"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing %q in projects:\n%s", label, view)
		}
	}
	model.console.filter = "full"
	model, command = updateKey(model, tea.KeyPressMsg{Code: tea.KeyRight})
	model = applyCommand(t, model, command)
	if backend.selected != "full" || model.workspace != "full" {
		t.Fatalf("filtered switch selected=%q workspace=%q", backend.selected, model.workspace)
	}
}

func TestEmptyMemoriesOffersAllProjectsWithoutPretendingAnError(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {120, 30}} {
		model := resize(NewModel("empty", &consoleTestBackend{}), size[0], size[1])
		model.destination = DestinationBrowse
		model.browseLoading = false
		model.overviewLoading = false
		view := ansi.Strip(model.View().Content)
		for _, label := range []string{"Project: empty", "0 saved memories", "All projects", "n Save"} {
			if !strings.Contains(view, label) {
				t.Errorf("missing %q at %v:\n%s", label, size, view)
			}
		}
		if strings.Contains(view, "unavailable") {
			t.Fatal("empty data presented as a connection failure")
		}
	}
}

func TestWorkspaceProjectDetailDistinguishesUnavailableAndLegacyCounts(t *testing.T) {
	for _, item := range []struct {
		record map[string]any
		label  string
	}{
		{map[string]any{"name": "old-server", "memory_count": 8.0}, "8 memories"},
		{map[string]any{"name": "missing", "memory_count": 0.0, "count_status": "unavailable"}, "count unavailable"},
		{map[string]any{"name": "unknown"}, "count unavailable"},
	} {
		lines := workspaceRecordDocument("Workspaces", item.record, 60, false)
		if !strings.Contains(strings.Join(lines, "\n"), item.label) {
			t.Fatalf("project detail %v rendered %v", item.record, lines)
		}
	}
}

func TestProjectsFitCompactAndWideViewsAndKeepNavigationVisible(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {80, 24}, {100, 20}, {160, 45}} {
		model := resize(NewModel("empty", &consoleTestBackend{}), size[0], size[1])
		model.overviewLoading = false
		model.destination = DestinationConsole
		model.console.screen = 12
		model.console.items = []map[string]any{{"name": "empty", "memory_count": 0}, {"name": strings.Repeat("大", 100), "memory_count": 2, "workspace_root": strings.Repeat("/long-root", 100)}}
		model.console.data = map[string]any{"projects": model.console.items}
		view := model.View().Content
		if len(strings.Split(view, "\n")) > size[1] {
			t.Fatalf("projects overflow rows at %v", size)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("projects overflow columns at %v", size)
			}
		}
		if size[0] >= 100 && !strings.Contains(ansi.Strip(view), "w  Workspaces") {
			t.Fatalf("project destination hidden at %v", size)
		}
	}
}
