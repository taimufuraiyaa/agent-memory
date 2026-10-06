package cli

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
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
	command.AddCommand(newSkillOrchestrationCommand(&flags))
	return command
}

func newSkillOrchestrationCommand(flags *commonFlags) *cobra.Command {
	command := &cobra.Command{Use: "orchestration", Short: "Inspect and control background skill workflows"}
	command.AddCommand(newSkillOrchestrationStatusCommand(flags))
	for _, action := range []string{"pause", "resume", "cancel", "reconcile", "retry", "replay", "drain"} {
		command.AddCommand(newSkillOrchestrationControlCommand(flags, action))
	}
	return command
}

func newSkillOrchestrationStatusCommand(flags *commonFlags) *cobra.Command {
	var actor, workflowID, environment, jobCursor, eventCursor string
	var limit int
	command := &cobra.Command{Use: "status", Short: "Show bounded workflow, job, and event history", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		if cfg.apiURL == "" {
			return errors.New("skill orchestration commands require --api")
		}
		query := url.Values{"workspace": {cfg.workspace}, "actor": {actor}, "workflow_id": {workflowID}, "environment": {environment}, "limit": {strconv.Itoa(limit)}}
		if jobCursor != "" {
			query.Set("job_cursor", jobCursor)
		}
		if eventCursor != "" {
			query.Set("event_cursor", eventCursor)
		}
		var output any
		if err := getAPI(cmd.Context(), cfg.apiURL, "/api/v1/skills/orchestration/status?"+query.Encode(), &output); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "skill.orchestration.status", output)
	}}
	command.Flags().StringVar(&actor, "actor", "", "Authorized actor identifier")
	command.Flags().StringVar(&workflowID, "workflow-id", "", "Workflow identifier")
	command.Flags().StringVar(&environment, "environment", "local", "Runtime environment")
	command.Flags().StringVar(&jobCursor, "job-cursor", "", "Opaque job page cursor")
	command.Flags().StringVar(&eventCursor, "event-cursor", "", "Opaque event page cursor")
	command.Flags().IntVar(&limit, "limit", 50, "Page size between 1 and 200")
	_ = command.MarkFlagRequired("actor")
	_ = command.MarkFlagRequired("workflow-id")
	return command
}

func newSkillOrchestrationControlCommand(flags *commonFlags, action string) *cobra.Command {
	var actor, workflowID, jobID, environment, reasonCode, idempotencyKey string
	var generation int64
	var limit int
	command := &cobra.Command{Use: action, Short: "Run the " + action + " orchestration control", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		if cfg.apiURL == "" {
			return errors.New("skill orchestration commands require --api")
		}
		body := map[string]any{"action": action, "workspace": cfg.workspace, "environment": environment, "actor": actor,
			"workflow_id": workflowID, "job_id": jobID, "expected_generation": generation, "reason_code": reasonCode,
			"idempotency_key": idempotencyKey, "limit": limit}
		var output any
		if err := postAPI(cmd.Context(), cfg.apiURL, "/api/v1/skills/orchestration/control", body, &output); err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "skill.orchestration."+action, output)
	}}
	command.Flags().StringVar(&actor, "actor", "", "Authorized actor identifier")
	command.Flags().StringVar(&workflowID, "workflow-id", "", "Workflow identifier")
	command.Flags().StringVar(&jobID, "job-id", "", "Job identifier")
	command.Flags().StringVar(&environment, "environment", "local", "Runtime environment")
	command.Flags().Int64Var(&generation, "expected-generation", 0, "Expected workflow generation")
	command.Flags().StringVar(&reasonCode, "reason-code", "operator_replay", "Safe replay reason code")
	command.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Stable replay idempotency key")
	command.Flags().IntVar(&limit, "limit", 100, "Bounded reconcile limit")
	_ = command.MarkFlagRequired("actor")
	if action == "pause" || action == "resume" || action == "reconcile" {
		_ = command.MarkFlagRequired("workflow-id")
	}
	if action == "cancel" || action == "retry" || action == "replay" {
		_ = command.MarkFlagRequired("job-id")
	}
	if action != "drain" && action != "replay" {
		_ = command.MarkFlagRequired("expected-generation")
	}
	if action == "replay" {
		_ = command.MarkFlagRequired("idempotency-key")
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
