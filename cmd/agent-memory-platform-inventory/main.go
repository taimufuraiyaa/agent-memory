package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

const reportSchemaV1 = "agent-memory-self-managed-platform-inventory-report-v1"

type report struct {
	Schema                      string   `json:"schema"`
	Ready                       bool     `json:"ready"`
	Environment                 string   `json:"environment"`
	ComponentCount              int      `json:"component_count"`
	FailureDomainCount          int      `json:"failure_domain_count"`
	EnabledExternalIntegrations []string `json:"enabled_external_integrations"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-memory-platform-inventory", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inventoryPath := flags.String("inventory", "", "Path to a self-managed platform inventory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*inventoryPath) == "" {
		fmt.Fprintln(stderr, "inventory path is required")
		return 2
	}
	inventory, err := platforminventory.Load(*inventoryPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	enabled := make([]string, 0, len(inventory.ExternalIntegrations))
	for _, integration := range inventory.ExternalIntegrations {
		if integration.Enabled {
			enabled = append(enabled, string(integration.Kind))
		}
	}
	sort.Strings(enabled)
	result := report{
		Schema:                      reportSchemaV1,
		Ready:                       true,
		Environment:                 string(inventory.Environment),
		ComponentCount:              len(inventory.Components),
		FailureDomainCount:          len(inventory.FailureDomains),
		EnabledExternalIntegrations: enabled,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, "encode platform inventory report")
		return 1
	}
	return 0
}
