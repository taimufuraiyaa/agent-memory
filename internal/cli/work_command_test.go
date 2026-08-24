package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
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
	for _, name := range []string{"start", "step", "checkpoint", "show", "end", "handoff"} {
		if child, _, findErr := work.Find([]string{name}); findErr != nil || child.Name() != name {
			t.Fatalf("missing work %s command", name)
		}
	}
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
