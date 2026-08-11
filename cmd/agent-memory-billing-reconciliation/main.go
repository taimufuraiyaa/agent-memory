package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/billingreconciliation"
)

const reportSchemaV1 = "agent-memory-production-billing-reconciliation-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (billingreconciliation.Receipt, error)

type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                     string `json:"schema"`
	Ready                      bool   `json:"ready"`
	ReceiptWritten             bool   `json:"receipt_written"`
	TenantSampleCount          int    `json:"tenant_sample_count"`
	ProcessorInvoiceCount      int    `json:"processor_invoice_count"`
	MatchedInvoiceCount        int    `json:"matched_invoice_count"`
	ProcessorSettlementCount   int    `json:"processor_settlement_count"`
	MatchedSettlementCount     int    `json:"matched_settlement_count"`
	UsageSampleCount           int    `json:"usage_sample_count"`
	MatchedUsageSampleCount    int    `json:"matched_usage_sample_count"`
	InvoiceVarianceMicroUSD    int64  `json:"invoice_variance_microusd"`
	SettlementVarianceMicroUSD int64  `json:"settlement_variance_microusd"`
	UsageVarianceQuantity      int64  `json:"usage_variance_quantity"`
	CoverageComplete           bool   `json:"coverage_complete"`
	VarianceBreachCount        int    `json:"variance_breach_count"`
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
	flags := flag.NewFlagSet("agent-memory-billing-reconciliation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to production platform inventory")
	plan := flags.String("plan", "", "Path to production infrastructure plan")
	change := flags.String("change", "", "Path to ready production infrastructure change")
	release := flags.String("release", "", "Path to passed production release")
	input := flags.String("input", "", "Path to content-free billing reconciliation")
	receiptPath := flags.String("receipt", "", "New normalized billing receipt path")
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
		deps.collect = billingreconciliation.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := billingreconciliation.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{
		Schema:                     reportSchemaV1,
		Ready:                      receipt.Ready,
		ReceiptWritten:             true,
		TenantSampleCount:          receipt.TenantSampleCount,
		ProcessorInvoiceCount:      receipt.ProcessorInvoiceCount,
		MatchedInvoiceCount:        receipt.MatchedInvoiceCount,
		ProcessorSettlementCount:   receipt.ProcessorSettlementCount,
		MatchedSettlementCount:     receipt.MatchedSettlementCount,
		UsageSampleCount:           receipt.UsageSampleCount,
		MatchedUsageSampleCount:    receipt.MatchedUsageSampleCount,
		InvoiceVarianceMicroUSD:    receipt.InvoiceVarianceMicroUSD,
		SettlementVarianceMicroUSD: receipt.SettlementVarianceMicroUSD,
		UsageVarianceQuantity:      receipt.UsageVarianceQuantity,
		CoverageComplete:           receipt.CoverageComplete,
		VarianceBreachCount:        receipt.VarianceBreachCount,
		CheckCount:                 receipt.CheckCount,
		PassedCount:                receipt.PassedCount,
		FailedCount:                receipt.FailedCount,
		InconclusiveCount:          receipt.InconclusiveCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode billing reconciliation report")
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
