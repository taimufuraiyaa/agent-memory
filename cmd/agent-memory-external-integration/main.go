package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/externalintegrationevidence"
)

const reportSchemaV1 = "agent-memory-external-integration-review-report-v1"

type collectFunc func(string, string, time.Time) (externalintegrationevidence.Receipt, error)
type dependencies struct {
	now     func() time.Time
	collect collectFunc
}
type report struct {
	Schema                        string `json:"schema"`
	Ready                         bool   `json:"ready"`
	ReceiptWritten                bool   `json:"receipt_written"`
	IntegrationCount              int    `json:"integration_count"`
	EnabledCount                  int    `json:"enabled_count"`
	DisabledCount                 int    `json:"disabled_count"`
	PassedIntegrationCount        int    `json:"passed_integration_count"`
	FailedIntegrationCount        int    `json:"failed_integration_count"`
	InconclusiveIntegrationCount  int    `json:"inconclusive_integration_count"`
	ApprovedDataFieldCount        int    `json:"approved_data_field_count"`
	SampledRequestCount           int    `json:"sampled_request_count"`
	CustomerContentByteCount      int    `json:"customer_content_byte_count"`
	UnapprovedFieldCount          int    `json:"unapproved_field_count"`
	UnallowlistedDestinationCount int    `json:"unallowlisted_destination_count"`
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
	flags := flag.NewFlagSet("agent-memory-external-integration", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to self-managed platform inventory")
	input := flags.String("input", "", "Path to content-free external-integration review input")
	receiptPath := flags.String("receipt", "", "New normalized P0.2-C receipt path")
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
		deps.collect = externalintegrationevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := externalintegrationevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{Schema: reportSchemaV1, Ready: receipt.Ready, ReceiptWritten: true,
		IntegrationCount: receipt.IntegrationCount, EnabledCount: receipt.EnabledCount, DisabledCount: receipt.DisabledCount,
		PassedIntegrationCount: receipt.PassedIntegrationCount, FailedIntegrationCount: receipt.FailedIntegrationCount, InconclusiveIntegrationCount: receipt.InconclusiveIntegrationCount,
		ApprovedDataFieldCount: receipt.ApprovedDataFieldCount, SampledRequestCount: receipt.SampledRequestCount, CustomerContentByteCount: receipt.CustomerContentByteCount,
		UnapprovedFieldCount: receipt.UnapprovedFieldCount, UnallowlistedDestinationCount: receipt.UnallowlistedDestinationCount,
		CheckCount: receipt.CheckCount, PassedCount: receipt.PassedCount, FailedCount: receipt.FailedCount, InconclusiveCount: receipt.InconclusiveCount}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode external-integration report")
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
