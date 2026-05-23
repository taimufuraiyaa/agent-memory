package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestTuningCommandJSONEnvelope(t *testing.T) {
	t.Setenv("AGENT_MEMORY_ADAPTIVE_POLICY_RECALL", `{"min_total_score":0.41}`)
	t.Setenv("AGENT_MEMORY_ADAPTIVE_FEEDBACK_COOLDOWNS", `{"rejected_cooldown":"2h"}`)

	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"tuning", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute tuning json: %v", err)
	}

	var payload struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			PolicyDefaults map[string]struct {
				MinTotalScore float64 `json:"min_total_score"`
			} `json:"policy_defaults"`
			FeedbackCooldowns struct {
				Rejected string `json:"rejected_cooldown"`
			} `json:"feedback_cooldowns"`
			EnvKeys struct {
				RecallPolicy string `json:"recall_policy"`
			} `json:"env_keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid tuning json: %v raw=%q", err, out.String())
	}
	if !payload.OK || payload.Command != "tuning" {
		t.Fatalf("unexpected tuning envelope: %+v", payload)
	}
	if payload.Data.PolicyDefaults["recall"].MinTotalScore != 0.41 {
		t.Fatalf("expected recall override in json output, got %+v", payload.Data.PolicyDefaults["recall"])
	}
	if payload.Data.FeedbackCooldowns.Rejected != "2h0m0s" {
		t.Fatalf("expected rejected cooldown override, got %q", payload.Data.FeedbackCooldowns.Rejected)
	}
	if payload.Data.EnvKeys.RecallPolicy != "AGENT_MEMORY_ADAPTIVE_POLICY_RECALL" {
		t.Fatalf("expected recall env key, got %q", payload.Data.EnvKeys.RecallPolicy)
	}
}

func TestTuningCommandTextOutput(t *testing.T) {
	t.Setenv("AGENT_MEMORY_ADAPTIVE_POLICY_SEARCH", `{"min_semantic_score":0.27}`)

	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"tuning"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute tuning text: %v", err)
	}

	text := out.String()
	for _, want := range []string{
		"Adaptive tuning",
		"policy defaults:",
		"search: min_semantic=0.2700",
		"feedback cooldowns:",
		"env keys:",
		"AGENT_MEMORY_ADAPTIVE_POLICY_SEARCH",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in tuning text output, got %q", want, text)
		}
	}
}
