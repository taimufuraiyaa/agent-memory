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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchstate"
)

const reportSchemaV1 = "agent-memory-staging-safe-platform-launch-state-report-v1"

type collectFunc func(context.Context, string, string, string, string, string, time.Time) (launchstate.Receipt, error)
type dependencies struct {
	now         func() time.Time
	postgresURL func() string
	collect     collectFunc
}
type report struct {
	Schema         string `json:"schema"`
	Ready          bool   `json:"ready"`
	ReceiptWritten bool   `json:"receipt_written"`
	Phase          string `json:"phase"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-launch-state", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the self-managed platform inventory receipt")
	planPath := flags.String("plan", "", "Path to the self-managed infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the ready self-managed infrastructure change receipt")
	releasePath := flags.String("release", "", "Path to the passed staging Kubernetes release receipt")
	receiptPath := flags.String("receipt", "", "New path for the safe-platform launch-state receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *planPath, *changePath, *releasePath, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.postgresURL == nil {
		deps.postgresURL = func() string { return strings.TrimSpace(os.Getenv("AGENT_MEMORY_POSTGRES_URL")) }
	}
	if deps.collect == nil {
		deps.collect = launchstate.Collect
	}
	connectionURL := deps.postgresURL()
	if strings.TrimSpace(connectionURL) == "" {
		fmt.Fprintln(stderr, "AGENT_MEMORY_POSTGRES_URL is required")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	receipt, err := deps.collect(ctx, *inventoryPath, *planPath, *changePath, *releasePath, connectionURL, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := launchstate.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, Phase: receipt.Phase}); err != nil {
		fmt.Fprintln(stderr, "encode safe-platform launch-state report")
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
