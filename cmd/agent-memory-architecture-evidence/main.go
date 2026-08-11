package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/architectureevidence"
)

const reportSchemaV1 = "agent-memory-self-managed-architecture-review-report-v1"

type dependencies struct {
	now     func() time.Time
	collect func(string, string, time.Time) (architectureevidence.Receipt, error)
	publish func(string, architectureevidence.Receipt) error
}

type report struct {
	Schema                        string `json:"schema"`
	Ready                         bool   `json:"ready"`
	ReceiptWritten                bool   `json:"receipt_written"`
	FacilityCount                 int    `json:"facility_count"`
	ReviewedFailureDomainCount    int    `json:"reviewed_failure_domain_count"`
	IndependentFailureDomainCount int    `json:"independent_failure_domain_count"`
	ComponentCount                int    `json:"component_count"`
	ComponentDomainReviewCount    int    `json:"component_domain_review_count"`
	DataFlowCount                 int    `json:"data_flow_count"`
	IntegrationCount              int    `json:"integration_count"`
	CheckCount                    int    `json:"check_count"`
	PassedCount                   int    `json:"passed_count"`
	FailedCount                   int    `json:"failed_count"`
	InconclusiveCount             int    `json:"inconclusive_count"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-architecture-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Self-managed staging or production inventory path")
	input := flags.String("input", "", "Content-free P0.2-A architecture review input path")
	receiptPath := flags.String("receipt", "", "New normalized P0.2-A receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = architectureevidence.Collect
	}
	if deps.publish == nil {
		deps.publish = architectureevidence.Publish
	}
	receipt, err := deps.collect(*inventory, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := deps.publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true, FacilityCount: receipt.FacilityCount, ReviewedFailureDomainCount: receipt.ReviewedFailureDomainCount, IndependentFailureDomainCount: receipt.IndependentFailureDomainCount, ComponentCount: receipt.ComponentCount, ComponentDomainReviewCount: receipt.ComponentDomainReviewCount, DataFlowCount: receipt.DataFlowCount, IntegrationCount: receipt.IntegrationCount, CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode architecture report")
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
