package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/migrationcohortevidence"
)

const reportSchemaV1 = "agent-memory-staging-migration-cohort-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (migrationcohortevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                    string `json:"schema"`
	Ready                     bool   `json:"ready"`
	ReceiptWritten            bool   `json:"receipt_written"`
	FormatCoverageComplete    bool   `json:"format_coverage_complete"`
	SizeCoverageComplete      bool   `json:"size_coverage_complete"`
	ReconciliationComplete    bool   `json:"reconciliation_complete"`
	AccountCount              int    `json:"account_count"`
	LibraryCount              int    `json:"library_count"`
	SourceCount               int    `json:"source_count"`
	ExpectedItemCount         int    `json:"expected_item_count"`
	FailedItemCount           int    `json:"failed_item_count"`
	UnexplainedLossCount      int    `json:"unexplained_loss_count"`
	DuplicatePublicationCount int    `json:"duplicate_publication_count"`
	CheckCount                int    `json:"check_count"`
	PassedCount               int    `json:"passed_count"`
	FailedCount               int    `json:"failed_count"`
	InconclusiveCount         int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-migration-cohort", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to installed staging platform inventory")
	plan := flags.String("plan", "", "Path to ready staging infrastructure plan")
	change := flags.String("change", "", "Path to ready staging infrastructure change receipt")
	release := flags.String("release", "", "Path to passed staging release receipt")
	input := flags.String("input", "", "Path to content-free migration cohort input")
	receiptPath := flags.String("receipt", "", "New normalized CP9-A receipt path")
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
		deps.collect = migrationcohortevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := migrationcohortevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true,
		FormatCoverageComplete: receipt.FormatCoverageComplete, SizeCoverageComplete: receipt.SizeCoverageComplete,
		ReconciliationComplete: receipt.ReconciliationComplete, AccountCount: receipt.AccountCount,
		LibraryCount: receipt.LibraryCount, SourceCount: receipt.SourceCount, ExpectedItemCount: receipt.ExpectedItemCount,
		FailedItemCount: receipt.FailedItemCount, UnexplainedLossCount: receipt.UnexplainedLossCount,
		DuplicatePublicationCount: receipt.DuplicatePublicationCount, CheckCount: receipt.CheckCount,
		PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode migration cohort report")
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
