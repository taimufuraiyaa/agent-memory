package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type workspaceUX struct {
	navFocused     bool
	navCursor      int
	homeCursor     int
	palette        int // 1 destinations, 2 actions
	paletteContext bool
	paletteQuery   string
	paletteCursor  int
	askQuestion    string
	askData        any
	askDraft       string
}

type workspaceDestination struct {
	Name, Key, Hint string
	Core            Destination
	Screen          int
}

func workspaceDestinations() []workspaceDestination {
	items := []workspaceDestination{
		{"Home", "1", "Workspace overview and next steps", DestinationHome, -1},
		{"Search", "2", "Find knowledge by meaning", DestinationSearch, -1},
		{"Memories", "3", "Recent and pinned knowledge", DestinationBrowse, -1},
		{"Ask", "5", "Ask a grounded question", "", 0},
		{"Notes", "6", "Write and edit workspace notes", "", 2},
		{"Sources", "7", "Import documents and study projects", "", 3},
		{"History", "h", "Saved chats and searches", "", 19},
		{"Activity", "9", "Sessions and recorded work", "", 6},
		{"Graph", "8", "Explore relationships and index status", "", 4},
		{"Settings", "0", "Local models and integrations", "", 11},
		{"Workspaces", "w", "Switch or manage registered workspaces", "", 12},
	}
	seen := map[string]bool{"Ask": true, "Memories": true, "Notes": true, "Sources": true, "Chat and search history": true, "Activity": true, "Graph": true, "Settings": true, "Workspaces": true}
	for index, screen := range consoleScreens {
		if !seen[screen.Name] {
			items = append(items, workspaceDestination{Name: screen.Name, Hint: "Advanced workspace tools", Screen: index})
		}
	}
	return append(items, workspaceDestination{Name: "Jev", Key: "4", Hint: "Local access token", Core: DestinationJev, Screen: -1})
}

func matchesWorkspaceQuery(text, query string) bool {
	text = strings.ToLower(text)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

func (m Model) paletteDestinations() []workspaceDestination {
	items := []workspaceDestination{}
	for _, item := range workspaceDestinations() {
		if matchesWorkspaceQuery(item.Name+" "+item.Hint, m.ux.paletteQuery) {
			items = append(items, item)
		}
	}
	return items
}

func (m Model) paletteActions() []Action {
	items := []Action{}
	for _, action := range consoleActions {
		if m.ux.paletteContext && action.Screen != m.currentScreenName() && !(m.currentScreenName() == "Sources" && strings.HasPrefix(action.ID, "note-")) {
			continue
		}
		if matchesWorkspaceQuery(action.Label+" "+action.Screen, m.ux.paletteQuery) {
			items = append(items, action)
		}
	}
	return items
}

func (m Model) currentScreenName() string {
	if m.detail != nil || m.destination == DestinationBrowse || m.destination == DestinationSearch {
		return "Memories"
	}
	if m.destination == DestinationConsole {
		return consoleScreens[m.console.screen].Name
	}
	if m.destination == DestinationJev {
		return "Jev"
	}
	return "Home"
}

func (m Model) currentRecord() map[string]any {
	if m.destination == DestinationConsole {
		return m.consoleSelection()
	}
	item := m.detail
	if item == nil && m.destination == DestinationBrowse && len(m.browseItems) > 0 {
		item = &m.browseItems[m.browseCursor]
	}
	if item == nil && m.destination == DestinationSearch && len(m.searchItems) > 0 {
		item = &m.searchItems[m.searchCursor]
	}
	if item == nil {
		return nil
	}
	return map[string]any{"id": item.ID, "content": item.Content, "type": item.Type, "pinned": item.Pinned}
}

func (m Model) activateWorkspaceDestination(item workspaceDestination) (Model, tea.Cmd) {
	m.ux.palette = 0
	m.ux.navFocused = false
	m.detail = nil
	m.searchFocused = false
	if item.Core != "" {
		m.console.generation++
		m.console.form = nil
		m.console.actions = false
		m.console.menu = false
		m.destination = item.Core
		return m, nil
	}
	return m.openConsoleScreen(item.Screen)
}

func (m Model) activateWorkspaceAction(action Action) Model {
	var record map[string]any
	if action.Screen == m.currentScreenName() || (m.currentScreenName() == "Sources" && strings.HasPrefix(action.ID, "note-")) {
		record = m.currentRecord()
	}
	for index, screen := range consoleScreens {
		if screen.Name == action.Screen {
			m.console.screen = index
			break
		}
	}
	m.ux.palette = 0
	m.ux.navFocused = false
	return m.openConsoleAction(action, record)
}

func (m Model) cycleWorkspaceDestination(direction int) (Model, tea.Cmd) {
	items := workspaceDestinations()[:10]
	index := 0
	for i, item := range items {
		if item.Core == m.destination || (m.destination == DestinationConsole && item.Screen == m.console.screen) {
			index = i
			break
		}
	}
	return m.activateWorkspaceDestination(items[(index+len(items)+direction)%len(items)])
}

func (m Model) updateWorkspaceUXKey(key tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	pressed := key.String()
	if m.console.busy || m.console.retired || m.console.confirm || m.console.form != nil || m.console.filtering || m.searchFocused || m.jevInput || m.jevConfirmClear || m.jevBusy {
		return m, nil, false
	}
	if m.ux.palette != 0 {
		length := len(m.paletteDestinations())
		if m.ux.palette == 2 {
			length = len(m.paletteActions())
		}
		switch pressed {
		case "esc", "left":
			m.ux.palette = 0
		case "up":
			m.ux.paletteCursor = clampCursor(m.ux.paletteCursor-1, length)
		case "down":
			m.ux.paletteCursor = clampCursor(m.ux.paletteCursor+1, length)
		case "ctrl+u":
			m.ux.paletteQuery = ""
			m.ux.paletteCursor = 0
		case "backspace":
			runes := []rune(m.ux.paletteQuery)
			if len(runes) > 0 {
				m.ux.paletteQuery = string(runes[:len(runes)-1])
				m.ux.paletteCursor = 0
			}
		case "enter", "right":
			if length > 0 {
				index := clampCursor(m.ux.paletteCursor, length)
				if m.ux.palette == 1 {
					next, cmd := m.activateWorkspaceDestination(m.paletteDestinations()[index])
					return next, cmd, true
				}
				return m.activateWorkspaceAction(m.paletteActions()[index]), nil, true
			}
		default:
			if key.Text != "" && len(m.ux.paletteQuery)+len(key.Text) <= 256 {
				m.ux.paletteQuery += sanitizeTerminalText(key.Text, false)
				m.ux.paletteCursor = 0
			}
		}
		return m, nil, true
	}
	if m.ux.navFocused {
		switch pressed {
		case "up", "k":
			m.ux.navCursor = clampCursor(m.ux.navCursor-1, 11)
		case "down", "j":
			m.ux.navCursor = clampCursor(m.ux.navCursor+1, 11)
		case "enter", "right":
			next, cmd := m.activateWorkspaceDestination(workspaceDestinations()[m.ux.navCursor])
			return next, cmd, true
		case "esc", "ctrl+b", "left":
			m.ux.navFocused = false
		default:
			if pressed == "q" || pressed == "?" {
				return m, nil, false
			}
		}
		return m, nil, true
	}
	if next, cmd, handled := m.updateWorkspaceArrowKey(key); handled {
		return next, cmd, true
	}
	switch pressed {
	case "ctrl+k", "ctrl+p", "a":
		if pressed == "a" && (m.consoleBackend == nil || m.currentScreenName() == "Home" || m.currentScreenName() == "Jev") {
			return m, nil, false
		}
		m.ux.palette = 1
		if pressed != "ctrl+k" {
			m.ux.palette = 2
		}
		m.ux.paletteContext = pressed == "a"
		m.ux.paletteQuery = ""
		m.ux.paletteCursor = 0
		return m, nil, true
	case "ctrl+b":
		return m.focusWorkspacePanel(), nil, true
	case "ctrl+w":
		next, cmd := m.activateWorkspaceDestination(workspaceDestinations()[10])
		return next, cmd, true
	case "h":
		next, cmd := m.activateWorkspaceDestination(workspaceDestinations()[6])
		return next, cmd, true
	case "i":
		if m.consoleBackend != nil {
			return m.activateWorkspaceAction(actionByID("source-import")), nil, true
		}
	case "n":
		if m.currentScreenName() == "Sources" {
			return m.activateWorkspaceAction(actionByID("source-import")), nil, true
		}
		if m.currentScreenName() == "Chat and search history" {
			next, cmd := m.activateWorkspaceDestination(workspaceDestinations()[3])
			return next, cmd, true
		}
		if m.currentScreenName() == "Home" || m.currentScreenName() == "Notes" {
			return m.activateWorkspaceAction(actionByID("note-create")), nil, true
		}
		if m.currentScreenName() == "Memories" {
			return m.activateWorkspaceAction(actionByID("memory-write")), nil, true
		}
	}
	for _, item := range workspaceDestinations() {
		if item.Key == pressed {
			next, cmd := m.activateWorkspaceDestination(item)
			return next, cmd, true
		}
	}
	return m, nil, false
}
