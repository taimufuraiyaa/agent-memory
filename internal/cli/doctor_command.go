package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/doctor"
)

const defaultServiceURL = "http://127.0.0.1:3211"

type doctorSummary struct {
	Total   int  `json:"total"`
	Pass    int  `json:"pass"`
	Warning int  `json:"warning"`
	Fail    int  `json:"fail"`
	Skipped int  `json:"skipped"`
	Healthy bool `json:"healthy"`
}

type doctorCommandResult struct {
	Checks         []doctor.Result `json:"checks"`
	Summary        doctorSummary   `json:"summary"`
	BeforeChecks   []doctor.Result `json:"before_checks,omitempty"`
	BeforeSummary  *doctorSummary  `json:"before_summary,omitempty"`
	Repaired       bool            `json:"repaired"`
	RepairsPlanned []string        `json:"repairs_planned"`
	RepairsApplied []string        `json:"repairs_applied"`
	DryRun         bool            `json:"dry_run"`
}

func newDoctorCommand() *cobra.Command {
	var root, dataDir, workspaceName, serviceURL, modelDir, format string
	var repair, fix, dryRun bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose agent-memory runtime and agent integration",
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFormat, err := validateTextOrJSONFormat(format)
			if err != nil {
				return err
			}
			fixRequested := repair || fix
			if dryRun && !fixRequested {
				return errors.New("--dry-run requires --fix or --repair")
			}
			if root == "" {
				root, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			if dataDir == "" {
				dataDir = defaultAgentMemoryDataDir()
			}
			if workspaceName == "" {
				workspaceName = filepath.Base(root)
			}
			if modelDir == "" {
				modelDir = filepath.Join(dataDir, "models", "all-MiniLM-L6-v2")
			}
			loadedConfig, _ := config.Load(root)
			var connectorConfigs []config.ConnectorConfig
			if loadedConfig != nil {
				connectorConfigs = loadedConfig.Connectors
			}
			options := doctor.Options{
				Root: root, DataDir: dataDir, Workspace: workspaceName, ServiceURL: serviceURL, ModelDir: modelDir, Connectors: connectorConfigs,
			}
			before := doctor.NewRunner(doctor.DefaultChecks(options)...).Run(cmd.Context())
			beforeSummary := summarizeDoctorResults(before)
			planned := []string{}
			applied := []string{}
			if fixRequested {
				planned, err = planSafeDataLayout(dataDir)
				if err != nil {
					return err
				}
				if !dryRun {
					applied, err = applySafeDataLayout(planned)
					if err != nil {
						return err
					}
				}
			}
			results := before
			if fixRequested && !dryRun {
				results = doctor.NewRunner(doctor.DefaultChecks(options)...).Run(cmd.Context())
			}
			report := doctorCommandResult{
				Checks: results, Summary: summarizeDoctorResults(results), Repaired: len(applied) > 0,
				RepairsPlanned: planned, RepairsApplied: applied, DryRun: dryRun,
			}
			if fixRequested {
				report.BeforeChecks = before
				report.BeforeSummary = &beforeSummary
			}
			if outputFormat == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "doctor", report)
			}
			return writeDoctorText(cmd, report, fixRequested)
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Project root to inspect")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "agent-memory data directory")
	cmd.Flags().StringVarP(&workspaceName, "workspace", "w", "", "Workspace name")
	cmd.Flags().StringVar(&serviceURL, "service-url", defaultServiceURL, "Local service URL")
	cmd.Flags().StringVar(&modelDir, "model-dir", "", "Embedding model directory")
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: text|json")
	cmd.Flags().BoolVar(&repair, "repair", false, "Apply only safe, scoped repairs")
	cmd.Flags().BoolVar(&fix, "fix", false, "Alias for --repair")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview repairs without changing files")
	return cmd
}

func summarizeDoctorResults(results []doctor.Result) doctorSummary {
	summary := doctorSummary{Total: len(results), Healthy: true}
	for _, result := range results {
		switch result.Status {
		case doctor.StatusPass:
			summary.Pass++
		case doctor.StatusWarning:
			summary.Warning++
		case doctor.StatusFail:
			summary.Fail++
			summary.Healthy = false
		case doctor.StatusSkipped:
			summary.Skipped++
		}
	}
	return summary
}

func safeDataPaths(dataDir string) []string {
	return []string{
		dataDir,
		filepath.Join(dataDir, "models"),
		filepath.Join(dataDir, "logs"),
		filepath.Join(dataDir, "onnxruntime"),
	}
}

func planSafeDataLayout(dataDir string) ([]string, error) {
	planned := []string{}
	for _, path := range safeDataPaths(dataDir) {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			planned = append(planned, path)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect repair path %s: %w", path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("repair path is not a directory: %s", path)
		}
		if info.Mode().Perm()&0o700 != 0o700 {
			planned = append(planned, path)
		}
	}
	return planned, nil
}

func applySafeDataLayout(planned []string) ([]string, error) {
	applied := make([]string, 0, len(planned))
	for _, path := range planned {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return applied, fmt.Errorf("create repair path %s: %w", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return applied, fmt.Errorf("inspect repaired path %s: %w", path, err)
		}
		if !info.IsDir() {
			return applied, fmt.Errorf("repair path is not a directory: %s", path)
		}
		mode := info.Mode().Perm() | 0o700
		if err := os.Chmod(path, mode); err != nil {
			return applied, fmt.Errorf("set owner permissions on %s: %w", path, err)
		}
		applied = append(applied, path)
	}
	return applied, nil
}

func writeDoctorText(cmd *cobra.Command, report doctorCommandResult, fixRequested bool) error {
	if fixRequested {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Before: %d pass, %d warning, %d fail, %d skipped\n", report.BeforeSummary.Pass, report.BeforeSummary.Warning, report.BeforeSummary.Fail, report.BeforeSummary.Skipped)
		for _, path := range report.RepairsPlanned {
			label := "planned"
			if !report.DryRun {
				label = "applied"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", label, path)
		}
	}
	for _, result := range report.Checks {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s", result.Status, result.Name)
		if result.Evidence != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), ": %s", result.Evidence)
		}
		if result.Message != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), ": %s", result.Message)
		}
		if result.NextAction != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), " -> %s", result.NextAction)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Summary: %d pass, %d warning, %d fail, %d skipped; healthy=%t\n", report.Summary.Pass, report.Summary.Warning, report.Summary.Fail, report.Summary.Skipped, report.Summary.Healthy)
	return nil
}
