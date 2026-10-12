package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

var errWorkspaceSelectionCancelled = errors.New("workspace selection cancelled")

type terminalWorkspacePicker struct {
	names                 []string
	cursor, width, height int
	selected              string
	context               context.Context
	loadProjects          func(context.Context) ([]workspace.ListItem, error)
	projects              map[string]workspace.ListItem
}

type terminalWorkspaceCountsMsg struct{ projects []workspace.ListItem }

func (m terminalWorkspacePicker) Init() tea.Cmd {
	if m.loadProjects == nil {
		return nil
	}
	return func() tea.Msg {
		projects, _ := m.loadProjects(m.context)
		return terminalWorkspaceCountsMsg{projects: projects}
	}
}

func (m terminalWorkspacePicker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case terminalWorkspaceCountsMsg:
		m.projects = make(map[string]workspace.ListItem, len(message.projects))
		for _, project := range message.projects {
			m.projects[project.Name] = project
		}
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
	case tea.KeyPressMsg:
		switch message.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.names) {
				m.cursor++
			}
		case "enter":
			if len(m.names) > 0 {
				m.selected = m.names[m.cursor]
				return m, tea.Quit
			}
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m terminalWorkspacePicker) View() tea.View {
	width, height := max(1, m.width), max(1, m.height)
	if height < 5 {
		view := tea.NewView(ansi.Truncate("Resize the terminal to choose a workspace", width, "…"))
		view.AltScreen = true
		return view
	}
	lines := []string{"Choose a registered workspace", "All projects · Enter opens the selected project's memories"}
	rows := max(1, height-4)
	start := max(0, m.cursor-rows+1)
	for i := start; i < len(m.names) && i < start+rows; i++ {
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		count := "checking memories…"
		if m.projects != nil {
			count = "count unavailable"
			if project, ok := m.projects[m.names[i]]; ok && project.CountStatus == "ready" {
				count = fmt.Sprintf("%d memories", project.MemoryCount)
				if project.MemoryCount == 1 {
					count = "1 memory"
				}
			}
		}
		lines = append(lines, ansi.Truncate(prefix+m.names[i]+" · "+count, width, "…"))
	}
	lines = append(lines, "", "↑/↓ choose · Enter open · Esc cancel")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}

func pickTerminalWorkspace(command *cobra.Command, names []string) (string, error) {
	for _, name := range names {
		if err := validation.ValidateWorkspaceName(name); err != nil {
			return "", errors.New("registry contains an invalid workspace name")
		}
	}
	ctx, cancel := context.WithCancel(command.Context())
	defer cancel()
	load := func(ctx context.Context) ([]workspace.ListItem, error) {
		manager, err := workspace.NewManager(defaultAgentMemoryDataDir())
		if err != nil {
			return nil, err
		}
		return manager.List(ctx)
	}
	model, err := tea.NewProgram(terminalWorkspacePicker{names: names, context: ctx, loadProjects: load, width: 80, height: 24},
		tea.WithContext(ctx), tea.WithInput(command.InOrStdin()), tea.WithOutput(command.OutOrStdout())).Run()
	if err != nil {
		return "", err
	}
	selected := model.(terminalWorkspacePicker).selected
	if selected == "" {
		return "", errWorkspaceSelectionCancelled
	}
	return selected, nil
}
