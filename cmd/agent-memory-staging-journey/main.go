package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/stagingjourney"
)

const reportSchemaV1 = "agent-memory-staging-client-journey-report-v1"

type dependencies struct {
	now func() time.Time
}

type report struct {
	Schema         string `json:"schema"`
	Ready          bool   `json:"ready"`
	ReceiptWritten bool   `json:"receipt_written"`
	ClientCount    int    `json:"client_count"`
	CheckCount     int    `json:"check_count"`
	PassedCount    int    `json:"passed_count"`
	FailedCount    int    `json:"failed_count"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-staging-journey", flag.ContinueOnError)
	flags.SetOutput(stderr)
	releasePath := flags.String("release", "", "Path to the passed staging release receipt")
	humanPath := flags.String("human-journey", "", "Path to the content-free human web journey receipt")
	agentPath := flags.String("agent-journey", "", "Path to the content-free scoped agent journey receipt")
	receiptPath := flags.String("receipt", "", "New path for the combined staging journey receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*releasePath, *humanPath, *agentPath, *receiptPath) {
		fmt.Fprintln(stderr, "release, human-journey, agent-journey, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	receipt, err := stagingjourney.Collect(*releasePath, *humanPath, *agentPath, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := stagingjourney.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	assessment := stagingjourney.Assess(receipt)
	result := report{
		Schema: reportSchemaV1, Ready: assessment.Ready, ReceiptWritten: true,
		ClientCount: assessment.ClientCount, CheckCount: assessment.CheckCount,
		PassedCount: assessment.PassedCount, FailedCount: assessment.FailedCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode staging journey report")
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
