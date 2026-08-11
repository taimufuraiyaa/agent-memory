package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/mvpreadinessevidence"
)

const reportSchemaV1 = "agent-memory-final-mvp-readiness-report-v1"

type collectFunc func(string, string, string, string, string, string, time.Time) (mvpreadinessevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                    string `json:"schema"`
	Ready                     bool   `json:"ready"`
	ReceiptWritten            bool   `json:"receipt_written"`
	CanonicalControlCount     int    `json:"canonical_control_count"`
	FoundationalControlCount  int    `json:"foundational_control_count"`
	VerifiedFoundationalCount int    `json:"verified_foundational_count"`
	MissingFoundationalCount  int    `json:"missing_foundational_count"`
	RejectedFoundationalCount int    `json:"rejected_foundational_count"`
	ExpiredFoundationalCount  int    `json:"expired_foundational_count"`
	FinalMVPControlCount      int    `json:"final_mvp_control_count"`
	PassedGateCount           int    `json:"passed_gate_count"`
	FailedGateCount           int    `json:"failed_gate_count"`
	InconclusiveGateCount     int    `json:"inconclusive_gate_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-mvp-readiness", flag.ContinueOnError)
	flags.SetOutput(stderr)
	catalog := flags.String("catalog", "", "Path to the canonical external control catalog")
	index := flags.String("index", "", "Path to the pre-final external evidence index")
	artifactRoot := flags.String("artifacts-root", "", "Root containing indexed dossier artifacts")
	trust := flags.String("trust", "", "Path to the external evidence trust bundle")
	approvals := flags.String("approvals-dir", "", "Directory containing signed foundational approvals")
	input := flags.String("input", "", "Path to the content-free MVP readiness input")
	receiptPath := flags.String("receipt", "", "New normalized final MVP readiness receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*catalog, *index, *artifactRoot, *trust, *approvals, *input, *receiptPath) {
		fmt.Fprintln(stderr, "catalog, index, artifacts root, trust, approvals directory, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = mvpreadinessevidence.Collect
	}
	receipt, err := deps.collect(*catalog, *index, *artifactRoot, *trust, *approvals, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := mvpreadinessevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{
		Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true,
		CanonicalControlCount: receipt.CanonicalControlCount, FoundationalControlCount: receipt.FoundationalControlCount,
		VerifiedFoundationalCount: receipt.VerifiedFoundationalCount, MissingFoundationalCount: receipt.MissingFoundationalCount,
		RejectedFoundationalCount: receipt.RejectedFoundationalCount, ExpiredFoundationalCount: receipt.ExpiredFoundationalCount,
		FinalMVPControlCount: receipt.FinalMVPControlCount,
	}
	for _, gate := range receipt.Gates {
		switch gate.Outcome {
		case mvpreadinessevidence.OutcomePassed:
			result.PassedGateCount++
		case mvpreadinessevidence.OutcomeFailed:
			result.FailedGateCount++
		case mvpreadinessevidence.OutcomeInconclusive:
			result.InconclusiveGateCount++
		}
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode MVP readiness report")
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
