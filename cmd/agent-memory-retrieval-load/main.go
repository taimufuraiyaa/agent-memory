package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrievalload"
)

const reportSchemaV1 = "agent-memory-staging-retrieval-load-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (retrievalload.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                                   string `json:"schema"`
	Ready                                    bool   `json:"ready"`
	ReceiptWritten                           bool   `json:"receipt_written"`
	CorpusSourceCount                        int    `json:"corpus_source_count"`
	CorpusPassageCount                       int    `json:"corpus_passage_count"`
	RequestCount                             int    `json:"request_count"`
	Concurrency                              int    `json:"concurrency"`
	ErrorCount                               int    `json:"error_count"`
	ModelCallCount                           int    `json:"model_call_count"`
	P50LatencyMicroseconds                   int64  `json:"p50_latency_microseconds"`
	P95LatencyMicroseconds                   int64  `json:"p95_latency_microseconds"`
	P99LatencyMicroseconds                   int64  `json:"p99_latency_microseconds"`
	SearchP95TargetMicroseconds              int64  `json:"search_p95_target_microseconds"`
	MaximumModelCostMicroUSDPer1000Requests  int64  `json:"maximum_model_cost_microusd_per_1000_requests"`
	ObservedModelCostMicroUSDPer1000Requests int64  `json:"observed_model_cost_microusd_per_1000_requests"`
	CheckCount                               int    `json:"check_count"`
	PassedCount                              int    `json:"passed_count"`
	FailedCount                              int    `json:"failed_count"`
	InconclusiveCount                        int    `json:"inconclusive_count"`
	MetricBreachCount                        int    `json:"metric_breach_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}
func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-retrieval-load", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the self-managed platform inventory receipt")
	planPath := flags.String("plan", "", "Path to the self-managed infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the ready self-managed infrastructure change receipt")
	releasePath := flags.String("release", "", "Path to the passed staging Kubernetes release receipt")
	inputPath := flags.String("input", "", "Path to the content-free deployed retrieval load input")
	receiptPath := flags.String("receipt", "", "New path for the normalized retrieval load receipt")
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
		deps.collect = retrievalload.Collect
	}
	receipt, err := deps.collect(*inventoryPath, *planPath, *changePath, *releasePath, *inputPath, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := retrievalload.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, CorpusSourceCount: receipt.CorpusSourceCount, CorpusPassageCount: receipt.CorpusPassageCount, RequestCount: receipt.RequestCount, Concurrency: receipt.Concurrency, ErrorCount: receipt.ErrorCount, ModelCallCount: receipt.ModelCallCount, P50LatencyMicroseconds: receipt.P50LatencyMicroseconds, P95LatencyMicroseconds: receipt.P95LatencyMicroseconds, P99LatencyMicroseconds: receipt.P99LatencyMicroseconds, SearchP95TargetMicroseconds: receipt.SearchP95TargetMicroseconds, MaximumModelCostMicroUSDPer1000Requests: receipt.MaximumModelCostMicroUSDPer1000Requests, ObservedModelCostMicroUSDPer1000Requests: receipt.ObservedModelCostMicroUSDPer1000Requests, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount, MetricBreachCount: receipt.MetricBreachCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode retrieval load report")
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
