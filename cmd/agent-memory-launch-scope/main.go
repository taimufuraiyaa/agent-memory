package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchscopeevidence"
)

const reportSchemaV1 = "agent-memory-launch-scope-report-v1"

type collectFunc func(string, time.Time) (launchscopeevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                  string `json:"schema"`
	Ready                   bool   `json:"ready"`
	ReceiptWritten          bool   `json:"receipt_written"`
	LaunchCountryCount      int    `json:"launch_country_count"`
	SupportLanguageCount    int    `json:"support_language_count"`
	NoticeJurisdictionCount int    `json:"notice_jurisdiction_count"`
	BlockingRiskCount       int    `json:"blocking_risk_count"`
	UnownedRiskCount        int    `json:"unowned_risk_count"`
	DeferredRiskCount       int    `json:"deferred_risk_count"`
	LegalPositionCount      int    `json:"legal_position_count"`
	LegalPassedCount        int    `json:"legal_passed_count"`
	LegalFailedCount        int    `json:"legal_failed_count"`
	LegalInconclusiveCount  int    `json:"legal_inconclusive_count"`
	CheckCount              int    `json:"check_count"`
	PassedCount             int    `json:"passed_count"`
	FailedCount             int    `json:"failed_count"`
	InconclusiveCount       int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-launch-scope", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "Path to content-free launch-scope and legal-review input")
	receiptPath := flags.String("receipt", "", "New normalized P0.1 receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*input) == "" || strings.TrimSpace(*receiptPath) == "" {
		fmt.Fprintln(stderr, "input and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = launchscopeevidence.Collect
	}
	receipt, err := deps.collect(*input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := launchscopeevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true,
		LaunchCountryCount: receipt.LaunchCountryCount, SupportLanguageCount: receipt.SupportLanguageCount, NoticeJurisdictionCount: receipt.NoticeJurisdictionCount,
		BlockingRiskCount: receipt.BlockingRiskCount, UnownedRiskCount: receipt.UnownedRiskCount, DeferredRiskCount: receipt.DeferredRiskCount,
		LegalPositionCount: receipt.LegalPositionCount, LegalPassedCount: receipt.LegalPassedCount, LegalFailedCount: receipt.LegalFailedCount, LegalInconclusiveCount: receipt.LegalInconclusiveCount,
		CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode launch-scope report")
		return 1
	}
	if !receipt.Ready {
		return 3
	}
	return 0
}
