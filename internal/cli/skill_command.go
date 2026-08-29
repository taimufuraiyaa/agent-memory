package cli

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func newSkillCommand() *cobra.Command {
	var flags commonFlags
	command := &cobra.Command{Use: "skill", Short: "Inspect and operate the revision lifecycle"}
	addCommonPersistentFlags(command, &flags)
	command.AddCommand(newSkillListCommand(&flags), newSkillInspectCommand(&flags))
	for _, operation := range []string{"propose", "evaluate", "approve", "canary", "promote", "resolve", "acknowledge", "complete", "disable", "pin", "rollback", "migration-verify"} {
		command.AddCommand(newSkillLifecycleOperationCommand(&flags, operation))
	}
	return command
}

func newSkillListCommand(flags *commonFlags) *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List logical skills and lifecycle state", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		if cfg.apiURL == "" {
			return errors.New("skill lifecycle commands require --api")
		}
		var output any
		path := "/api/v1/skills/lifecycle/list?workspace=" + url.QueryEscape(cfg.workspace)
		if err = getAPI(cmd.Context(), cfg.apiURL, path, &output); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "skill.list", output)
	}}
}

func newSkillInspectCommand(flags *commonFlags) *cobra.Command {
	var skillID, environment string
	cmd := &cobra.Command{Use: "inspect", Short: "Inspect one logical skill and all revisions", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		if cfg.apiURL == "" {
			return errors.New("skill lifecycle commands require --api")
		}
		path := "/api/v1/skills/inspect?workspace=" + url.QueryEscape(cfg.workspace) + "&skill_id=" + url.QueryEscape(skillID) + "&environment=" + url.QueryEscape(environment)
		var output any
		if err = getAPI(cmd.Context(), cfg.apiURL, path, &output); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "skill.inspect", output)
	}}
	cmd.Flags().StringVar(&skillID, "skill-id", "", "Logical skill identifier")
	cmd.Flags().StringVar(&environment, "environment", "local", "Activation environment")
	_ = cmd.MarkFlagRequired("skill-id")
	return cmd
}

func newSkillLifecycleOperationCommand(flags *commonFlags, operation string) *cobra.Command {
	var actor, payload string
	cmd := &cobra.Command{Use: operation, Short: "Run the " + operation + " lifecycle operation", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		if cfg.apiURL == "" {
			return errors.New("skill lifecycle commands require --api")
		}
		var parsed json.RawMessage
		if strings.TrimSpace(payload) == "" {
			parsed = json.RawMessage(`{}`)
		} else if !json.Valid([]byte(payload)) {
			return errors.New("payload must be valid JSON")
		} else {
			parsed = json.RawMessage(payload)
		}
		body := map[string]any{"operation": operation, "workspace": cfg.workspace, "actor": actor, "payload": parsed}
		var output any
		if err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/skills/lifecycle", body, &output); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "skill."+operation, output)
	}}
	cmd.Flags().StringVar(&actor, "actor", "", "Authorized actor identifier")
	cmd.Flags().StringVar(&payload, "payload", "{}", "Operation-specific JSON payload")
	_ = cmd.MarkFlagRequired("actor")
	return cmd
}
