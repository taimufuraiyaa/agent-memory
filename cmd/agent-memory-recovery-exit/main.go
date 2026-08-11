package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/recoveryexitevidence"
)

const reportSchemaV1 = "agent-memory-component-recovery-exit-report-v1"

type collectFunc func(string, string, time.Time) (recoveryexitevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                     string `json:"schema"`
	Ready                      bool   `json:"ready"`
	ReceiptWritten             bool   `json:"receipt_written"`
	SubjectCount               int    `json:"subject_count"`
	ComponentCount             int    `json:"component_count"`
	IntegrationCount           int    `json:"integration_count"`
	EnabledIntegrationCount    int    `json:"enabled_integration_count"`
	PassedSubjectCount         int    `json:"passed_subject_count"`
	FailedSubjectCount         int    `json:"failed_subject_count"`
	InconclusiveSubjectCount   int    `json:"inconclusive_subject_count"`
	OperationCount             int    `json:"operation_count"`
	PassedOperationCount       int    `json:"passed_operation_count"`
	FailedOperationCount       int    `json:"failed_operation_count"`
	InconclusiveOperationCount int    `json:"inconclusive_operation_count"`
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
	flags := flag.NewFlagSet("agent-memory-recovery-exit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to self-managed platform inventory")
	input := flags.String("input", "", "Path to content-free recovery/exit input")
	receiptPath := flags.String("receipt", "", "New normalized P0.2-B receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = recoveryexitevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err = recoveryexitevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, SubjectCount: receipt.SubjectCount, ComponentCount: receipt.ComponentCount, IntegrationCount: receipt.IntegrationCount, EnabledIntegrationCount: receipt.EnabledIntegrationCount, PassedSubjectCount: receipt.PassedSubjectCount, FailedSubjectCount: receipt.FailedSubjectCount, InconclusiveSubjectCount: receipt.InconclusiveSubjectCount, OperationCount: receipt.OperationCount, PassedOperationCount: receipt.PassedOperationCount, FailedOperationCount: receipt.FailedOperationCount, InconclusiveOperationCount: receipt.InconclusiveOperationCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err = json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode recovery-exit report")
		return 1
	}
	if !receipt.Ready {
		return 3
	}
	return 0
}
func anyBlank(values ...string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return true
		}
	}
	return false
}
