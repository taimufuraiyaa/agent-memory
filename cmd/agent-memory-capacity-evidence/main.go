package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/capacityevidence"
)

const reportSchemaV1 = "agent-memory-staging-capacity-economics-report-v1"

type collectFunc func(string, string, string, string, string, string, time.Time) (capacityevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                                string `json:"schema"`
	Ready                                 bool   `json:"ready"`
	ReceiptWritten                        bool   `json:"receipt_written"`
	BetaAccountCap                        int    `json:"beta_account_cap"`
	PlannedPeakConcurrentTenants          int    `json:"planned_peak_concurrent_tenants"`
	SupportedConcurrentTenants            int    `json:"supported_concurrent_tenants"`
	PlannedPeakRetrievalRequestsPerMinute int64  `json:"planned_peak_retrieval_requests_per_minute"`
	SustainedRetrievalRequestsPerMinute   int64  `json:"sustained_retrieval_requests_per_minute"`
	EstimatedWorstCaseMonthlyCostMicroUSD int64  `json:"estimated_worst_case_monthly_cost_microusd"`
	ApprovedMonthlyCostCeilingMicroUSD    int64  `json:"approved_monthly_cost_ceiling_microusd"`
	CheckCount                            int    `json:"check_count"`
	PassedCount                           int    `json:"passed_count"`
	FailedCount                           int    `json:"failed_count"`
	InconclusiveCount                     int    `json:"inconclusive_count"`
	MetricBreachCount                     int    `json:"metric_breach_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}
func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-capacity-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to platform inventory")
	plan := flags.String("plan", "", "Path to infrastructure plan")
	change := flags.String("change", "", "Path to ready infrastructure change")
	release := flags.String("release", "", "Path to passed staging release")
	load := flags.String("retrieval-load", "", "Path to ready CP5-C retrieval-load receipt")
	input := flags.String("input", "", "Path to content-free capacity assessment")
	receiptPath := flags.String("receipt", "", "New normalized capacity receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *plan, *change, *release, *load, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, retrieval-load, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = capacityevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *load, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := capacityevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, BetaAccountCap: receipt.BetaAccountCap, PlannedPeakConcurrentTenants: receipt.PlannedPeakConcurrentTenants, SupportedConcurrentTenants: receipt.SupportedConcurrentTenants, PlannedPeakRetrievalRequestsPerMinute: receipt.PlannedPeakRetrievalRequestsPerMinute, SustainedRetrievalRequestsPerMinute: receipt.SustainedRetrievalRequestsPerMinute, EstimatedWorstCaseMonthlyCostMicroUSD: receipt.EstimatedWorstCaseMonthlyCostMicroUSD, ApprovedMonthlyCostCeilingMicroUSD: receipt.ApprovedMonthlyCostCeilingMicroUSD, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount, MetricBreachCount: receipt.MetricBreachCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode capacity report")
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
