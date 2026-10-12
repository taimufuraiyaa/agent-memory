package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type consoleTestBackend struct {
	fakeBackend
	requests []Request
	response json.RawMessage
	err      error
}

func (b *consoleTestBackend) Execute(_ context.Context, r Request) (json.RawMessage, error) {
	b.requests = append(b.requests, r)
	return b.response, b.err
}

func TestConsoleNavigationAndBoundedUnsafeDetail(t *testing.T) {
	backend := &consoleTestBackend{response: json.RawMessage(`{"notes":[{"id":"n1","title":"First","revision":3}]}`)}
	model := resize(NewModel("selected", backend), 80, 24)
	model, cmd := updateKey(model, tea.KeyPressMsg{Code: '6', Text: "6"})
	model = applyCommand(t, model, cmd)
	if model.destination != DestinationConsole || len(model.console.items) != 1 {
		t.Fatalf("notes not loaded: %+v", model.console)
	}
	model.console.data = map[string]any{"body": "\x1b]52;c;attack\a" + strings.Repeat("長文", 2000)}
	model.console.detail = true
	view := ansi.Strip(model.View().Content)
	if strings.Contains(view, "attack") || len(strings.Split(view, "\n")) > 24 {
		t.Fatalf("unsafe or unbounded view: %s", view)
	}
}

func TestConsoleMutationReviewCancelAndBusy(t *testing.T) {
	backend := &consoleTestBackend{response: json.RawMessage(`{"note":{"id":"n1","revision":1}}`)}
	model := NewModel("selected", backend)
	model.destination = DestinationConsole
	model = model.openConsoleAction(actionByID("note-create"), nil)
	model.console.form.values = []string{"Title", "notes/test.md", "本文", "{}"}
	model, cmd := updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || !model.console.confirm || len(backend.requests) != 0 {
		t.Fatal("mutation dispatched without review")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.console.confirm || model.console.form == nil {
		t.Fatal("cancel discarded editable form")
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model, cmd = updateKey(model, press('y', "y"))
	if cmd == nil || !model.console.busy {
		t.Fatal("confirmation did not start action")
	}
	_, duplicate := updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if duplicate != nil {
		t.Fatal("duplicate submission")
	}
	model = applyCommand(t, model, cmd)
	if model.console.busy || model.console.err != "" || len(backend.requests) != 1 {
		t.Fatalf("action failed: %+v", model.console)
	}
}

func TestConsoleValidationRevisionAutofillAndErrorRetainsForm(t *testing.T) {
	backend := &consoleTestBackend{err: errors.New("revision conflict")}
	model := NewModel("selected", backend)
	model = model.openConsoleAction(actionByID("note-update"), map[string]any{"id": "n1", "title": "Title", "path": "notes/test.md", "body": "saved body", "revision": float64(7), "properties": map[string]any{}})
	form := model.console.form
	if form.values[0] != "n1" || form.values[1] != "7" || form.values[4] != "saved body" {
		t.Fatalf("autofill=%v", form.values)
	}
	model.console.form.values[1] = "bad"
	model, cmd := updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || model.console.err == "" {
		t.Fatal("invalid revision accepted")
	}
	model.console.form.values[1] = "7"
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
	model, cmd = updateKey(model, press('y', "y"))
	model = applyCommand(t, model, cmd)
	if model.console.form == nil || !strings.Contains(model.console.err, "revision conflict") {
		t.Fatal("error lost editable form")
	}
}

func TestConsoleMutationRetriesKeepIdempotencyKey(t *testing.T) {
	for _, actionID := range []string{"graph-operation", "skill-control"} {
		t.Run(actionID, func(t *testing.T) {
			backend := &consoleTestBackend{err: errors.New("response interrupted")}
			model := NewModel("selected", backend).openConsoleAction(actionByID(actionID), nil)
			model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
			model, command := updateKey(model, press('y', "y"))
			model = applyCommand(t, model, command)
			if model.console.form == nil || len(backend.requests) != 1 {
				t.Fatal("interrupted response did not preserve the retry form")
			}
			key, _ := backend.requests[0].Values["idempotency_key"].(string)
			if key == "" {
				t.Fatal("mutation has no idempotency key")
			}
			backend.err = nil
			backend.response = json.RawMessage(`{"accepted":true}`)
			model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEnter})
			model, command = updateKey(model, press('y', "y"))
			model = applyCommand(t, model, command)
			if len(backend.requests) != 2 || backend.requests[1].Values["idempotency_key"] != key || model.console.err != "" {
				t.Fatal("retry did not reuse the original mutation identity")
			}
		})
	}
}

func TestConsoleRejectsStaleLoadAndSupportsUnicodePaste(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{})
	model.console.generation = 3
	updated, _ := model.Update(consoleLoadedMsg{generation: 2, raw: json.RawMessage(`{"notes":[{"id":"old"}]}`)})
	model = updated.(Model)
	if len(model.console.items) != 0 {
		t.Fatal("stale load accepted")
	}
	model = model.openConsoleAction(actionByID("note-create"), nil)
	model.console.form.cursor = 2
	updated, _ = model.Update(tea.PasteMsg{Content: "first\n日本語"})
	model = updated.(Model)
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if model.console.form.values[2] != "first\n日本" {
		t.Fatalf("paste/backspace=%q", model.console.form.values[2])
	}
}

func TestConsoleInventoryCoversFormerFrontendDomains(t *testing.T) {
	for _, id := range []string{"memory-write", "memory-pin", "memory-delete", "ask", "translate", "note-create", "note-update", "note-trash", "note-restore", "note-delete", "note-revisions", "note-backlinks", "note-retry", "source-import", "source-structure", "source-query", "source-review", "study", "rights-accept", "solution-review", "request-feedback", "scheduler-run", "skill-lifecycle", "skill-control", "client-create", "client-update", "client-delete", "graph-operation", "graph-review", "graph-feedback", "llm-save", "project-init", "project-rename", "project-delete", "portable-export", "memory-import"} {
		if actionByID(id).ID == "" {
			t.Errorf("missing operation %s", id)
		}
	}
}

func TestConsoleDetailReturnsToListAndSelectsNestedSourceMetadata(t *testing.T) {
	backend := &consoleTestBackend{response: json.RawMessage(`{"notes":[{"id":"n1","title":"First"}]}`)}
	model, command := NewModel("selected", backend).openConsoleScreen(2)
	model = applyCommand(t, model, command)
	model = model.acceptConsoleResult(consoleLoadedMsg{generation: model.console.generation, kind: "detail", raw: json.RawMessage(`{"note":{"id":"n1","body":"Full note","properties":{"library_edition_id":"e1"}}}`)})
	formModel := model.openConsoleAction(actionByID("source-structure"), model.consoleSelection())
	if formModel.console.form.values[0] != "e1" {
		t.Fatalf("source edition not selected: %v", formModel.console.form.values)
	}
	model, _ = updateKey(model, tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.console.detail || len(model.console.items) != 1 || model.console.items[0]["id"] != "n1" {
		t.Fatal("detail lost original list")
	}
}

func TestConsoleGraphBindingCannotOverrideWorkspace(t *testing.T) {
	values := map[string]any{"workspace": "other", "scope": map[string]any{"tenant_id": "other", "workspace_id": "other"}}
	bindConsoleValues("/api/v1/graph-index/review", values, "selected")
	if _, exists := values["workspace"]; exists {
		t.Fatal("strict graph body includes extraneous workspace")
	}
	scope := values["scope"].(map[string]any)
	if scope["workspace_id"] != "selected" || scope["tenant_id"] != nil {
		t.Fatalf("scope=%v", scope)
	}
}

func TestConsoleWorkspaceSwitchRejectsOldResults(t *testing.T) {
	old := NewModel("old", &consoleTestBackend{})
	switched, _ := old.Update(workspaceSelectedMsg{generation: old.console.generation, workspace: "new", backend: &consoleTestBackend{}})
	model := switched.(Model)
	if model.workspace != "new" {
		t.Fatal("workspace not switched")
	}
	model = model.acceptConsoleResult(consoleLoadedMsg{generation: old.console.generation, kind: "load", raw: json.RawMessage(`{"notes":[{"id":"old"}]}`)})
	updated, _ := model.Update(recentLoadedMsg{generation: old.browseGeneration, items: []MemoryItem{{ID: "old"}}})
	model = updated.(Model)
	if len(model.browseItems) != 0 || len(model.console.items) != 0 {
		t.Fatal("previous workspace results leaked into switched model")
	}
}

func TestConsoleEditorUpdatesOnlyCurrentDraftWithoutWriting(t *testing.T) {
	backend := &consoleTestBackend{}
	model := NewModel("selected", backend).openConsoleAction(actionByID("note-create"), nil)
	generation := model.console.generation
	updated, _ := model.Update(editorResultMsg{generation: generation, cursor: 2, text: "Edited 日本語"})
	model = updated.(Model)
	if model.console.form.values[2] != "Edited 日本語" || len(backend.requests) != 0 {
		t.Fatal("editor failed to update draft without writing")
	}
	model = model.openConsoleAction(actionByID("note-create"), nil)
	updated, _ = model.Update(editorResultMsg{generation: generation, cursor: 2, text: "Old draft"})
	model = updated.(Model)
	if model.console.form.values[2] != "" {
		t.Fatal("old editor result replaced a new draft")
	}
}

func TestConsoleFormsHideNestedSecrets(t *testing.T) {
	model := resize(NewModel("selected", &consoleTestBackend{}), 100, 24).openConsoleAction(actionByID("skill-lifecycle"), nil)
	model.console.form.values = []string{"codex", "propose", `{"api_key":"private-test-key","nested":{"access_token":"private-test-token"}}`}
	view := ansi.Strip(model.View().Content)
	if strings.Contains(view, "private-test-key") || strings.Contains(view, "private-test-token") {
		t.Fatal("structured form exposed secrets")
	}
}

func TestConsoleWorkspaceRemovalPreventsReopeningDeletedDatabase(t *testing.T) {
	model := NewModel("selected", &consoleTestBackend{})
	model.destination = DestinationConsole
	model = model.acceptConsoleResult(consoleLoadedMsg{generation: model.console.generation, kind: "action", label: actionByID("project-delete").Label, raw: json.RawMessage(`{"deleted":true}`)})
	model, command := updateKey(model, press('r', "r"))
	if command != nil || !model.console.retired {
		t.Fatal("deleted workspace could be reloaded")
	}
	model, _ = updateKey(model, press('1', "1"))
	if model.destination != DestinationConsole {
		t.Fatal("deleted workspace allowed old browse navigation")
	}
}
