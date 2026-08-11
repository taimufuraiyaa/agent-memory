package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaintegrityevidence"
)

const reportSchemaV1 = "agent-memory-production-beta-integrity-report-v1"

type collectFunc func(string, string, string, string, string, string, string, time.Time) (betaintegrityevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                               string `json:"schema"`
	Ready                                bool   `json:"ready"`
	ReceiptWritten                       bool   `json:"receipt_written"`
	ChainCoverageComplete                bool   `json:"chain_coverage_complete"`
	ArchiveReconciliationComplete        bool   `json:"archive_reconciliation_complete"`
	IsolationClassificationComplete      bool   `json:"isolation_classification_complete"`
	AuditIntegrityClassificationComplete bool   `json:"audit_integrity_classification_complete"`
	FindingClosureComplete               bool   `json:"finding_closure_complete"`
	IntegrityBreachCount                 int    `json:"integrity_breach_count"`
	UnexplainedSignalCount               int    `json:"unexplained_signal_count"`
	UnclassifiedSignalCount              int    `json:"unclassified_signal_count"`
	OpenFindingCount                     int    `json:"open_finding_count"`
	CheckCount                           int    `json:"check_count"`
	PassedCount                          int    `json:"passed_count"`
	FailedCount                          int    `json:"failed_count"`
	InconclusiveCount                    int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-beta-integrity", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to production platform inventory")
	plan := flags.String("plan", "", "Path to production infrastructure plan")
	change := flags.String("change", "", "Path to ready production infrastructure change")
	release := flags.String("release", "", "Path to passed production release")
	betaSLO := flags.String("beta-slo", "", "Path to ready production beta SLO receipt")
	betaOperations := flags.String("beta-operations", "", "Path to ready production beta operations receipt")
	input := flags.String("input", "", "Path to content-free beta integrity review")
	receiptPath := flags.String("receipt", "", "New normalized beta integrity receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *plan, *change, *release, *betaSLO, *betaOperations, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, beta-slo, beta-operations, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = betaintegrityevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *betaSLO, *betaOperations, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := betaintegrityevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, ChainCoverageComplete: receipt.ChainCoverageComplete, ArchiveReconciliationComplete: receipt.ArchiveReconciliationComplete, IsolationClassificationComplete: receipt.IsolationClassificationComplete, AuditIntegrityClassificationComplete: receipt.AuditIntegrityClassificationComplete, FindingClosureComplete: receipt.FindingClosureComplete, IntegrityBreachCount: receipt.IntegrityBreachCount, UnexplainedSignalCount: receipt.UnexplainedSignalCount, UnclassifiedSignalCount: receipt.UnclassifiedSignalCount, OpenFindingCount: receipt.OpenFindingCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode beta integrity report")
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
