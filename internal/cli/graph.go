package cli

import (
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type graphCommandFlags struct {
	common                                                   commonFlags
	configurationID, idempotencyKey, expectedRevision, jobID string
}

func newGraphCommand() *cobra.Command {
	var flags graphCommandFlags
	cmd := &cobra.Command{Use: "graph-index", Short: "Operate the derived GraphRAG index"}
	addCommonPersistentFlags(cmd, &flags.common)
	cmd.PersistentFlags().StringVar(&flags.configurationID, "configuration", "default", "Graph configuration identity")
	cmd.PersistentFlags().StringVar(&flags.idempotencyKey, "idempotency-key", "", "Stable request key for update or rebuild")
	cmd.PersistentFlags().StringVar(&flags.expectedRevision, "expected-revision", "", "Expected active revision (compare-and-swap)")
	cmd.PersistentFlags().StringVar(&flags.jobID, "job-id", "", "Graph job identity for cancel or retry")
	cmd.AddCommand(newGraphReadCommand("readiness", &flags), newGraphReadCommand("status", &flags))
	for _, action := range []application.GraphOperationAction{application.GraphOperationUpdate, application.GraphOperationRebuild, application.GraphOperationCancel, application.GraphOperationRetry, application.GraphOperationDisable, application.GraphOperationRollback} {
		cmd.AddCommand(newGraphOperationCommand(action, &flags))
	}
	return cmd
}

func newGraphReadCommand(kind string, flags *graphCommandFlags) *cobra.Command {
	return &cobra.Command{Use: kind, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(flags.common)
		if err != nil {
			return err
		}
		if err := validateOutputFormat(flags.common.format, false); err != nil {
			return err
		}
		if cfg.apiURL != "" {
			path := "/api/v1/graph-index/" + kind + "?workspace=" + url.QueryEscape(cfg.workspace) + "&configuration_id=" + url.QueryEscape(flags.configurationID)
			var out any
			if err := getAPI(cmd.Context(), cfg.apiURL, path, &out); err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "graph-index."+kind, out)
		}
		store, err := openStore(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		service := application.NewGraphOperationService(store)
		scope := core.GraphScope{WorkspaceID: cfg.workspace}
		if kind == "readiness" {
			out, err := service.Readiness(cmd.Context(), scope, flags.configurationID)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "graph-index.readiness", out)
		}
		out, err := service.Status(cmd.Context(), scope, flags.configurationID)
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "graph-index.status", out)
	}}
}

func newGraphOperationCommand(action application.GraphOperationAction, flags *graphCommandFlags) *cobra.Command {
	return &cobra.Command{Use: string(action), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(flags.common)
		if err != nil {
			return err
		}
		request := application.GraphOperationRequest{Scope: core.GraphScope{WorkspaceID: cfg.workspace}, ConfigurationID: strings.TrimSpace(flags.configurationID), Action: action, IdempotencyKey: flags.idempotencyKey, ExpectedRevision: flags.expectedRevision, JobID: flags.jobID, Actor: "cli"}
		if cfg.apiURL != "" {
			var out any
			if err := postAPI(cmd.Context(), cfg.apiURL, "/api/v1/graph-index/operations", request, &out); err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "graph-index."+string(action), out)
		}
		store, err := openStore(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer store.Close()
		out, err := application.NewGraphOperationService(store).Operate(cmd.Context(), request)
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "graph-index."+string(action), out)
	}}
}
