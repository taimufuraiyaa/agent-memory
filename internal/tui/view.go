package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	minimumWidth  = 60
	minimumHeight = 16
)

var (
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6B6ED")).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A8A8A"))
	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#EEEEEE")).Background(lipgloss.Color("#303030")).Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8F8F")).Bold(true)
	canvasStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#DEDEDE")).Background(lipgloss.Color("#111111"))
	panelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#DEDEDE")).Background(lipgloss.Color("#1C1C1C"))
)

func (m Model) View() tea.View {
	width, height := m.viewport()
	var content string
	if width < minimumWidth || height < minimumHeight {
		content = errorStyle.Render("Terminal too small") + "\n" +
			fmt.Sprintf("Resize to at least %dx%d · q Quit", minimumWidth, minimumHeight)
	} else {
		content = m.renderWorkspaceShell(width, height)
	}
	view := tea.NewView(boundContent(content, width, height))
	view.AltScreen = true
	view.WindowTitle = "Agent Memory · " + m.workspace
	return view
}

func (m Model) viewport() (int, int) {
	width, height := m.width, m.height
	if width <= 0 {
		width = 88
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func (m Model) renderSearch(availableRows int) []string {
	if m.searchLoading {
		return []string{mutedStyle.Render("Searching memories…")}
	}
	if m.searchError != "" {
		return []string{errorStyle.Render("Search unavailable: " + m.searchError), mutedStyle.Render("Press r to retry.")}
	}
	if m.lastSearchQuery == "" {
		return []string{mutedStyle.Render("No search submitted yet.")}
	}
	if len(m.searchItems) == 0 {
		return []string{mutedStyle.Render("No memories matched “" + sanitizeTerminalText(m.lastSearchQuery, false) + "”."),
			"Scope: project " + sanitizeTerminalText(m.workspace, false), "w / Ctrl-W All projects · i Import source"}
	}
	if availableRows < 1 {
		availableRows = 1
	}
	start := 0
	if m.searchCursor >= availableRows {
		start = m.searchCursor - availableRows + 1
	}
	end := start + availableRows
	if end > len(m.searchItems) {
		end = len(m.searchItems)
	}
	lines := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		item := m.searchItems[index]
		cursor := "  "
		if index == m.searchCursor {
			cursor = "› "
		}
		score := "   —"
		if item.Score != nil {
			score = fmt.Sprintf("%.3f", *item.Score)
		}
		line := fmt.Sprintf("%s%s  %-10s %-12s %s", cursor, score, sanitizeTerminalText(item.Type, false), sanitizeTerminalText(item.StorageTier, false), memoryPreview(item.Content))
		if index == m.searchCursor {
			line = activeStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m Model) renderOverview() []string {
	if m.overviewLoading {
		return []string{mutedStyle.Render("Loading workspace overview…")}
	}
	if m.overviewError != "" {
		return []string{errorStyle.Render("Overview unavailable: " + m.overviewError), mutedStyle.Render("Press r to retry.")}
	}
	updated := "never"
	if !m.overview.UpdatedAt.IsZero() {
		updated = m.overview.UpdatedAt.Local().Format("2006-01-02 15:04")
	}
	lines := []string{
		fmt.Sprintf("Memories  %d", m.overview.Total),
		fmt.Sprintf("Pinned    %d", m.overview.Pinned),
		fmt.Sprintf("Diagrams  %d", m.overview.Diagrams),
		fmt.Sprintf("Updated   %s", updated),
		"",
		"Types     " + renderCounts(m.overview.TypeCounts),
		"Tiers     " + renderCounts(m.overview.TierCounts),
	}
	return lines
}

func (m Model) renderJev() []string {
	if m.jevCredentials == nil {
		return []string{mutedStyle.Render("Local Jev credential setup is unavailable.")}
	}
	if m.jevLoading {
		return []string{mutedStyle.Render("Checking local credential status…")}
	}
	status := "Not configured"
	if m.jevConfigured {
		status = "Configured (not verified with Jev)"
	}
	lines := []string{"Status  " + status, "", mutedStyle.Render("Stored locally for this user. No connection test is available yet."), ""}
	if m.jevInput {
		lines = append(lines, "Token   [hidden input]", mutedStyle.Render("Type or paste token · Enter Save · Esc Cancel"))
	} else if m.jevConfirmClear {
		lines = append(lines, errorStyle.Render("Remove the saved Jev token?"), "y Confirm · any other key Cancel")
	} else if m.jevBusy {
		lines = append(lines, mutedStyle.Render("Updating local credential…"))
	} else {
		lines = append(lines, "s Set or replace token")
		if m.jevConfigured {
			lines = append(lines, "x Remove token")
		}
	}
	if m.jevError != "" {
		lines = append(lines, "", errorStyle.Render(m.jevError))
	}
	if m.jevNotice != "" {
		lines = append(lines, "", mutedStyle.Render(m.jevNotice))
	}
	return lines
}

func renderCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "—"
	}
	order := []string{"episodic", "semantic", "procedural", "outcome", "vector", "vector+graph", "markdown", "cold"}
	parts := make([]string, 0, len(counts))
	seen := make(map[string]struct{}, len(counts))
	for _, key := range order {
		if count, ok := counts[key]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", key, count))
			seen[key] = struct{}{}
		}
	}
	for key, count := range counts {
		if _, ok := seen[key]; !ok {
			parts = append(parts, fmt.Sprintf("%s %d", sanitizeTerminalText(key, false), count))
		}
	}
	return strings.Join(parts, "  ·  ")
}

func (m Model) renderBrowse(availableRows int) []string {
	if m.browseLoading {
		return []string{mutedStyle.Render("Loading recent memories…")}
	}
	if m.browseError != "" {
		return []string{errorStyle.Render("Browse unavailable: " + m.browseError), mutedStyle.Render("Press r to retry.")}
	}
	if len(m.browseItems) == 0 {
		return []string{mutedStyle.Render("No memories in this workspace yet."),
			"Project: " + sanitizeTerminalText(m.workspace, false) + " · 0 saved memories", "",
			"w / Ctrl-W All projects", "n Save a memory · i Import source", "Sources → s Study this project"}
	}
	if availableRows < 1 {
		availableRows = 1
	}
	start := 0
	if m.browseCursor >= availableRows {
		start = m.browseCursor - availableRows + 1
	}
	end := start + availableRows
	if end > len(m.browseItems) {
		end = len(m.browseItems)
	}
	lines := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		item := m.browseItems[index]
		cursor := "  "
		if index == m.browseCursor {
			cursor = "› "
		}
		pin := ""
		if item.Pinned {
			pin = " ★"
		}
		line := fmt.Sprintf("%s%-10s %-12s %s%s", cursor, sanitizeTerminalText(item.Type, false), sanitizeTerminalText(item.StorageTier, false), memoryPreview(item.Content), pin)
		if index == m.browseCursor {
			line = activeStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func memoryPreview(content string) string {
	clean := strings.TrimSpace(sanitizeTerminalText(content, false))
	if clean == "" {
		return "(empty memory)"
	}
	return clean
}

func (m Model) renderDetail(width, height int) string {
	item := *m.detail
	divider := mutedStyle.Render(strings.Repeat("─", width))
	lines := []string{
		accentStyle.Render("Memory detail"),
		divider,
		"ID       " + sanitizeTerminalText(item.ID, false),
		fmt.Sprintf("Type     %s  ·  Tier %s  ·  Pinned %t", sanitizeTerminalText(item.Type, false), sanitizeTerminalText(item.StorageTier, false), item.Pinned),
	}
	source := sanitizeTerminalText(item.SourceType, false)
	if item.SourcePath != "" {
		source += " · " + sanitizeTerminalText(item.SourcePath, false)
	}
	lines = append(lines, "Source   "+source)
	if !item.UpdatedAt.IsZero() {
		lines = append(lines, "Updated  "+item.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	if item.Score != nil {
		lines = append(lines, fmt.Sprintf("Score    %.3f", *item.Score))
	}
	if item.Outcome != nil {
		lines = append(lines, "Outcome  "+sanitizeTerminalText(item.Outcome.Result, false))
		if item.Outcome.Approach != "" {
			lines = append(lines, "Approach "+sanitizeTerminalText(item.Outcome.Approach, false))
		}
		if item.Outcome.Reason != "" {
			lines = append(lines, "Reason   "+sanitizeTerminalText(item.Outcome.Reason, false))
		}
	}
	lines = append(lines, "", accentStyle.Render("Content"))
	content := ansi.Wordwrap(sanitizeTerminalText(item.Content, true), width, " ")
	contentLines := strings.Split(content, "\n")
	available := height - len(lines) - 2
	if available < 1 {
		available = 1
	}
	maxScroll := len(contentLines) - available
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := m.detailScroll
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := scroll + available
	if end > len(contentLines) {
		end = len(contentLines)
	}
	lines = append(lines, contentLines[scroll:end]...)
	lines = append(lines, divider, mutedStyle.Render(fmt.Sprintf("↑/↓ Scroll  ·  %d–%d/%d  ·  Esc Back  ·  q Quit", scroll+1, end, len(contentLines))))
	return strings.Join(lines, "\n")
}

func (m Model) renderHelp(width, height int) string {
	lines := []string{
		accentStyle.Render("Keyboard help"),
		"Ctrl-K Go to a destination · Ctrl-P Search commands",
		"↑↓ Select/scroll · → Read/workspace · ← Return",
		"Ctrl-B Workspace · Ctrl-W Scope · PgUp/PgDn Scroll",
		"Tab Cycle · 1 Home · 2 Search · 3 Browse · 5 Ask",
		"n New note/memory · e Edit note · i Import source",
		"f Filter list · t Active/trash notes · a Actions",
		"Enter Read/send/review · Ctrl-S Review a form",
		"Ctrl-O Advanced options · Ctrl-E External editor",
		"Editing: ↑↓ Lines · ←→ Caret · Tab Fields",
		"Ctrl-J New line · Esc Back · r Refresh · h History",
		"? Close help · q / Ctrl-C Quit",
	}
	return strings.Join(fitWorkspaceLines(lines, width, height), "\n")
}

func boundContent(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for index, line := range lines {
		lines[index] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}
