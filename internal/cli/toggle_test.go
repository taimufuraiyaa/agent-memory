package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToggleFlagsWriteEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--toggle-off", "--run-label", "off"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	envPath := filepath.Join(home, ".agent-memory", "agent-memory.env")
	b, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "AGENT_MEMORY_ENABLED") {
		t.Fatalf("expected AGENT_MEMORY_ENABLED in env file, got %q", s)
	}
	if !strings.Contains(s, "AGENT_MEMORY_RUN_LABEL") {
		t.Fatalf("expected AGENT_MEMORY_RUN_LABEL in env file, got %q", s)
	}
}

