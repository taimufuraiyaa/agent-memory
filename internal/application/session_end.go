package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type SessionEndInput struct {
	Workspace, SessionID, PrincipalID, Transcript, IdempotencyKey string
	TerminalStatus                                                core.SolutionEpisodeStatus
}

type SessionEndResult struct {
	Mode             string                   `json:"mode"`
	Partial          bool                     `json:"partial"`
	Failures         []string                 `json:"failures,omitempty"`
	Episode          *core.SolutionEpisode    `json:"episode,omitempty"`
	Summary          *core.SolutionSummary    `json:"summary,omitempty"`
	TotalExtracted   int                      `json:"total_extracted"`
	WrittenIDs       []string                 `json:"written_ids"`
	TotalSkipped     int                      `json:"total_skipped"`
	TotalFailed      int                      `json:"total_failed"`
	LifecycleRan     bool                     `json:"lifecycle_ran"`
	LifecycleMetrics *engine.LifecycleMetrics `json:"lifecycle_metrics,omitempty"`
}

// RunSessionEnd coordinates structured episode finalization with the legacy
// transcript extractor. A matching structured episode is authoritative, so the
// heuristic path is never run for the same session.
func RunSessionEnd(ctx context.Context, input SessionEndInput, store *sqlite.Store, pipeline *engine.WritePipeline) (*SessionEndResult, error) {
	if store == nil || pipeline == nil {
		return nil, errors.New("store and pipeline are required")
	}
	input.Workspace, input.SessionID, input.PrincipalID = strings.TrimSpace(input.Workspace), strings.TrimSpace(input.SessionID), strings.TrimSpace(input.PrincipalID)
	if input.Workspace == "" {
		return nil, errors.New("workspace is required")
	}
	if input.SessionID != "" && input.PrincipalID != "" {
		episode, err := store.FindLatestSolutionEpisode(ctx, input.Workspace, input.SessionID, input.PrincipalID)
		if err == nil {
			return finalizeSessionEpisode(ctx, input, episode, store, pipeline), nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return runHeuristicSessionEnd(ctx, input.Workspace, input.Transcript, store, pipeline)
}

func finalizeSessionEpisode(ctx context.Context, input SessionEndInput, episode core.SolutionEpisode, store *sqlite.Store, pipeline *engine.WritePipeline) *SessionEndResult {
	result := &SessionEndResult{Mode: "structured_episode", WrittenIDs: []string{}, Failures: []string{}}
	status := input.TerminalStatus
	if !status.Terminal() {
		status = core.SolutionEpisodePartial
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		key = "session-end:" + episode.ID
	}
	solutions := NewSolutionService(store, engine.NewSolutionAdmissionPolicy())
	terminal := episode
	if !episode.Status.Terminal() {
		var err error
		terminal, err = solutions.Transition(ctx, SolutionTransitionInput{
			Workspace: episode.Workspace, PrincipalID: episode.PrincipalID, EpisodeID: episode.ID,
			ExpectedVersion: episode.Version, Status: status, IdempotencyKey: key + ":transition",
		})
		if err != nil {
			result.Partial = true
			result.Failures = append(result.Failures, "episode_transition_failed")
			result.Episode = &episode
			return result
		}
	}
	result.Episode = &terminal
	summary, err := solutions.Finalize(ctx, SolutionFinalizeInput{
		Workspace: terminal.Workspace, PrincipalID: terminal.PrincipalID, EpisodeID: terminal.ID,
		ExpectedVersion: terminal.Version, IdempotencyKey: key + ":finalize",
	})
	if err != nil {
		result.Partial = true
		result.Failures = append(result.Failures, "episode_finalization_failed")
		return result
	}
	result.Summary = &summary
	metrics, err := engine.NewLifecycleManager(store, pipeline).Run(ctx, input.Workspace)
	if err != nil {
		result.Partial = true
		result.Failures = append(result.Failures, "memory_lifecycle_failed")
		return result
	}
	result.LifecycleRan, result.LifecycleMetrics = true, metrics
	return result
}

func runHeuristicSessionEnd(ctx context.Context, workspace, transcript string, store *sqlite.Store, pipeline *engine.WritePipeline) (*SessionEndResult, error) {
	extracted, err := engine.NewSessionEndExtractor(pipeline).ExtractAndStore(ctx, workspace, transcript)
	if extracted == nil {
		return nil, err
	}
	result := &SessionEndResult{Mode: "heuristic_fallback", WrittenIDs: append([]string(nil), extracted.WrittenIDs...),
		TotalExtracted: extracted.TotalExtracted, TotalSkipped: extracted.TotalSkipped, TotalFailed: extracted.TotalFailed}
	if err != nil {
		result.Partial = true
		result.Failures = []string{"heuristic_extraction_failed"}
		return result, nil
	}
	metrics, lifecycleErr := engine.NewLifecycleManager(store, pipeline).Run(ctx, workspace)
	if lifecycleErr != nil {
		result.Partial = true
		result.Failures = []string{"memory_lifecycle_failed"}
		return result, nil
	}
	result.LifecycleRan, result.LifecycleMetrics = true, metrics
	return result, nil
}
