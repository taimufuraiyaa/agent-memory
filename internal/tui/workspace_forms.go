package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

func (f *consoleForm) visibleFields() []int {
	primary := map[string][]string{
		"ask": {"task_description"}, "history-ask": {"task_description"},
		"note-create": {"title", "body"}, "note-update": {"title", "body"},
		"memory-write":  {"type", "content"},
		"source-import": {"source_file", "title", "format", "rights_basis"},
	}
	wanted, custom := primary[f.action.ID]
	indices := []int{}
	for index, field := range f.action.Fields {
		visible := f.advanced || !custom
		for _, name := range wanted {
			if field.Name == name {
				visible = true
			}
		}
		if visible {
			indices = append(indices, index)
		}
	}
	return indices
}

func (f *consoleForm) moveField(direction int) {
	indices := f.visibleFields()
	if len(indices) == 0 {
		return
	}
	position := 0
	for i, index := range indices {
		if f.cursor == index {
			position = i
			break
		}
	}
	f.cursor = indices[(position+len(indices)+direction)%len(indices)]
	f.caret = -1
	f.preferredColumn = -1
}

func (f *consoleForm) moveCaretVertical(direction int) {
	runes := []rune(f.values[f.cursor])
	caret := f.caretPosition()
	start := caret
	for start > 0 && runes[start-1] != '\n' {
		start--
	}
	if f.preferredColumn < 0 {
		f.preferredColumn = caret - start
	}
	if direction < 0 {
		if start == 0 {
			return
		}
		end := start - 1
		start = end
		for start > 0 && runes[start-1] != '\n' {
			start--
		}
		f.caret = start + min(f.preferredColumn, end-start)
		return
	}
	end := caret
	for end < len(runes) && runes[end] != '\n' {
		end++
	}
	if end == len(runes) {
		return
	}
	start = end + 1
	end = start
	for end < len(runes) && runes[end] != '\n' {
		end++
	}
	f.caret = start + min(f.preferredColumn, end-start)
}

func (f *consoleForm) caretPosition() int {
	length := len([]rune(f.values[f.cursor]))
	if f.caret < 0 || f.caret > length {
		return length
	}
	return f.caret
}

func (f *consoleForm) value(name string) string {
	for index, field := range f.action.Fields {
		if field.Name == name {
			return f.values[index]
		}
	}
	return ""
}

func (f *consoleForm) prepareNotePath() {
	if f.action.ID != "note-create" || strings.TrimSpace(f.value("path")) != "" {
		return
	}
	slug := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return '-'
	}, strings.TrimSpace(f.value("title")))
	slug = strings.Trim(slug, "-")
	if len([]rune(slug)) > 40 {
		slug = string([]rune(slug)[:40])
	}
	if slug == "" {
		slug = "note"
	}
	for index, field := range f.action.Fields {
		if field.Name == "path" {
			f.values[index] = "notes/" + slug + "-" + f.pathSeed + ".md"
			return
		}
	}
}

func noteDocument(title, body string) string {
	title = strings.TrimSpace(sanitizeTerminalText(title, false))
	first, rest, hasNewline := strings.Cut(body, "\n")
	hashes := len(first) - len(strings.TrimLeft(first, "#"))
	if hashes > 0 && hashes <= 6 && len(first) > hashes && first[hashes] == ' ' {
		if hasNewline {
			return "# " + title + "\n" + rest
		}
		return "# " + title
	}
	return "# " + title + "\n\n" + body
}

func workspaceFieldLabel(field Field) string {
	labels := map[string]string{"task_description": "Question", "source_file": "Document path", "rights_basis": "Rights basis", "expected_revision": "Saved revision", "note_id": "Note ID", "top_k": "Result limit", "token_budget": "Context budget", "graph_mode": "Graph routing", "properties": "Properties", "path": "Note path", "idempotency_key": "Retry identity"}
	if label := labels[field.Name]; label != "" {
		return label
	}
	label := field.Label
	if label != "" {
		runes := []rune(label)
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}
	return field.Name
}

func fieldPlaceholder(field Field) string {
	switch field.Name {
	case "title":
		return "Give this a useful title"
	case "body":
		return "Write your note · Ctrl-J new line · Ctrl-E editor"
	case "task_description":
		return "What would you like to know?"
	case "source_file":
		return "/path/to/document.pdf, .epub, .md or .txt"
	case "path":
		return "Generated when you review the new note"
	default:
		return "Enter " + strings.ToLower(workspaceFieldLabel(field))
	}
}

func (m Model) renderWorkspaceForm(width, rows int) []string {
	f := m.console.form
	indices := f.visibleFields()
	mode := "Essentials · Ctrl-O Advanced options"
	if f.advanced {
		mode = "All options · Ctrl-O Essentials"
	}
	lines := []string{mutedStyle.Render(mode), ""}
	position := 0
	for i, index := range indices {
		if index == f.cursor {
			position = i
		}
	}
	start := max(0, position-max(1, (rows-4)/3)+1)
	for _, index := range indices[start:] {
		field := f.action.Fields[index]
		label := workspaceFieldLabel(field)
		if field.Required {
			label += " *"
		}
		value := safeFormValue(field, f.values[index])
		if field.Kind == "multiline" {
			value = sanitizeTerminalText(f.values[index], true)
		}
		focused := index == f.cursor
		if focused && field.Kind != "secret" && !isSecretField(field.Name) && field.Kind != "json" {
			runes := []rune(value)
			caret := f.caretPosition()
			if caret > len(runes) {
				caret = len(runes)
			}
			value = string(runes[:caret]) + "▏" + string(runes[caret:])
		}
		if f.values[index] == "" {
			value = fieldPlaceholder(field)
			if focused {
				value = "▏ " + value
			}
		}
		if focused {
			label = accentStyle.Render("› " + label)
		} else {
			label = "  " + label
		}
		chunk := []string{label}
		valueLines := strings.Split(ansi.Wordwrap(value, max(1, width-4), " "), "\n")
		limit := 1
		if focused && (field.Kind == "multiline" || field.Kind == "json") {
			limit = min(5, max(1, rows-len(lines)-3))
		}
		startLine := 0
		if focused {
			for i, line := range valueLines {
				if strings.Contains(line, "▏") && i >= limit {
					startLine = i - limit + 1
					break
				}
			}
		}
		for _, line := range valueLines[startLine:min(len(valueLines), startLine+limit)] {
			line = "  " + line
			if focused {
				line = activeStyle.Render(ansi.Truncate(line, width, "…"))
			}
			chunk = append(chunk, line)
		}
		if len(lines)+len(chunk) > rows && index != f.cursor {
			break
		}
		lines = append(lines, chunk...)
		if focused && len(field.Choices) > 0 {
			lines = append(lines, mutedStyle.Render("  ←/→ Choose · "+strings.Join(field.Choices, " / ")))
		}
		lines = append(lines, "")
	}
	return lines
}

func (m Model) renderPreparedReview(width, rows int) []string {
	lines := []string{errorStyle.Render("Confirm " + m.console.form.action.Label + " in " + m.workspace + "?"), mutedStyle.Render("Review the prepared change. Nothing is written until you confirm."), ""}
	for index, field := range m.console.form.action.Fields {
		value := m.console.form.values[index]
		if prepared, ok := m.console.pending.Values[field.Name]; ok {
			value = fmt.Sprint(prepared)
			if field.Kind == "json" {
				encoded, _ := json.Marshal(prepared)
				value = string(encoded)
			}
		}
		if field.Kind == "multiline" && !isSecretField(field.Name) {
			lines = append(lines, workspaceFieldLabel(field)+":")
			lines = append(lines, strings.Split(ansi.Wordwrap(sanitizeTerminalText(value, true), width, " "), "\n")...)
		} else {
			lines = append(lines, workspaceFieldLabel(field)+": "+safeFormValue(field, value))
		}
	}
	return scrollConsoleLines(lines, m.console.scroll, rows)
}
