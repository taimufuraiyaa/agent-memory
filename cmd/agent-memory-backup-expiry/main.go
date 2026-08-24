package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/backupexpiry"
)

const reportSchemaV1 = "agent-memory-self-managed-backup-expiry-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (backupexpiry.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                      string `json:"schema"`
	Ready                       bool   `json:"ready"`
	ReceiptWritten              bool   `json:"receipt_written"`
	CheckCount                  int    `json:"check_count"`
	PassedCount                 int    `json:"passed_count"`
	FailedCount                 int    `json:"failed_count"`
	BackupRetentionSeconds      int64  `json:"backup_retention_seconds"`
	ElapsedSinceDeletionSeconds int64  `json:"elapsed_since_deletion_seconds"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}
func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-backup-expiry", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the self-managed platform inventory receipt")
	planPath := flags.String("plan", "", "Path to the self-managed infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the ready self-managed infrastructure change receipt")
	retentionPath := flags.String("retention-inventory", "", "Path to the installed retention inventory receipt")
	drillPath := flags.String("drill", "", "Path to the content-free aged-backup drill")
	receiptPath := flags.String("receipt", "", "New path for the normalized aged-backup receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *planPath, *changePath, *retentionPath, *drillPath, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, retention-inventory, drill, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = backupexpiry.Collect
	}
	receipt, err := deps.collect(*inventoryPath, *planPath, *changePath, *retentionPath, *drillPath, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := backupexpiry.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, BackupRetentionSeconds: receipt.BackupRetentionSeconds, ElapsedSinceDeletionSeconds: receipt.ElapsedSinceDeletionSeconds}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode backup expiry report")
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
