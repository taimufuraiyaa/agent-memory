package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
)

const reportSchemaV1 = "agent-memory-self-managed-infrastructure-change-report-v1"

type report struct {
	Schema            string `json:"schema"`
	Ready             bool   `json:"ready"`
	Environment       string `json:"environment"`
	Apply             string `json:"apply"`
	Rollback          string `json:"rollback"`
	ResourceInventory string `json:"resource_inventory"`
	Drift             string `json:"drift"`
	CapabilityCount   int    `json:"capability_count"`
	ResourceCount     int    `json:"resource_count"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-memory-platform-change", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to the validated self-managed platform inventory")
	planPath := flags.String("plan", "", "Path to the validated infrastructure plan receipt")
	changePath := flags.String("change", "", "Path to the infrastructure apply and drift receipt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || anyBlank(*inventoryPath, *planPath, *changePath) {
		fmt.Fprintln(stderr, "inventory, plan, and change paths are required")
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
	assessment := platformchange.Assess(change)
	result := report{
		Schema:            reportSchemaV1,
		Ready:             assessment.Ready,
		Environment:       string(change.Environment),
		Apply:             string(assessment.ApplyOutcome),
		Rollback:          string(assessment.RollbackOutcome),
		ResourceInventory: string(assessment.ResourceInventoryOutcome),
		Drift:             string(assessment.DriftOutcome),
		CapabilityCount:   assessment.CapabilityCount,
		ResourceCount:     assessment.ResourceCount,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode infrastructure change report")
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
