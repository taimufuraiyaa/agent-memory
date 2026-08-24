package api

import (
	"context"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

// recallParams are the inputs shared by /api/v1/memories/recall and
// /api/v1/memories/recall/preview, after each handler has resolved its own
// request-field aliases (task_description/task, token_budget/budget, etc.).
type recallParams struct {
	workspace           string
	task                string
	topK                int
	budget              int
	includeObservations bool
	observationSession  string
	observationLimit    int
}

// recallResult bundles the outputs of the shared recall pipeline (see
// runRecallPipeline) consumed by both /recall and /recall/preview to build
// their (differently shaped) responses.
type recallResult struct {
	requestID            string
	task                 string
	topK                 int
	originalBudget       int
	includeObservations  bool
	observationBlock     string
	observationTokens    int
	observationCount     int
	observationSessionID string

	retrieved      *engine.RetrievalResult
	decision       engine.RecallGateDecision
	reconstruction *engine.RecallReconstructionMeta
	rebalanced     []engine.RetrievalHit
	included       []engine.RetrievalHit
	clip           engine.ClipMetadata
	contextBlock   string
}

// runRecallPipeline runs the recall pipeline shared by
// /api/v1/memories/recall and /api/v1/memories/recall/preview: it builds the
// optional recent-observations block, runs the continuation/search-probe/
// recall-gate decision and retrieval, augments the result with
// tombstone-based reconstruction, rebalances hits for the task, clips to the
// token budget, assembles the final context block, and records recall
// token-savings metrics.
//
// Both endpoints must apply identical gating/retrieval/reconstruction/
// clipping logic; previously this ~80-line pipeline was duplicated between
// the two handlers, which risked them drifting out of sync (a fix applied to
// one could silently miss the other). This consolidates the HTTP-side copy
// into one place; each handler builds its own response shape from the
// returned *recallResult.
func runRecallPipeline(ctx context.Context, assets *workspaceAssets, p recallParams) (*recallResult, error) {
	shared, err := assets.Application.Recall(ctx, application.RecallOptions{
		Workspace:           p.workspace,
		Task:                p.task,
		TopK:                p.topK,
		Budget:              p.budget,
		IncludeObservations: p.includeObservations && observeEnabled(),
		ObservationSession:  p.observationSession,
		ObservationLimit:    p.observationLimit,
	})
	if err != nil {
		return nil, err
	}
	return &recallResult{
		requestID:            shared.RequestID,
		task:                 shared.Task,
		topK:                 shared.TopK,
		originalBudget:       shared.OriginalBudget,
		includeObservations:  shared.IncludeObservations,
		observationBlock:     shared.ObservationBlock,
		observationTokens:    shared.ObservationTokens,
		observationCount:     shared.ObservationCount,
		observationSessionID: shared.ObservationSessionID,
		retrieved:            shared.Retrieved,
		decision:             shared.Decision,
		reconstruction:       shared.Reconstruction,
		rebalanced:           shared.Rebalanced,
		included:             shared.Included,
		clip:                 shared.Clip,
		contextBlock:         shared.ContextBlock,
	}, nil
}
