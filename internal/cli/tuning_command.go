package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
)

func newTuningCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "tuning",
		Short: "Show effective adaptive runtime tuning",
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := validateTuningFormat(format)
			if err != nil {
				return err
			}
			switch f {
			case "json":
				return writeSuccessEnvelope(cmd.OutOrStdout(), "tuning", config.InspectAdaptiveTuning())
			default:
				writeAdaptiveTuningText(cmd.OutOrStdout(), config.InspectAdaptiveTuning())
				return nil
			}
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: json|text")
	return cmd
}

func validateTuningFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text":
		return "text", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("invalid format: allowed values are json|text")
	}
}

func writeAdaptiveTuningText(w io.Writer, snapshot config.AdaptiveTuningSnapshot) {
	_, _ = fmt.Fprintf(w, "Adaptive tuning\n")
	_, _ = fmt.Fprintf(w, "policy defaults:\n")
	modes := make([]string, 0, len(snapshot.PolicyDefaults))
	for mode := range snapshot.PolicyDefaults {
		modes = append(modes, mode)
	}
	sort.Strings(modes)
	for _, mode := range modes {
		policy := snapshot.PolicyDefaults[mode]
		_, _ = fmt.Fprintf(w, "  %s: min_semantic=%0.4f min_total=%0.4f relative_cutoff=%0.4f weak_semantic=%0.4f weak_total=%0.4f weak_relative=%0.4f\n",
			mode,
			policy.MinSemanticScore,
			policy.MinTotalScore,
			policy.RelativeScoreCutoff,
			policy.WeakSemanticScore,
			policy.WeakTotalScore,
			policy.WeakRelativeCutoff,
		)
	}
	_, _ = fmt.Fprintf(w, "feedback cooldowns:\n")
	_, _ = fmt.Fprintf(w, "  rejected: %s\n", snapshot.FeedbackCooldowns.Rejected)
	_, _ = fmt.Fprintf(w, "  harmful: %s\n", snapshot.FeedbackCooldowns.Harmful)
	_, _ = fmt.Fprintf(w, "  contradicted: %s\n", snapshot.FeedbackCooldowns.Contradicted)
	_, _ = fmt.Fprintf(w, "env keys:\n")
	_, _ = fmt.Fprintf(w, "  default_policy: %s\n", snapshot.EnvKeys.DefaultPolicy)
	_, _ = fmt.Fprintf(w, "  search_policy: %s\n", snapshot.EnvKeys.SearchPolicy)
	_, _ = fmt.Fprintf(w, "  recall_policy: %s\n", snapshot.EnvKeys.RecallPolicy)
	_, _ = fmt.Fprintf(w, "  relate_policy: %s\n", snapshot.EnvKeys.RelatePolicy)
	_, _ = fmt.Fprintf(w, "  outcomes_policy: %s\n", snapshot.EnvKeys.OutcomesPolicy)
	_, _ = fmt.Fprintf(w, "  feedback_cooldowns: %s\n", snapshot.EnvKeys.FeedbackWindows)
}
