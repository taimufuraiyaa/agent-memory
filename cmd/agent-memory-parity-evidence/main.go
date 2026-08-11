package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/parityevidence"
)

const reportSchemaV1 = "agent-memory-staging-retrieval-parity-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (parityevidence.Receipt, error)

type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                              string  `json:"schema"`
	Ready                               bool    `json:"ready"`
	ReceiptWritten                      bool    `json:"receipt_written"`
	CaseCount                           int     `json:"case_count"`
	CheckCount                          int     `json:"check_count"`
	PassedCount                         int     `json:"passed_count"`
	FailedCount                         int     `json:"failed_count"`
	InconclusiveCount                   int     `json:"inconclusive_count"`
	MetricBreachCount                   int     `json:"metric_breach_count"`
	MinimumTopKOverlap                  float64 `json:"minimum_top_k_overlap"`
	ObservedTopKOverlap                 float64 `json:"observed_top_k_overlap"`
	MaximumNormalizedScoreDelta         float64 `json:"maximum_normalized_score_delta"`
	ObservedMaximumNormalizedScoreDelta float64 `json:"observed_maximum_normalized_score_delta"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-parity-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the self-managed platform inventory receipt")
	planPath := flags.String("plan", "", "Path to the self-managed infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the ready self-managed infrastructure change receipt")
	releasePath := flags.String("release", "", "Path to the passed staging Kubernetes release receipt")
	inputPath := flags.String("input", "", "Path to the content-free representative retrieval-parity input")
	receiptPath := flags.String("receipt", "", "New path for the normalized retrieval-parity receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *planPath, *changePath, *releasePath, *inputPath, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = parityevidence.Collect
	}
	receipt, err := deps.collect(*inventoryPath, *planPath, *changePath, *releasePath, *inputPath, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := parityevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, CaseCount: receipt.CaseCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount, MetricBreachCount: receipt.MetricBreachCount, MinimumTopKOverlap: receipt.MinimumTopKOverlap, ObservedTopKOverlap: receipt.ObservedTopKOverlap, MaximumNormalizedScoreDelta: receipt.MaximumNormalizedScoreDelta, ObservedMaximumNormalizedScoreDelta: receipt.ObservedMaximumNormalizedScoreDelta}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode retrieval parity report")
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
