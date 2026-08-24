package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/retentioninventory"
)

const reportSchemaV1 = "agent-memory-self-managed-retention-inventory-report-v1"

type collectFunc func(context.Context, string, string, string, string, time.Time) (retentioninventory.Receipt, error)

type dependencies struct {
	now         func() time.Time
	postgresURL func() string
	collect     collectFunc
}

type report struct {
	Schema         string `json:"schema"`
	Ready          bool   `json:"ready"`
	ReceiptWritten bool   `json:"receipt_written"`
	PolicyCount    int    `json:"policy_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-retention-inventory", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the self-managed platform inventory receipt")
	planPath := flags.String("plan", "", "Path to the self-managed infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the ready self-managed infrastructure change receipt")
	receiptPath := flags.String("receipt", "", "New path for the retention inventory receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *planPath, *changePath, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.postgresURL == nil {
		deps.postgresURL = func() string { return strings.TrimSpace(os.Getenv("AGENT_MEMORY_POSTGRES_URL")) }
	}
	if deps.collect == nil {
		deps.collect = retentioninventory.Collect
	}
	connectionURL := deps.postgresURL()
	if strings.TrimSpace(connectionURL) == "" {
		fmt.Fprintln(stderr, "AGENT_MEMORY_POSTGRES_URL is required")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	receipt, err := deps.collect(ctx, *inventoryPath, *planPath, *changePath, connectionURL, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := retentioninventory.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, PolicyCount: receipt.PolicyCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode retention inventory report")
		return 1
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
