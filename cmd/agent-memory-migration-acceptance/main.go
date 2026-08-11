package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/migrationacceptanceevidence"
)

const reportSchemaV1 = "agent-memory-staging-migration-acceptance-report-v1"

type collectFunc func(string, string, string, time.Time) (migrationacceptanceevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema            string `json:"schema"`
	Ready             bool   `json:"ready"`
	ReceiptWritten    bool   `json:"receipt_written"`
	CheckCount        int    `json:"check_count"`
	PassedCount       int    `json:"passed_count"`
	FailedCount       int    `json:"failed_count"`
	InconclusiveCount int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-migration-acceptance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cohort := flags.String("cohort", "", "Path to ready CP9-A migration cohort receipt")
	parity := flags.String("parity", "", "Path to ready CP5-A retrieval parity receipt")
	input := flags.String("input", "", "Path to content-free rollback acceptance input")
	receiptPath := flags.String("receipt", "", "New normalized CP9-B receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*cohort, *parity, *input, *receiptPath) {
		fmt.Fprintln(stderr, "cohort, parity, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = migrationacceptanceevidence.Collect
	}
	receipt, err := deps.collect(*cohort, *parity, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := migrationacceptanceevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true,
		CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount,
		FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode migration acceptance report")
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
