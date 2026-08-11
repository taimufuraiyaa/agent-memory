package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/approvalexportevidence"
)

const reportSchemaV1 = "agent-memory-public-beta-approval-export-report-v1"

type collectFunc func(string, string, string, string, string, string, time.Time) (approvalexportevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                string `json:"schema"`
	Ready                 bool   `json:"ready"`
	ReceiptWritten        bool   `json:"receipt_written"`
	ApprovalArtifactCount int    `json:"approval_artifact_count"`
	RequiredControlCount  int    `json:"required_control_count"`
	VerifiedControlCount  int    `json:"verified_control_count"`
	MissingControlCount   int    `json:"missing_control_count"`
	RejectedControlCount  int    `json:"rejected_control_count"`
	ExpiredControlCount   int    `json:"expired_control_count"`
	MinimumExpirySeconds  int64  `json:"minimum_expiry_seconds"`
	CheckCount            int    `json:"check_count"`
	PassedCount           int    `json:"passed_count"`
	FailedCount           int    `json:"failed_count"`
	InconclusiveCount     int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-approval-export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	launch := flags.String("launch-assets", "", "Path to ready CP11-A launch-asset receipt")
	gate := flags.String("public-beta-gate", "", "Path to ready CP11-B public-beta gate receipt")
	trust := flags.String("approver-keys", "", "Path to independently managed approver trust bundle")
	approvals := flags.String("approvals-dir", "", "Path to immutable public-beta approval export directory")
	manifest := flags.String("export-manifest", "", "Path to exact approval-export manifest")
	input := flags.String("input", "", "Path to content-free CP11-C review input")
	receiptPath := flags.String("receipt", "", "New normalized CP11-C receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*launch, *gate, *trust, *approvals, *manifest, *input, *receiptPath) {
		fmt.Fprintln(stderr, "launch-assets, public-beta-gate, approver-keys, approvals-dir, export-manifest, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = approvalexportevidence.Collect
	}
	receipt, err := deps.collect(*launch, *gate, *trust, *approvals, *manifest, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := approvalexportevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{
		Schema:                reportSchemaV1,
		Ready:                 receipt.Ready,
		ReceiptWritten:        true,
		ApprovalArtifactCount: receipt.ApprovalArtifactCount,
		RequiredControlCount:  receipt.RequiredControlCount,
		VerifiedControlCount:  receipt.VerifiedControlCount,
		MissingControlCount:   receipt.MissingControlCount,
		RejectedControlCount:  receipt.RejectedControlCount,
		ExpiredControlCount:   receipt.ExpiredControlCount,
		MinimumExpirySeconds:  receipt.MinimumExpirySeconds,
		CheckCount:            receipt.CheckCount,
		PassedCount:           receipt.PassedCount,
		FailedCount:           receipt.FailedCount,
		InconclusiveCount:     receipt.InconclusiveCount,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode approval export report")
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
