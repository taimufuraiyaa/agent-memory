package cli

import (
	"context"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func newWorkCommand() *cobra.Command {
	var flags commonFlags
	command := &cobra.Command{Use: "work", Short: "Capture and resume a bounded solution path"}
	addCommonPersistentFlags(command, &flags)
	command.AddCommand(
		newWorkStartCommand(&flags), newWorkStepCommand(&flags), newWorkCheckpointCommand(&flags),
		newWorkShowCommand(&flags), newWorkEndCommand(&flags), newWorkHandoffCommand(&flags),
		newWorkRecallCommand(&flags), newWorkPromoteCommand(&flags),
		newWorkToolEventCommand(&flags), newWorkDeriveToolLessonCommand(&flags), newWorkPromoteToolLessonCommand(&flags),
	)
	return command
}

func newWorkToolEventCommand(flags *commonFlags) *cobra.Command {
	var principal, episode, step, kind, toolName, toolVersion, operation, capability, inputSummary, resultClass, key string
	var taskVerified bool
	var durationMS int64
	cmd := &cobra.Command{Use: "tool-event", Short: "Record a safe tool event", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		key = idempotencyKeyOrNew(key)
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principal, "episode_id": episode, "step_id": step,
			"kind": kind, "tool_name": toolName, "tool_version": toolVersion, "operation": operation, "capability": capability,
			"input_summary": inputSummary, "result_class": resultClass, "task_verified": taskVerified, "duration_ms": durationMS, "idempotency_key": key}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/tool-events", body, &output)
		} else {
			err = withSolutionService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				event, callErr := service.RecordToolEvent(cmd.Context(), application.SolutionToolEventInput{Workspace: cfg.workspace, PrincipalID: principal,
					EpisodeID: episode, StepID: step, Kind: core.SolutionToolEventKind(kind), ToolName: toolName, ToolVersion: toolVersion,
					Operation: operation, Capability: capability, InputSummary: inputSummary, ResultClass: core.SolutionToolResultClass(resultClass),
					TaskVerified: taskVerified, DurationMS: durationMS, IdempotencyKey: key})
				output = map[string]any{"event": event}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.tool-event", output)
	}}
	cmd.Flags().StringVar(&principal, "principal", "", "Principal identifier")
	cmd.Flags().StringVar(&episode, "episode", "", "Episode identifier")
	cmd.Flags().StringVar(&step, "step", "", "Source step identifier")
	cmd.Flags().StringVar(&kind, "kind", string(core.SolutionToolResult), "Tool event kind")
	cmd.Flags().StringVar(&toolName, "tool", "", "Tool name")
	cmd.Flags().StringVar(&toolVersion, "tool-version", "", "Tool version")
	cmd.Flags().StringVar(&operation, "operation", "", "Tool operation")
	cmd.Flags().StringVar(&capability, "capability", "", "Safe capability summary")
	cmd.Flags().StringVar(&inputSummary, "input-summary", "", "Safe input or result summary")
	cmd.Flags().StringVar(&resultClass, "result", string(core.SolutionToolResultUnknown), "Tool result classification")
	cmd.Flags().BoolVar(&taskVerified, "task-verified", false, "Mark a successful result as task verified")
	cmd.Flags().Int64Var(&durationMS, "duration-ms", 0, "Tool duration in milliseconds")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Stable retry key")
	for _, flag := range []string{"principal", "episode", "step", "tool", "operation", "capability"} {
		_ = cmd.MarkFlagRequired(flag)
	}
	return cmd
}

func newWorkDeriveToolLessonCommand(flags *commonFlags) *cobra.Command {
	var principal, fallback string
	var eventIDs []string
	var reviewed bool
	cmd := &cobra.Command{Use: "derive-tool-lesson", Short: "Derive a validated lesson from tool events", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principal, "event_ids": eventIDs, "fallback": fallback, "reviewed": reviewed}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/tool-lessons/derive", body, &output)
		} else {
			err = withSolutionService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				lesson, callErr := service.DeriveToolLesson(cmd.Context(), application.SolutionToolLessonInput{Workspace: cfg.workspace, PrincipalID: principal, EventIDs: eventIDs, Fallback: fallback, Reviewed: reviewed})
				output = map[string]any{"lesson": lesson}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.derive-tool-lesson", output)
	}}
	cmd.Flags().StringVar(&principal, "principal", "", "Principal identifier")
	cmd.Flags().StringSliceVar(&eventIDs, "event", nil, "Source tool event identifier (repeatable)")
	cmd.Flags().StringVar(&fallback, "fallback", "", "Safe fallback guidance")
	cmd.Flags().BoolVar(&reviewed, "reviewed", false, "Record an explicit review")
	_ = cmd.MarkFlagRequired("principal")
	_ = cmd.MarkFlagRequired("event")
	return cmd
}

func newWorkPromoteToolLessonCommand(flags *commonFlags) *cobra.Command {
	var principal, lesson, key string
	cmd := &cobra.Command{Use: "promote-tool-lesson", Short: "Promote a verified tool lesson into durable memory", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		key = idempotencyKeyOrNew(key)
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principal, "lesson_id": lesson, "idempotency_key": key}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/tool-lessons/promote", body, &output)
		} else {
			err = withSolutionWriterService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				promotion, callErr := service.PromoteToolLesson(cmd.Context(), application.ToolLessonPromotionInput{Workspace: cfg.workspace, PrincipalID: principal, LessonID: lesson, IdempotencyKey: key})
				output = map[string]any{"promotion": promotion}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.promote-tool-lesson", output)
	}}
	cmd.Flags().StringVar(&principal, "principal", "", "Principal identifier")
	cmd.Flags().StringVar(&lesson, "lesson", "", "Verified tool lesson identifier")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Stable retry key")
	_ = cmd.MarkFlagRequired("principal")
	_ = cmd.MarkFlagRequired("lesson")
	return cmd
}

func newWorkRecallCommand(flags *commonFlags) *cobra.Command {
	var principalID, sessionID, task string
	var budget, maxCandidates int
	cmd := &cobra.Command{Use: "recall", Short: "Recall prior solution paths", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principalID, "session_id": sessionID, "task": task,
			"token_budget": budget, "max_candidates": maxCandidates}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/recall", body, &output)
		} else {
			store, openErr := sqlite.Open(cmd.Context(), cfg.dbPath)
			if openErr != nil {
				return openErr
			}
			defer func() { _ = store.Close() }()
			output, err = engine.NewHowRecallService(store).Recall(cmd.Context(), engine.HowRecallInput{
				Workspace: cfg.workspace, PrincipalID: principalID, SessionID: sessionID, Task: task,
				TokenBudget: budget, MaxCandidates: maxCandidates,
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.recall", output)
	}}
	cmd.Flags().StringVar(&principalID, "principal", "", "Principal identifier for active-state recall")
	cmd.Flags().StringVar(&sessionID, "session", "", "Session identifier for active-state recall")
	cmd.Flags().StringVar(&task, "task", "", "How-oriented task")
	cmd.Flags().IntVar(&budget, "budget", 800, "Token budget")
	cmd.Flags().IntVar(&maxCandidates, "max-candidates", 50, "Maximum candidates to rank")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

func newWorkPromoteCommand(flags *commonFlags) *cobra.Command {
	var principalID, episodeID, summaryID, memoryType, content, key string
	var sourceStepIDs []string
	cmd := &cobra.Command{Use: "promote", Short: "Promote verified How knowledge into durable What memory", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		key = idempotencyKeyOrNew(key)
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principalID, "episode_id": episodeID, "summary_id": summaryID,
			"idempotency_key": key, "targets": []map[string]any{{"memory_type": memoryType, "content": content, "source_step_ids": sourceStepIDs}}}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/promote", body, &output)
		} else {
			err = withSolutionWriterService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				result, callErr := service.Promote(cmd.Context(), application.SolutionPromoteInput{
					Workspace: cfg.workspace, PrincipalID: principalID, EpisodeID: episodeID, SummaryID: summaryID, IdempotencyKey: key,
					Targets: []application.SolutionPromotionTarget{{MemoryType: core.MemoryType(memoryType), Content: content, SourceStepIDs: sourceStepIDs}},
				})
				output = result
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.promote", output)
	}}
	cmd.Flags().StringVar(&principalID, "principal", "", "Principal identifier")
	cmd.Flags().StringVar(&episodeID, "episode", "", "Episode identifier")
	cmd.Flags().StringVar(&summaryID, "summary", "", "Finalized summary identifier")
	cmd.Flags().StringVar(&memoryType, "memory-type", string(core.ProceduralMemory), "Durable memory type")
	cmd.Flags().StringVar(&content, "content", "", "Optional promoted content; defaults to the finalized summary")
	cmd.Flags().StringSliceVar(&sourceStepIDs, "source-step", nil, "Source step identifier (repeatable)")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Stable retry key")
	for _, flag := range []string{"principal", "episode", "summary"} {
		_ = cmd.MarkFlagRequired(flag)
	}
	return cmd
}

func newWorkStartCommand(flags *commonFlags) *cobra.Command {
	var sessionID, principalID, clientID, goal, capture, retention, key string
	cmd := &cobra.Command{Use: "start", Short: "Start a solution episode", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		key = idempotencyKeyOrNew(key)
		body := map[string]any{"workspace": cfg.workspace, "session_id": sessionID, "principal_id": principalID, "client_id": clientID,
			"goal_summary": goal, "capture_policy": capture, "retention_class": retention, "idempotency_key": key}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/start", body, &output)
		} else {
			err = withSolutionService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				episode, deduplicated, callErr := service.Start(cmd.Context(), application.SolutionStartInput{Workspace: cfg.workspace, SessionID: sessionID,
					PrincipalID: principalID, ClientID: clientID, GoalSummary: goal, CapturePolicy: core.SolutionCapturePolicy(capture),
					RetentionClass: core.SolutionRetentionClass(retention), IdempotencyKey: key, Origin: engine.SolutionOriginAgent})
				output = map[string]any{"episode": episode, "deduplicated": deduplicated}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.start", output)
	}}
	cmd.Flags().StringVar(&sessionID, "session", "", "Session identifier")
	cmd.Flags().StringVar(&principalID, "principal", "", "Principal identifier")
	cmd.Flags().StringVar(&clientID, "client", "", "Client identifier")
	cmd.Flags().StringVar(&goal, "goal", "", "Safe goal summary")
	cmd.Flags().StringVar(&capture, "capture-policy", string(core.SolutionCaptureStructured), "Capture policy: structured|summary_only")
	cmd.Flags().StringVar(&retention, "retention-class", string(core.SolutionRetentionStandard), "Retention class: transient|standard|pinned")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Stable retry key (generated when omitted)")
	_ = cmd.MarkFlagRequired("session")
	_ = cmd.MarkFlagRequired("principal")
	_ = cmd.MarkFlagRequired("client")
	_ = cmd.MarkFlagRequired("goal")
	return cmd
}

func newWorkStepCommand(flags *commonFlags) *cobra.Command {
	var principalID, episodeID, kind, status, summary, rationale, source, sensitivity, key string
	var confidence float64
	cmd := &cobra.Command{Use: "step", Short: "Append a safe solution step", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		key = idempotencyKeyOrNew(key)
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principalID, "episode_id": episodeID, "kind": kind, "status": status,
			"summary": summary, "rationale_summary": rationale, "source": source, "confidence": confidence, "sensitivity": sensitivity, "idempotency_key": key}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/steps", body, &output)
		} else {
			err = withSolutionService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				step, deduplicated, callErr := service.AppendStep(cmd.Context(), application.SolutionAppendStepInput{Workspace: cfg.workspace,
					PrincipalID: principalID, EpisodeID: episodeID, Kind: core.SolutionStepKind(kind), Status: core.SolutionStepStatus(status),
					Summary: summary, RationaleSummary: rationale, Source: source, Confidence: confidence,
					Sensitivity: core.SolutionSensitivity(sensitivity), IdempotencyKey: key, Origin: engine.SolutionOriginAgent})
				output = map[string]any{"step": step, "deduplicated": deduplicated}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.step", output)
	}}
	cmd.Flags().StringVar(&principalID, "principal", "", "Principal identifier")
	cmd.Flags().StringVar(&episodeID, "episode", "", "Episode identifier")
	cmd.Flags().StringVar(&kind, "kind", string(core.SolutionStepAction), "Step kind")
	cmd.Flags().StringVar(&status, "status", string(core.SolutionStepCompleted), "Step status")
	cmd.Flags().StringVar(&summary, "summary", "", "Safe step summary")
	cmd.Flags().StringVar(&rationale, "rationale", "", "Brief rationale summary")
	cmd.Flags().StringVar(&source, "source", "agent", "Step source")
	cmd.Flags().Float64Var(&confidence, "confidence", 0.5, "Confidence from 0 to 1")
	cmd.Flags().StringVar(&sensitivity, "sensitivity", string(core.SolutionSensitivityInternal), "Sensitivity classification")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Stable retry key")
	_ = cmd.MarkFlagRequired("principal")
	_ = cmd.MarkFlagRequired("episode")
	_ = cmd.MarkFlagRequired("summary")
	return cmd
}

func newWorkCheckpointCommand(flags *commonFlags) *cobra.Command {
	var principalID, episodeID, goal, nextAction, sensitivity string
	var constraints, completed, questions []string
	var generation int64
	var ttl time.Duration
	cmd := &cobra.Command{Use: "checkpoint", Short: "Save expiring continuation state", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principalID, "episode_id": episodeID, "expected_generation": generation,
			"goal_summary": goal, "constraints": constraints, "completed_items": completed, "open_questions": questions,
			"next_action": nextAction, "sensitivity": sensitivity, "ttl_seconds": int64(ttl / time.Second)}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/checkpoint", body, &output)
		} else {
			err = withSolutionService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				state, callErr := service.Checkpoint(cmd.Context(), application.SolutionCheckpointInput{Workspace: cfg.workspace, PrincipalID: principalID,
					EpisodeID: episodeID, ExpectedGeneration: generation, GoalSummary: goal, Constraints: constraints, CompletedItems: completed,
					OpenQuestions: questions, NextAction: nextAction, Sensitivity: core.SolutionSensitivity(sensitivity), TTL: ttl, Origin: engine.SolutionOriginAgent})
				output = map[string]any{"working_state": state}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.checkpoint", output)
	}}
	cmd.Flags().StringVar(&principalID, "principal", "", "Principal identifier")
	cmd.Flags().StringVar(&episodeID, "episode", "", "Episode identifier")
	cmd.Flags().Int64Var(&generation, "expected-generation", 0, "Expected working-state generation")
	cmd.Flags().StringVar(&goal, "goal", "", "Safe goal summary")
	cmd.Flags().StringSliceVar(&constraints, "constraint", nil, "Constraint (repeatable)")
	cmd.Flags().StringSliceVar(&completed, "completed", nil, "Completed item (repeatable)")
	cmd.Flags().StringSliceVar(&questions, "open-question", nil, "Open question (repeatable)")
	cmd.Flags().StringVar(&nextAction, "next-action", "", "Next action")
	cmd.Flags().StringVar(&sensitivity, "sensitivity", string(core.SolutionSensitivityInternal), "Sensitivity classification")
	cmd.Flags().DurationVar(&ttl, "ttl", 24*time.Hour, "Working-state TTL (maximum 168h)")
	_ = cmd.MarkFlagRequired("principal")
	_ = cmd.MarkFlagRequired("episode")
	_ = cmd.MarkFlagRequired("goal")
	return cmd
}

func newWorkShowCommand(flags *commonFlags) *cobra.Command {
	var principalID, episodeID string
	cmd := &cobra.Command{Use: "show", Short: "Recover current continuation state", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		var output any
		if cfg.apiURL != "" {
			path := "/api/v1/solutions/state?workspace=" + url.QueryEscape(cfg.workspace) + "&principal_id=" + url.QueryEscape(principalID) + "&episode_id=" + url.QueryEscape(episodeID)
			err = getAPI(cmd.Context(), cfg.apiURL, path, &output)
		} else {
			err = withSolutionService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				state, callErr := service.GetWorkingState(cmd.Context(), cfg.workspace, principalID, episodeID)
				output = map[string]any{"working_state": state}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.show", output)
	}}
	cmd.Flags().StringVar(&principalID, "principal", "", "Principal identifier")
	cmd.Flags().StringVar(&episodeID, "episode", "", "Episode identifier")
	_ = cmd.MarkFlagRequired("principal")
	_ = cmd.MarkFlagRequired("episode")
	return cmd
}

func newWorkEndCommand(flags *commonFlags) *cobra.Command {
	var principalID, episodeID, status, key string
	var version int64
	cmd := &cobra.Command{Use: "end", Short: "End or change a solution episode", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		key = idempotencyKeyOrNew(key)
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principalID, "episode_id": episodeID, "expected_version": version, "status": status, "idempotency_key": key}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/transition", body, &output)
		} else {
			err = withSolutionService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				episode, callErr := service.Transition(cmd.Context(), application.SolutionTransitionInput{Workspace: cfg.workspace, PrincipalID: principalID, EpisodeID: episodeID, ExpectedVersion: version, Status: core.SolutionEpisodeStatus(status), IdempotencyKey: key})
				output = map[string]any{"episode": episode}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.end", output)
	}}
	cmd.Flags().StringVar(&principalID, "principal", "", "Principal identifier")
	cmd.Flags().StringVar(&episodeID, "episode", "", "Episode identifier")
	cmd.Flags().Int64Var(&version, "expected-version", 0, "Expected episode version")
	cmd.Flags().StringVar(&status, "status", string(core.SolutionEpisodeCompleted), "Target status: active|paused|completed|partial|abandoned|cancelled")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Stable retry key")
	_ = cmd.MarkFlagRequired("principal")
	_ = cmd.MarkFlagRequired("episode")
	_ = cmd.MarkFlagRequired("expected-version")
	return cmd
}

func newWorkHandoffCommand(flags *commonFlags) *cobra.Command {
	var principalID, episodeID, targetPrincipal, targetSession, key string
	var version int64
	cmd := &cobra.Command{Use: "handoff", Short: "Transfer an episode to another principal", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := resolveRuntime(*flags)
		if err != nil {
			return err
		}
		key = idempotencyKeyOrNew(key)
		body := map[string]any{"workspace": cfg.workspace, "principal_id": principalID, "episode_id": episodeID, "expected_version": version, "target_principal_id": targetPrincipal, "target_session_id": targetSession, "idempotency_key": key}
		var output any
		if cfg.apiURL != "" {
			err = postAPI(cmd.Context(), cfg.apiURL, "/api/v1/solutions/handoff", body, &output)
		} else {
			err = withSolutionService(cmd.Context(), cfg, func(service *application.SolutionService) error {
				episode, callErr := service.Handoff(cmd.Context(), application.SolutionHandoffInput{Workspace: cfg.workspace, PrincipalID: principalID, EpisodeID: episodeID, ExpectedVersion: version, TargetPrincipalID: targetPrincipal, TargetSessionID: targetSession, IdempotencyKey: key})
				output = map[string]any{"episode": episode}
				return callErr
			})
		}
		if err != nil {
			return err
		}
		return writeSuccessEnvelope(cmd.OutOrStdout(), "work.handoff", output)
	}}
	cmd.Flags().StringVar(&principalID, "principal", "", "Current principal identifier")
	cmd.Flags().StringVar(&episodeID, "episode", "", "Episode identifier")
	cmd.Flags().Int64Var(&version, "expected-version", 0, "Expected episode version")
	cmd.Flags().StringVar(&targetPrincipal, "target-principal", "", "New principal identifier")
	cmd.Flags().StringVar(&targetSession, "target-session", "", "New session identifier")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Stable retry key")
	for _, flag := range []string{"principal", "episode", "expected-version", "target-principal", "target-session"} {
		_ = cmd.MarkFlagRequired(flag)
	}
	return cmd
}

func withSolutionService(ctx context.Context, cfg runtimeConfig, run func(*application.SolutionService) error) error {
	store, err := sqlite.Open(ctx, cfg.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return run(application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy()))
}

func withSolutionWriterService(ctx context.Context, cfg runtimeConfig, run func(*application.SolutionService) error) error {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if err := os.MkdirAll(cfg.modelDir, 0o755); err != nil {
		return err
	}
	provider, err := embeddings.NewLocalProvider(cfg.modelDir)
	if err != nil {
		return err
	}
	writer := engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{Embedder: provider})
	return run(application.NewSolutionService(store, engine.NewSolutionAdmissionPolicy(), application.WithSolutionWriter(writer)))
}

func idempotencyKeyOrNew(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return uuid.NewString()
}
