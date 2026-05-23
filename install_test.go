package main

import (
	"strings"
	"testing"

	amconfig "github.com/time/timebooks/agent-memory/internal/config"
)

func TestMergeEnvFileAddsAdaptiveTuningGuidance(t *testing.T) {
	merged, err := mergeEnvFile("/tmp/agent-memory.env", map[string]string{
		"AGENT_MEMORY_ENABLED": "1",
	})
	if err != nil {
		t.Fatalf("merge env file: %v", err)
	}
	if !strings.Contains(merged, "export AGENT_MEMORY_ENABLED=\"1\"") {
		t.Fatalf("expected base env assignment, got %q", merged)
	}
	if !strings.Contains(merged, amconfig.AdaptiveTuningEnvGuidanceHeader()) {
		t.Fatalf("expected adaptive tuning guidance, got %q", merged)
	}
	if !strings.Contains(merged, "agent-memory tuning") {
		t.Fatalf("expected tuning command hint, got %q", merged)
	}
}

func TestMergeEnvFileGuidanceIsIdempotent(t *testing.T) {
	first := amconfig.EnsureAdaptiveTuningEnvGuidance("export AGENT_MEMORY_ENABLED=\"1\"\n")
	second, err := mergeEnvFile("/tmp/agent-memory.env", map[string]string{
		"AGENT_MEMORY_ENABLED": "1",
	})
	if err != nil {
		t.Fatalf("merge env file: %v", err)
	}
	second = amconfig.EnsureAdaptiveTuningEnvGuidance(second)
	if strings.Count(first, amconfig.AdaptiveTuningEnvGuidanceHeader()) != 1 {
		t.Fatalf("expected one guidance header in first output, got %q", first)
	}
	if strings.Count(second, amconfig.AdaptiveTuningEnvGuidanceHeader()) != 1 {
		t.Fatalf("expected one guidance header in second output, got %q", second)
	}
}
