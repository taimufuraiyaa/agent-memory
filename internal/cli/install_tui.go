package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func shouldOpenInstallTUI(noTUI, explicitSelection, inputTerminal, outputTerminal bool) bool {
	return !noTUI && !explicitSelection && inputTerminal && outputTerminal
}

func runInstallSelectionTUI(ctx context.Context, input io.Reader, output io.Writer, components []installComponent, models []localLLMOption) (installSelection, bool, error) {
	program := tea.NewProgram(newInstallSelectionModelWithOptions(components, models), tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output))
	final, err := program.Run()
	if err != nil {
		return installSelection{}, false, err
	}
	model, ok := final.(installSelectionModel)
	if !ok {
		return installSelection{}, false, errors.New("installer returned an unexpected terminal model")
	}
	if model.cancelled || !model.confirmed {
		return installSelection{}, true, nil
	}
	selection, err := model.selection()
	return selection, false, err
}

type installComponentID string

const (
	installComponentCore         installComponentID = "core"
	installComponentONNX         installComponentID = "onnx-runtime"
	installComponentMiniLM       installComponentID = "minilm"
	installComponentDashboard    installComponentID = "dashboard"
	installComponentPlanner      installComponentID = "qwen-planner"
	installComponentWorkspace    installComponentID = "workspace-rules"
	installComponentPlannerIndex                    = 4
)

type installDetection struct {
	ONNXRuntime   bool
	MiniLM        bool
	Dashboard     bool
	Ollama        bool
	QwenPlanner   bool
	PlannerModels map[string]bool
}

type installComponent struct {
	ID          installComponentID
	Label       string
	Description string
	Disk        string
	Required    bool
	Selected    bool
	Installed   bool
}

type installSelection struct {
	SkipONNXRuntime bool
	SkipModel       bool
	NoDashboard     bool
	InstallPlanner  bool
	PlannerModel    string
	NoInit          bool
}

type localLLMOption struct {
	Model       string
	Label       string
	Description string
	Disk        string
	Recommended bool
	Installed   bool
}

func defaultLocalLLMOptions(installed map[string]bool) []localLLMOption {
	return []localLLMOption{
		{Label: "None — parser only", Description: "No generative query planning", Disk: "0 GB"},
		{Model: "qwen3:4b", Label: "Qwen3 4B", Description: "Faster and lower-memory", Disk: "2.5 GB", Installed: installed["qwen3:4b"]},
		{Model: "qwen3:8b", Label: "Qwen3 8B", Description: "Balanced multilingual planning", Disk: "5.2 GB", Recommended: true, Installed: installed["qwen3:8b"]},
		{Model: "qwen3:14b", Label: "Qwen3 14B", Description: "Higher quality and resource use", Disk: "9.3 GB", Installed: installed["qwen3:14b"]},
	}
}

func supportedLocalLLMModel(model string) bool {
	for _, option := range defaultLocalLLMOptions(nil) {
		if option.Model != "" && option.Model == model {
			return true
		}
	}
	return false
}

func localLLMModelIDs() []string {
	options := defaultLocalLLMOptions(nil)
	models := make([]string, 0, len(options)-1)
	for _, option := range options {
		if option.Model != "" {
			models = append(models, option.Model)
		}
	}
	return models
}

func anyLocalLLMInstalled(installed map[string]bool) bool {
	for _, ready := range installed {
		if ready {
			return true
		}
	}
	return false
}

func defaultInstallComponents(detected installDetection) []installComponent {
	return []installComponent{
		{ID: installComponentCore, Label: "Agent Memory core", Description: "CLI binary and private data directories", Disk: "~50 MB", Required: true, Selected: true, Installed: true},
		{ID: installComponentONNX, Label: "ONNX Runtime", Description: "Local embedding execution runtime", Disk: "~25 MB", Selected: true, Installed: detected.ONNXRuntime},
		{ID: installComponentMiniLM, Label: "MiniLM embeddings", Description: "Current 384-dimension local search projection", Disk: "~90 MB", Selected: true, Installed: detected.MiniLM},
		{ID: installComponentDashboard, Label: "Unified dashboard", Description: "Human Library, Memory, Data, and Settings UI", Disk: "embedded", Selected: true, Installed: detected.Dashboard},
		{ID: installComponentPlanner, Label: "Local LLM planner", Description: "Choose None, Qwen3 4B, 8B, or 14B next", Disk: "0–9.3 GB", Selected: true, Installed: detected.Ollama && (detected.QwenPlanner || anyLocalLLMInstalled(detected.PlannerModels))},
		{ID: installComponentWorkspace, Label: "Workspace agent rules", Description: "Connect the current project to Agent Memory", Disk: "<1 MB", Selected: true},
	}
}

func resolveInstallSelection(components []installComponent, plannerModel string) (installSelection, error) {
	selected := map[installComponentID]bool{}
	for _, component := range components {
		if component.Required && !component.Selected {
			return installSelection{}, fmt.Errorf("required install component %q is not selected", component.ID)
		}
		selected[component.ID] = component.Selected
	}
	if !selected[installComponentCore] {
		return installSelection{}, errors.New("required install component core is missing")
	}
	if plannerModel != "" && !supportedLocalLLMModel(plannerModel) {
		return installSelection{}, fmt.Errorf("unsupported local LLM model %q", plannerModel)
	}
	installPlanner := selected[installComponentPlanner] && plannerModel != ""
	if !installPlanner {
		plannerModel = ""
	}
	return installSelection{
		SkipONNXRuntime: !selected[installComponentONNX],
		SkipModel:       !selected[installComponentMiniLM],
		NoDashboard:     !selected[installComponentDashboard],
		InstallPlanner:  installPlanner,
		PlannerModel:    plannerModel,
		NoInit:          !selected[installComponentWorkspace],
	}, nil
}

type installTUIPhase int

const (
	installPhaseComponents installTUIPhase = iota
	installPhaseModel
	installPhaseReview
)

type installSelectionModel struct {
	components    []installComponent
	models        []localLLMOption
	cursor        int
	modelCursor   int
	selectedModel string
	phase         installTUIPhase
	confirmed     bool
	cancelled     bool
	width         int
	height        int
}

func newInstallSelectionModel(components []installComponent) installSelectionModel {
	return newInstallSelectionModelWithOptions(components, defaultLocalLLMOptions(nil))
}

func newInstallSelectionModelWithOptions(components []installComponent, models []localLLMOption) installSelectionModel {
	copyOfComponents := append([]installComponent(nil), components...)
	copyOfModels := append([]localLLMOption(nil), models...)
	modelCursor := 0
	selectedModel := ""
	for index, option := range copyOfModels {
		if option.Recommended {
			modelCursor = index
			selectedModel = option.Model
			break
		}
	}
	return installSelectionModel{components: copyOfComponents, models: copyOfModels, modelCursor: modelCursor, selectedModel: selectedModel, phase: installPhaseComponents}
}

func (m installSelectionModel) selection() (installSelection, error) {
	return resolveInstallSelection(m.components, m.selectedModel)
}

func (m installSelectionModel) plannerSelected() bool {
	for _, component := range m.components {
		if component.ID == installComponentPlanner {
			return component.Selected
		}
	}
	return false
}

func (installSelectionModel) Init() tea.Cmd { return nil }

func (m installSelectionModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		return m, nil
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	pressed := key.String()
	if m.phase == installPhaseReview {
		switch pressed {
		case "enter":
			m.confirmed = true
			return m, tea.Quit
		case "esc", "b", "backspace":
			if m.plannerSelected() {
				m.phase = installPhaseModel
			} else {
				m.phase = installPhaseComponents
			}
			return m, nil
		case "q", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		default:
			return m, nil
		}
	}
	if m.phase == installPhaseModel {
		switch pressed {
		case "up", "k":
			if m.modelCursor > 0 {
				m.modelCursor--
			}
		case "down", "j":
			if m.modelCursor < len(m.models)-1 {
				m.modelCursor++
			}
		case " ", "space":
			m.selectedModel = m.models[m.modelCursor].Model
		case "enter":
			m.selectedModel = m.models[m.modelCursor].Model
			m.phase = installPhaseReview
		case "esc", "b", "backspace":
			m.phase = installPhaseComponents
		case "q", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
		return m, nil
	}
	switch pressed {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.components)-1 {
			m.cursor++
		}
	case " ", "space":
		if !m.components[m.cursor].Required {
			m.components[m.cursor].Selected = !m.components[m.cursor].Selected
		}
	case "enter":
		if m.plannerSelected() {
			m.phase = installPhaseModel
		} else {
			m.phase = installPhaseReview
		}
	case "esc", "q", "ctrl+c":
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m installSelectionModel) View() tea.View {
	width, height := m.viewport()
	compact := width < 72 || height < 24
	var body []string
	var summary, footer string
	switch m.phase {
	case installPhaseModel:
		body = m.renderModelRows(width, compact)
		summary = fmt.Sprintf("CHOICES %d  │  SELECTED %s", len(m.models), displayModel(m.selectedModel))
		footer = "↑/↓ Navigate  │  Space Select  │  Enter Continue  │  Esc Back  │  Q Quit"
	case installPhaseReview:
		body = m.renderReviewRows(width)
		summary = fmt.Sprintf("READY TO INSTALL  │  %d ITEMS SELECTED", m.selectedComponentCount())
		footer = "Enter Install  │  B/Esc Back  │  Q Quit"
	default:
		body = m.renderComponentRows(width, compact)
		summary = fmt.Sprintf("SELECTED %d/%d  │  INSTALLED %d", m.selectedComponentCount(), len(m.components), m.installedComponentCount())
		footer = "↑/↓ Navigate  │  Space Toggle  │  Enter Continue  │  Q/Esc Quit"
	}

	content := m.renderFrame(width, height, summary, body, footer)
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "Agent Memory installer"
	return view
}

var (
	installerAccentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#C9A7FF")).Bold(true)
	installerTextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAF2F0"))
	installerMutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7F8B8D"))
	installerReadyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#9DD9B5"))
	installerFocusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#102326")).Background(lipgloss.Color("#DDF5F1")).Bold(true)
)

func (m installSelectionModel) viewport() (int, int) {
	width, height := m.width, m.height
	if width <= 0 {
		width = 88
	}
	if height <= 0 {
		height = 24
	}
	if width < 42 {
		width = 42
	}
	if height < 14 {
		height = 14
	}
	return width, height
}

func (m installSelectionModel) renderFrame(width, height int, summary string, body []string, footer string) string {
	inner := width - 4
	step := "STEP 1 OF 3"
	if m.phase == installPhaseModel {
		step = "STEP 2 OF 3"
	} else if m.phase == installPhaseReview {
		step = "STEP 3 OF 3"
	}
	left := "◆  AGENT MEMORY  /  LOCAL SETUP"
	headerGap := inner - lipgloss.Width(left) - lipgloss.Width(step)
	if headerGap < 1 {
		headerGap = 1
	}
	header := installerAccentStyle.Render(left) + strings.Repeat(" ", headerGap) + installerMutedStyle.Render(step)
	divider := installerMutedStyle.Render(strings.Repeat("─", inner))
	lines := []string{"", "  " + header, "  " + divider, "  " + installerTextStyle.Bold(true).Render(summary), ""}
	lines = append(lines, body...)
	footerLines := renderShortcutFooterLines(footer, inner)
	reservedFooterLines := len(footerLines) + 2
	for len(lines) < height-reservedFooterLines {
		lines = append(lines, "")
	}
	lines = append(lines, "  "+divider)
	for _, footerLine := range footerLines {
		lines = append(lines, "  "+footerLine)
	}
	lines = append(lines, "")
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (m installSelectionModel) renderComponentRows(width int, compact bool) []string {
	inner := width - 4
	rows := make([]string, 0, len(m.components)*2)
	for index, component := range m.components {
		cursor := " "
		if index == m.cursor {
			cursor = "›"
		}
		mark := "[ ]"
		if component.Selected {
			mark = "[x]"
		}
		state := ""
		if component.Required {
			state = "required"
		} else if component.Installed {
			state = "installed"
		}
		row := fmt.Sprintf("%s  %s  %-26s %-11s %s", cursor, mark, component.Label, component.Disk, state)
		rows = append(rows, renderInstallerRow(row, index == m.cursor, inner))
		if !compact {
			rows = append(rows, "       "+installerMutedStyle.Render(component.Description))
		}
	}
	return rows
}

func (m installSelectionModel) renderModelRows(width int, compact bool) []string {
	inner := width - 4
	rows := make([]string, 0, len(m.models)*2)
	for index, option := range m.models {
		cursor := " "
		if index == m.modelCursor {
			cursor = "›"
		}
		mark := "( )"
		if option.Model == m.selectedModel {
			mark = "(●)"
		}
		state := ""
		if option.Recommended {
			state = "recommended"
		}
		if option.Installed {
			if state != "" {
				state += ", "
			}
			state += "installed"
		}
		row := fmt.Sprintf("%s  %s  %-18s %-12s %-9s %s", cursor, mark, option.Label, displayModel(option.Model), option.Disk, state)
		rows = append(rows, renderInstallerRow(row, index == m.modelCursor, inner))
		if !compact {
			rows = append(rows, "       "+installerMutedStyle.Render(option.Description))
		}
	}
	return rows
}

func (m installSelectionModel) renderReviewRows(width int) []string {
	inner := width - 4
	rows := []string{installerAccentStyle.Render("  REVIEW INSTALLATION PLAN"), ""}
	for _, component := range m.components {
		if !component.Selected {
			continue
		}
		label, detail := component.Label, component.Disk
		if component.ID == installComponentPlanner {
			if m.selectedModel == "" {
				label, detail = "Parser only", "no local LLM"
			} else {
				label, detail = "Local LLM planner", m.selectedModel
			}
		}
		row := fmt.Sprintf("   ✓  %-28s %s", label, detail)
		rows = append(rows, "  "+installerReadyStyle.Render(fitTerminalText(row, inner-2)))
	}
	rows = append(rows, "", "  "+installerMutedStyle.Render("Downloads and local changes begin only after confirmation."))
	return rows
}

func (m installSelectionModel) selectedComponentCount() int {
	count := 0
	for _, component := range m.components {
		if component.Selected {
			count++
		}
	}
	return count
}

func (m installSelectionModel) installedComponentCount() int {
	count := 0
	for _, component := range m.components {
		if component.Installed {
			count++
		}
	}
	return count
}

func renderInstallerRow(row string, focused bool, width int) string {
	row = fitTerminalText(row, width)
	row += strings.Repeat(" ", max(0, width-lipgloss.Width(row)))
	if focused {
		return "  " + installerFocusStyle.Render(row)
	}
	return "  " + installerTextStyle.Render(row)
}

func renderShortcutFooterLines(footer string, width int) []string {
	parts := strings.Split(footer, "  │  ")
	var lines []string
	var plainLine, styledLine string
	separator := "  │  "
	styledSeparator := installerMutedStyle.Render(separator)
	for _, part := range parts {
		candidate := part
		if plainLine != "" {
			candidate = plainLine + separator + part
		}
		if plainLine != "" && lipgloss.Width(candidate) > width {
			lines = append(lines, styledLine)
			plainLine, styledLine = "", ""
		}
		styledPart := styleShortcutPart(part)
		if plainLine == "" {
			plainLine, styledLine = part, styledPart
		} else {
			plainLine += separator + part
			styledLine += styledSeparator + styledPart
		}
	}
	if styledLine != "" {
		lines = append(lines, styledLine)
	}
	return lines
}

func styleShortcutPart(part string) string {
	words := strings.SplitN(part, " ", 2)
	if len(words) != 2 {
		return installerTextStyle.Render(part)
	}
	return installerMutedStyle.Render(words[0]) + " " + installerTextStyle.Bold(true).Render(words[1])
}

func displayModel(model string) string {
	if model == "" {
		return "none"
	}
	return model
}

func fitTerminalText(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	for len(runes) > 1 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
