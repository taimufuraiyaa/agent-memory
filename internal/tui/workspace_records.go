package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func recordDeleted(record map[string]any) bool {
	if record["deleted"] == true {
		return true
	}
	value := record["deleted_at"]
	return value != nil && fmt.Sprint(value) != "" && fmt.Sprint(value) != "<nil>"
}

func (m Model) visibleConsoleRecords() []map[string]any {
	items := []map[string]any{}
	name := consoleScreens[m.console.screen].Name
	for _, record := range m.console.items {
		if name == "Notes" && recordDeleted(record) != m.console.showTrash {
			continue
		}
		if name == "Sources" && recordValue(record, "properties.library_edition_id") == nil {
			continue
		}
		if !matchesWorkspaceQuery(recordLabel(record)+" "+fmt.Sprint(record["path"]), m.console.filter) {
			continue
		}
		items = append(items, record)
	}
	return items
}

func (m *Model) reconcileConsoleNote(note map[string]any) {
	id, _ := note["id"].(string)
	if id == "" {
		return
	}
	for index, record := range m.console.items {
		if record["id"] == id {
			m.console.items[index] = note
			return
		}
	}
	m.console.items = append([]map[string]any{note}, m.console.items...)
}

func (m Model) renderRecordWorkspace(width, rows int) []string {
	items := m.visibleConsoleRecords()
	mode := fmt.Sprintf("%d loaded records", len(items))
	name := consoleScreens[m.console.screen].Name
	if name == "Notes" {
		mode = "Active notes"
		if m.console.showTrash {
			mode = "Trash"
		}
		mode += fmt.Sprintf(" · %d shown · t Toggle", len(items))
	}
	if name == "Workspaces" {
		mode = projectListSummary(m.console.items)
	}
	if m.console.detail {
		mode += " · Reading"
	} else {
		mode += " · Records"
	}
	filter := m.console.filter
	if filter == "" {
		filter = "f Filter this loaded list"
	}
	if m.console.filtering {
		filter = m.console.filter + "▏"
	}
	lines := []string{mutedStyle.Render(mode), "Filter: " + filter, ""}
	available := max(1, rows-len(lines))
	if len(items) == 0 {
		switch name {
		case "Notes":
			return append(lines, "No notes here yet.", "n  Create your first note · t  Check trash")
		case "Sources":
			return append(lines, "No imported documents match this view.", "i  Import PDF, EPUB, Markdown or text", "s  Study the registered project")
		case "Chat and search history":
			return append(lines, "Your successful questions and searches appear here.", "5  Ask a question · /  Search memories")
		default:
			return append(lines, "No matching records. Clear the filter or refresh.", "a  Available actions · r  Refresh")
		}
	}
	cursor := clampCursor(m.console.cursor, len(items))
	listWidth := width
	if width >= 68 {
		listWidth = max(26, width*2/5)
	}
	visible := max(1, available/3)
	start := max(0, cursor-visible+1)
	list := []string{}
	for i := start; i < len(items) && i < start+visible; i++ {
		prefix := "  "
		if i == cursor {
			prefix = "› "
		}
		label := recordLabel(items[i])
		if name == "Workspaces" && items[i]["name"] == m.workspace {
			label += " · current"
		}
		line := ansi.Truncate(prefix+sanitizeTerminalText(label, false), listWidth, "…")
		if i == cursor {
			line = activeStyle.Render(line)
		}
		meta := ""
		for _, key := range []string{"path", "kind", "client_kind", "updated_at", "created_at"} {
			if value := items[i][key]; value != nil {
				meta = fmt.Sprint(value)
				break
			}
		}
		if name == "Workspaces" {
			meta = projectMemoryLabel(items[i])
		}
		list = append(list, line, mutedStyle.Render("  "+sanitizeTerminalText(meta, false)), "")
	}
	if width < 68 {
		return append(lines, list...)
	}
	record := items[cursor]
	if m.console.detail {
		record = selectedRecord(m.console.data)
	}
	previewWidth := width - listWidth - 3
	preview := workspaceRecordDocument(name, record, previewWidth, m.console.inspect)
	if m.console.detail {
		preview = scrollConsoleLines(preview, m.console.scroll, available)
	}
	list = fitWorkspaceLines(list, listWidth, available)
	preview = fitWorkspaceLines(preview, previewWidth, available)
	for i := range list {
		lines = append(lines, lipgloss.NewStyle().Width(listWidth).Render(list[i])+mutedStyle.Render(" │ ")+preview[i])
	}
	return lines
}

func workspaceRecordDocument(screen string, data any, width int, inspect bool) []string {
	if inspect {
		return append([]string{accentStyle.Render("Advanced details"), ""}, documentLines(redactConsoleSecrets(data), width, 0)...)
	}
	record := selectedRecord(data)
	if record == nil {
		return documentLines(data, width, 0)
	}
	if screen == "Workspaces" {
		return projectRecordDocument(record)
	}
	if screen == "Chat and search history" {
		query, _ := record["query"].(string)
		if result, ok := record["result"].(map[string]any); ok {
			return append([]string{accentStyle.Render("Saved question"), sanitizeTerminalText(query, false), ""}, askResultLines(result, width)...)
		}
	}
	if body, ok := record["body"].(string); ok || ((screen == "Notes" || screen == "Sources") && record["path"] != nil) {
		lines := []string{accentStyle.Render(sanitizeTerminalText(recordLabel(record), false))}
		for _, key := range []string{"path", "index_state", "revision"} {
			if value := record[key]; value != nil {
				lines = append(lines, strings.ReplaceAll(key, "_", " ")+"  "+sanitizeTerminalText(fmt.Sprint(value), false))
			}
		}
		lines = append(lines, "")
		if ok {
			lines = append(lines, strings.Split(ansi.Wordwrap(sanitizeTerminalText(body, true), max(1, width), " "), "\n")...)
		} else {
			lines = append(lines, mutedStyle.Render("Enter to load the full document."))
		}
		if issue, ok := record["index_error"].(string); ok && issue != "" {
			lines = append(lines, "", errorStyle.Render(sanitizeTerminalText(issue, false)), "a  Retry indexing")
		}
		return append(lines, "", mutedStyle.Render("e Edit · a Actions · d Advanced details"))
	}
	if content, ok := record["content"].(string); ok {
		item := MemoryItem{Content: content}
		item.ID, _ = record["id"].(string)
		item.Type, _ = record["type"].(string)
		item.StorageTier, _ = record["storage_tier"].(string)
		if source, ok := record["source"].(map[string]any); ok {
			item.SourceType, _ = source["type"].(string)
			item.SourcePath, _ = source["file_path"].(string)
		}
		return memoryDocument(item, width)
	}
	return documentLines(redactConsoleSecrets(data), width, 0)
}
