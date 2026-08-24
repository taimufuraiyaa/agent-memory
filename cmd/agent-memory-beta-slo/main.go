package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
)

const reportSchemaV1 = "agent-memory-production-beta-slo-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (betasloevidence.Receipt, error)

type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                     string `json:"schema"`
	Ready                      bool   `json:"ready"`
	ReceiptWritten             bool   `json:"receipt_written"`
	ObservationDurationSeconds int64  `json:"observation_duration_seconds"`
	CoverageComplete           bool   `json:"coverage_complete"`
	CoverageShortfallCount     int    `json:"coverage_shortfall_count"`
	MetricCount                int    `json:"metric_count"`
	MetricBreachCount          int    `json:"metric_breach_count"`
	CheckCount                 int    `json:"check_count"`
	PassedCount                int    `json:"passed_count"`
	FailedCount                int    `json:"failed_count"`
	InconclusiveCount          int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-beta-slo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to production platform inventory")
	plan := flags.String("plan", "", "Path to production infrastructure plan")
	change := flags.String("change", "", "Path to ready production infrastructure change")
	release := flags.String("release", "", "Path to passed production release")
	input := flags.String("input", "", "Path to content-free beta SLO observation")
	receiptPath := flags.String("receipt", "", "New normalized beta SLO receipt path")
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
		deps.collect = betasloevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := betasloevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{
		Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true,
		ObservationDurationSeconds: receipt.ObservationDurationSeconds,
		CoverageComplete:           receipt.CoverageComplete, CoverageShortfallCount: receipt.CoverageShortfallCount,
		MetricCount: len(receipt.MetricResults), MetricBreachCount: receipt.MetricBreachCount,
		CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount,
		FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode beta SLO report")
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
