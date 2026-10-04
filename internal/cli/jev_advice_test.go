package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

type advisoryJev struct {
	calls     int
	state     string
	questions map[string]jev.ChoiceQuestion
	answers   map[string]jev.ChoiceAnswer
	err       error
}

func (f *advisoryJev) Probe(context.Context, string) error { return nil }
func (f *advisoryJev) Choose(_ context.Context, _ string, state string, questions map[string]jev.ChoiceQuestion) (map[string]jev.ChoiceAnswer, error) {
	f.calls++
	f.state, f.questions = state, questions
	return f.answers, f.err
}

func TestJevAdvisoryUsesOnlyTaskAndProjectSkillMetadata(t *testing.T) {
	dataDir, root := t.TempDir(), t.TempDir()
	if err := jevconfig.NewTokenStore(dataDir).Save(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, ".claude", "skills", "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: review\ndescription: Review code changes\n---\nPrivate instruction body must stay local.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &advisoryJev{answers: map[string]jev.ChoiceAnswer{
		"complexity": {Choice: "deep", Confidence: 0.9, Probabilities: map[string]float64{"light": 0.01, "standard": 0.04, "deep": 0.95}},
		"skill":      {Choice: "review", Confidence: 0.9, Probabilities: map[string]float64{"none": 0.02, "review": 0.98}},
	}}
	advice := claudeJevAdvice(context.Background(), dataDir, root, "Inspect the race", fake)
	if !strings.Contains(advice, "deep") || !strings.Contains(advice, "review") || strings.Contains(fake.state, "Private instruction") || strings.Contains(advice, "Private instruction") {
		t.Fatalf("invalid advice or metadata egress: advice=%q state=%q", advice, fake.state)
	}
	if fake.questions["skill"].Criteria["review"] != "Review code changes" {
		t.Fatalf("skill metadata missing: %+v", fake.questions)
	}
}

func TestJevAdvisorySuppressesLowConfidence(t *testing.T) {
	dataDir, root := t.TempDir(), t.TempDir()
	if err := jevconfig.NewTokenStore(dataDir).Save(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
	fake := &advisoryJev{answers: map[string]jev.ChoiceAnswer{
		"complexity": {Choice: "deep", Confidence: 0.2, Probabilities: map[string]float64{"light": 0.1, "standard": 0.1, "deep": 0.8}},
	}}
	if advice := claudeJevAdvice(context.Background(), dataDir, root, "task", fake); advice != "" {
		t.Fatalf("low-confidence advice emitted: %q", advice)
	}
	if result := claudeJevDecision(context.Background(), dataDir, root, "task", fake); result.Status != "" {
		t.Fatalf("low-confidence status emitted: %q", result.Status)
	}
}

func TestJevAdvisorySuggestsOnlyClaudeCatalogModel(t *testing.T) {
	dataDir, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "model-catalog.json"), []byte(`{"version":1,"hosts":{"claude":[{"id":"claude-sonnet-5-5","description":"Balanced"},{"id":"claude-opus-5-5","description":"Complex"}],"chatgpt":[{"id":"gpt-6-luna","description":"Quick"},{"id":"gpt-6.1-sol","description":"Complex"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jevconfig.NewTokenStore(dataDir).Save(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
	fake := &advisoryJev{answers: map[string]jev.ChoiceAnswer{"model": {Choice: "model_2", Confidence: 0.95, Probabilities: map[string]float64{"model_1": 0.05, "model_2": 0.95}}}}
	advice := claudeJevAdvice(context.Background(), dataDir, root, "Analyze the architecture", fake)
	if !strings.Contains(advice, "claude-opus-5-5") || strings.Contains(advice, "gpt-6") || len(fake.questions) != 1 {
		t.Fatalf("invalid Claude model advisory: %q questions=%+v", advice, fake.questions)
	}
	result := claudeJevDecision(context.Background(), dataDir, root, "Analyze the architecture", fake)
	if result.Status != "[Jev] Recommended Claude model: claude-opus-5-5 (confidence 0.95). Model not switched." {
		t.Fatalf("invalid visible model status: %q", result.Status)
	}
}
