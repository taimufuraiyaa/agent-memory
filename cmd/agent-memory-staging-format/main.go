package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/stagingformat"
)

const reportSchemaV1 = "agent-memory-staging-format-ingestion-report-v1"

type dependencies struct{ now func() time.Time }

type report struct {
	Schema         string `json:"schema"`
	Ready          bool   `json:"ready"`
	ReceiptWritten bool   `json:"receipt_written"`
	FormatCount    int    `json:"format_count"`
	CheckCount     int    `json:"check_count"`
	PassedCount    int    `json:"passed_count"`
	FailedCount    int    `json:"failed_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-staging-format", flag.ContinueOnError)
	flags.SetOutput(stderr)
	releasePath := flags.String("release", "", "Path to the passed staging release receipt")
	inputPath := flags.String("input", "", "Path to the content-free four-format staging input")
	receiptPath := flags.String("receipt", "", "New path for the normalized four-format receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*releasePath, *inputPath, *receiptPath) {
		fmt.Fprintln(stderr, "release, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	receipt, err := stagingformat.Collect(*releasePath, *inputPath, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := stagingformat.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	assessment := stagingformat.Assess(receipt)
	result := report{
		Schema: reportSchemaV1, Ready: assessment.Ready, ReceiptWritten: true,
		FormatCount: assessment.FormatCount, CheckCount: assessment.CheckCount,
		PassedCount: assessment.PassedCount, FailedCount: assessment.FailedCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode staging format report")
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
