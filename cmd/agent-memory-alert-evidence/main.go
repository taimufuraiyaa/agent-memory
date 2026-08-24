package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/alertevidence"
)

const reportSchemaV1 = "agent-memory-staging-alert-routing-report-v1"

type collectFunc func(string, string, string, string, string, time.Time) (alertevidence.Receipt, error)

type dependencies struct {
	now     func() time.Time
	collect collectFunc
}

type report struct {
	Schema                              string `json:"schema"`
	Ready                               bool   `json:"ready"`
	ReceiptWritten                      bool   `json:"receipt_written"`
	AlertCount                          int    `json:"alert_count"`
	PassedCount                         int    `json:"passed_count"`
	FailedCount                         int    `json:"failed_count"`
	InconclusiveCount                   int    `json:"inconclusive_count"`
	TargetBreachCount                   int    `json:"target_breach_count"`
	MaximumDeliverySeconds              int64  `json:"maximum_delivery_seconds"`
	MaximumDeliveryTargetSeconds        int64  `json:"maximum_delivery_target_seconds"`
	MaximumAcknowledgementSeconds       int64  `json:"maximum_acknowledgement_seconds"`
	MaximumAcknowledgementTargetSeconds int64  `json:"maximum_acknowledgement_target_seconds"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	return runWithDependencies(args, stdout, stderr, dependencies{})
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("agent-memory-alert-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventory := flags.String("inventory", "", "Path to platform inventory")
	plan := flags.String("plan", "", "Path to infrastructure plan")
	change := flags.String("change", "", "Path to ready infrastructure change")
	release := flags.String("release", "", "Path to passed staging release")
	input := flags.String("input", "", "Path to content-free alert-routing assessment")
	receiptPath := flags.String("receipt", "", "New normalized alert-routing receipt path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventory, *plan, *change, *release, *input, *receiptPath) {
		fmt.Fprintln(stderr, "inventory, plan, change, release, input, and receipt are required")
		return 2
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.collect == nil {
		deps.collect = alertevidence.Collect
	}
	receipt, err := deps.collect(*inventory, *plan, *change, *release, *input, deps.now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := alertevidence.Publish(*receiptPath, receipt); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := report{
		Schema:                              reportSchemaV1,
		Ready:                               receipt.Ready,
		ReceiptWritten:                      true,
		AlertCount:                          receipt.AlertCount,
		PassedCount:                         receipt.PassedCount,
		FailedCount:                         receipt.FailedCount,
		InconclusiveCount:                   receipt.InconclusiveCount,
		TargetBreachCount:                   receipt.TargetBreachCount,
		MaximumDeliverySeconds:              receipt.MaximumDeliverySeconds,
		MaximumDeliveryTargetSeconds:        receipt.MaximumDeliveryTargetSeconds,
		MaximumAcknowledgementSeconds:       receipt.MaximumAcknowledgementSeconds,
		MaximumAcknowledgementTargetSeconds: receipt.MaximumAcknowledgementTargetSeconds,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode alert-routing report")
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
