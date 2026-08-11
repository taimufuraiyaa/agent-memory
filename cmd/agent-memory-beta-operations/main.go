package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaoperationsevidence"
)

const reportSchemaV1 = "agent-memory-production-beta-operations-report-v1"

type collectFunc func(string, string, string, string, string, string, time.Time) (betaoperationsevidence.Receipt, error)

type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                 string `json:"schema"`
	Ready                  bool   `json:"ready"`
	ReceiptWritten         bool   `json:"receipt_written"`
	DueCaseCount           int    `json:"due_case_count"`
	WithinTargetCount      int    `json:"within_target_count"`
	LateCaseCount          int    `json:"late_case_count"`
	OverdueOpenCount       int    `json:"overdue_open_count"`
	SampleCoverageComplete bool   `json:"sample_coverage_complete"`
	SampleShortfallCount   int    `json:"sample_shortfall_count"`
	TargetBreachCount      int    `json:"target_breach_count"`
	CheckCount             int    `json:"check_count"`
	PassedCount            int    `json:"passed_count"`
	FailedCount            int    `json:"failed_count"`
	InconclusiveCount      int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-beta-operations", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to production platform inventory")
	plan := flags.String("plan", "", "Path to production infrastructure plan")
	change := flags.String("change", "", "Path to ready production infrastructure change")
	release := flags.String("release", "", "Path to passed production release")
	betaSLO := flags.String("beta-slo", "", "Path to ready production beta SLO receipt")
	input := flags.String("input", "", "Path to content-free beta operations assessment")
	receiptPath := flags.String("receipt", "", "New normalized beta operations receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *plan, *change, *release, *betaSLO, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, beta-slo, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = betaoperationsevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *betaSLO, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := betaoperationsevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{
		Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true,
		DueCaseCount: receipt.DueCaseCount, WithinTargetCount: receipt.WithinTargetCount,
		LateCaseCount: receipt.LateCaseCount, OverdueOpenCount: receipt.OverdueOpenCount,
		SampleCoverageComplete: receipt.SampleCoverageComplete, SampleShortfallCount: receipt.SampleShortfallCount,
		TargetBreachCount: receipt.TargetBreachCount, CheckCount: receipt.CheckCount,
		PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode beta operations report")
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
