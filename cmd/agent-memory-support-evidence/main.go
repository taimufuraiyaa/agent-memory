package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/supportevidence"
)

const reportSchemaV1 = "agent-memory-production-support-staffing-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (supportevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                  string `json:"schema"`
	Ready                   bool   `json:"ready"`
	ReceiptWritten          bool   `json:"receipt_written"`
	CoverageComplete        bool   `json:"coverage_complete"`
	RequiredCoverageMinutes int    `json:"required_coverage_minutes"`
	PrimaryCoveredMinutes   int    `json:"primary_covered_minutes"`
	BackupCoveredMinutes    int    `json:"backup_covered_minutes"`
	PrimarySlotCount        int    `json:"primary_slot_count"`
	BackupSlotCount         int    `json:"backup_slot_count"`
	DrillCount              int    `json:"drill_count"`
	TargetBreachCount       int    `json:"target_breach_count"`
	CheckCount              int    `json:"check_count"`
	PassedCount             int    `json:"passed_count"`
	FailedCount             int    `json:"failed_count"`
	InconclusiveCount       int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}
func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-support-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to production platform inventory")
	plan := flags.String("plan", "", "Path to production infrastructure plan")
	change := flags.String("change", "", "Path to ready production infrastructure change")
	release := flags.String("release", "", "Path to passed production release")
	input := flags.String("input", "", "Path to content-free support staffing evidence")
	receiptPath := flags.String("receipt", "", "New normalized support staffing receipt path")
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
		deps.collect = supportevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := supportevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, CoverageComplete: receipt.CoverageComplete, RequiredCoverageMinutes: receipt.RequiredCoverageMinutes, PrimaryCoveredMinutes: receipt.PrimaryCoveredMinutes, BackupCoveredMinutes: receipt.BackupCoveredMinutes, PrimarySlotCount: receipt.PrimarySlotCount, BackupSlotCount: receipt.BackupSlotCount, DrillCount: len(receipt.DrillResults), TargetBreachCount: receipt.TargetBreachCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode support staffing report")
		return 1
	}
	if !receipt.Ready {
		return 3
	}
	return 0
}
func anyBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}
