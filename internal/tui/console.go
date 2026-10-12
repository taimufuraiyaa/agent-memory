package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
)

const DestinationConsole Destination = "console"

type consoleForm struct {
	action          Action
	values          []string
	cursor          int
	caret           int
	preferredColumn int
	advanced        bool
	pathSeed        string
}
type consoleState struct {
	filter                                                       string
	filtering, showTrash, inspect                                bool
	evidence                                                     bool
	screen, menuCursor, actionCursor, cursor, scroll, generation int
	menu, actions, detail, loading, busy, confirm                bool
	retired                                                      bool
	form                                                         *consoleForm
	pending                                                      Request
	data, listData                                               any
	items                                                        []map[string]any
	err, notice, label                                           string
}
type consoleLoadedMsg struct {
	generation  int
	kind, label string
	raw         json.RawMessage
	err         error
}

func (m Model) consoleRequest(request Request, kind, label string) (Model, tea.Cmd) {
	if m.consoleBackend == nil {
		return m, nil
	}
	m.console.generation++
	generation := m.console.generation
	backend, ctx := m.consoleBackend, m.context
	m.console.err = ""
	m.console.label = label
	if kind == "action" {
		m.console.busy = true
		m.console.confirm = false
	} else {
		m.console.loading = true
		if kind == "detail" || kind == "edit-note" {
			m.console.detail = true
		}
	}
	return m, func() tea.Msg {
		raw, err := backend.Execute(ctx, request)
		return consoleLoadedMsg{generation: generation, kind: kind, label: label, raw: raw, err: err}
	}
}

func (m Model) openConsoleScreen(index int) (Model, tea.Cmd) {
	m.destination = DestinationConsole
	m.detail = nil
	m.console.screen = clampCursor(index, len(consoleScreens))
	m.console.filter = ""
	m.console.filtering = false
	m.console.inspect = false
	m.console.showTrash = false
	m.console.evidence = false
	m.console.menu = false
	m.console.actions = false
	m.console.detail = false
	m.console.form = nil
	m.console.data = nil
	m.console.listData = nil
	m.console.items = nil
	m.console.cursor = 0
	m.console.scroll = 0
	m.console.err = ""
	m.console.notice = ""
	m.console.loading = false
	m.console.generation++
	screen := consoleScreens[m.console.screen]
	if screen.Name == "Ask" {
		m.console.data = m.ux.askData
		return m.openConsoleAction(actionByID("ask"), nil), nil
	}
	if screen.Path == "" {
		return m, nil
	}
	return m.consoleRequest(Request{Method: "GET", Path: screen.Path, Values: screen.Values}, "load", screen.Name)
}

func (m Model) acceptConsoleResult(message consoleLoadedMsg) Model {
	if message.generation != m.console.generation {
		return m
	}
	m.console.loading = false
	m.console.busy = false
	if message.err != nil {
		m.console.err = sanitizeTerminalText(message.err.Error(), false)
		return m
	}
	var data any
	if err := json.Unmarshal(message.raw, &data); err != nil {
		m.console.err = "Cannot decode result; retry the request."
		return m
	}
	m.console.data = data
	m.console.err = ""
	m.console.scroll = 0
	if message.kind == "edit-note" {
		m.console.detail = true
		return m.openConsoleAction(actionByID("note-update"), m.consoleSelection())
	}
	if message.kind == "action" {
		actionID := ""
		if m.console.form != nil {
			actionID = m.console.form.action.ID
		}
		if object, ok := data.(map[string]any); ok && object["skipped"] == true {
			m.console.err = "Operation skipped: " + fmt.Sprint(object["reason"])
			return m
		}
		if m.console.form != nil && (m.console.form.action.ID == "ask" || m.console.form.action.ID == "history-ask") {
			m.ux.askQuestion = m.console.form.value("task_description")
			m.ux.askData = data
			m.ux.askDraft = ""
			m.console.screen = 0
			m.console.evidence = false
			m.console.items = groundedAskRecords(data)
		}
		m.console.form = nil
		m.console.confirm = false
		m.console.actions = false
		m.console.detail = true
		m.console.notice = message.label + " completed"
		if object, ok := data.(map[string]any); ok {
			if note, ok := object["note"].(map[string]any); ok {
				m.reconcileConsoleNote(note)
				if message.label == actionByID("note-create").Label {
					m.console.showTrash = false
				}
			}
		}
		if actionID == "note-trash" || actionID == "note-restore" || actionID == "note-delete" {
			if actionID == "note-delete" {
				id := m.console.pending.Values["note_id"]
				records := []map[string]any{}
				for _, record := range m.console.items {
					if record["id"] != id {
						records = append(records, record)
					}
				}
				m.console.items = records
			}
			m.console.detail = false
			m.console.data = m.console.listData
			m.console.cursor = clampCursor(m.console.cursor, len(m.visibleConsoleRecords()))
		}
		if message.label == actionByID("project-delete").Label || message.label == actionByID("project-rename").Label {
			m.console.retired = true
			m.console.notice = "Workspace changed. Press q to exit and reopen the registered workspace."
		}
		if consoleScreens[m.console.screen].Name == "Memories" {
			if records := recordsFromData(data, "results"); records != nil {
				m.console.listData = data
				m.console.items = records
				m.console.cursor = 0
				m.console.detail = false
			}
		}
	}
	if message.kind == "detail" {
		m.console.detail = true
	}
	if message.kind == "load" {
		m.console.listData = data
		m.console.items = recordsFromData(data, consoleScreens[m.console.screen].Array)
		m.console.cursor = clampCursor(m.console.cursor, len(m.console.items))
	}
	return m
}

func recordsFromData(data any, key string) []map[string]any {
	var values []any
	if root, ok := data.(map[string]any); ok {
		if key != "" {
			values, _ = root[key].([]any)
		}
		if values == nil {
			for _, name := range []string{"results", "notes", "episodes", "profiles", "projects", "skills", "history", "sessions", "observations", "runs", "events", "nodes", "entities", "communities", "statements"} {
				if items, ok := root[name].([]any); ok {
					values = items
					break
				}
			}
		}
	} else {
		values, _ = data.([]any)
	}
	if values == nil {
		return nil
	}
	records := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if record, ok := value.(map[string]any); ok {
			records = append(records, record)
		}
	}
	return records
}

func selectedRecord(data any) map[string]any {
	root, _ := data.(map[string]any)
	for _, key := range []string{"note", "profile", "config", "detail", "episode", "result", "proposal"} {
		if nested, ok := root[key].(map[string]any); ok {
			return selectedRecord(nested)
		}
	}
	return root
}

func (m Model) consoleSelection() map[string]any {
	if m.console.detail {
		return selectedRecord(m.console.data)
	}
	if records := m.visibleConsoleRecords(); len(records) > 0 {
		return records[clampCursor(m.console.cursor, len(records))]
	}
	return selectedRecord(m.console.data)
}

func (m Model) openConsoleAction(action Action, record map[string]any) Model {
	m.destination = DestinationConsole
	m.detail = nil
	m.console.actions = false
	m.console.menu = false
	m.console.confirm = false
	m.console.err = ""
	m.console.notice = ""
	form := &consoleForm{action: action, values: make([]string, len(action.Fields)), caret: -1, preferredColumn: -1, pathSeed: uuid.NewString()[:8]}
	m.console.generation++
	m.console.loading = false
	for index, field := range action.Fields {
		value := field.Default
		if field.Name == "idempotency_key" && value == "" {
			value = uuid.NewString()
		}
		selected := recordValue(record, field.From)
		if field.From != "" && selected != nil {
			switch field.Kind {
			case "json":
				raw, _ := json.Marshal(selected)
				value = string(raw)
			default:
				value = fmt.Sprint(selected)
			}
		}
		form.values[index] = value
		if action.ID == "ask" && field.Name == "task_description" {
			form.values[index] = m.ux.askDraft
		}
	}
	m.console.form = form
	if indices := form.visibleFields(); len(indices) > 0 {
		form.cursor = indices[0]
	}
	return m
}

func (f *consoleForm) request() (Request, error) {
	f.prepareNotePath()
	request := Request{Method: f.action.Method, Path: f.action.Path, Values: map[string]any{}}
	request.ProjectSource = f.action.ID == "source-import"
	for index, field := range f.action.Fields {
		text := f.values[index]
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			if field.Required {
				return Request{}, fmt.Errorf("%s is required", field.Label)
			}
			continue
		}
		var value any = text
		if len(field.Choices) > 0 {
			valid := false
			for _, choice := range field.Choices {
				if trimmed == choice {
					valid = true
					break
				}
			}
			if !valid {
				return Request{}, fmt.Errorf("%s must be one of %s", field.Label, strings.Join(field.Choices, ", "))
			}
		}
		switch field.Kind {
		case "integer":
			number, err := strconv.Atoi(trimmed)
			if err != nil {
				return Request{}, fmt.Errorf("%s must be an integer", field.Label)
			}
			value = number
		case "boolean":
			if trimmed != "true" && trimmed != "false" {
				return Request{}, fmt.Errorf("%s must be true or false", field.Label)
			}
			value = trimmed == "true"
		case "json":
			if err := json.Unmarshal([]byte(text), &value); err != nil {
				return Request{}, fmt.Errorf("%s must contain valid JSON", field.Label)
			}
		case "array":
			parts := strings.Split(text, ",")
			items := []string{}
			for _, part := range parts {
				if item := strings.TrimSpace(part); item != "" {
					items = append(items, item)
				}
			}
			if len(items) == 0 {
				return Request{}, fmt.Errorf("%s needs at least one ID", field.Label)
			}
			value = items
		case "upload":
			request.UploadPath = trimmed
			continue
		case "output":
			request.OutputPath = trimmed
			continue
		case "input":
			request.InputPath = trimmed
			continue
		}
		if field.Name == "payload" && f.action.ID != "skill-lifecycle" {
			object, ok := value.(map[string]any)
			if !ok {
				return Request{}, fmt.Errorf("%s must be a JSON object", field.Label)
			}
			for key, item := range object {
				request.Values[key] = item
			}
		} else {
			request.Values[field.Name] = value
		}
	}
	if strings.Contains(request.Path, "{id}") {
		id, _ := request.Values["id"].(string)
		if strings.ContainsAny(id, "/\\?#") || id == "." || id == ".." {
			return Request{}, fmt.Errorf("invalid client profile ID")
		}
		request.Path = strings.ReplaceAll(request.Path, "{id}", url.PathEscape(id))
	}
	if f.action.ID == "note-create" || f.action.ID == "note-update" {
		body := noteDocument(f.value("title"), f.value("body"))
		if len(body) > 65536 {
			return Request{}, fmt.Errorf("note body including its title exceeds 64 KiB")
		}
		request.Values["body"] = body
	}
	return request, nil
}

func (m Model) updateConsoleInput(text string) Model {
	form := m.console.form
	if form == nil || len(form.values) == 0 {
		return m
	}
	field := form.action.Fields[form.cursor]
	text = sanitizeTerminalText(text, field.Kind == "multiline" || field.Kind == "json")
	if len(form.values[form.cursor])+len(text) <= 65536 {
		runes := []rune(form.values[form.cursor])
		caret := form.caretPosition()
		form.values[form.cursor] = string(runes[:caret]) + text + string(runes[caret:])
		form.caret = caret + len([]rune(text))
		form.preferredColumn = -1
	}
	return m
}

func (m Model) updateConsoleKey(key tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if m.consoleBackend == nil {
		return m, nil, false
	}
	pressed := key.String()
	if m.console.retired {
		return m, nil, pressed != "q" && pressed != "?" && pressed != "ctrl+c"
	}
	if m.console.busy {
		return m, nil, pressed != "ctrl+c"
	}
	if m.console.filtering {
		switch pressed {
		case "esc", "enter":
			m.console.filtering = false
		case "ctrl+u":
			m.console.filter = ""
			m.console.cursor = 0
		case "backspace":
			runes := []rune(m.console.filter)
			if len(runes) > 0 {
				m.console.filter = string(runes[:len(runes)-1])
				m.console.cursor = 0
			}
		default:
			if key.Text != "" && len(m.console.filter)+len(key.Text) <= 256 {
				m.console.filter += sanitizeTerminalText(key.Text, false)
				m.console.cursor = 0
			}
		}
		return m, nil, true
	}
	if m.console.confirm {
		switch pressed {
		case "y":
			next, cmd := m.consoleRequest(m.console.pending, "action", m.console.form.action.Label)
			return next, cmd, true
		case "esc", "n":
			m.console.confirm = false
		case "up", "k":
			if m.console.scroll > 0 {
				m.console.scroll--
			}
		case "down", "j":
			m.console.scroll++
		case "g":
			m.console.scroll = 0
		case "G":
			m.console.scroll = int(^uint(0) >> 1)
		}
		return m, nil, true
	}
	if m.console.form != nil {
		form := m.console.form
		switch pressed {
		case "esc":
			if form.action.ID == "ask" {
				m.ux.askDraft = form.value("task_description")
			}
			m.console.form = nil
			m.console.err = ""
		case "tab", "ctrl+down":
			form.moveField(1)
		case "shift+tab", "ctrl+up":
			form.moveField(-1)
		case "up", "down":
			direction := 1
			if pressed == "up" {
				direction = -1
			}
			field := form.action.Fields[form.cursor]
			if field.Kind == "multiline" || field.Kind == "json" {
				form.moveCaretVertical(direction)
			} else {
				form.moveField(direction)
			}
		case "ctrl+o":
			form.advanced = !form.advanced
			if !form.advanced {
				indices := form.visibleFields()
				if len(indices) > 0 {
					form.cursor = indices[0]
					form.caret = -1
					form.preferredColumn = -1
				}
			}
		case "ctrl+j":
			m = m.updateConsoleInput("\n")
		case "ctrl+e":
			next, cmd := m.editConsoleField()
			return next, cmd, true
		case "left", "right":
			form.preferredColumn = -1
			if len(form.values) > 0 {
				field := form.action.Fields[form.cursor]
				if len(field.Choices) > 0 {
					index := -1
					for current, choice := range field.Choices {
						if choice == form.values[form.cursor] {
							index = current
							break
						}
					}
					if pressed == "left" {
						if index < 0 {
							index = 0
						}
						index = (index + len(field.Choices) - 1) % len(field.Choices)
					} else {
						index = (index + 1) % len(field.Choices)
					}
					form.values[form.cursor] = field.Choices[index]
				} else {
					step := 1
					if pressed == "left" {
						step = -1
					}
					form.caret = max(0, min(len([]rune(form.values[form.cursor])), form.caretPosition()+step))
				}
			}
		case "backspace":
			form.preferredColumn = -1
			if len(form.values) > 0 {
				runes := []rune(form.values[form.cursor])
				caret := form.caretPosition()
				if caret > 0 {
					form.values[form.cursor] = string(runes[:caret-1]) + string(runes[caret:])
					form.caret = caret - 1
				}
			}
		case "delete":
			form.preferredColumn = -1
			if len(form.values) > 0 {
				runes := []rune(form.values[form.cursor])
				caret := form.caretPosition()
				if caret < len(runes) {
					form.values[form.cursor] = string(runes[:caret]) + string(runes[caret+1:])
					form.caret = caret
				}
			}
		case "home":
			form.caret = 0
			form.preferredColumn = -1
		case "end":
			form.caret = -1
			form.preferredColumn = -1
		case "ctrl+u":
			if len(form.values) > 0 {
				form.values[form.cursor] = ""
				form.caret = -1
				form.preferredColumn = -1
			}
		case "enter", "ctrl+enter", "ctrl+s":
			request, err := form.request()
			if err != nil {
				m.console.err = err.Error()
				form.advanced = true
				return m, nil, true
			}
			if form.action.Mutation {
				m.console.pending = request
				m.console.confirm = true
				m.console.scroll = 0
				return m, nil, true
			}
			next, cmd := m.consoleRequest(request, "action", form.action.Label)
			return next, cmd, true
		default:
			if key.Text != "" {
				m = m.updateConsoleInput(key.Text)
			}
		}
		return m, nil, true
	}
	if pressed == "ctrl+k" {
		m.destination = DestinationConsole
		m.detail = nil
		m.console.menu = !m.console.menu
		m.console.menuCursor = m.console.screen
		return m, nil, true
	}
	for digit, screen := range map[string]int{"5": 0, "6": 2, "7": 3, "8": 4, "9": 6, "0": 11} {
		if pressed == digit {
			next, cmd := m.openConsoleScreen(screen)
			return next, cmd, true
		}
	}
	if pressed == "a" && (m.detail != nil || m.destination == DestinationBrowse || m.destination == DestinationSearch) {
		record := map[string]any{}
		var memory *MemoryItem
		if m.detail != nil {
			memory = m.detail
		} else if m.destination == DestinationBrowse && len(m.browseItems) > 0 {
			memory = &m.browseItems[m.browseCursor]
		} else if len(m.searchItems) > 0 {
			memory = &m.searchItems[m.searchCursor]
		}
		if memory != nil {
			record = map[string]any{"id": memory.ID, "pinned": memory.Pinned, "content": memory.Content}
		}
		m.detail = nil
		m.destination = DestinationConsole
		m.console.screen = 1
		m.console.data = record
		m.console.detail = true
		m.console.actions = true
		m.console.actionCursor = 0
		return m, nil, true
	}
	if m.destination != DestinationConsole {
		return m, nil, false
	}
	if m.console.menu {
		switch pressed {
		case "esc":
			m.console.menu = false
		case "up", "k":
			m.console.menuCursor = clampCursor(m.console.menuCursor-1, len(consoleScreens))
		case "down", "j":
			m.console.menuCursor = clampCursor(m.console.menuCursor+1, len(consoleScreens))
		case "enter":
			next, cmd := m.openConsoleScreen(m.console.menuCursor)
			return next, cmd, true
		case "q", "?":
			return m, nil, false
		}
		return m, nil, true
	}
	if m.console.actions {
		actions := actionsForScreen(consoleScreens[m.console.screen].Name)
		switch pressed {
		case "esc":
			m.console.actions = false
		case "up", "k":
			m.console.actionCursor = clampCursor(m.console.actionCursor-1, len(actions))
		case "down", "j":
			m.console.actionCursor = clampCursor(m.console.actionCursor+1, len(actions))
		case "enter":
			if len(actions) > 0 {
				m = m.openConsoleAction(actions[m.console.actionCursor], m.consoleSelection())
			}
		case "q", "?":
			return m, nil, false
		}
		return m, nil, true
	}
	switch pressed {
	case "f":
		m.console.filtering = true
		return m, nil, true
	case "v":
		if consoleScreens[m.console.screen].Name == "Ask" {
			m.console.evidence = true
			m.console.items = groundedAskRecords(m.ux.askData)
			m.console.detail = false
			m.console.cursor = 0
			return m, nil, true
		}
	case "t":
		if consoleScreens[m.console.screen].Name == "Notes" {
			m.console.showTrash = !m.console.showTrash
			m.console.detail = false
			m.console.cursor = 0
			return m, nil, true
		}
	case "d":
		m.console.inspect = !m.console.inspect
		m.console.scroll = 0
		return m, nil, true
	case "e":
		if consoleScreens[m.console.screen].Name == "Notes" || consoleScreens[m.console.screen].Name == "Sources" {
			record := m.consoleSelection()
			if record["id"] == nil {
				return m, nil, true
			}
			if m.console.detail {
				return m.openConsoleAction(actionByID("note-update"), record), nil, true
			}
			next, cmd := m.consoleRequest(Request{Method: "GET", Path: "/api/v1/notes/get", Values: map[string]any{"note_id": record["id"]}}, "edit-note", "Open note for editing")
			return next, cmd, true
		}
	case "x":
		if consoleScreens[m.console.screen].Name == "Notes" && len(m.visibleConsoleRecords()) > 0 {
			id := "note-trash"
			if m.console.showTrash {
				id = "note-delete"
			}
			return m.openConsoleAction(actionByID(id), m.consoleSelection()), nil, true
		}
	case "u":
		if consoleScreens[m.console.screen].Name == "Notes" && m.console.showTrash && len(m.visibleConsoleRecords()) > 0 {
			return m.openConsoleAction(actionByID("note-restore"), m.consoleSelection()), nil, true
		}
	case "s":
		if consoleScreens[m.console.screen].Name == "Sources" {
			return m.openConsoleAction(actionByID("study"), nil), nil, true
		}
		if consoleScreens[m.console.screen].Name == "Ask" && m.ux.askQuestion != "" {
			m.destination = DestinationSearch
			m.query = m.ux.askQuestion
			next, cmd := m.startSearch(m.query)
			return next.(Model), cmd, true
		}
	case "n":
		if consoleScreens[m.console.screen].Name == "Ask" {
			return m.openConsoleAction(actionByID("ask"), nil), nil, true
		}
		m.console.actions = true
		m.console.actionCursor = 0
		return m, nil, true
	case "a":
		m.console.actions = true
		m.console.actionCursor = 0
		return m, nil, true
	case "r":
		next, cmd := m.openConsoleScreen(m.console.screen)
		next.console.filter = m.console.filter
		next.console.showTrash = m.console.showTrash
		return next, cmd, true
	case "esc":
		if consoleScreens[m.console.screen].Name == "Ask" && m.console.evidence && !m.console.detail {
			m.console.evidence = false
			m.console.data = m.ux.askData
			return m, nil, true
		}
		m.console.generation++
		m.console.loading = false
		m.console.detail = false
		m.console.data = m.console.listData
		m.console.scroll = 0
		return m, nil, true
	case "up", "k":
		if m.console.detail || len(m.console.items) == 0 || (consoleScreens[m.console.screen].Name == "Ask" && !m.console.evidence) {
			if m.console.scroll > 0 {
				m.console.scroll--
			}
		} else {
			m.console.cursor = clampCursor(m.console.cursor-1, len(m.visibleConsoleRecords()))
		}
		return m, nil, true
	case "down", "j":
		if m.console.detail || len(m.console.items) == 0 || (consoleScreens[m.console.screen].Name == "Ask" && !m.console.evidence) {
			m.console.scroll++
		} else {
			m.console.cursor = clampCursor(m.console.cursor+1, len(m.visibleConsoleRecords()))
		}
		return m, nil, true
	case "g":
		m.console.scroll = 0
		m.console.cursor = 0
		return m, nil, true
	case "G":
		if len(m.console.items) > 0 && !m.console.detail && !(consoleScreens[m.console.screen].Name == "Ask" && !m.console.evidence) {
			m.console.cursor = max(0, len(m.visibleConsoleRecords())-1)
		} else {
			m.console.scroll = int(^uint(0) >> 1)
		}
		return m, nil, true
	case "enter":
		if consoleScreens[m.console.screen].Name == "Ask" && !m.console.evidence {
			return m.openConsoleAction(actionByID("ask"), nil), nil, true
		}
		if len(m.visibleConsoleRecords()) == 0 {
			return m, nil, true
		}
		record := m.consoleSelection()
		switch consoleScreens[m.console.screen].Name {
		case "Workspaces":
			workspace, _ := record["name"].(string)
			selector, ok := m.backend.(WorkspaceSelector)
			if !ok || workspace == "" {
				m.console.err = "Workspace switching is unavailable."
				return m, nil, true
			}
			m.console.generation++
			generation := m.console.generation
			m.console.loading = true
			m.console.label = "Workspace"
			return m, func() tea.Msg {
				backend, err := selector.SelectWorkspace(m.context, workspace)
				return workspaceSelectedMsg{generation: generation, workspace: workspace, backend: backend, err: err}
			}, true
		case "Notes", "Sources":
			next, cmd := m.consoleRequest(Request{Method: "GET", Path: "/api/v1/notes/get", Values: map[string]any{"note_id": record["id"]}}, "detail", "Note detail")
			return next, cmd, true
		case "How history":
			next, cmd := m.consoleRequest(Request{Method: "GET", Path: "/api/v1/solutions/activity", Values: map[string]any{"episode_id": record["id"]}}, "detail", "Solution detail")
			return next, cmd, true
		default:
			m.console.data = record
			m.console.detail = true
			m.console.scroll = 0
		}
		return m, nil, true
	}
	return m, nil, false
}

func recordValue(record map[string]any, locator string) any {
	var value any = record
	for _, key := range strings.Split(locator, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}
