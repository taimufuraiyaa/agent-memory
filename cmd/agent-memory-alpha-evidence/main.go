package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/alphaevidence"
)

const reportSchemaV1 = "agent-memory-staging-internal-alpha-report-v1"

type dependencies struct {
	now     func() time.Time
	collect func(string, string, string, string, string, string, time.Time) (alphaevidence.Receipt, error)
	publish func(string, alphaevidence.Receipt) error
}

type report struct {
	Schema            string `json:"schema"`
	Ready             bool   `json:"ready"`
	ReceiptWritten    bool   `json:"receipt_written"`
	AccountCount      int    `json:"account_count"`
	SourceCount       int    `json:"source_count"`
	FormatCount       int    `json:"format_count"`
	StageCount        int    `json:"stage_count"`
	SupportCaseCount  int    `json:"support_case_count"`
	AlphaDays         int    `json:"alpha_days"`
	TargetBreachCount int    `json:"target_breach_count"`
	CheckCount        int    `json:"check_count"`
	PassedCount       int    `json:"passed_count"`
	FailedCount       int    `json:"failed_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-alpha-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Self-managed staging inventory path")
	plan := flags.String("plan", "", "Reviewed staging plan path")
	change := flags.String("change", "", "Ready applied-change receipt path")
	release := flags.String("release", "", "Passed staging release receipt path")
	journey := flags.String("journey", "", "Ready CP3-A staging journey receipt path")
	input := flags.String("input", "", "Content-free P10.1-A alpha evidence input path")
	receiptPath := flags.String("receipt", "", "New normalized P10.1-A receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *plan, *change, *release, *journey, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, journey, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = alphaevidence.Collect
	}
	if deps.publish == nil {
		deps.publish = alphaevidence.Publish
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *journey, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := deps.publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, AccountCount: receipt.AccountCount, SourceCount: receipt.SourceCount, FormatCount: receipt.FormatCount, StageCount: receipt.StageCount, SupportCaseCount: receipt.SupportCaseCount, AlphaDays: receipt.AlphaDays, TargetBreachCount: receipt.TargetBreachCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode internal-alpha report")
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
