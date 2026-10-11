package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestWorkStartCheckpointAndShowAcrossInvocations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "work.db")
	start := executeWorkCommand(t, "work", "start", "--workspace", "ws", "--db", dbPath,
		"--session", "session-1", "--principal", "agent-1", "--client", "codex",
		"--goal", "Resume the implementation.", "--idempotency-key", "start-1")
	episode := start["episode"].(map[string]any)
	episodeID := episode["id"].(string)

	checkpoint := executeWorkCommand(t, "work", "checkpoint", "--workspace", "ws", "--db", dbPath,
		"--principal", "agent-1", "--episode", episodeID, "--goal", "Resume the implementation.",
		"--constraint", "Preserve compatibility", "--next-action", "Run focused tests")
	if checkpoint["working_state"].(map[string]any)["generation"] != float64(1) {
		t.Fatalf("unexpected checkpoint: %#v", checkpoint)
	}

	shown := executeWorkCommand(t, "work", "show", "--workspace", "ws", "--db", dbPath,
		"--principal", "agent-1", "--episode", episodeID)
	if shown["working_state"].(map[string]any)["next_action"] != "Run focused tests" {
		t.Fatalf("unexpected recovered state: %#v", shown)
	}
}

func TestWorkStepEndAndHandoffCommandsAreRegistered(t *testing.T) {
	root := NewRootCommand()
	work, _, err := root.Find([]string{"work"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"start", "step", "checkpoint", "show", "end", "handoff", "recall", "promote", "tool-event", "derive-tool-lesson", "promote-tool-lesson"} {
		if child, _, findErr := work.Find([]string{name}); findErr != nil || child.Name() != name {
			t.Fatalf("missing work %s command", name)
		}
	}
}

func TestSessionEndCommandFinalizesStructuredEpisodeWithoutTranscriptOrModelFiles(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "structured-session-end.db")
	modelDir := filepath.Join(t.TempDir(), "empty-model-dir")
	started := executeWorkCommand(t, "work", "start", "--workspace", "ws", "--db", dbPath,
		"--session", "session-cli", "--principal", "agent-cli", "--client", "codex",
		"--goal", "Finalize through the session-end command.", "--idempotency-key", "cli-start")
	episodeID := started["episode"].(map[string]any)["id"].(string)
	executeWorkCommand(t, "work", "step", "--workspace", "ws", "--db", dbPath,
		"--principal", "agent-cli", "--episode", episodeID, "--kind", "result", "--status", "completed",
		"--summary", "The CLI integration passed.", "--idempotency-key", "cli-step")

	ended := executeWorkCommand(t, "session-end", "--workspace", "ws", "--db", dbPath, "--model-dir", modelDir,
		"--session", "session-cli", "--principal", "agent-cli", "--terminal-status", "completed", "--idempotency-key", "cli-finish")
	if ended["mode"] != "structured_episode" || ended["partial"] != false || ended["total_extracted"] != float64(0) {
		t.Fatalf("unexpected structured session-end output: %#v", ended)
	}
	if ended["episode"].(map[string]any)["status"] != "completed" || ended["summary"].(map[string]any)["episode_id"] != episodeID {
		t.Fatalf("structured episode was not finalized: %#v", ended)
	}
}

func TestNaturalHowWorkflowRecallsAndPromotesWhat(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "natural-how.db")
	modelDir := filepath.Join(t.TempDir(), "model")
	started := executeWorkCommand(t, "work", "start", "--workspace", "ws", "--db", dbPath,
		"--session", "session-how", "--principal", "agent-how", "--client", "codex",
		"--goal", "Verify the release naturally.", "--idempotency-key", "how-start")
	episodeID := started["episode"].(map[string]any)["id"].(string)
	step := executeWorkCommand(t, "work", "step", "--workspace", "ws", "--db", dbPath,
		"--principal", "agent-how", "--episode", episodeID, "--kind", "decision", "--status", "completed",
		"--summary", "Use a fresh temporary database and public commands.", "--rationale", "This detects wiring gaps hidden by unit fixtures.",
		"--idempotency-key", "how-decision")
	stepID := step["step"].(map[string]any)["id"].(string)
	executeWorkCommand(t, "work", "step", "--workspace", "ws", "--db", dbPath,
		"--principal", "agent-how", "--episode", episodeID, "--kind", "result", "--status", "completed",
		"--summary", "The fresh workflow passed.", "--confidence", "1", "--idempotency-key", "how-result")
	executeWorkCommand(t, "work", "checkpoint", "--workspace", "ws", "--db", dbPath,
		"--principal", "agent-how", "--episode", episodeID, "--goal", "Verify the release naturally.",
		"--completed", "capture", "--next-action", "Finalize and recall")
	ended := executeWorkCommand(t, "session-end", "--workspace", "ws", "--db", dbPath, "--model-dir", modelDir,
		"--session", "session-how", "--principal", "agent-how", "--terminal-status", "completed", "--idempotency-key", "how-finish")
	summaryID := ended["summary"].(map[string]any)["id"].(string)
	standaloneStore, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, contextBlock, err := executeRecall(context.Background(), runtimeConfig{workspace: "ws", dbPath: dbPath}, standaloneStore, naturalTestProvider{}, true,
		recallRequest{Task: "How do I verify the release naturally?", Budget: 800})
	_ = standaloneStore.Close()
	if err != nil || payload["how_recall"] == nil || payload["how_request_id"] == "" || contextBlock == "" {
		t.Fatalf("normal standalone recall did not compose How context: payload=%#v err=%v", payload, err)
	}

	recalled := executeWorkCommand(t, "work", "recall", "--workspace", "ws", "--db", dbPath,
		"--task", "How do I verify the release naturally?", "--budget", "800")
	paths := recalled["paths"].([]any)
	if len(paths) == 0 {
		t.Fatalf("finalized path was not recalled: %#v", recalled)
	}

	promoted := executeWorkCommand(t, "work", "promote", "--workspace", "ws", "--db", dbPath, "--model-dir", modelDir,
		"--principal", "agent-how", "--episode", episodeID, "--summary", summaryID,
		"--memory-type", "procedural", "--source-step", stepID, "--idempotency-key", "how-promote")
	if promoted["partial"] != false || len(promoted["promotions"].([]any)) != 1 {
		t.Fatalf("unexpected promotion result: %#v", promoted)
	}

	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	detail, err := application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()).GetActivityEpisode(context.Background(), "ws", episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.PromotionTargets) != 1 || detail.PromotionTargets[0].Availability != "available" || detail.PromotionTargets[0].Memory == nil {
		t.Fatalf("promoted What was not grouped under How: %#v", detail.PromotionTargets)
	}
}

type naturalTestProvider struct{}

func (naturalTestProvider) Name() string         { return "natural-test" }
func (naturalTestProvider) ModelVersion() string { return "natural-test-v1" }
func (naturalTestProvider) Dimension() int       { return 8 }
func (naturalTestProvider) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 0, 0, 0, 0, 0, 0, 0}, nil
}
func (provider naturalTestProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for index, text := range texts {
		vector, err := provider.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		result[index] = vector
	}
	return result, nil
}

func executeWorkCommand(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	var envelope struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	if !envelope.OK {
		t.Fatalf("command failed: %s", out.String())
	}
	return envelope.Data
}
