package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type editorResultMsg struct {
	generation, cursor int
	text               string
	err                error
}

func (m Model) editConsoleField() (Model, tea.Cmd) {
	form := m.console.form
	if form == nil || len(form.values) == 0 {
		return m, nil
	}
	field := form.action.Fields[form.cursor]
	if field.Kind != "multiline" && field.Kind != "json" {
		m.console.err = "The editor is available for body and JSON fields."
		return m, nil
	}
	command := strings.TrimSpace(os.Getenv("VISUAL"))
	if command == "" {
		command = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if command == "" {
		command = "vi"
	}
	arguments := strings.Fields(command)
	generation, cursor, text, ctx := m.console.generation, form.cursor, form.values[form.cursor], m.context
	return m, func() tea.Msg {
		file, err := os.CreateTemp("", "agent-memory-edit-*.md")
		if err != nil {
			return editorResultMsg{generation: generation, cursor: cursor, err: errors.New("cannot create private editor file")}
		}
		filePath := file.Name()
		if _, err = file.WriteString(text); err != nil {
			_ = file.Close()
			_ = os.Remove(filePath)
			return editorResultMsg{generation: generation, cursor: cursor, err: errors.New("cannot write editor draft")}
		}
		if err = file.Close(); err != nil {
			_ = os.Remove(filePath)
			return editorResultMsg{generation: generation, cursor: cursor, err: errors.New("cannot close editor draft")}
		}
		args := append(append([]string(nil), arguments[1:]...), filePath)
		process := exec.CommandContext(ctx, arguments[0], args...)
		return tea.ExecProcess(process, func(err error) tea.Msg {
			defer os.Remove(filePath)
			result := editorResultMsg{generation: generation, cursor: cursor}
			if err != nil {
				result.err = errors.New("editor failed; draft unchanged")
				return result
			}
			saved, err := os.Open(filePath)
			if err != nil {
				result.err = errors.New("cannot read editor draft")
				return result
			}
			defer saved.Close()
			info, err := saved.Stat()
			if err != nil || !info.Mode().IsRegular() {
				result.err = errors.New("editor draft must be a regular file")
				return result
			}
			data, err := io.ReadAll(io.LimitReader(saved, 65537))
			if err != nil || len(data) > 65536 {
				result.err = errors.New("editor draft exceeds 64 KiB; draft unchanged")
				return result
			}
			result.text = sanitizeTerminalText(string(data), true)
			return result
		})()
	}
}
