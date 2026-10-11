package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileExplicitSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "isolated.env")
	if err := os.WriteFile(path, []byte("AGENT_MEMORY_ENV_SELECTION_PROBE=selected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_MEMORY_ENV_FILE", path)
	t.Setenv("AGENT_MEMORY_ENV_SELECTION_PROBE", "process")
	loadEnvFile(NewRootCommand())
	if got := os.Getenv("AGENT_MEMORY_ENV_SELECTION_PROBE"); got != "selected" {
		t.Fatalf("explicit persisted file was not selected: %q", got)
	}
}

func TestEnvFileSelectedToggleWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "isolated.env")
	t.Setenv("AGENT_MEMORY_ENV_FILE", path)
	t.Setenv("AGENT_MEMORY_RUN_LABEL", "")
	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--run-label", "isolated-native-smoke"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("isolated-native-smoke")) {
		t.Fatalf("selected toggle file missing: %v %q", err, data)
	}
}

func TestEnvFileRejectsRelativeSelection(t *testing.T) {
	t.Setenv("AGENT_MEMORY_ENV_FILE", "relative.env")
	if _, err := configuredEnvFilePath(); err == nil {
		t.Fatal("relative selection was accepted")
	}
}
