package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestWorkspaceUXShowsDailyNavigationAndUsefulNextActions(t *testing.T) {
	model := resize(NewModel("selected", &consoleTestBackend{}), 120, 32)
	model.overviewLoading = false
	view := ansi.Strip(model.View().Content)
	for _, label := range []string{"WORKSPACE", "Ask", "Notes", "Sources", "History", "Settings", "New note", "Import source", "Ctrl-P"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing %q:\n%s", label, view)
		}
	}
}

func TestWorkspaceUXDestinationPaletteFiltersAndReturnsToCurrentScreen(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{response: json.RawMessage(`{"notes":[]}`)})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	for _, character := range "notes" {
		model, _ = updateKey(model, press(character, string(character)))
	}
	model, command := updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = applyCommand(t, model, command)
	if model.destination != DestinationConsole || consoleScreens[model.console.screen].Name != "Notes" {
		t.Fatal("filtered destination did not open Notes")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.destination != DestinationConsole || consoleScreens[model.console.screen].Name != "Notes" {
		t.Fatal("palette cancellation changed the current screen")
	}
}

func TestWorkspaceUXCommandPaletteFindsAdvancedActionsWithoutMixingIDs(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{})
	model.destination = DestinationBrowse
	model.browseItems = sampleMemories()
	model, _ = updateKey(model, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for _, character := range "edit a note" {
		model, _ = updateKey(model, press(character, string(character)))
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.console.form == nil || model.console.form.action.ID != "note-update" {
		t.Fatal("command palette did not open the matching action")
	}
	if model.console.form.values[0] != "" {
		t.Fatal("memory ID was reused as a note ID")
	}
}

func TestWorkspaceUXShellFitsAllSupportedViewportSizes(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {80, 24}, {100, 30}, {160, 45}} {
		model := resize(NewModel("selected", &consoleTestBackend{}), size[0], size[1])
		for _, key := range []tea.KeyPressMsg{press('1', "1"), press('3', "3"), {Code: 'k', Mod: tea.ModCtrl}} {
			model, _ = updateKey(model, key)
			view := model.View().Content
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatalf("too many rows at %v", size)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("overflow at %v: %q", size, line)
				}
			}
		}
		for _, actionID := range []string{"ask", "note-create", "note-update", "source-import"} {
			model = model.openConsoleAction(actionByID(actionID), map[string]any{"id": "n1", "revision": 7, "title": "Title", "path": "note.md", "body": strings.Repeat("長い本文 with spaces\n", 30)})
			for _, advanced := range []bool{false, true} {
				model.console.form.advanced = advanced
				view := model.View().Content
				if len(strings.Split(view, "\n")) > size[1] {
					t.Fatalf("%s advanced=%v overflow at %v", actionID, advanced, size)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("%s advanced=%v row overflow at %v: %q", actionID, advanced, size, line)
					}
				}
			}
		}
	}
}

func TestWorkspaceUXNoteFormUsesPrimaryFieldsAndStablePreparedDocument(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{}).openConsoleAction(actionByID("note-create"), nil)
	model.console.form.values[0] = "A helpful note"
	model.console.form.values[2] = "Body paragraph\n\nMore detail"
	request, err := model.console.form.request()
	if err != nil {
		t.Fatal(err)
	}
	if request.Values["body"] != "# A helpful note\n\nBody paragraph\n\nMore detail" {
		t.Fatalf("body=%q", request.Values["body"])
	}
	path := request.Values["path"]
	retry, err := model.console.form.request()
	if err != nil || retry.Values["path"] != path || path == "" {
		t.Fatal("note path is not stable")
	}
	model = resize(model, 100, 30)
	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "Title *") || !strings.Contains(view, "Body *") || strings.Contains(view, "properties:") {
		t.Fatalf("primary form=%s", view)
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if !strings.Contains(ansi.Strip(model.View().Content), "Properties") {
		t.Fatal("advanced note controls are inaccessible")
	}
}

func TestWorkspaceUXFormCaretSupportsUnicodeInsertionAndDeletion(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{}).openConsoleAction(actionByID("note-create"), nil)
	model, _ = updateKey(model, press('界', "界面"))
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyLeft})
	model, _ = updateKey(model, press('の', "の"))
	if model.console.form.values[0] != "界の面" {
		t.Fatalf("insert=%q", model.console.form.values[0])
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDelete})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if model.console.form.values[0] != "界" {
		t.Fatalf("delete=%q", model.console.form.values[0])
	}
}

func TestWorkspaceUXFilteredNoteEditingFetchesTheChosenFullRevision(t *testing.T) {
	backend := &consoleTestBackend{response: json.RawMessage(`{"note":{"id":"wanted","title":"Chosen","path":"chosen.md","revision":7,"body":"# Chosen\n\nFull body","properties":{}}}`)}
	model := NewModel("selected", backend)
	model.destination = DestinationConsole
	model.console.screen = 2
	model.console.items = []map[string]any{{"id": "other", "title": "Other"}, {"id": "wanted", "title": "Chosen"}, {"id": "trash", "title": "Chosen trash", "deleted_at": "2026-10-11"}}
	model, _ = updateKey(model, press('f', "f"))
	for _, r := range "chosen" {
		model, _ = updateKey(model, press(r, string(r)))
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model, command := updateKey(model, press('e', "e"))
	model = applyCommand(t, model, command)
	if len(backend.requests) != 1 || backend.requests[0].Values["note_id"] != "wanted" {
		t.Fatalf("wrong record: %v", backend.requests)
	}
	if model.console.form == nil || model.console.form.value("body") != "# Chosen\n\nFull body" || model.console.form.value("expected_revision") != "7" {
		t.Fatal("edit lost body or revision")
	}
}

func TestWorkspaceUXAskUsesDirectComposerAndReadableGrounding(t *testing.T) {
	backend := &consoleTestBackend{response: json.RawMessage(`{"context_block":"Verified context.","memories_included_full":[{"id":"m1","type":"semantic","content":"Verified knowledge","source":{"type":"user_input"}}],"weak_memories":[{"id":"m2","type":"episodic","content":"Unverified observation"}],"graph_route":{"selected_mode":"basic","reason_code":"auto_direct"}}`)}
	model := resize(NewModel("selected", backend), 120, 42)
	model, _ = updateKey(model, press('5', "5"))
	if model.console.form == nil || model.console.form.action.ID != "ask" {
		t.Fatal("Ask did not focus the composer")
	}
	model, _ = updateKey(model, press('w', "What do we know?"))
	model, command := updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = applyCommand(t, model, command)
	if backend.requests[0].Values["graph_mode"] != "auto" {
		t.Fatal("Ask did not use Graph Auto")
	}
	view := ansi.Strip(model.View().Content)
	for _, want := range []string{"What do we know?", "Grounded context", "Durable memory", "Weak context", "Graph routing", "Verified knowledge"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q: %s", want, view)
		}
	}
	model, _ = updateKey(model, press('1', "1"))
	model, _ = updateKey(model, press('5', "5"))
	if !strings.Contains(ansi.Strip(model.View().Content), "Verified context") {
		t.Fatal("navigation lost the last Ask result")
	}
}

func TestWorkspaceUXAskEmptyGroundingOffersRecoveryWithoutInventedAnswer(t *testing.T) {
	view := strings.Join(askResultLines(map[string]any{"context_block": "## Task only", "weak_memories": []any{map[string]any{"content": "Weak lead", "type": "episodic"}}}, 80), "\n")
	if !strings.Contains(view, "No grounded memory") || !strings.Contains(view, "Weak context") || !strings.Contains(view, "s Search") || strings.Contains(view, "Grounded context") {
		t.Fatalf("empty state=%s", view)
	}
}

func TestWorkspaceUXAskEvidenceReturnsToScrollableResultAndRetainsDraft(t *testing.T) {
	backend := &consoleTestBackend{response: json.RawMessage(`{"memories_included_full":[{"id":"m1","content":"Long grounded context"}]}`)}
	model := NewModel("selected", backend)
	model, _ = updateKey(model, press('5', "5"))
	model, _ = updateKey(model, press('q', "Question"))
	model, command := updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = applyCommand(t, model, command)
	model, _ = updateKey(model, press('v', "v"))
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !model.console.detail || selectedRecord(model.console.data)["id"] != "m1" {
		t.Fatal("evidence record did not open")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEscape})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEscape})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.console.evidence || model.console.scroll != 1 {
		t.Fatal("returning from evidence disabled result scrolling")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model, _ = updateKey(model, press('n', "Next question"))
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEscape})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.console.form.value("task_description") != "Next question" || model.ux.askQuestion != "Question" {
		t.Fatal("cancel/reopen lost the draft or replaced the completed question")
	}
	next, _ := model.Update(workspaceSelectedMsg{generation: model.console.generation, workspace: "other", backend: backend})
	if next.(Model).ux.askData != nil || next.(Model).ux.askDraft != "" {
		t.Fatal("workspace switch retained another workspace's context")
	}
}

func TestWorkspaceUXChoiceStartsAtFirstOption(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{}).openConsoleAction(actionByID("source-import"), nil)
	for index, field := range model.console.form.action.Fields {
		if field.Name == "rights_basis" {
			model.console.form.cursor = index
			model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyRight})
			if model.console.form.value("rights_basis") != field.Choices[0] {
				t.Fatalf("first selection skipped %q", field.Choices[0])
			}
			return
		}
	}
	t.Fatal("import rights basis field missing")
}

func TestWorkspaceUXAskComposerKeepsCaretVisibleInLongQuestion(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{}).openConsoleAction(actionByID("ask"), nil)
	model = model.updateConsoleInput("First words " + strings.Repeat("long question ", 60))
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyHome})
	view := ansi.Strip(strings.Join(model.renderAskPane(56, 12), "\n"))
	if !strings.Contains(view, "▏First words") {
		t.Fatal("moving the caret to the start leaves the composer showing its tail")
	}
}

func TestWorkspaceUXNotesTrashRestoreDeleteAndRefreshPreserveView(t *testing.T) {
	backend := &consoleTestBackend{}
	model := NewModel("selected", backend)
	model.destination = DestinationConsole
	model.console.screen = 2
	model.console.filter = "chosen"
	model.console.items = []map[string]any{{"id": "n1", "title": "Chosen", "path": "chosen.md", "revision": 1}}
	var command tea.Cmd
	for _, step := range []struct {
		key, response string
		trash         bool
		visible       int
	}{
		{"x", `{"note":{"id":"n1","title":"Chosen","path":"chosen.md","deleted_at":"2026-10-11"}}`, false, 0},
		{"u", `{"note":{"id":"n1","title":"Chosen","path":"chosen.md","deleted_at":null}}`, true, 0},
		{"x", `{"note":{"id":"n1","title":"Chosen","path":"chosen.md","deleted_at":"2026-10-11"}}`, false, 0},
		{"x", `{"deleted":true}`, true, 0},
	} {
		model.console.showTrash = step.trash
		backend.response = json.RawMessage(step.response)
		model, _ = updateKey(model, press(rune(step.key[0]), step.key))
		model, _ = updateKey(model, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		model, command = updateKey(model, press('y', "y"))
		model = applyCommand(t, model, command)
		if model.console.detail || model.console.form != nil || len(model.visibleConsoleRecords()) != step.visible {
			t.Fatalf("%s did not reconcile the current view: %+v", step.key, model.console)
		}
	}
	if len(model.console.items) != 0 {
		t.Fatal("permanently deleted record remains selectable")
	}
	model, _ = updateKey(model, press('r', "r"))
	if !model.console.showTrash || model.console.filter != "chosen" {
		t.Fatal("refresh discarded the chosen view")
	}
}

func TestWorkspaceUXArrowsMoveBetweenListReadingAndRightWorkspacePanel(t *testing.T) {
	model := resize(NewModel("selected", &consoleTestBackend{}), 120, 32)
	model.destination = DestinationBrowse
	model.browseItems = sampleMemories()
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyRight})
	if model.detail == nil || model.detail.ID != model.browseItems[1].ID {
		t.Fatal("right did not open the selected memory")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.detailScroll != 1 || model.browseCursor != 1 {
		t.Fatal("reading arrows changed list selection")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyRight})
	if !model.ux.navFocused {
		t.Fatal("right from reading did not reach the workspace panel")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyLeft})
	if model.ux.navFocused || model.detail == nil {
		t.Fatal("left from the workspace panel did not return to reading")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyLeft})
	if model.detail != nil || model.browseCursor != 1 {
		t.Fatal("left from reading did not preserve list selection")
	}
}

func TestWorkspaceUXArrowNoteReadingCanCancelALateDetail(t *testing.T) {
	backend := &consoleTestBackend{response: json.RawMessage(`{"note":{"id":"wanted","title":"Chosen","path":"chosen.md","body":"Full body","revision":7}}`)}
	model := NewModel("selected", backend)
	model.destination = DestinationConsole
	model.console.screen = 2
	model.console.filter = "chosen"
	model.console.items = []map[string]any{{"id": "other", "title": "Other"}, {"id": "wanted", "title": "Chosen"}}
	for _, cancel := range []rune{tea.KeyLeft, tea.KeyEscape} {
		model, command := updateKey(model, tea.KeyPressMsg{Code: tea.KeyRight})
		if command == nil {
			t.Fatal("right did not request the filtered note")
		}
		model, _ = updateKey(model, tea.KeyPressMsg{Code: cancel})
		model = applyCommand(t, model, command)
		if model.console.detail || model.console.loading || backend.requests[len(backend.requests)-1].Values["note_id"] != "wanted" {
			t.Fatal("late detail reopened cancelled reading or loaded the wrong note")
		}
	}
}

func TestWorkspaceUXContextPanelAndComposerHaveSpatialLayout(t *testing.T) {
	model := resize(NewModel("selected", &consoleTestBackend{}), 120, 32)
	home := ansi.Strip(model.View().Content)
	first, _, _ := strings.Cut(home, "Workspace overview")
	if !strings.Contains(home, "Start here") || strings.Contains(first, "WORKSPACE") {
		t.Fatal("workspace panel precedes main content instead of sitting to its right")
	}
	model, _ = updateKey(model, press('5', "5"))
	view := ansi.Strip(model.View().Content)
	if strings.Index(view, "Ask your workspace") >= strings.Index(view, "Question *") || !strings.Contains(view, "Graph auto") || !strings.Contains(view, "╰") {
		t.Fatal("Ask transcript and framed composer do not follow the new hierarchy")
	}
}

func TestWorkspaceUXMinimumAskViewportShowsTheRetrievedContent(t *testing.T) {
	backend := &consoleTestBackend{response: json.RawMessage(`{"context_block":"Verified fact.","memories_included_full":[{"id":"m1","content":"Verified fact."}]}`)}
	model := resize(NewModel("selected", backend), 60, 16)
	model, _ = updateKey(model, press('5', "5"))
	model = model.updateConsoleInput("What is the verified fact?")
	model, command := updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model = applyCommand(t, model, command)
	if !strings.Contains(ansi.Strip(model.View().Content), "Verified fact.") {
		t.Fatal("the minimum viewport hides the retrieved content behind the question frame")
	}
}

func TestWorkspaceUXPageMovementScrollsReadingRatherThanSelection(t *testing.T) {
	model := resize(NewModel("selected", &consoleTestBackend{}), 100, 30)
	model.destination = DestinationBrowse
	model.browseItems = sampleMemories()
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyRight})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if model.detailScroll <= 1 || model.browseCursor != 0 {
		t.Fatal("page down changed selection or failed to scroll the reading pane")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if model.detailScroll != 0 {
		t.Fatal("page up did not return to the start")
	}
}

func TestWorkspaceUXHomeAndCompactPanelSupportArrows(t *testing.T) {
	model := resize(NewModel("selected", &consoleTestBackend{}), 80, 24)
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.destination != DestinationSearch {
		t.Fatal("Home arrow selection did not open Search")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEscape})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyRight})
	if model.ux.palette != 1 {
		t.Fatal("compact right boundary did not open the switcher")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyRight})
	if model.destination != DestinationBrowse {
		t.Fatal("right did not activate the compact destination")
	}
	model = resize(model, 120, 32)
	model, _ = updateKey(model, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	model = resize(model, 80, 24)
	if model.ux.navFocused || model.ux.palette != 1 {
		t.Fatal("resize trapped focus in an invisible panel")
	}
}

func TestWorkspaceUXMultilineArrowsKeepTheDesiredUnicodeColumn(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{}).openConsoleAction(actionByID("note-create"), nil)
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyTab})
	model = model.updateConsoleInput("一二三四\nx\n界面三四")
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyUp})
	if model.console.form.caretPosition() != 6 || model.console.form.value("body") == "" {
		t.Fatal("up did not clamp to the short preceding line")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyUp})
	if model.console.form.caretPosition() != 4 || model.console.form.action.Fields[model.console.form.cursor].Name != "body" {
		t.Fatal("up lost the desired column or changed the field")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyLeft})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyDown})
	if model.console.form.caretPosition() != 10 || model.ux.navFocused || model.console.confirm {
		t.Fatal("horizontal editing did not reset the preferred column")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl})
	if model.console.form.action.Fields[model.console.form.cursor].Name != "title" {
		t.Fatal("explicit field movement is unavailable")
	}
}
