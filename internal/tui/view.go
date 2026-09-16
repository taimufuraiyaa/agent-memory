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
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#C9A7FF")).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7F8B8D"))
	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#102326")).Background(lipgloss.Color("#DDF5F1")).Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8F8F")).Bold(true)
)

func (m Model) View() tea.View {
	width, height := m.viewport()
	var content string
	if width < minimumWidth || height < minimumHeight {
		content = errorStyle.Render("Terminal too small") + "\n" +
			fmt.Sprintf("Resize to at least %dx%d · q Quit", minimumWidth, minimumHeight)
	} else if m.helpVisible {
		content = m.renderHelp(width, height)
	} else if m.detail != nil {
		content = m.renderDetail(width, height)
	} else {
		content = m.renderWorkspace(width, height)
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

func (m Model) renderWorkspace(width, height int) string {
	divider := mutedStyle.Render(strings.Repeat("─", width))
	header := accentStyle.Render("◆ AGENT MEMORY") + "  " + mutedStyle.Render(m.workspace)
	lines := []string{header, divider, m.renderNavigation(), ""}
	switch m.destination {
	case DestinationSearch:
		lines = append(lines, accentStyle.Render("Search memories"))
		query := m.query
		if query == "" {
			query = "Type / to enter a semantic query"
		}
		prefix := "  "
		if m.searchFocused {
			prefix = "› "
		}
		lines = append(lines, prefix+sanitizeTerminalText(query, false), "")
		lines = append(lines, m.renderSearch(height-len(lines)-2)...)
	case DestinationBrowse:
		lines = append(lines, accentStyle.Render("Recent memories"), "")
		lines = append(lines, m.renderBrowse(height-len(lines)-2)...)
	default:
		lines = append(lines, accentStyle.Render("Workspace overview"), "")
		lines = append(lines, m.renderOverview()...)
	}
	footer := "Tab Switch  ·  / Search  ·  ? Help  ·  q Quit"
	for len(lines) < height-2 {
		lines = append(lines, "")
	}
	lines = append(lines, divider, mutedStyle.Render(footer))
	return strings.Join(lines, "\n")
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
		return []string{mutedStyle.Render("No memories matched “" + sanitizeTerminalText(m.lastSearchQuery, false) + "”.")}
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
		return []string{mutedStyle.Render("No memories in this workspace yet.")}
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

func (m Model) renderNavigation() string {
	items := []struct {
		destination Destination
		label       string
	}{
		{DestinationHome, "1 Home"},
		{DestinationSearch, "2 Search"},
		{DestinationBrowse, "3 Browse"},
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		label := " " + item.label + " "
		if item.destination == m.destination {
			parts = append(parts, activeStyle.Render(label))
		} else {
			parts = append(parts, mutedStyle.Render(label))
		}
	}
	return strings.Join(parts, "  ")
}

func (m Model) renderHelp(width, height int) string {
	lines := []string{
		accentStyle.Render("Keyboard help"),
		mutedStyle.Render(strings.Repeat("─", width)),
		"Tab / Shift-Tab  Switch Home, Search, and Browse",
		"1 / 2 / 3        Open Home, Search, or Browse",
		"/                Focus Search input",
		"↑ / ↓ or j / k   Move through a list or detail",
		"Enter            Search or open selected memory",
		"Esc              Close input, detail, or help",
		"r                Refresh the active destination",
		"?                Toggle this help",
		"q / Ctrl-C       Quit",
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, mutedStyle.Render("Esc or ? Close"))
	return strings.Join(lines, "\n")
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
