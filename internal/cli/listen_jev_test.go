package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/integration"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

type fakeJevClient struct {
	probes int
	fail   bool
}

func (f *fakeJevClient) Probe(_ context.Context, token string) error {
	f.probes++
	if token != "test-key" || f.fail {
		return context.Canceled
	}
	return nil
}

func TestListenOnWithJevEnablesBothOnlyAfterVerifiedOptIn(t *testing.T) {
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
	fake := &fakeJevClient{}
	previous := newJevClient
	newJevClient = func() jevDecisionClient { return fake }
	t.Cleanup(func() { newJevClient = previous })
	run := func(args ...string) (map[string]any, error) {
		cmd := NewRootCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(append(append([]string{"listen"}, args...), "--workspace", "ws", "--data-dir", dataDir))
		err := cmd.Execute()
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(out.Bytes(), &envelope)
		return envelope.Data, err
	}
	if _, err := run("on", "--jev"); err == nil {
		t.Fatal("combined listen enabled without a token")
	}
	if state, _ := integration.NewClaudeListenStore(dataDir).Status("ws"); state.Enabled {
		t.Fatal("failed combined enable changed local state")
	}
	if err := jevconfig.NewTokenStore(dataDir).Save(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
	fake.fail = true
	if _, err := run("on", "--jev"); err == nil {
		t.Fatal("combined listen enabled after denied probe")
	}
	if state, _ := integration.NewClaudeListenStore(dataDir).Status("ws"); state.Enabled {
		t.Fatal("denied probe changed local state")
	}
	fake.fail = false
	status, err := run("on", "--jev")
	if err != nil || status["enabled"] != true || status["jev_enabled"] != true || status["jev_decisions_ready"] != true {
		t.Fatalf("combined listen failed: %+v %v", status, err)
	}
	status, err = run("off")
	if err != nil || status["enabled"] != false || status["jev_enabled"] != false {
		t.Fatalf("off did not disable both: %+v %v", status, err)
	}
}
func (f *fakeJevClient) Choose(context.Context, string, string, map[string]jev.ChoiceQuestion) (map[string]jev.ChoiceAnswer, error) {
	return nil, nil
}

func TestListenJevRequiresSeparateConsentAndLiveProbe(t *testing.T) {
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
	store := integration.NewClaudeListenStore(dataDir)
	if _, err := store.Enable("ws", root); err != nil {
		t.Fatal(err)
	}
	fake := &fakeJevClient{}
	previous := newJevClient
	newJevClient = func() jevDecisionClient { return fake }
	t.Cleanup(func() { newJevClient = previous })
	run := func(action string) (map[string]any, error) {
		cmd := NewRootCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"listen", action, "--workspace", "ws", "--data-dir", dataDir})
		err := cmd.Execute()
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(out.Bytes(), &envelope)
		return envelope.Data, err
	}
	if _, err := run("jev-on"); err == nil || fake.probes != 0 {
		t.Fatalf("Jev enabled without token: probes=%d err=%v", fake.probes, err)
	}
	if err := jevconfig.NewTokenStore(dataDir).Save(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
	status, err := run("status")
	if err != nil || status["jev_decisions_ready"] != false || fake.probes != 0 {
		t.Fatalf("status probed without consent: %+v %v", status, err)
	}
	status, err = run("jev-on")
	if err != nil || status["jev_decisions_ready"] != true || fake.probes != 1 {
		t.Fatalf("Jev opt-in failed: %+v %v", status, err)
	}
	status, err = run("jev-off")
	if err != nil || status["jev_decisions_ready"] != false {
		t.Fatalf("Jev opt-out failed: %+v %v", status, err)
	}
}
