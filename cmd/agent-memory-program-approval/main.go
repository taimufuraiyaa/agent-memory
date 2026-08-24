package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/programapprovalevidence"
)

const reportSchemaV1 = "agent-memory-checkpoint-zero-program-report-v1"

type collectFunc func(string, string, string, string, time.Time) (programapprovalevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                     string `json:"schema"`
	Ready                      bool   `json:"ready"`
	ReceiptWritten             bool   `json:"receipt_written"`
	BlockerCategoryCount       int    `json:"blocker_category_count"`
	TotalBlockerCount          int    `json:"total_blocker_count"`
	DeferredBlockerCount       int    `json:"deferred_blocker_count"`
	OpenBlockerCount           int    `json:"open_blocker_count"`
	UnownedBlockerCount        int    `json:"unowned_blocker_count"`
	StaffingDomainCount        int    `json:"staffing_domain_count"`
	CoveredStaffingDomainCount int    `json:"covered_staffing_domain_count"`
	BetaAccountCap             int    `json:"beta_account_cap"`
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
	flags := flag.NewFlagSet("agent-memory-program-approval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to self-managed platform inventory")
	launchScope := flags.String("launch-scope-receipt", "", "Path to ready P0.1 receipt")
	integration := flags.String("integration-receipt", "", "Path to ready P0.2-C receipt")
	input := flags.String("input", "", "Path to content-free CP0 program review input")
	receiptPath := flags.String("receipt", "", "New normalized CP0 receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *launchScope, *integration, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, launch-scope-receipt, integration-receipt, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = programapprovalevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *launchScope, *integration, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := programapprovalevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, BlockerCategoryCount: receipt.BlockerCategoryCount,
		TotalBlockerCount: receipt.TotalBlockerCount, DeferredBlockerCount: receipt.DeferredBlockerCount, OpenBlockerCount: receipt.OpenBlockerCount, UnownedBlockerCount: receipt.UnownedBlockerCount,
		StaffingDomainCount: receipt.StaffingDomainCount, CoveredStaffingDomainCount: receipt.CoveredStaffingDomainCount, BetaAccountCap: receipt.BetaAccountCap,
		CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode checkpoint-zero report")
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
