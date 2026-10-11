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
)

func TestModelChoiceRequiresExplicitEgressAndReturnsHostModel(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dataDir, "model-catalog.json"), []byte(`{"version":1,"hosts":{"chatgpt":[{"id":"gpt-6-luna","description":"Quick"},{"id":"gpt-6.1-sol","description":"Complex"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jevconfig.NewTokenStore(dataDir).Save(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
	fake := &advisoryJev{answers: map[string]jev.ChoiceAnswer{"model": {Choice: "model_2", Confidence: 0.9, Probabilities: map[string]float64{"model_1": 0.1, "model_2": 0.9}}}}
	previous := newJevClient
	newJevClient = func() jevDecisionClient { return fake }
	t.Cleanup(func() { newJevClient = previous })
	run := func(allow bool) (string, error) {
		cmd := NewRootCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader("Solve the multi-file regression"))
		args := []string{"model-choice", "--workspace", "ws", "--host", "chatgpt", "--data-dir", dataDir}
		if allow {
			args = append(args, "--allow-jev")
		}
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	if _, err := run(false); err == nil || fake.calls != 0 {
		t.Fatalf("egress without approval: calls=%d err=%v", fake.calls, err)
	}
	out, err := run(true)
	if err != nil || !strings.Contains(out, `"model":"gpt-6.1-sol"`) || !strings.Contains(out, `"model_switched":false`) || fake.calls != 1 {
		t.Fatalf("model-choice failed: %s %v calls=%d", out, err, fake.calls)
	}
}
