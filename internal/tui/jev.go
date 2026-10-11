package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type jevStatusMsg struct {
	generation int
	configured bool
	err        error
}

type jevMutationMsg struct {
	configured bool
	err        error
}

func loadJevStatusCmd(ctx context.Context, store JevCredentialStore, generation int) tea.Cmd {
	return func() tea.Msg {
		configured, err := store.Configured(ctx)
		return jevStatusMsg{generation: generation, configured: configured, err: err}
	}
}

func saveJevTokenCmd(ctx context.Context, store JevCredentialStore, token string) tea.Cmd {
	return func() tea.Msg {
		return jevMutationMsg{configured: true, err: store.Save(ctx, token)}
	}
}

func clearJevTokenCmd(ctx context.Context, store JevCredentialStore) tea.Cmd {
	return func() tea.Msg {
		return jevMutationMsg{configured: false, err: store.Clear(ctx)}
	}
}

func (m Model) updateJevKey(key tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	pressed := key.String()
	if m.jevInput {
		switch pressed {
		case "esc":
			m.jevInput = false
			m.jevDraft = ""
		case "enter":
			if m.jevDraft == "" || m.jevCredentials == nil {
				return m, nil, true
			}
			token := m.jevDraft
			m.jevDraft = ""
			m.jevInput = false
			m.jevBusy = true
			m.jevGeneration++
			m.jevError = ""
			return m, saveJevTokenCmd(m.context, m.jevCredentials, token), true
		case "backspace":
			if len(m.jevDraft) > 0 {
				runes := []rune(m.jevDraft)
				m.jevDraft = string(runes[:len(runes)-1])
			}
		default:
			if key.Text != "" && len(m.jevDraft)+len(key.Text) <= 4096 && !strings.ContainsAny(key.Text, "\r\n") {
				m.jevDraft += key.Text
			}
		}
		return m, nil, true
	}
	if m.jevConfirmClear {
		m.jevConfirmClear = false
		if pressed == "y" && m.jevCredentials != nil {
			m.jevBusy = true
			m.jevGeneration++
			m.jevError = ""
			return m, clearJevTokenCmd(m.context, m.jevCredentials), true
		}
		return m, nil, true
	}
	if m.destination != DestinationJev {
		return m, nil, false
	}
	if m.jevBusy {
		return m, nil, true
	}
	switch pressed {
	case "s":
		if m.jevCredentials != nil {
			m.jevInput = true
			m.jevDraft = ""
			m.jevNotice = ""
		}
		return m, nil, true
	case "x":
		if m.jevCredentials != nil && m.jevConfigured {
			m.jevConfirmClear = true
		}
		return m, nil, true
	}
	return m, nil, false
}
