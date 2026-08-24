package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/gadrillevidence"
)

const reportSchemaV1 = "agent-memory-production-ga-drill-report-v1"

type collectFunc func(string, string, time.Time) (gadrillevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                 string `json:"schema"`
	Ready                  bool   `json:"ready"`
	ReceiptWritten         bool   `json:"receipt_written"`
	DrillCount             int    `json:"drill_count"`
	ScenarioCount          int    `json:"scenario_count"`
	PassedDrillCount       int    `json:"passed_drill_count"`
	FailedDrillCount       int    `json:"failed_drill_count"`
	InconclusiveDrillCount int    `json:"inconclusive_drill_count"`
	CheckCount             int    `json:"check_count"`
	PassedCheckCount       int    `json:"passed_check_count"`
	FailedCheckCount       int    `json:"failed_check_count"`
	InconclusiveCheckCount int    `json:"inconclusive_check_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}
func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-ga-drills", flag.ContinueOnError)
	flags.SetOutput(stderr)
	scorecard := flags.String("ga-scorecard", "", "Path to ready P12.2-A receipt")
	input := flags.String("input", "", "Path to content-free repeated GA drill input")
	receiptPath := flags.String("receipt", "", "New normalized P12.2-B receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*scorecard, *input, *receiptPath) {
		fmt.Fprintln(stderr, "ga-scorecard, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = gadrillevidence.Collect
	}
	receipt, err := deps.collect(*scorecard, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := gadrillevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, DrillCount: receipt.DrillCount, ScenarioCount: receipt.ScenarioCount, PassedDrillCount: receipt.PassedDrillCount, FailedDrillCount: receipt.FailedDrillCount, InconclusiveDrillCount: receipt.InconclusiveDrillCount, CheckCount: receipt.CheckCount, PassedCheckCount: receipt.PassedCheckCount, FailedCheckCount: receipt.FailedCheckCount, InconclusiveCheckCount: receipt.InconclusiveCheckCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode GA drill report")
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
