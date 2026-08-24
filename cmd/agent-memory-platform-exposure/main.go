package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformexposure"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
)

const reportSchemaV1 = "agent-memory-production-private-authority-exposure-report-v1"

type report struct {
	Schema            string `json:"schema"`
	Ready             bool   `json:"ready"`
	Environment       string `json:"environment"`
	TargetCount       int    `json:"target_count"`
	BlockedCount      int    `json:"blocked_count"`
	ReachableCount    int    `json:"reachable_count"`
	InconclusiveCount int    `json:"inconclusive_count"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-memory-platform-exposure", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the validated production platform inventory")
	planPath := flags.String("plan", "", "Path to the validated production infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the ready production infrastructure change receipt")
	exposurePath := flags.String("exposure", "", "Path to the production private-authority exposure receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *planPath, *changePath, *exposurePath) {
		fmt.Fprintln(stderr, "inventory, plan, change, and exposure paths are required")
		return 2
	}
	inventory, err := platforminventory.Load(*inventoryPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	plan, err := platformplan.Load(*planPath, inventory)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	change, err := platformchange.Load(*changePath, inventory, plan)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	exposure, err := platformexposure.Load(*exposurePath, inventory, change)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	assessment := platformexposure.Assess(exposure)
	result := report{
		Schema:            reportSchemaV1,
		Ready:             assessment.Ready,
		Environment:       string(exposure.Environment),
		TargetCount:       assessment.TargetCount,
		BlockedCount:      assessment.BlockedCount,
		ReachableCount:    assessment.ReachableCount,
		InconclusiveCount: assessment.InconclusiveCount,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode production exposure report")
		return 1
	}
	if !assessment.Ready {
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
