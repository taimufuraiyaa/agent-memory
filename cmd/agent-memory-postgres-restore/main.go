package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/postgresrestore"
)

const reportSchemaV1 = "agent-memory-self-managed-postgres-restore-report-v1"

type dependencies struct{ now func() time.Time }

type report struct {
	Schema         string `json:"schema"`
	Ready          bool   `json:"ready"`
	ReceiptWritten bool   `json:"receipt_written"`
	CheckCount     int    `json:"check_count"`
	PassedCount    int    `json:"passed_count"`
	FailedCount    int    `json:"failed_count"`
	RPOSeconds     int64  `json:"rpo_seconds"`
	RTOSeconds     int64  `json:"rto_seconds"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-postgres-restore", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the self-managed platform inventory receipt")
	planPath := flags.String("plan", "", "Path to the self-managed infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the ready self-managed infrastructure change receipt")
	drillPath := flags.String("drill", "", "Path to the content-free PostgreSQL restore drill receipt")
	receiptPath := flags.String("receipt", "", "New path for the normalized restore receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *planPath, *changePath, *drillPath, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, drill, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	receipt, err := postgresrestore.Collect(*inventoryPath, *planPath, *changePath, *drillPath, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := postgresrestore.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	assessment := postgresrestore.Assess(receipt)
	result := report{
		Schema: reportSchemaV1, Ready: assessment.Ready, ReceiptWritten: true,
		CheckCount: assessment.CheckCount, PassedCount: assessment.PassedCount,
		FailedCount: assessment.FailedCount, RPOSeconds: assessment.RPOSeconds,
		RTOSeconds: assessment.RTOSeconds,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode PostgreSQL restore report")
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
