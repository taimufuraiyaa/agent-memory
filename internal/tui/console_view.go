package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func (m Model) renderConsolePane(width, rows int) ([]string, string) {
	screen := consoleScreens[m.console.screen]
	title := screen.Name
	if title == "Chat and search history" {
		title = "History"
	}
	if m.console.form != nil && m.console.form.action.ID != "ask" {
		title = m.console.form.action.Label
	}
	lines := []string{accentStyle.Render(title)}
	descriptions := map[string]string{"Notes": "Write, review and keep useful knowledge.", "Sources": "Documents and the registered project.", "Graph": "Relationships, evidence and index operations.", "Chat and search history": "Private saved questions and searches.", "Settings": "Your local models and integrations."}
	descriptions["Workspaces"] = "All registered projects · choose a project to open its data."
	if description := descriptions[screen.Name]; description != "" && m.console.form == nil {
		lines = append(lines, mutedStyle.Render(description))
	} else {
		lines = append(lines, "")
	}
	if m.console.err != "" {
		lines = append(lines, errorStyle.Render(ansi.Truncate(sanitizeTerminalText(m.console.err, false), width, "…")))
	}
	if m.console.notice != "" && m.console.err == "" {
		lines = append(lines, mutedStyle.Render(sanitizeTerminalText(m.console.notice, false)))
	}
	available := max(1, rows-len(lines))
	footer := "Records · ↑↓ Select · → / Enter Read · a Actions · r Refresh"
	var body []string
	switch {
	case m.console.busy:
		body = []string{mutedStyle.Render("Working… " + m.console.label), "Please wait. Ctrl-C cancels the program."}
	case m.console.confirm:
		body = m.renderPreparedReview(width, available)
		footer = "↑↓ Review · y Confirm · n / Esc Return to form"
	case screen.Name == "Ask" && !m.console.evidence && (m.console.form == nil || m.console.form.action.ID == "ask"):
		body = m.renderAskPane(width, available)
		footer = "Reading · ↑↓ Scroll · Enter Ask · → Workspace · v Memories"
		if m.console.form != nil {
			footer = "Enter Send · ↑↓←→ Caret · Esc Read · Ctrl-O Options"
		}
	case m.console.form != nil:
		body = m.renderWorkspaceForm(width, available)
		footer = "Ctrl-S Review · Tab Field · Arrows Edit · Esc Cancel"
	case m.console.menu:
		names := []string{}
		for _, destination := range consoleScreens {
			names = append(names, destination.Name)
		}
		body = renderConsoleChoices(names, m.console.menuCursor, available)
	case m.console.actions:
		names := []string{}
		for _, action := range actionsForScreen(screen.Name) {
			names = append(names, action.Label)
		}
		body = renderConsoleChoices(names, m.console.actionCursor, available)
	case m.console.loading:
		body = []string{mutedStyle.Render("Loading " + m.console.label + "…")}
	case len(m.visibleConsoleRecords()) > 0 && !(m.console.detail && width < 68):
		body = m.renderRecordWorkspace(width, available)
	case m.console.detail && m.console.data != nil:
		body = scrollConsoleLines(workspaceRecordDocument(screen.Name, m.console.data, width, m.console.inspect), m.console.scroll, available)
	case screen.Name == "Notes" || screen.Name == "Sources" || screen.Name == "Chat and search history":
		body = m.renderRecordWorkspace(width, available)
	case m.console.data != nil:
		body = scrollConsoleLines(workspaceRecordDocument(screen.Name, m.console.data, width, m.console.inspect), m.console.scroll, available)
	default:
		body = []string{"Choose an action to get started.", "a Context actions · Ctrl-P Search every command"}
	}
	if screen.Name == "Notes" && m.console.form == nil && !m.console.confirm {
		footer = "Records · ↑↓ Select · → Read · n New · e Edit · f Filter · t Trash"
		if m.console.showTrash {
			footer = "u Restore · x Delete · t Active · f Filter · Enter Read"
		}
		if m.console.detail {
			footer = "Reading · ↑↓ Scroll · ← / Esc List · → Workspace · e Edit"
			if m.console.showTrash {
				footer = "u Restore · x Delete · Esc List · d Details"
			}
		}
	}
	if screen.Name == "Sources" && m.console.form == nil {
		footer = "Records · ↑↓ Select · → Read · i Import · s Study · a Actions"
		if m.console.detail {
			footer = "Reading · ↑↓ Scroll · ← / Esc List · → Workspace · a Actions"
		}
	}
	if screen.Name == "Workspaces" && m.console.form == nil && !m.console.confirm {
		footer = "Projects · ↑↓ Select · → / Enter Open project · f Filter · r Refresh"
	}
	if m.console.filtering {
		footer = "Type filter · Enter Apply · Esc Done · Ctrl-U Clear"
	}
	lines = append(lines, body...)
	return fitWorkspaceLines(lines, width, rows), footer
}

func safeFormValue(field Field, text string) string {
	if field.Kind == "secret" || isSecretField(field.Name) {
		return "[hidden]"
	}
	if field.Kind == "json" {
		var value any
		if json.Unmarshal([]byte(text), &value) == nil {
			redacted, _ := json.Marshal(redactConsoleSecrets(value))
			text = string(redacted)
		}
	}
	return sanitizeTerminalText(text, false)
}

func redactConsoleSecrets(data any) any {
	switch value := data.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, child := range value {
			if isSecretField(key) {
				result[key] = "[hidden]"
			} else {
				result[key] = redactConsoleSecrets(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = redactConsoleSecrets(child)
		}
		return result
	default:
		return value
	}
}

func renderConsoleChoices(names []string, cursor, rows int) []string {
	if len(names) == 0 {
		return []string{mutedStyle.Render("No records. Press a for actions or r to refresh.")}
	}
	start := 0
	if cursor >= rows {
		start = cursor - rows + 1
	}
	end := start + rows
	if end > len(names) {
		end = len(names)
	}
	lines := []string{}
	for index := start; index < end; index++ {
		line := "  " + sanitizeTerminalText(names[index], false)
		if index == cursor {
			line = activeStyle.Render("› " + sanitizeTerminalText(names[index], false))
		}
		lines = append(lines, line)
	}
	return lines
}

func scrollConsoleLines(lines []string, scroll, rows int) []string {
	maximum := len(lines) - rows
	if maximum < 0 {
		maximum = 0
	}
	if scroll > maximum {
		scroll = maximum
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + rows
	if end > len(lines) {
		end = len(lines)
	}
	return lines[scroll:end]
}

func recordLabel(record map[string]any) string {
	label := ""
	for _, key := range []string{"title", "display_name", "name", "goal_summary", "task", "content", "question", "id", "session_id", "request_id"} {
		if value := record[key]; value != nil && fmt.Sprint(value) != "" {
			label = fmt.Sprint(value)
			break
		}
	}
	for _, key := range []string{"state", "status", "index_state", "result", "type"} {
		if value := record[key]; value != nil {
			label += " · " + fmt.Sprint(value)
			break
		}
	}
	if label == "" {
		label = "Record"
	}
	return label
}

func documentLines(data any, width, depth int) []string {
	if depth > 12 {
		return []string{"…"}
	}
	indent := strings.Repeat("  ", depth)
	lines := []string{}
	switch value := data.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			label := sanitizeTerminalText(strings.ReplaceAll(key, "_", " "), false)
			if isSecretField(key) {
				lines = append(lines, indent+label+": [hidden]")
				continue
			}
			switch child := value[key].(type) {
			case map[string]any, []any:
				lines = append(lines, indent+label+":")
				lines = append(lines, documentLines(child, width, depth+1)...)
			default:
				lines = append(lines, documentLines(indent+label+": "+fmt.Sprint(child), width, 0)...)
			}
			if len(lines) > 10000 {
				lines = append(lines, "Result truncated; narrow the query.")
				break
			}
		}
	case []any:
		if len(value) == 0 {
			return []string{indent + "(none)"}
		}
		for index, item := range value {
			lines = append(lines, indent+fmt.Sprintf("[%d]", index+1))
			lines = append(lines, documentLines(item, width, depth+1)...)
			if len(lines) > 10000 {
				lines = append(lines, "Result truncated; narrow the query.")
				break
			}
		}
	case nil:
		lines = append(lines, indent+"—")
	default:
		text := sanitizeTerminalText(fmt.Sprint(value), true)
		lines = append(lines, strings.Split(ansi.Hardwrap(text, width, true), "\n")...)
	}
	return lines
}
