package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func fitWorkspaceLines(lines []string, width, height int) []string {
	result := make([]string, 0, height)
	for _, line := range lines {
		if len(result) >= height {
			break
		}
		result = append(result, ansi.Truncate(line, width, "…"))
	}
	for len(result) < height {
		result = append(result, "")
	}
	return result
}

func (m Model) renderWorkspaceShell(width, height int) string {
	rows := height - 4
	contentWidth := width
	railWidth := 0
	if width >= 100 && height >= 20 {
		railWidth = 24
		contentWidth = width - railWidth - 3
	}
	var pane []string
	footer := "Tab Switch · / Search · n New note · i Import source"
	if m.ux.palette != 0 {
		pane = m.renderWorkspacePalette(contentWidth, rows)
		footer = "↑↓ Choose · → / Enter Open · ← / Esc Back"
	} else {
		pane, footer = m.renderWorkspacePane(contentWidth, rows)
	}
	pane = fitWorkspaceLines(pane, contentWidth, rows)
	if railWidth > 0 {
		rail := fitWorkspaceLines(m.renderWorkspaceRail(railWidth, rows), railWidth, rows)
		for i := range pane {
			pane[i] = lipgloss.NewStyle().Width(contentWidth).Render(pane[i]) + mutedStyle.Render(" │ ") + panelStyle.Width(railWidth).Render(rail[i])
		}
	}
	name := m.currentScreenName()
	if m.destination == DestinationSearch && m.detail == nil {
		name = "Search"
	}
	if name == "Home" {
		name = "Overview"
	}
	header := accentStyle.Render("agent memory") + "  " + name + "  " + mutedStyle.Render("· Project: "+sanitizeTerminalText(m.workspace, false))
	lines := []string{panelStyle.Width(width).Render(ansi.Truncate(header, width, "…")), ""}
	lines = append(lines, pane...)
	global := "Ctrl-P Commands · Ctrl-K Go to · Ctrl-W Projects · ? Help · q Quit"
	if width < 80 {
		global = "Ctrl-P Commands · w Projects · ? Help · q Quit"
	}
	if m.console.form != nil {
		global = "Ctrl-E Editor · Ctrl-J New line · Ctrl-C Quit"
	}
	if m.searchFocused {
		global = "Enter Search · Esc Read · Ctrl-C Quit"
	}
	if m.jevInput {
		global = "Enter Save · Esc Cancel · Ctrl-C Quit"
	}
	if m.ux.palette != 0 {
		global = "Esc Close · Ctrl-U Clear filter · Ctrl-C Quit"
	}
	if m.console.busy {
		global = "Ctrl-C Quit"
	}
	if m.ux.navFocused {
		footer = "Workspace panel · ↑↓ Select · → / Enter Open · ← Return"
	}
	lines = append(lines, mutedStyle.Render(footer), mutedStyle.Render(global))
	for index, line := range lines {
		lines[index] = canvasStyle.Width(width).Render(ansi.Truncate(line, width, "…"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderWorkspaceRail(width, rows int) []string {
	context := fmt.Sprintf("%d memories · %d pinned", m.overview.Total, m.overview.Pinned)
	if m.overviewLoading {
		context = "Loading memory count…"
	} else if m.overviewError != "" {
		context = "Count unavailable"
	}
	lines := []string{mutedStyle.Render("WORKSPACE"), accentStyle.Render(ansi.Truncate(sanitizeTerminalText(m.workspace, false), width, "…")), context, mutedStyle.Render("w  All projects"), ""}
	for index, item := range workspaceDestinations()[:11] {
		if index == 8 && rows >= 22 {
			lines = append(lines, "", mutedStyle.Render("MANAGE"))
		}
		label := fmt.Sprintf("%s  %s", item.Key, item.Name)
		if index == 10 {
			label = "w  Workspaces"
		}
		if (item.Core != "" && item.Core == m.destination) || (m.destination == DestinationConsole && item.Screen == m.console.screen) {
			label = accentStyle.Render("● " + label)
		} else {
			label = "  " + label
		}
		if m.ux.navFocused && index == m.ux.navCursor {
			label = activeStyle.Render(ansi.Truncate("› "+item.Name, width, "…"))
		}
		lines = append(lines, label)
	}
	if m.ux.askData != nil {
		lines = append(lines, fmt.Sprintf("%d grounded memories", len(groundedAskRecords(m.ux.askData))))
	}
	return append(lines, "", mutedStyle.Render("Ctrl-P  All commands"), mutedStyle.Render("Ctrl-W  All projects"))
}

func (m Model) renderWorkspacePalette(width, rows int) []string {
	title := "Workspace destinations"
	if m.ux.palette == 2 {
		title = "Command palette"
	}
	dialogWidth := min(width-2, 68)
	dialogRows := min(rows, 18)
	lines := []string{activeStyle.Render("Find: " + m.ux.paletteQuery + "▏"), ""}
	names := []string{}
	if m.ux.palette == 1 {
		for _, item := range m.paletteDestinations() {
			names = append(names, item.Name+"  ·  "+item.Hint)
		}
	} else {
		for _, item := range m.paletteActions() {
			names = append(names, item.Label+"  ·  "+item.Screen)
		}
	}
	if len(names) == 0 {
		lines = append(lines, mutedStyle.Render("No matches. Ctrl-U clears the filter."))
	} else {
		lines = append(lines, renderConsoleChoices(names, clampCursor(m.ux.paletteCursor, len(names)), max(1, dialogRows-5))...)
	}
	lines = fitWorkspaceLines(lines, dialogWidth-4, dialogRows-2)
	block := workspacePanel(title, lines, dialogWidth, true)
	return append(make([]string, max(0, (rows-len(block))/3)), centerWorkspaceBlock(block, width)...)
}

func (m Model) renderWorkspacePane(width, rows int) ([]string, string) {
	if m.helpVisible {
		return strings.Split(m.renderHelp(width, rows), "\n"), "Esc Close help · Ctrl-K Destinations · Ctrl-P Commands"
	}
	if m.detail != nil || m.destination == DestinationBrowse || m.destination == DestinationSearch {
		footer := "Records · ↑↓ Select · → / Enter Read · a Actions · / Search"
		if m.detail != nil {
			footer = "Reading · ↑↓ Scroll · ← / Esc List · → Workspace · a Actions"
		}
		return m.renderMemoryWorkspace(width, rows), footer
	}
	if m.destination == DestinationJev {
		return append([]string{accentStyle.Render("Jev access token"), ""}, m.renderJev()...), "s Set token · x Remove token · Tab Switch"
	}
	if m.destination == DestinationConsole {
		return m.renderConsolePane(width, rows)
	}
	return m.renderWorkspaceHome(width, rows), "Start · ↑↓ Choose · Enter Open · → Workspace · n New · i Import"
}

func (m Model) renderWorkspaceHome(width, rows int) []string {
	lines := []string{mutedStyle.Render("Workspace overview")}
	lines = append(lines, make([]string, max(0, (rows-18)/3))...)
	lines = append(lines, centerWorkspaceBlock([]string{accentStyle.Render("agent memory"), mutedStyle.Render("What would you like to remember?")}, width)...)
	status := fmt.Sprintf("Memories %d · Pinned %d", m.overview.Total, m.overview.Pinned)
	if m.overviewLoading {
		status = "Loading workspace context…"
	} else if m.overviewError != "" {
		status = "Overview unavailable: " + sanitizeTerminalText(m.overviewError, false) + " · r Retry"
	}
	lines = append(lines, centerWorkspaceBlock([]string{mutedStyle.Render(status)}, width)...)
	lines = append(lines, "")
	cardWidth := min(width-2, 66)
	actions := []string{}
	for index, action := range homeActions {
		prefix := "  "
		if index == m.ux.homeCursor && !m.ux.navFocused {
			prefix = "› "
		}
		line := ansi.Truncate(prefix+fmt.Sprintf("%-23s %s", action.name, action.key), cardWidth-4, "…")
		if index == m.ux.homeCursor && !m.ux.navFocused {
			line = activeStyle.Width(cardWidth - 4).Render(line)
		}
		actions = append(actions, line)
	}
	lines = append(lines, centerWorkspaceBlock(workspacePanel("Start here", actions, cardWidth, !m.ux.navFocused), width)...)
	if rows-len(lines) >= 5 {
		lines = append(lines, "", mutedStyle.Render(homeActions[m.ux.homeCursor].hint), mutedStyle.Render("Types  "+renderCounts(m.overview.TypeCounts)), mutedStyle.Render("Tiers  "+renderCounts(m.overview.TierCounts)), mutedStyle.Render("Ctrl-P searches every command. Ctrl-K changes destination."))
	}
	return lines
}

func workspacePanel(title string, lines []string, width int, focused bool) []string {
	border := mutedStyle
	if focused {
		border = accentStyle
	}
	caption := ansi.Truncate("─ "+title+" ", width-2, "…")
	result := []string{border.Render("╭" + caption + strings.Repeat("─", max(0, width-2-ansi.StringWidth(caption))) + "╮")}
	for _, line := range lines {
		content := panelStyle.Width(width - 2).Render(" " + ansi.Truncate(line, width-4, "…") + " ")
		result = append(result, border.Render("│")+content+border.Render("│"))
	}
	return append(result, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
}

func centerWorkspaceBlock(lines []string, width int) []string {
	blockWidth := 0
	for _, line := range lines {
		blockWidth = max(blockWidth, ansi.StringWidth(line))
	}
	indent := strings.Repeat(" ", max(0, (width-blockWidth)/2))
	result := make([]string, len(lines))
	for index, line := range lines {
		result[index] = indent + line
	}
	return result
}

func (m Model) renderMemoryWorkspace(width, rows int) []string {
	title := "Recent memories"
	items, cursor := m.browseItems, m.browseCursor
	if m.destination == DestinationSearch {
		title = "Search memories"
		items, cursor = m.searchItems, m.searchCursor
	}
	lines := []string{accentStyle.Render(title)}
	if m.destination == DestinationSearch {
		query := m.query
		if query == "" {
			query = "Press / to search by meaning"
		}
		if m.searchFocused {
			query += "▏"
		}
		lines = append(lines, activeStyle.Render(ansi.Truncate("Search: "+query, width, "…")))
	}
	lines = append(lines, "")
	available := max(1, rows-len(lines))
	if m.detail != nil && (width < 68 || len(items) == 0) {
		return strings.Split(m.renderDetail(width, rows), "\n")
	}
	if m.destination == DestinationSearch && (m.searchLoading || m.searchError != "" || m.lastSearchQuery == "" || len(items) == 0) {
		return append(lines, m.renderSearch(available)...)
	}
	if m.destination != DestinationSearch && (m.browseLoading || m.browseError != "" || len(items) == 0) {
		return append(lines, m.renderBrowse(available)...)
	}
	listWidth := width
	if width >= 68 {
		listWidth = max(26, width*2/5)
	}
	list := []string{mutedStyle.Render(fmt.Sprintf("%d memories · latest bounded results", len(items)))}
	visible := max(1, (available-1)/2)
	start := max(0, cursor-visible+1)
	for i := start; i < len(items) && i < start+visible; i++ {
		prefix := "  "
		if i == cursor {
			prefix = "› "
		}
		label := ansi.Truncate(prefix+memoryPreview(items[i].Content), listWidth, "…")
		if i == cursor {
			label = activeStyle.Render(label)
		}
		meta := sanitizeTerminalText(items[i].Type, false)
		if items[i].Pinned {
			meta += " · ★ pinned"
		}
		if items[i].Score != nil {
			meta += fmt.Sprintf(" · score %.3f", *items[i].Score)
		}
		list = append(list, label, mutedStyle.Render("  "+meta))
	}
	if width < 68 {
		return append(lines, list...)
	}
	item := items[clampCursor(cursor, len(items))]
	if m.detail != nil {
		item = *m.detail
	}
	preview := memoryDocument(item, width-listWidth-3)
	scroll := 0
	if m.detail != nil {
		scroll = m.detailScroll
	}
	preview = scrollConsoleLines(preview, scroll, available)
	list = fitWorkspaceLines(list, listWidth, available)
	preview = fitWorkspaceLines(preview, width-listWidth-3, available)
	for i := range list {
		lines = append(lines, lipgloss.NewStyle().Width(listWidth).Render(list[i])+mutedStyle.Render(" │ ")+preview[i])
	}
	return lines
}

func memoryDocument(item MemoryItem, width int) []string {
	lines := []string{accentStyle.Render("Memory detail"), sanitizeTerminalText(item.Type, false) + " · " + sanitizeTerminalText(item.StorageTier, false), "ID  " + sanitizeTerminalText(item.ID, false)}
	if item.SourceType != "" || item.SourcePath != "" {
		lines = append(lines, "Source  "+sanitizeTerminalText(item.SourceType+" "+item.SourcePath, false))
	}
	if item.Score != nil {
		lines = append(lines, fmt.Sprintf("Relevance %.3f", *item.Score))
	}
	lines = append(lines, "", accentStyle.Render("Content"))
	lines = append(lines, strings.Split(ansi.Wordwrap(sanitizeTerminalText(item.Content, true), max(1, width), " "), "\n")...)
	if item.Outcome != nil {
		lines = append(lines, "", "Outcome  "+sanitizeTerminalText(item.Outcome.Result, false), sanitizeTerminalText(item.Outcome.Approach, false), sanitizeTerminalText(item.Outcome.Reason, false))
	}
	return lines
}
