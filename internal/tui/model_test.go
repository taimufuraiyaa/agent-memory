package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func press(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

func resize(model Model, width, height int) Model {
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func updateKey(model Model, key tea.KeyPressMsg) (Model, tea.Cmd) {
	updated, command := model.Update(key)
	return updated.(Model), command
}

func TestModelStartsAtHomeAndCyclesDestinations(t *testing.T) {
	model := NewModel("agent-memory", nil)
	if model.destination != DestinationHome {
		t.Fatalf("destination=%q want home", model.destination)
	}

	model, _ = updateKey(model, press(tea.KeyTab, ""))
	if model.destination != DestinationSearch {
		t.Fatalf("destination after tab=%q want search", model.destination)
	}
	model, _ = updateKey(model, press(tea.KeyTab, ""))
	if model.destination != DestinationBrowse {
		t.Fatalf("destination after second tab=%q want browse", model.destination)
	}
	model, _ = updateKey(model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if model.destination != DestinationSearch {
		t.Fatalf("destination after shift+tab=%q want search", model.destination)
	}
}

func TestModelSlashFocusesSearchAndUnicodeBackspaceEditsQuery(t *testing.T) {
	model := NewModel("agent-memory", nil)
	model, _ = updateKey(model, press('/', "/"))
	if model.destination != DestinationSearch || !model.searchFocused {
		t.Fatalf("slash did not focus search: %+v", model)
	}
	model, _ = updateKey(model, press('界', "界"))
	model, _ = updateKey(model, press('面', "面"))
	if model.query != "界面" {
		t.Fatalf("query=%q", model.query)
	}
	model, _ = updateKey(model, press(tea.KeyBackspace, ""))
	if model.query != "界" {
		t.Fatalf("unicode backspace query=%q", model.query)
	}
	model, _ = updateKey(model, press(tea.KeyEsc, ""))
	if model.searchFocused {
		t.Fatal("escape did not leave search input")
	}
}

func TestModelHelpAndQuitAreDiscoverable(t *testing.T) {
	model := resize(NewModel("agent-memory", nil), 100, 30)
	model, _ = updateKey(model, press('?', "?"))
	if !model.helpVisible {
		t.Fatal("question mark did not open help")
	}
	plain := ansi.Strip(model.View().Content)
	for _, want := range []string{"Keyboard help", "Tab", "Search", "Browse", "Ctrl-C"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("help view missing %q:\n%s", want, plain)
		}
	}
	model, _ = updateKey(model, press(tea.KeyEsc, ""))
	if model.helpVisible {
		t.Fatal("escape did not close help")
	}
	_, command := updateKey(model, press('q', "q"))
	if command == nil {
		t.Fatal("q did not return a quit command")
	}
}

func TestModelSmallTerminalRendersOnlyResizeState(t *testing.T) {
	model := resize(NewModel("private-workspace", nil), 50, 12)
	plain := ansi.Strip(model.View().Content)
	if !strings.Contains(plain, "Terminal too small") || !strings.Contains(plain, "60x16") {
		t.Fatalf("small terminal view missing resize guidance:\n%s", plain)
	}
	if strings.Contains(plain, "private-workspace") {
		t.Fatalf("small terminal leaked normal content:\n%s", plain)
	}
}

func TestModelViewStaysWithinTerminalWidth(t *testing.T) {
	model := resize(NewModel("a-workspace-with-a-long-name", nil), 72, 20)
	for _, line := range strings.Split(model.View().Content, "\n") {
		if width := ansi.StringWidth(line); width > 72 {
			t.Fatalf("line width=%d exceeds terminal:\n%s", width, ansi.Strip(line))
		}
	}
}
