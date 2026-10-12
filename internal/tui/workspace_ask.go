package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func askRecords(data any, key string) []map[string]any {
	root, _ := data.(map[string]any)
	values, _ := root[key].([]any)
	items := []map[string]any{}
	for _, value := range values {
		record, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if memory, ok := record["memory"].(map[string]any); ok {
			record = memory
		}
		items = append(items, record)
	}
	return items
}

func groundedAskRecords(data any) []map[string]any {
	items := askRecords(data, "memories_included_full")
	if len(items) == 0 {
		items = askRecords(data, "memories_used")
	}
	return items
}

func askResultLines(data any, width int) []string {
	root, _ := data.(map[string]any)
	if root == nil {
		return []string{"Ask a question to retrieve grounded workspace context."}
	}
	memories := groundedAskRecords(data)
	sources := askRecords(data, "source_evidence")
	weak := askRecords(data, "weak_memories")
	lines := []string{}
	if len(memories) == 0 && len(sources) == 0 {
		lines = append(lines, errorStyle.Render("No grounded memory matched this question."), "Search these words, add a source, or try a more specific question.", "s Search this question · i Import source · Enter Ask again")
	} else {
		lines = append(lines, accentStyle.Render("Grounded context"), "")
		if context, _ := root["context_block"].(string); strings.TrimSpace(context) != "" {
			lines = append(lines, strings.Split(ansi.Wordwrap(sanitizeTerminalText(context, true), max(1, width), " "), "\n")...)
		}
	}
	for _, section := range []struct {
		name  string
		items []map[string]any
	}{{"Source evidence", sources}, {"Durable memory", memories}, {"Weak context · verify before using", weak}} {
		if len(section.items) == 0 {
			continue
		}
		lines = append(lines, "", accentStyle.Render(fmt.Sprintf("%s (%d)", section.name, len(section.items))))
		for index, item := range section.items {
			content, _ := item["content"].(string)
			if content == "" {
				content, _ = item["body"].(string)
			}
			lines = append(lines, fmt.Sprintf("[%d] %s", index+1, sanitizeTerminalText(fmt.Sprint(item["type"]), false)))
			preview := strings.Split(ansi.Wordwrap(sanitizeTerminalText(content, true), max(1, width), " "), "\n")
			lines = append(lines, preview[:min(5, len(preview))]...)
			if len(preview) > 5 {
				lines = append(lines, mutedStyle.Render("… v opens the full durable memories · d shows advanced details"))
			}
			if source, ok := item["source"].(map[string]any); ok {
				if path, ok := source["file_path"].(string); ok && path != "" {
					lines = append(lines, mutedStyle.Render("Source: "+sanitizeTerminalText(path, false)))
				}
			}
		}
	}
	if graph, ok := root["graph_route"].(map[string]any); ok {
		lines = append(lines, "", accentStyle.Render("Graph routing"))
		for _, key := range []string{"selected_mode", "reason_code", "fallback", "degraded"} {
			if value := graph[key]; value != nil {
				lines = append(lines, strings.ReplaceAll(key, "_", " ")+": "+sanitizeTerminalText(fmt.Sprint(value), false))
			}
		}
	}
	if graph := root["graph_context"]; graph != nil {
		lines = append(lines, "", accentStyle.Render("Graph context · derived relationships"))
		lines = append(lines, documentLines(redactConsoleSecrets(graph), width, 0)...)
	}
	return lines
}

func (m Model) renderAskPane(width, rows int) []string {
	editing := m.console.form != nil && m.console.form.action.ID == "ask"
	composerRows := 4
	if editing {
		composerRows = min(8, max(5, rows/3))
	}
	resultRows := max(1, rows-composerRows)
	result := []string{}
	if m.ux.askData != nil {
		question := strings.Split(ansi.Wordwrap(sanitizeTerminalText(m.ux.askQuestion, true), max(1, width-4), " "), "\n")
		if rows < 16 {
			result = append(result, accentStyle.Render("You · ")+ansi.Truncate(sanitizeTerminalText(m.ux.askQuestion, false), max(1, width-6), "…"), accentStyle.Render("Agent Memory · recall"))
		} else {
			result = append(result, workspacePanel("You", question, width, false)...)
			result = append(result, "", accentStyle.Render("Agent Memory · recall"), "")
		}
		if m.console.inspect {
			result = append(result, documentLines(redactConsoleSecrets(m.ux.askData), width, 0)...)
		} else {
			result = append(result, askResultLines(m.ux.askData, width)...)
		}
		result = scrollConsoleLines(result, m.console.scroll, resultRows)
	} else {
		result = []string{accentStyle.Render("Ask your workspace"), "Find a fact, recall a decision, or learn how a task was done.", "", "Retrieved memories and weak context stay clearly labeled.", mutedStyle.Render("Questions and results are saved in private local history.")}
	}
	if !editing {
		result = fitWorkspaceLines(result, width, resultRows)
		return append(result, workspacePanel("Ask again", []string{mutedStyle.Render("Enter opens the question composer"), mutedStyle.Render("↑↓ Read · → Workspace · h History")}, width, false)...)
	}
	if m.console.form.advanced {
		return m.renderWorkspaceForm(width, rows)
	}
	result = fitWorkspaceLines(result, width, resultRows)
	value := m.console.form.value("task_description")
	if value == "" {
		value = "▏ What would you like to know?"
	} else {
		runes := []rune(value)
		caret := m.console.form.caretPosition()
		value = string(runes[:caret]) + "▏" + string(runes[caret:])
	}
	wrapped := strings.Split(ansi.Wordwrap(sanitizeTerminalText(value, true), max(1, width-4), " "), "\n")
	visible := max(1, composerRows-3)
	start := 0
	for index, line := range wrapped {
		if strings.Contains(line, "▏") {
			start = max(0, index-visible+1)
			break
		}
	}
	composer := fitWorkspaceLines(wrapped[start:min(len(wrapped), start+visible)], width-4, visible)
	mode := m.console.form.value("graph_mode")
	composer = append(composer, mutedStyle.Render("Ask · Graph "+mode+" · "+sanitizeTerminalText(m.workspace, false)))
	return append(result, workspacePanel("Question *", composer, width, true)...)
}
