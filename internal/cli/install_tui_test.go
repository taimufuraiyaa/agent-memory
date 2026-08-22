package cli

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestInstallTUIRendersFullScreenHierarchyAndActiveRow(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	model = updated.(installSelectionModel)
	if model.width != 100 || model.height != 28 {
		t.Fatalf("viewport=%dx%d", model.width, model.height)
	}

	rendered := model.View().Content
	plain := ansi.Strip(rendered)
	for _, expected := range []string{
		"AGENT MEMORY", "LOCAL SETUP", "STEP 1 OF 3", "SELECTED 6/6",
		"›  [x]  Agent Memory core", "↑/↓ Navigate", "Space Toggle", "Enter Continue", "Q/Esc Quit",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("full-screen view missing %q:\n%s", expected, plain)
		}
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("active view has no terminal styling:\n%s", rendered)
	}
}

func TestInstallTUICompactViewportKeepsPrimaryControlsAndModels(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 54, Height: 18})
	model = updated.(installSelectionModel)
	componentView := ansi.Strip(model.View().Content)
	if strings.Contains(componentView, "CLI binary and private data directories") {
		t.Fatalf("compact view retained secondary descriptions:\n%s", componentView)
	}
	for _, expected := range []string{"Agent Memory core", "0–9.3 GB", "↑/↓", "Space", "Enter", "Quit"} {
		if !strings.Contains(componentView, expected) {
			t.Fatalf("compact component view missing %q:\n%s", expected, componentView)
		}
	}
	for _, line := range strings.Split(model.View().Content, "\n") {
		if width := ansi.StringWidth(line); width > 54 {
			t.Fatalf("compact line width=%d exceeds viewport:\n%s", width, ansi.Strip(line))
		}
	}

	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	modelView := ansi.Strip(model.View().Content)
	for _, exactModel := range []string{"qwen3:4b", "qwen3:8b", "qwen3:14b"} {
		if !strings.Contains(modelView, exactModel) {
			t.Fatalf("compact model view missing %q:\n%s", exactModel, modelView)
		}
	}
}

func TestInstallTUIShortViewportDropsDescriptionsBeforeFooter(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	model = updated.(installSelectionModel)
	plain := ansi.Strip(model.View().Content)
	if strings.Contains(plain, "CLI binary and private data directories") {
		t.Fatalf("short viewport retained descriptions:\n%s", plain)
	}
	if !strings.Contains(plain, "Q/Esc Quit") {
		t.Fatalf("short viewport lost footer:\n%s", plain)
	}
}

func TestInstallSelectionLocksCoreAndTogglesOptionalComponents(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: ' '}))
	model = updated.(installSelectionModel)
	if !model.components[0].Selected || !model.components[0].Required {
		t.Fatalf("required core was toggled: %+v", model.components[0])
	}

	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	model = updated.(installSelectionModel)
	if model.cursor != 1 {
		t.Fatalf("cursor=%d", model.cursor)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: ' '}))
	model = updated.(installSelectionModel)
	if model.components[1].Selected {
		t.Fatalf("optional component remained selected: %+v", model.components[1])
	}
}

func TestInstallSelectionConfirmsOrCancelsWithoutMutation(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, command := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	choosingModel := updated.(installSelectionModel)
	if choosingModel.phase != installPhaseModel || choosingModel.confirmed || command != nil {
		t.Fatalf("model selection=%+v command=%v", choosingModel, command)
	}
	updated, command = choosingModel.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	reviewing := updated.(installSelectionModel)
	if reviewing.phase != installPhaseReview || reviewing.confirmed || command != nil {
		t.Fatalf("reviewing model=%+v command=%v", reviewing, command)
	}
	updated, command = reviewing.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	confirmed := updated.(installSelectionModel)
	if !confirmed.confirmed || confirmed.cancelled || command == nil {
		t.Fatalf("confirmed model=%+v command=%v", confirmed, command)
	}

	model = newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, command = model.Update(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	cancelled := updated.(installSelectionModel)
	if !cancelled.cancelled || cancelled.confirmed || command == nil {
		t.Fatalf("cancelled model=%+v command=%v", cancelled, command)
	}
}

func TestInstallLLMModelCatalogHasSingleRecommendedChoiceAndCosts(t *testing.T) {
	options := defaultLocalLLMOptions(map[string]bool{"qwen3:4b": true})
	if len(options) != 4 {
		t.Fatalf("options=%+v", options)
	}
	wantIDs := []string{"", "qwen3:4b", "qwen3:8b", "qwen3:14b"}
	recommended := 0
	for index, option := range options {
		if option.Model != wantIDs[index] {
			t.Fatalf("option %d model=%q want=%q", index, option.Model, wantIDs[index])
		}
		if option.Recommended {
			recommended++
		}
	}
	if recommended != 1 || !options[1].Installed || options[2].Disk != "5.2 GB" || options[3].Disk != "9.3 GB" {
		t.Fatalf("catalog=%+v", options)
	}
}

func TestInstallLLMModelSelectionIsRadioStyleAndPropagatesExactModel(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	if model.phase != installPhaseModel || model.modelCursor != 2 || model.selectedModel != "qwen3:8b" {
		t.Fatalf("default model selection=%+v", model)
	}

	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	model = updated.(installSelectionModel)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: ' '}))
	model = updated.(installSelectionModel)
	if model.selectedModel != "qwen3:14b" {
		t.Fatalf("selected model=%q", model.selectedModel)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	selection, err := model.selection()
	if err != nil {
		t.Fatal(err)
	}
	if selection.PlannerModel != "qwen3:14b" || !strings.Contains(model.View().Content, "qwen3:14b") {
		t.Fatalf("selection=%+v view=%s", selection, model.View().Content)
	}
}

func TestInstallLLMNoneDisablesPlannerAndBackPreservesComponents(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	model = updated.(installSelectionModel)
	if model.phase != installPhaseComponents || !model.components[installComponentPlannerIndex].Selected {
		t.Fatalf("back did not preserve components: %+v", model)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	for model.modelCursor > 0 {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
		model = updated.(installSelectionModel)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: ' '}))
	model = updated.(installSelectionModel)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	selection, err := model.selection()
	if err != nil {
		t.Fatal(err)
	}
	if selection.InstallPlanner || selection.PlannerModel != "" {
		t.Fatalf("parser-only selection=%+v", selection)
	}
}

func TestInstallSelectionExplainsKeysDiskCostAndInstalledState(t *testing.T) {
	model := newInstallSelectionModelWithOptions(
		defaultInstallComponents(installDetection{Ollama: true, QwenPlanner: true}),
		defaultLocalLLMOptions(map[string]bool{"qwen3:8b": true}),
	)
	componentView := model.View().Content
	for _, expected := range []string{"Space", "Enter", "installed", "required"} {
		if !strings.Contains(componentView, expected) {
			t.Fatalf("component view missing %q:\n%s", expected, componentView)
		}
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	modelView := model.View().Content
	for _, expected := range []string{"5.2 GB", "qwen3:8b", "recommended", "installed", "Space", "Enter"} {
		if !strings.Contains(modelView, expected) {
			t.Fatalf("model view missing %q:\n%s", expected, modelView)
		}
	}
}

func TestInstallTUIComponentFilterNarrowsNavigatesAndTogglesSourceOption(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	for _, key := range []rune{'/', 'p', 'l', 'a', 'n', 'n', 'e', 'r'} {
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: string(key), Code: key}))
		model = updated.(installSelectionModel)
	}
	plain := ansi.Strip(model.View().Content)
	if !strings.Contains(plain, "/planner") || !strings.Contains(plain, "Local LLM planner") || strings.Contains(plain, "ONNX Runtime") {
		t.Fatalf("filtered component view:\n%s", plain)
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	if model.phase != installPhaseComponents {
		t.Fatalf("enter while filtering changed phase: %+v", model)
	}
	if plain = ansi.Strip(model.View().Content); !strings.Contains(plain, "FILTER /planner") {
		t.Fatalf("enter did not retain filter:\n%s", plain)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: ' '}))
	model = updated.(installSelectionModel)
	if model.components[installComponentPlannerIndex].Selected {
		t.Fatalf("space did not toggle filtered source option: %+v", model.components[installComponentPlannerIndex])
	}
}

func TestInstallTUIFilterNavigatesAcrossVisibleMatches(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	for _, key := range []rune{'/', 'l', 'o', 'c', 'a', 'l'} {
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: string(key), Code: key}))
		model = updated.(installSelectionModel)
	}
	if model.cursor != 1 {
		t.Fatalf("initial matching cursor=%d want=1", model.cursor)
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	model = updated.(installSelectionModel)
	if model.cursor != 2 {
		t.Fatalf("next matching cursor=%d want=2", model.cursor)
	}
}

func TestInstallTUIFilterSupportsBackspaceEscapeAndNoMatches(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	for _, key := range []rune{'/', 'z', 'z'} {
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: string(key), Code: key}))
		model = updated.(installSelectionModel)
	}
	if plain := ansi.Strip(model.View().Content); !strings.Contains(plain, "No options match") {
		t.Fatalf("missing empty-result message:\n%s", plain)
	}
	updated, command := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	model = updated.(installSelectionModel)
	if command != nil || model.cancelled {
		t.Fatalf("no-match navigation was not a no-op: model=%+v command=%v", model, command)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	if model.phase != installPhaseComponents {
		t.Fatalf("no-match enter changed phase: %+v", model)
	}
	model.filtering = true
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	model = updated.(installSelectionModel)
	if model.filterQuery != "z" {
		t.Fatalf("filter query after backspace=%q", model.filterQuery)
	}
	updated, command = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	model = updated.(installSelectionModel)
	if model.filtering || model.filterQuery != "" || model.cancelled || command != nil {
		t.Fatalf("escape did not only clear filter: model=%+v command=%v", model, command)
	}

	for _, key := range []rune{'/', 'c', 'o', 'r', 'e'} {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: string(key), Code: key}))
		model = updated.(installSelectionModel)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	updated, command = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	model = updated.(installSelectionModel)
	if model.filterQuery != "" || model.cancelled || command != nil {
		t.Fatalf("escape did not clear retained filter: model=%+v command=%v", model, command)
	}
}

func TestInstallTUIModelFilterMatchesMetadataAndSelectsVisibleModel(t *testing.T) {
	model := newInstallSelectionModel(defaultInstallComponents(installDetection{}))
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	for _, key := range []rune{'/', 'h', 'i', 'g', 'h', 'e', 'r'} {
		updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: string(key), Code: key}))
		model = updated.(installSelectionModel)
	}
	plain := ansi.Strip(model.View().Content)
	if !strings.Contains(plain, "Qwen3 14B") || strings.Contains(plain, "Qwen3 8B") {
		t.Fatalf("filtered model view:\n%s", plain)
	}
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(installSelectionModel)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Code: ' '}))
	model = updated.(installSelectionModel)
	if model.selectedModel != "qwen3:14b" {
		t.Fatalf("selected filtered model=%q", model.selectedModel)
	}
}

func TestInstallSelectionMapsComponentsToLegacyFlags(t *testing.T) {
	components := defaultInstallComponents(installDetection{})
	for index := range components {
		components[index].Selected = components[index].ID == installComponentCore || components[index].ID == installComponentPlanner
	}
	selection, err := resolveInstallSelection(components, "qwen3:4b")
	if err != nil {
		t.Fatal(err)
	}
	if !selection.InstallPlanner || selection.PlannerModel != "qwen3:4b" || !selection.SkipONNXRuntime || !selection.SkipModel || !selection.NoDashboard || !selection.NoInit {
		t.Fatalf("selection=%+v", selection)
	}
}

func TestInstallTUIOpensOnlyForImplicitInteractiveExecution(t *testing.T) {
	if !shouldOpenInstallTUI(false, false, true, true) {
		t.Fatal("implicit terminal install did not open the TUI")
	}
	for _, input := range []struct {
		noTUI, explicit, stdinTTY, outputTTY bool
	}{
		{noTUI: true, stdinTTY: true, outputTTY: true},
		{explicit: true, stdinTTY: true, outputTTY: true},
		{stdinTTY: false, outputTTY: true},
		{stdinTTY: true, outputTTY: false},
	} {
		if shouldOpenInstallTUI(input.noTUI, input.explicit, input.stdinTTY, input.outputTTY) {
			t.Fatalf("unexpected TUI for %+v", input)
		}
	}
}
