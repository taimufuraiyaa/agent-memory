package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/securityclosureevidence"
)

const reportSchemaV1 = "agent-memory-staging-security-closure-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (securityclosureevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                          string `json:"schema"`
	Ready                           bool   `json:"ready"`
	ReceiptWritten                  bool   `json:"receipt_written"`
	CoverageComplete                bool   `json:"coverage_complete"`
	ExpectedTargetCount             int    `json:"expected_target_count"`
	ObservedTargetCount             int    `json:"observed_target_count"`
	FindingCount                    int    `json:"finding_count"`
	BlockingFindingCount            int    `json:"blocking_finding_count"`
	OpenBlockingFindingCount        int    `json:"open_blocking_finding_count"`
	RetestIncompleteCount           int    `json:"retest_incomplete_count"`
	InconclusiveClassificationCount int    `json:"inconclusive_classification_count"`
	SourceCount                     int    `json:"source_count"`
	CheckCount                      int    `json:"check_count"`
	PassedCount                     int    `json:"passed_count"`
	FailedCount                     int    `json:"failed_count"`
	InconclusiveCount               int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}
func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-security-closure", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to installed staging platform inventory")
	plan := flags.String("plan", "", "Path to ready staging infrastructure plan")
	change := flags.String("change", "", "Path to ready staging infrastructure change receipt")
	release := flags.String("release", "", "Path to passed staging release receipt")
	input := flags.String("input", "", "Path to content-free security closure input")
	receiptPath := flags.String("receipt", "", "New normalized P10.2-B receipt path")
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
		deps.collect = securityclosureevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := securityclosureevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, CoverageComplete: receipt.CoverageComplete, ExpectedTargetCount: receipt.ExpectedTargetCount, ObservedTargetCount: receipt.ObservedTargetCount, FindingCount: receipt.FindingCount, BlockingFindingCount: receipt.BlockingFindingCount, OpenBlockingFindingCount: receipt.OpenBlockingFindingCount, RetestIncompleteCount: receipt.RetestIncompleteCount, InconclusiveClassificationCount: receipt.InconclusiveClassificationCount, SourceCount: receipt.SourceCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode security closure report")
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
