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

func TestJevModelChoiceUsesOnlyRequestedHostCandidates(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "model-catalog.json"), []byte(`{"version":1,"hosts":{"claude":[{"id":"claude-sonnet-5-5","description":"Balanced coding"},{"id":"claude-opus-5-5","description":"Deep work"}],"chatgpt":[{"id":"gpt-6-luna","description":"Quick tasks"},{"id":"gpt-6.1-sol","description":"Complex work"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jevconfig.NewTokenStore(dataDir).Save(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
	fake := &advisoryJev{answers: map[string]jev.ChoiceAnswer{"model": {Choice: "model_2", Confidence: 0.9, Probabilities: map[string]float64{"model_1": 0.1, "model_2": 0.9}}}}
	chosen, ok := jevModelChoice(context.Background(), dataDir, "chatgpt", "Fix this test", fake)
	if !ok || chosen != "gpt-6.1-sol" {
		t.Fatalf("unexpected model: %q %t", chosen, ok)
	}
	for _, criterion := range fake.questions["model"].Criteria {
		if strings.Contains(criterion, "claude-") {
			t.Fatalf("cross-host model leaked: %q", criterion)
		}
	}
	fake.answers["model"] = jev.ChoiceAnswer{Choice: "model_2", Confidence: 0.2, Probabilities: map[string]float64{"model_1": 0.1, "model_2": 0.9}}
	if selected, ok := jevModelChoice(context.Background(), dataDir, "claude", "Fix this test", fake); ok {
		t.Fatalf("low confidence accepted: %s", selected)
	}
}
