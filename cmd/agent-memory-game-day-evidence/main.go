package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/gamedayevidence"
)

const reportSchemaV1 = "agent-memory-staging-game-day-report-v1"

type dependencies struct {
	now     func() time.Time
	collect func(string, string, string, string, string, time.Time) (gamedayevidence.Receipt, error)
	publish func(string, gamedayevidence.Receipt) error
}
type report struct {
	Schema                  string `json:"schema"`
	Ready                   bool   `json:"ready"`
	ReceiptWritten          bool   `json:"receipt_written"`
	DrillCount              int    `json:"drill_count"`
	ComponentSubjectCount   int    `json:"component_subject_count"`
	IntegrationSubjectCount int    `json:"integration_subject_count"`
	EnabledIntegrationCount int    `json:"enabled_integration_count"`
	TargetBreachCount       int    `json:"target_breach_count"`
	CheckCount              int    `json:"check_count"`
	PassedCount             int    `json:"passed_count"`
	FailedCount             int    `json:"failed_count"`
	InconclusiveCount       int    `json:"inconclusive_count"`
	BundleCheckCount        int    `json:"bundle_check_count"`
	BundlePassedCount       int    `json:"bundle_passed_count"`
	BundleFailedCount       int    `json:"bundle_failed_count"`
	BundleInconclusiveCount int    `json:"bundle_inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}
func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-game-day-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Self-managed staging inventory path")
	plan := flags.String("plan", "", "Reviewed staging plan path")
	change := flags.String("change", "", "Ready applied-change receipt path")
	release := flags.String("release", "", "Passed staging release receipt path")
	input := flags.String("input", "", "Content-free P10.3-A game-day input path")
	receiptPath := flags.String("receipt", "", "New normalized P10.3-A receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *plan, *change, *release, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = gamedayevidence.Collect
	}
	if deps.publish == nil {
		deps.publish = gamedayevidence.Publish
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err = deps.publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, DrillCount: receipt.DrillCount, ComponentSubjectCount: receipt.ComponentSubjectCount, IntegrationSubjectCount: receipt.IntegrationSubjectCount, EnabledIntegrationCount: receipt.EnabledIntegrationCount, TargetBreachCount: receipt.TargetBreachCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount, BundleCheckCount: receipt.BundleCheckCount, BundlePassedCount: receipt.BundlePassedCount, BundleFailedCount: receipt.BundleFailedCount, BundleInconclusiveCount: receipt.BundleInconclusiveCount}
	if err = json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode game-day report")
		return 1
	}
	if !receipt.Ready {
		return 3
	}
	return 0
}
func anyBlank(values ...string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return true
		}
	}
	return false
}
