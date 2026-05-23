package config

import (
	"testing"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
)

func TestResolveAdaptivePolicyAppliesDefaultAndModePatches(t *testing.T) {
	t.Setenv(envAdaptivePolicyDefault, `{"min_total_score":0.11,"weak_total_score":0.05}`)
	t.Setenv(envAdaptivePolicyRecall, `{"min_total_score":0.19,"relative_score_cutoff":0.21}`)

	got := ResolveAdaptivePolicy("recall")

	if got.MinSemanticScore != core.DefaultAdaptivePolicy("recall").MinSemanticScore {
		t.Fatalf("expected untouched recall min semantic default, got %f", got.MinSemanticScore)
	}
	if got.MinTotalScore != 0.19 {
		t.Fatalf("expected recall min total override, got %f", got.MinTotalScore)
	}
	if got.RelativeScoreCutoff != 0.21 {
		t.Fatalf("expected recall relative cutoff override, got %f", got.RelativeScoreCutoff)
	}
	if got.WeakTotalScore != 0.05 {
		t.Fatalf("expected inherited default weak total override, got %f", got.WeakTotalScore)
	}
}

func TestResolveAdaptivePolicyIgnoresMalformedAndInvalidFields(t *testing.T) {
	base := core.DefaultAdaptivePolicy("search")

	t.Setenv(envAdaptivePolicySearch, `{"min_total_score":1.4,"relative_score_cutoff":0.25}`)
	got := ResolveAdaptivePolicy("search")
	if got.MinTotalScore != base.MinTotalScore {
		t.Fatalf("expected invalid min_total_score to be ignored, got %f", got.MinTotalScore)
	}
	if got.RelativeScoreCutoff != 0.25 {
		t.Fatalf("expected valid field to still apply, got %f", got.RelativeScoreCutoff)
	}

	t.Setenv(envAdaptivePolicySearch, `{"min_total_score":`)
	got = ResolveAdaptivePolicy("search")
	if got != base {
		t.Fatalf("expected malformed patch to fall back to defaults, got %+v", got)
	}
}

func TestResolveAdaptiveFeedbackTuningAppliesCooldownOverrides(t *testing.T) {
	t.Setenv(envAdaptiveFeedbackWindows, `{"rejected_cooldown":"2h","harmful_cooldown":10800,"contradicted_cooldown":"45m"}`)

	got := ResolveAdaptiveFeedbackTuning()

	if got.RejectedCooldown != 2*time.Hour {
		t.Fatalf("expected rejected cooldown override, got %s", got.RejectedCooldown)
	}
	if got.HarmfulCooldown != 3*time.Hour {
		t.Fatalf("expected harmful cooldown override, got %s", got.HarmfulCooldown)
	}
	if got.ContradictedCooldown != 45*time.Minute {
		t.Fatalf("expected contradicted cooldown override, got %s", got.ContradictedCooldown)
	}
}
