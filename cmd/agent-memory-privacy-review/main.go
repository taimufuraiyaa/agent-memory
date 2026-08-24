package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/privacyreviewevidence"
)

const reportSchemaV1 = "agent-memory-privacy-review-report-v1"

type dependencies struct {
	now     func() time.Time
	collect func(string, time.Time) (privacyreviewevidence.Receipt, error)
	publish func(string, privacyreviewevidence.Receipt) error
}
type report struct {
	Schema                    string `json:"schema"`
	Ready                     bool   `json:"ready"`
	ReceiptWritten            bool   `json:"receipt_written"`
	SurfaceCount              int    `json:"surface_count"`
	SurfacePassedCount        int    `json:"surface_passed_count"`
	SurfaceFailedCount        int    `json:"surface_failed_count"`
	SurfaceInconclusiveCount  int    `json:"surface_inconclusive_count"`
	ContractCount             int    `json:"contract_count"`
	ContractPassedCount       int    `json:"contract_passed_count"`
	ContractFailedCount       int    `json:"contract_failed_count"`
	ContractInconclusiveCount int    `json:"contract_inconclusive_count"`
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
	flags := flag.NewFlagSet("agent-memory-privacy-review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "Path to content-free CP7-A review input")
	receiptPath := flags.String("receipt", "", "New normalized CP7-A receipt path")
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
		deps.collect = privacyreviewevidence.Collect
	}
	if deps.publish == nil {
		deps.publish = privacyreviewevidence.Publish
	}
	receipt, err := deps.collect(*input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err = deps.publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, SurfaceCount: receipt.SurfaceCount, SurfacePassedCount: receipt.SurfacePassedCount, SurfaceFailedCount: receipt.SurfaceFailedCount, SurfaceInconclusiveCount: receipt.SurfaceInconclusiveCount, ContractCount: receipt.ContractCount, ContractPassedCount: receipt.ContractPassedCount, ContractFailedCount: receipt.ContractFailedCount, ContractInconclusiveCount: receipt.ContractInconclusiveCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err = json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode privacy review report")
		return 1
	}
	if !receipt.Ready {
		return 3
	}
	return 0
}
