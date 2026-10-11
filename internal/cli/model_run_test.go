package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
	"github.com/taimufuraiyaa/agent-memory/internal/openaiapi"
)

type fakeOpenAIModel struct {
	probes, generations int
	model, input        string
}

func (f *fakeOpenAIModel) ProbeModel(_ context.Context, key, model string) error {
	f.probes++
	f.model = model
	return nil
}
func (f *fakeOpenAIModel) Generate(_ context.Context, key, model, input string, max int) (openaiapi.Result, error) {
	f.generations++
	f.model, f.input = model, input
	return openaiapi.Result{Model: model, Text: "Done", InputTokens: 7, OutputTokens: 2}, nil
}

func TestModelRunRequiresSeparateEgressAndUsesAPIOnlyCatalog(t *testing.T) {
	dataDir := t.TempDir()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	registry := map[string]any{"projects": []any{map[string]any{"name": "ws", "workspace_root": root, "db_path": filepath.Join(dataDir, "ws.db")}}}
	encoded, _ := json.Marshal(registry)
	if err := os.WriteFile(filepath.Join(dataDir, "workspaces.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "model-catalog.json"), []byte(`{"version":1,"hosts":{"chatgpt":[{"id":"app-light","description":"Light app"},{"id":"app-deep","description":"Deep app"}],"openai_api":[{"id":"api-light","description":"Light API"},{"id":"api-deep","description":"Deep API"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jevconfig.NewTokenStore(dataDir).Save(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
	jevFake := &advisoryJev{answers: map[string]jev.ChoiceAnswer{"model": {Choice: "model_2", Confidence: 0.9, Probabilities: map[string]float64{"model_1": 0.1, "model_2": 0.9}}}}
	apiFake := &fakeOpenAIModel{}
	oldJev, oldAPI := newJevClient, newOpenAIClient
	newJevClient = func() jevDecisionClient { return jevFake }
	newOpenAIClient = func() openAIModelClient { return apiFake }
	t.Cleanup(func() { newJevClient, newOpenAIClient = oldJev, oldAPI })
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	run := func(flags ...string) (string, error) {
		cmd := NewRootCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader("Investigate the race"))
		cmd.SetArgs(append([]string{"model-run", "--workspace", "ws", "--data-dir", dataDir}, flags...))
		err := cmd.Execute()
		return out.String(), err
	}
	if _, err := run(); err == nil || jevFake.calls != 0 || apiFake.generations != 0 {
		t.Fatalf("unapproved egress: %v", err)
	}
	if _, err := run("--allow-jev"); err == nil || jevFake.calls != 0 {
		t.Fatalf("OpenAI egress not separately gated: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := run("--allow-jev", "--allow-openai-api"); err == nil || jevFake.calls != 0 || apiFake.generations != 0 {
		t.Fatalf("missing API key reached a provider: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	out, err := run("--allow-jev", "--allow-openai-api")
	if err != nil || !strings.Contains(out, `"model":"api-deep"`) || !strings.Contains(out, `"text":"Done"`) || apiFake.model != "api-deep" || apiFake.generations != 1 || jevFake.calls != 1 {
		t.Fatalf("API route failed: out=%s err=%v jev=%d api=%+v", out, err, jevFake.calls, apiFake)
	}
	for _, criterion := range jevFake.questions["model"].Criteria {
		if strings.Contains(criterion, "app-") {
			t.Fatalf("app candidate leaked into API choice: %q", criterion)
		}
	}
}
