package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/publicbetagateevidence"
)

const reportSchemaV1 = "agent-memory-production-public-beta-gate-report-v1"

type collectFunc func(string, string, string, string, string, string, string, string, string, time.Time) (publicbetagateevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                              string `json:"schema"`
	Ready                               bool   `json:"ready"`
	ReceiptWritten                      bool   `json:"receipt_written"`
	AbuseClassificationComplete         bool   `json:"abuse_classification_complete"`
	CostWithinCeiling                   bool   `json:"cost_within_ceiling"`
	OpenLaunchBlockingAbuseFindingCount int    `json:"open_launch_blocking_abuse_finding_count"`
	UnclassifiedAbuseFindingCount       int    `json:"unclassified_abuse_finding_count"`
	ActualCostPerActiveTenantMicroUSD   int64  `json:"actual_cost_per_active_tenant_microusd"`
	CheckCount                          int    `json:"check_count"`
	PassedCount                         int    `json:"passed_count"`
	FailedCount                         int    `json:"failed_count"`
	InconclusiveCount                   int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-public-beta-gate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to production platform inventory")
	plan := flags.String("plan", "", "Path to production infrastructure plan")
	change := flags.String("change", "", "Path to ready production infrastructure change")
	release := flags.String("release", "", "Path to passed production release")
	billing := flags.String("billing", "", "Path to ready production billing receipt")
	slo := flags.String("beta-slo", "", "Path to ready beta SLO receipt")
	operations := flags.String("beta-operations", "", "Path to ready beta operations receipt")
	integrity := flags.String("beta-integrity", "", "Path to ready beta integrity receipt")
	input := flags.String("input", "", "Path to content-free public-beta gate review")
	receiptPath := flags.String("receipt", "", "New normalized public-beta gate receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *plan, *change, *release, *billing, *slo, *operations, *integrity, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, billing, beta-slo, beta-operations, beta-integrity, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = publicbetagateevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *billing, *slo, *operations, *integrity, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := publicbetagateevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{
		Schema:                              reportSchemaV1,
		Ready:                               receipt.Ready,
		ReceiptWritten:                      true,
		AbuseClassificationComplete:         receipt.AbuseClassificationComplete,
		CostWithinCeiling:                   receipt.CostWithinCeiling,
		OpenLaunchBlockingAbuseFindingCount: receipt.OpenLaunchBlockingAbuseFindingCount,
		UnclassifiedAbuseFindingCount:       receipt.UnclassifiedAbuseFindingCount,
		ActualCostPerActiveTenantMicroUSD:   receipt.ActualCostPerActiveTenantMicroUSD,
		CheckCount:                          receipt.CheckCount,
		PassedCount:                         receipt.PassedCount,
		FailedCount:                         receipt.FailedCount,
		InconclusiveCount:                   receipt.InconclusiveCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode public beta gate report")
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
