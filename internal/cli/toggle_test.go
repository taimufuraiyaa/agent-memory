package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
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

func TestEnvFileLoadedBeforeEngineResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envDir := filepath.Join(home, ".agent-memory")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(envDir, "agent-memory.env")
	if err := os.WriteFile(envPath, []byte("export AGENT_MEMORY_TERM_BLOOM_MODE=off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Process environment must lose to the env file per documented precedence.
	t.Setenv("AGENT_MEMORY_TERM_BLOOM_MODE", "gate")

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := os.Getenv("AGENT_MEMORY_TERM_BLOOM_MODE"); got != "off" {
		t.Fatalf("expected env file to override process environment, got %q", got)
	}
	if got := engine.TermBloomMode(); got != engine.TermBloomOff {
		t.Fatalf("expected term bloom mode picked up as off, got %v", got)
	}
}

func TestToggleFlagsOverrideEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envDir := filepath.Join(home, ".agent-memory")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(envDir, "agent-memory.env")
	if err := os.WriteFile(envPath, []byte("export AGENT_MEMORY_ENABLED=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--toggle-on"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Flags win over the env file per documented precedence.
	if got := os.Getenv("AGENT_MEMORY_ENABLED"); got != "1" {
		t.Fatalf("expected --toggle-on to override env file, got %q", got)
	}
	b, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if !strings.Contains(string(b), `AGENT_MEMORY_ENABLED="1"`) {
		t.Fatalf("expected env file updated by toggle, got %q", string(b))
	}
}

func TestMalformedEnvFileWarnsAndContinues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envDir := filepath.Join(home, ".agent-memory")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(envDir, "agent-memory.env")
	content := "AGENT_MEMORY_ENABLED=1\n" +
		"this line is not an assignment\n" +
		"# comment lines are ignored\n" +
		"AGENT_MEMORY_RUN_LABEL=from-file\n"
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var errBuf bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("malformed env file must not crash the CLI: %v", err)
	}
	if !strings.Contains(errBuf.String(), "warning") {
		t.Fatalf("expected warning on stderr, got %q", errBuf.String())
	}
	// Valid lines must still be applied.
	if got := os.Getenv("AGENT_MEMORY_RUN_LABEL"); got != "from-file" {
		t.Fatalf("expected valid line applied, got %q", got)
	}
}
