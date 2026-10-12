package tui

import tea "charm.land/bubbletea/v2"

type homeAction struct{ name, key, hint string }

var homeActions = []homeAction{
	{"Ask your workspace", "5", "Recall a fact, decision or way of working"},
	{"Search memories", "/", "Find knowledge by meaning"},
	{"New note", "n", "Keep a useful idea or document"},
	{"Import source", "i", "Bring in PDF, EPUB, Markdown or text"},
	{"Saved history", "h", "Reopen a question or search"},
}

func (m Model) focusWorkspacePanel() Model {
	m.ux.navCursor = 0
	for index, item := range workspaceDestinations()[:11] {
		if (item.Core != "" && item.Core == m.destination) || (m.destination == DestinationConsole && item.Screen == m.console.screen) {
			m.ux.navCursor = index
			break
		}
	}
	width, height := m.viewport()
	if width >= 100 && height >= 20 {
		m.ux.navFocused = true
	} else {
		m.ux.palette = 1
		m.ux.paletteQuery = ""
		m.ux.paletteCursor = m.ux.navCursor
	}
	return m
}

func (m Model) updateWorkspaceArrowKey(key tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	pressed := key.String()
	if m.destination == DestinationHome && m.detail == nil {
		switch pressed {
		case "up":
			m.ux.homeCursor = clampCursor(m.ux.homeCursor-1, len(homeActions))
			return m, nil, true
		case "down":
			m.ux.homeCursor = clampCursor(m.ux.homeCursor+1, len(homeActions))
			return m, nil, true
		case "enter":
			key := []rune(homeActions[m.ux.homeCursor].key)[0]
			next, cmd := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
			return next.(Model), cmd, true
		}
	}
	switch pressed {
	case "left":
		if m.detail != nil {
			m.detail = nil
			m.detailScroll = 0
		} else if m.destination == DestinationConsole && m.console.detail {
			m.console.generation++
			m.console.loading = false
			m.console.detail = false
			m.console.data = m.console.listData
			m.console.scroll = 0
		}
		return m, nil, true
	case "right":
		if m.destination == DestinationConsole && consoleScreens[m.console.screen].Name == "Ask" && !m.console.evidence {
			return m.focusWorkspacePanel(), nil, true
		}
		if m.detail != nil || (m.destination == DestinationConsole && m.console.detail) {
			return m.focusWorkspacePanel(), nil, true
		}
		if m.destination == DestinationBrowse && len(m.browseItems) > 0 || m.destination == DestinationSearch && len(m.searchItems) > 0 {
			next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return next.(Model), cmd, true
		}
		if m.destination == DestinationConsole && len(m.visibleConsoleRecords()) > 0 {
			return m.updateConsoleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return m.focusWorkspacePanel(), nil, true
	case "pgup", "pgdown":
		_, height := m.viewport()
		step := max(1, height-10)
		if pressed == "pgup" {
			step = -step
		}
		if m.detail != nil {
			m.detailScroll = pageScroll(m.detailScroll, step)
		} else if m.destination == DestinationConsole {
			if m.console.detail || consoleScreens[m.console.screen].Name == "Ask" || len(m.console.items) == 0 {
				m.console.scroll = pageScroll(m.console.scroll, step)
			} else {
				m.console.cursor = clampCursor(m.console.cursor+step, len(m.visibleConsoleRecords()))
			}
		} else if m.destination == DestinationBrowse {
			m.browseCursor = clampCursor(m.browseCursor+step, len(m.browseItems))
		} else if m.destination == DestinationSearch {
			m.searchCursor = clampCursor(m.searchCursor+step, len(m.searchItems))
		}
		return m, nil, true
	}
	return m, nil, false
}

func pageScroll(current, step int) int {
	maximum := int(^uint(0) >> 1)
	if step > 0 && current > maximum-step {
		return maximum
	}
	return max(0, current+step)
}
