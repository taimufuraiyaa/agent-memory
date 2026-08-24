package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
)

const reportSchemaV1 = "agent-memory-self-managed-infrastructure-plan-report-v1"

type actionCounts struct {
	Create   int `json:"create"`
	Update   int `json:"update"`
	NoChange int `json:"no_change"`
	Replace  int `json:"replace"`
	Delete   int `json:"delete"`
}

type report struct {
	Schema          string       `json:"schema"`
	Ready           bool         `json:"ready"`
	Environment     string       `json:"environment"`
	CapabilityCount int          `json:"capability_count"`
	ToolCount       int          `json:"tool_count"`
	Actions         actionCounts `json:"actions"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-memory-platform-plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the validated self-managed platform inventory")
	planPath := flags.String("plan", "", "Path to the sanitized infrastructure plan receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*inventoryPath) == "" || strings.TrimSpace(*planPath) == "" {
		fmt.Fprintln(stderr, "inventory and plan paths are required")
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
	assessment := platformplan.Assess(plan)
	result := report{
		Schema:          reportSchemaV1,
		Ready:           assessment.Ready,
		Environment:     string(plan.Environment),
		CapabilityCount: assessment.CapabilityCount,
		ToolCount:       assessment.ToolCount,
		Actions: actionCounts{
			Create:   assessment.ActionCounts[platformplan.ActionCreate],
			Update:   assessment.ActionCounts[platformplan.ActionUpdate],
			NoChange: assessment.ActionCounts[platformplan.ActionNoChange],
			Replace:  assessment.ActionCounts[platformplan.ActionReplace],
			Delete:   assessment.ActionCounts[platformplan.ActionDelete],
		},
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode infrastructure plan report")
		return 1
	}
	if !assessment.Ready {
		return 3
	}
	return 0
}
