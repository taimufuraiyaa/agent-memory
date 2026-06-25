package api

import (
	"context"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
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
	res := &recallResult{
		task:                p.task,
		topK:                p.topK,
		includeObservations: p.includeObservations,
	}
	if res.topK <= 0 {
		res.topK = 50
	}

	budget := p.budget
	if budget <= 0 {
		budget = 4000
	}
	res.originalBudget = budget

	if p.includeObservations && observeEnabled() {
		block, sid, count := buildRecentObservationBlock(ctx, assets.Store, p.workspace, strings.TrimSpace(p.observationSession), p.observationLimit)
		res.observationBlock = block
		res.observationSessionID = sid
		res.observationCount = count
		res.observationTokens = len(strings.Fields(block)) + len(strings.Fields("## Recent Observations"))
		if budget-res.observationTokens > 0 {
			budget -= res.observationTokens
		} else {
			budget = 0
		}
	}

	var (
		retrieved *engine.RetrievalResult
		decision  engine.RecallGateDecision
		err       error
	)
	if engine.IsContinuationPrompt(p.task) {
		decision = engine.DecideRecallGate(p.task, nil)
		retrieved, err = assets.Retrieval.Retrieve(ctx, engine.RetrievalOptions{
			Workspace: p.workspace,
			Query:     p.task,
			TopK:      res.topK,
			Mode:      engine.ModeRecall,
		})
	} else {
		var searchProbe *engine.RetrievalResult
		searchProbe, err = assets.Retrieval.Retrieve(ctx, engine.RetrievalOptions{
			Workspace: p.workspace,
			Query:     p.task,
			TopK:      res.topK,
			Mode:      engine.ModeSearch,
		})
		if err != nil {
			return nil, err
		}
		decision = engine.DecideRecallGate(p.task, searchProbe)
		if decision.SearchSufficient {
			retrieved = &engine.RetrievalResult{
				Mode:           engine.ModeSearch,
				Weights:        searchProbe.Weights,
				Policy:         searchProbe.Policy,
				Hits:           append([]engine.RetrievalHit(nil), searchProbe.StrongHits...),
				StrongHits:     append([]engine.RetrievalHit(nil), searchProbe.StrongHits...),
				WeakHits:       append([]engine.RetrievalHit(nil), searchProbe.WeakHits...),
				SuppressedHits: append([]engine.RetrievalHit(nil), searchProbe.SuppressedHits...),
			}
		} else {
			retrieved, err = assets.Retrieval.Retrieve(ctx, engine.RetrievalOptions{
				Workspace: p.workspace,
				Query:     p.task,
				TopK:      res.topK,
				Mode:      engine.ModeRecall,
			})
		}
	}
	if err != nil {
		return nil, err
	}

	retrieved, reconstruction, err := engine.AugmentRecallWithReconstruction(ctx, p.workspace, p.task, retrieved, assets.Retrieval, assets.Store, assets.Writer, res.topK)
	if err != nil {
		return nil, err
	}

	res.decision = decision
	res.retrieved = retrieved
	res.reconstruction = reconstruction
	res.rebalanced = engine.RebalanceRecallHits(p.task, retrieved.Hits)
	res.included, res.clip = assets.Clipper.Clip(res.rebalanced, budget)
	res.contextBlock = engine.AssembleRecallSectionsWithObservations(p.task, res.observationBlock, res.included)

	if assets.Store != nil {
		tokensUsed := res.clip.UsedTokens + res.observationTokens
		baseline := recallBaselineTokens(res.rebalanced, res.observationTokens)
		_ = assets.Store.AddTokenMetricV2(ctx, p.workspace, "recall", tokensUsed, baseline, engine.RunLabel(), engine.MemoryEnabled())
	}

	return res, nil
}

func buildRecentObservationBlock(ctx context.Context, store *sqlite.Store, workspace string, preferredSessionID string, limit int) (string, string, int) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	sessionID := strings.TrimSpace(preferredSessionID)
	if sessionID == "" {
		sessions, err := store.ListSessions(ctx, workspace, 1)
		if err != nil || len(sessions) == 0 {
			return "", "", 0
		}
		sessionID = sessions[0].SessionID
	}
	obs, err := store.ListObservations(ctx, workspace, sessionID, nil, nil, limit)
	if err != nil || len(obs) == 0 {
		return "", sessionID, 0
	}
	var b strings.Builder
	b.WriteString("Session: ")
	b.WriteString(sessionID)
	b.WriteString("\n")
	count := 0
	for _, o := range obs {
		if count >= limit {
			break
		}
		line := strings.TrimSpace(o.Summary)
		if line == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(o.OccurredAt.UTC().Format(time.RFC3339))
		b.WriteString(" ")
		b.WriteString(engine.ClipString(line, 240))
		b.WriteString("\n")
		count++
	}
	return strings.TrimSpace(b.String()), sessionID, count
}

func recallBaselineTokens(hits []engine.RetrievalHit, observationTokens int) int {
	return sumHitTokens(hits) + observationTokens
}

func sumHitTokens(hits []engine.RetrievalHit) int {
	total := 0
	for _, h := range hits {
		total += len(strings.Fields(h.Memory.Content))
	}
	return total
}
