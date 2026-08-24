package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/objectcustody"
)

const reportSchemaV1 = "agent-memory-staging-object-custody-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (objectcustody.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema         string `json:"schema"`
	Ready          bool   `json:"ready"`
	ReceiptWritten bool   `json:"receipt_written"`
	CheckCount     int    `json:"check_count"`
	PassedCount    int    `json:"passed_count"`
	FailedCount    int    `json:"failed_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}
func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-object-custody", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the self-managed platform inventory receipt")
	planPath := flags.String("plan", "", "Path to the self-managed infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the ready self-managed infrastructure change receipt")
	releasePath := flags.String("release", "", "Path to the passed staging Kubernetes release receipt")
	reviewPath := flags.String("review", "", "Path to the content-free object-custody review")
	receiptPath := flags.String("receipt", "", "New path for the normalized object-custody receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *planPath, *changePath, *releasePath, *reviewPath, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, review, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = objectcustody.Collect
	}
	receipt, err := deps.collect(*inventoryPath, *planPath, *changePath, *releasePath, *reviewPath, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := objectcustody.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode object custody report")
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
