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

func TestParseEnvAssignmentLineRejectsShellSyntaxAndInvalidNames(t *testing.T) {
	invalid := []string{
		`case ":$PATH:" in`,
		`*) export PATH="$HOME/.local/bin:$PATH" ;;`,
		`esac`,
		`BAD-NAME=value`,
		`9KEY=value`,
	}
	for _, line := range invalid {
		if key, _, ok := parseEnvAssignmentLine(line); ok {
			t.Fatalf("expected %q to be rejected, parsed key %q", line, key)
		}
	}

	valid := []string{`KEY=value`, `export PATH="/bin"`, `set _KEY=value`, `SeT Mixed_9=value`}
	for _, line := range valid {
		if _, _, ok := parseEnvAssignmentLine(line); !ok {
			t.Fatalf("expected %q to be accepted", line)
		}
	}
}

func TestUpsertEnvFileRemovesLegacyPathShellBlock(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "agent-memory.env")
	initial := "export KEEP=\"secret value\"\n\n" +
		"# Put the agent-memory binary on PATH\n" +
		"case \":$PATH:\" in\n" +
		"    *\":$HOME/.local/bin:\"*) ;;\n" +
		"    *) export PATH=\"$HOME/.local/bin:$PATH\" ;;\n" +
		"esac\n\n" +
		"# user comment\n"
	if err := os.WriteFile(envPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	updated, err := upsertEnvFile(envPath, map[string]string{"KEEP": "secret value"})
	if err != nil {
		t.Fatalf("upsert env file: %v", err)
	}
	if !updated {
		t.Fatal("expected legacy block cleanup to report an update")
	}
	b, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	content := string(b)
	if strings.Contains(content, "case ") || strings.Contains(content, "Put the agent-memory binary on PATH") {
		t.Fatalf("legacy shell block was not removed: %q", content)
	}
	if !strings.Contains(content, `export KEEP="secret value"`) || !strings.Contains(content, "# user comment") {
		t.Fatalf("unrelated env content was not preserved: %q", content)
	}

	updated, err = upsertEnvFile(envPath, map[string]string{"KEEP": "secret value"})
	if err != nil {
		t.Fatalf("repeat upsert: %v", err)
	}
	if updated {
		t.Fatal("expected cleanup to be idempotent")
	}
}
