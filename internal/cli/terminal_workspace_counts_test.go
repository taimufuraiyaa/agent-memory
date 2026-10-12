package cli

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestTerminalPickerCountsArriveWithoutChangingSelection(t *testing.T) {
	picker := terminalWorkspacePicker{
		names: []string{"empty", "full", "missing"}, width: 80, height: 10,
		loadProjects: func(context.Context) ([]workspace.ListItem, error) {
			return []workspace.ListItem{{Name: "full", MemoryCount: 17, CountStatus: "ready"}, {Name: "empty", CountStatus: "ready"}, {Name: "missing", CountStatus: "unavailable"}}, nil
		},
		context: context.Background(),
	}
	command := picker.Init()
	if command == nil {
		t.Fatal("picker does not request project summaries asynchronously")
	}
	model, _ := picker.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = model.Update(command())
	view := ansi.Strip(model.View().Content)
	for _, label := range []string{"All projects", "empty · 0 memories", "full · 17 memories", "missing · count unavailable"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing %q in picker:\n%s", label, view)
		}
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.(terminalWorkspacePicker).selected != "full" {
		t.Fatal("count arrival changed the selected project")
	}
}
