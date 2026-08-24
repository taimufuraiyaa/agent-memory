package cli

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/advisor"
)

func newAdvisorCommand() *cobra.Command {
	var flags commonFlags
	cmd := &cobra.Command{
		Use:   "advisor",
		Short: "Analyze workspace memory quality and efficiency",
		Long:  "Build a read-only, deterministic Memory Advisor report from existing workspace telemetry. Recommendations are never applied automatically.",
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := validateAdvisorFormat(flags.format)
			if err != nil {
				return err
			}
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			var report advisor.Report
			if cfg.apiURL != "" {
				path := "/api/v1/advisor?workspace=" + url.QueryEscape(cfg.workspace)
				if err := getAPI(cmd.Context(), cfg.apiURL, path, &report); err != nil {
					return err
				}
			} else {
				store, err := openStore(cmd.Context(), cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				report, err = advisor.BuildReport(cmd.Context(), store, cfg.workspace)
				if err != nil {
					return err
				}
			}
			if format == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "advisor", report)
			}
			writeAdvisorText(cmd.OutOrStdout(), report)
			return nil
		},
	}
	addCommonFlags(cmd, &flags)
	flags.format = "text"
	cmd.Flags().Lookup("format").DefValue = "text"
	cmd.Flags().Lookup("format").Usage = "Output format: text|json"
	return cmd
}

func validateAdvisorFormat(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "text":
		return "text", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("invalid format: allowed values are text|json")
	}
}

func writeAdvisorText(w io.Writer, report advisor.Report) {
	_, _ = fmt.Fprintln(w, "Memory Advisor")
	_, _ = fmt.Fprintf(w, "workspace: %s\n", report.Workspace)
	if report.Neutral {
		_, _ = fmt.Fprintln(w, "grade: U — insufficient evidence in all dimensions")
	} else {
		_, _ = fmt.Fprintf(w, "grade: %s (%d/100)\n", report.Grade, report.Score)
	}
	_, _ = fmt.Fprintln(w, "dimensions:")
	for _, dimension := range report.Dimensions {
		if dimension.Available {
			sufficientMark := ""
			if !dimension.Sufficient {
				sufficientMark = " [insufficient evidence]"
			}
			_, _ = fmt.Fprintf(w, "  %s: %d/100%s — %s\n", dimension.Label, dimension.Score, sufficientMark, dimension.Detail)
		} else {
			_, _ = fmt.Fprintf(w, "  %s: N/A — %s\n", dimension.Label, dimension.Detail)
		}
	}
	_, _ = fmt.Fprintln(w, "recommendations:")
	if len(report.Recommendations) == 0 {
		_, _ = fmt.Fprintln(w, "  none")
		return
	}
	for _, recommendation := range report.Recommendations {
		metric := ""
		if recommendation.Metric != "" {
			metric = " (" + recommendation.Metric + ")"
		}
		_, _ = fmt.Fprintf(w, "  [%s] %s%s\n", strings.ToUpper(string(recommendation.Severity)), recommendation.Title, metric)
		_, _ = fmt.Fprintf(w, "    %s\n", recommendation.Detail)
	}
}
