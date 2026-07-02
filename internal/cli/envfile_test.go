package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	amconfig "github.com/taimufuraiyaa/agent-memory/internal/config"
)

func TestEnsureAdaptiveTuningGuidanceAppendsManagedBlock(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "agent-memory.env")
	if err := os.WriteFile(envPath, []byte("export AGENT_MEMORY_ENABLED=\"1\"\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	updated, err := ensureAdaptiveTuningGuidance(envPath)
	if err != nil {
		t.Fatalf("ensure guidance: %v", err)
	}
	if !updated {
		t.Fatalf("expected guidance update")
	}

	b, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, amconfig.AdaptiveTuningEnvGuidanceHeader()) {
		t.Fatalf("expected adaptive tuning guidance, got %q", content)
	}
	if !strings.Contains(content, "AGENT_MEMORY_ADAPTIVE_POLICY_RECALL") {
		t.Fatalf("expected adaptive tuning env keys, got %q", content)
	}
}

func TestEnsureAdaptiveTuningGuidanceIsIdempotent(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "agent-memory.env")
	initial := amconfig.EnsureAdaptiveTuningEnvGuidance("export AGENT_MEMORY_ENABLED=\"1\"\n")
	if err := os.WriteFile(envPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	updated, err := ensureAdaptiveTuningGuidance(envPath)
	if err != nil {
		t.Fatalf("ensure guidance: %v", err)
	}
	if updated {
		t.Fatalf("expected no-op when guidance already exists")
	}
}
