package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func newAuditCommand() *cobra.Command {
	var flags commonFlags
	var operation, actor, requestID, from, to string
	var limit int
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect or export append-only memory audit events",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if cfg.apiURL != "" {
				return fmt.Errorf("audit CLI currently uses direct workspace storage; omit --api")
			}
			store, err := openStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			filter := sqlite.AuditFilter{Workspace: cfg.workspace, Operation: strings.TrimSpace(operation), Actor: strings.TrimSpace(actor), RequestID: strings.TrimSpace(requestID), Limit: limit}
			if from != "" {
				parsed, ok := parseTimeFlexibleCLI(from)
				if !ok {
					return fmt.Errorf("invalid from")
				}
				filter.From = &parsed
			}
			if to != "" {
				parsed, ok := parseTimeFlexibleCLI(to)
				if !ok {
					return fmt.Errorf("invalid to")
				}
				filter.To = &parsed
			}
			events, err := store.ListAuditEvents(cmd.Context(), filter)
			if err != nil {
				return err
			}
			switch strings.ToLower(strings.TrimSpace(flags.format)) {
			case "json", "":
				return writeSuccessEnvelope(cmd.OutOrStdout(), "audit", map[string]any{"workspace": cfg.workspace, "events": events, "count": len(events)})
			case "ndjson":
				encoder := json.NewEncoder(cmd.OutOrStdout())
				for _, event := range events {
					if err := encoder.Encode(event); err != nil {
						return err
					}
				}
				return nil
			case "text":
				for _, event := range events {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s\n", event.OccurredAt.Format("2006-01-02T15:04:05Z"), event.Operation, event.Outcome, event.RequestID)
				}
				return nil
			default:
				return fmt.Errorf("invalid format: allowed values are json|ndjson|text")
			}
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&operation, "operation", "", "Filter by operation")
	cmd.Flags().StringVar(&actor, "actor", "", "Filter by actor")
	cmd.Flags().StringVar(&requestID, "request-id", "", "Filter by correlation request ID")
	cmd.Flags().StringVar(&from, "from", "", "Filter from timestamp")
	cmd.Flags().StringVar(&to, "to", "", "Filter to timestamp")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum events to return")
	return cmd
}
