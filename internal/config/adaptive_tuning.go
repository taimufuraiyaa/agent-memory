package config

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	envAdaptivePolicyDefault   = "AGENT_MEMORY_ADAPTIVE_POLICY_DEFAULT"
	envAdaptivePolicySearch    = "AGENT_MEMORY_ADAPTIVE_POLICY_SEARCH"
	envAdaptivePolicyRecall    = "AGENT_MEMORY_ADAPTIVE_POLICY_RECALL"
	envAdaptivePolicyRelate    = "AGENT_MEMORY_ADAPTIVE_POLICY_RELATE"
	envAdaptivePolicyOutcomes  = "AGENT_MEMORY_ADAPTIVE_POLICY_OUTCOMES"
	envAdaptiveFeedbackWindows = "AGENT_MEMORY_ADAPTIVE_FEEDBACK_COOLDOWNS"
	adaptiveTuningGuidanceHdr  = "# agent-memory adaptive tuning (optional)"
)

type AdaptiveTuningSnapshot struct {
	PolicyDefaults    map[string]AdaptivePolicySnapshot `json:"policy_defaults"`
	FeedbackCooldowns AdaptiveCooldownSnapshot          `json:"feedback_cooldowns"`
	EnvKeys           AdaptiveTuningEnvKeys             `json:"env_keys"`
}

type AdaptivePolicySnapshot struct {
	MinSemanticScore    float64 `json:"min_semantic_score"`
	MinTotalScore       float64 `json:"min_total_score"`
	RelativeScoreCutoff float64 `json:"relative_score_cutoff"`
	WeakSemanticScore   float64 `json:"weak_semantic_score"`
	WeakTotalScore      float64 `json:"weak_total_score"`
	WeakRelativeCutoff  float64 `json:"weak_relative_cutoff"`
	SemanticScoreBand   float64 `json:"semantic_score_band"`
}

type AdaptiveCooldownSnapshot struct {
	Rejected     string `json:"rejected_cooldown"`
	Harmful      string `json:"harmful_cooldown"`
	Contradicted string `json:"contradicted_cooldown"`
}

type AdaptiveTuningEnvKeys struct {
	DefaultPolicy   string `json:"default_policy"`
	SearchPolicy    string `json:"search_policy"`
	RecallPolicy    string `json:"recall_policy"`
	RelatePolicy    string `json:"relate_policy"`
	OutcomesPolicy  string `json:"outcomes_policy"`
	FeedbackWindows string `json:"feedback_cooldowns"`
}

func InspectAdaptiveTuning() AdaptiveTuningSnapshot {
	feedback := ResolveAdaptiveFeedbackTuning()
	return AdaptiveTuningSnapshot{
		PolicyDefaults: map[string]AdaptivePolicySnapshot{
			"search":   policySnapshot(ResolveAdaptivePolicy("search")),
			"recall":   policySnapshot(ResolveAdaptivePolicy("recall")),
			"relate":   policySnapshot(ResolveAdaptivePolicy("relate")),
			"outcomes": policySnapshot(ResolveAdaptivePolicy("outcomes")),
		},
		FeedbackCooldowns: AdaptiveCooldownSnapshot{
			Rejected:     feedback.RejectedCooldown.String(),
			Harmful:      feedback.HarmfulCooldown.String(),
			Contradicted: feedback.ContradictedCooldown.String(),
		},
		EnvKeys: AdaptiveTuningEnvKeys{
			DefaultPolicy:   envAdaptivePolicyDefault,
			SearchPolicy:    envAdaptivePolicySearch,
			RecallPolicy:    envAdaptivePolicyRecall,
			RelatePolicy:    envAdaptivePolicyRelate,
			OutcomesPolicy:  envAdaptivePolicyOutcomes,
			FeedbackWindows: envAdaptiveFeedbackWindows,
		},
	}
}

func AdaptiveTuningEnvGuidanceHeader() string {
	return adaptiveTuningGuidanceHdr
}

func AdaptiveTuningEnvGuidanceBlock() string {
	lines := []string{
		adaptiveTuningGuidanceHdr,
		"# Leave these commented to keep safe defaults.",
		"# Inspect active values with: agent-memory tuning",
		"#",
		"# Optional policy override examples:",
		"# export " + envAdaptivePolicyDefault + "='{\"min_total_score\":0.03}'",
		"# export " + envAdaptivePolicySearch + "='{\"min_semantic_score\":0.03}'",
		"# export " + envAdaptivePolicyRecall + "='{\"min_semantic_score\":0.05,\"min_total_score\":0.05}'",
		"# export " + envAdaptivePolicyRelate + "='{\"relative_score_cutoff\":0.35}'",
		"# export " + envAdaptivePolicyOutcomes + "='{\"min_total_score\":0.2}'",
		"#",
		"# Optional feedback cooldown example:",
		"# export " + envAdaptiveFeedbackWindows + "='{\"rejected_cooldown\":\"12h\",\"harmful_cooldown\":\"48h\"}'",
	}
	return strings.Join(lines, "\n") + "\n"
}

func EnsureAdaptiveTuningEnvGuidance(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if strings.Contains(content, adaptiveTuningGuidanceHdr) {
		return content
	}
	out := strings.TrimRight(content, "\n")
	if strings.TrimSpace(out) != "" {
		out += "\n\n"
	}
	out += AdaptiveTuningEnvGuidanceBlock()
	return out
}

func policySnapshot(policy core.AdaptivePolicyDefaults) AdaptivePolicySnapshot {
	return AdaptivePolicySnapshot{
		MinSemanticScore:    policy.MinSemanticScore,
		MinTotalScore:       policy.MinTotalScore,
		RelativeScoreCutoff: policy.RelativeScoreCutoff,
		WeakSemanticScore:   policy.WeakSemanticScore,
		WeakTotalScore:      policy.WeakTotalScore,
		WeakRelativeCutoff:  policy.WeakRelativeCutoff,
		SemanticScoreBand:   policy.SemanticScoreBand,
	}
}

// ResolveAdaptivePolicy returns the effective policy defaults after applying
// optional environment patches on top of the code defaults for a mode.
func ResolveAdaptivePolicy(mode string) core.AdaptivePolicyDefaults {
	resolved := core.DefaultAdaptivePolicy(mode)
	resolved = applyPolicyPatch(resolved, os.Getenv(envAdaptivePolicyDefault))
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "recall":
		return applyPolicyPatch(resolved, os.Getenv(envAdaptivePolicyRecall))
	case "relate":
		return applyPolicyPatch(resolved, os.Getenv(envAdaptivePolicyRelate))
	case "outcomes":
		return applyPolicyPatch(resolved, os.Getenv(envAdaptivePolicyOutcomes))
	default:
		return applyPolicyPatch(resolved, os.Getenv(envAdaptivePolicySearch))
	}
}

// ResolveAdaptiveFeedbackTuning returns the effective feedback tuning after
// applying optional environment patches to cooldown windows.
func ResolveAdaptiveFeedbackTuning() core.AdaptiveFeedbackTuning {
	resolved := core.DefaultAdaptiveFeedbackTuning()
	fields, ok := decodePatch(os.Getenv(envAdaptiveFeedbackWindows))
	if !ok {
		return resolved
	}
	applyDurationField(fields, "rejected_cooldown", &resolved.RejectedCooldown)
	applyDurationField(fields, "harmful_cooldown", &resolved.HarmfulCooldown)
	applyDurationField(fields, "contradicted_cooldown", &resolved.ContradictedCooldown)
	return resolved
}

func applyPolicyPatch(base core.AdaptivePolicyDefaults, raw string) core.AdaptivePolicyDefaults {
	fields, ok := decodePatch(raw)
	if !ok {
		return base
	}
	applyUnitFloatField(fields, "min_semantic_score", &base.MinSemanticScore)
	applyUnitFloatField(fields, "min_total_score", &base.MinTotalScore)
	applyUnitFloatField(fields, "relative_score_cutoff", &base.RelativeScoreCutoff)
	applyUnitFloatField(fields, "weak_semantic_score", &base.WeakSemanticScore)
	applyUnitFloatField(fields, "weak_total_score", &base.WeakTotalScore)
	applyUnitFloatField(fields, "weak_relative_cutoff", &base.WeakRelativeCutoff)
	applyUnitFloatField(fields, "semantic_score_band", &base.SemanticScoreBand)
	return base
}

func decodePatch(raw string) (map[string]json.RawMessage, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, false
	}
	return fields, true
}

func applyUnitFloatField(fields map[string]json.RawMessage, key string, target *float64) {
	raw, ok := fields[key]
	if !ok {
		return
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return
	}
	if value < 0 || value > 1 {
		return
	}
	*target = value
}

func applyDurationField(fields map[string]json.RawMessage, key string, target *time.Duration) {
	raw, ok := fields[key]
	if !ok {
		return
	}
	value, ok := parseDurationField(raw)
	if !ok || value <= 0 {
		return
	}
	*target = value
}

func parseDurationField(raw json.RawMessage) (time.Duration, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			return 0, false
		}
		value, err := time.ParseDuration(text)
		if err != nil {
			return 0, false
		}
		return value, true
	}
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err == nil {
		if seconds <= 0 {
			return 0, false
		}
		return time.Duration(seconds * float64(time.Second)), true
	}
	return 0, false
}
