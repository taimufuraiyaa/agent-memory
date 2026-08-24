package application

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

type RecallOptions struct {
	Workspace           string
	Task                string
	TopK                int
	Budget              int
	IncludeObservations bool
	ObservationSession  string
	ObservationLimit    int
}

type RecallResult struct {
	RequestID            string
	Task                 string
	TopK                 int
	OriginalBudget       int
	IncludeObservations  bool
	ObservationBlock     string
	ObservationTokens    int
	ObservationCount     int
	ObservationSessionID string
	Retrieved            *engine.RetrievalResult
	Decision             engine.RecallGateDecision
	Reconstruction       *engine.RecallReconstructionMeta
	Rebalanced           []engine.RetrievalHit
	Included             []engine.RetrievalHit
	Clip                 engine.ClipMetadata
	ContextBlock         string
}

func (s *MemoryService) Recall(ctx context.Context, options RecallOptions) (*RecallResult, error) {
	result := &RecallResult{
		RequestID:           uuid.NewString(),
		Task:                strings.TrimSpace(options.Task),
		TopK:                options.TopK,
		IncludeObservations: options.IncludeObservations,
	}
	if result.TopK <= 0 {
		result.TopK = 50
	}
	budget := options.Budget
	if budget <= 0 {
		budget = 4000
	}
	result.OriginalBudget = budget
	if s.store != nil {
		_ = s.store.LogRetrievalRequest(ctx, result.RequestID, options.Workspace, "recall", result.Task)
	}

	if options.IncludeObservations && s.store != nil {
		block, sessionID, count := s.recentObservationBlock(ctx, options.Workspace, options.ObservationSession, options.ObservationLimit)
		result.ObservationBlock = block
		result.ObservationSessionID = sessionID
		result.ObservationCount = count
		result.ObservationTokens = len(strings.Fields(block))
		if block != "" {
			result.ObservationTokens += len(strings.Fields("## Recent Observations"))
		}
		if budget > result.ObservationTokens {
			budget -= result.ObservationTokens
		} else {
			budget = 0
		}
	}

	retrieved, decision, err := s.retrieveForRecall(ctx, options.Workspace, result.Task, result.TopK)
	if err != nil {
		return nil, err
	}
	retrieved, reconstruction, err := engine.AugmentRecallWithReconstruction(ctx, options.Workspace, result.Task, retrieved, s.retrieval, s.store, s.writer, result.TopK)
	if err != nil {
		return nil, err
	}
	result.Retrieved = retrieved
	result.Decision = decision
	result.Reconstruction = reconstruction
	result.Rebalanced = engine.RebalanceRecallHits(result.Task, retrieved.Hits)
	result.Included, result.Clip = engine.NewTokenClipper(nil).Clip(result.Rebalanced, budget)
	result.ContextBlock = engine.AssembleRecallSectionsWithObservations(result.Task, result.ObservationBlock, result.Included)
	if s.store != nil {
		used := result.Clip.UsedTokens + result.ObservationTokens
		baseline := hitTokens(result.Rebalanced) + result.ObservationTokens
		_ = s.store.AddTokenMetricV2(ctx, options.Workspace, "recall", used, baseline, engine.RunLabel(), engine.MemoryEnabled())
	}
	return result, nil
}

func (s *MemoryService) retrieveForRecall(ctx context.Context, workspace, task string, topK int) (*engine.RetrievalResult, engine.RecallGateDecision, error) {
	if engine.IsContinuationPrompt(task) {
		decision := engine.DecideRecallGate(task, nil)
		retrieved, err := s.retrieval.Retrieve(ctx, engine.RetrievalOptions{Workspace: workspace, Query: task, TopK: topK, Mode: engine.ModeRecall})
		return retrieved, decision, err
	}
	probe, err := s.retrieval.Retrieve(ctx, engine.RetrievalOptions{Workspace: workspace, Query: task, TopK: topK, Mode: engine.ModeSearch})
	if err != nil {
		return nil, engine.RecallGateDecision{}, err
	}
	decision := engine.DecideRecallGate(task, probe)
	if decision.SearchSufficient {
		return &engine.RetrievalResult{
			Mode:           engine.ModeSearch,
			Weights:        probe.Weights,
			Policy:         probe.Policy,
			Hits:           append([]engine.RetrievalHit(nil), probe.StrongHits...),
			StrongHits:     append([]engine.RetrievalHit(nil), probe.StrongHits...),
			WeakHits:       append([]engine.RetrievalHit(nil), probe.WeakHits...),
			SuppressedHits: append([]engine.RetrievalHit(nil), probe.SuppressedHits...),
		}, decision, nil
	}
	retrieved, err := s.retrieval.Retrieve(ctx, engine.RetrievalOptions{Workspace: workspace, Query: task, TopK: topK, Mode: engine.ModeRecall})
	return retrieved, decision, err
}

func (s *MemoryService) recentObservationBlock(ctx context.Context, workspace, preferredSessionID string, limit int) (string, string, int) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	sessionID := strings.TrimSpace(preferredSessionID)
	if sessionID == "" {
		sessions, err := s.store.ListSessions(ctx, workspace, 1)
		if err != nil || len(sessions) == 0 {
			return "", "", 0
		}
		sessionID = sessions[0].SessionID
	}
	observations, err := s.store.ListObservations(ctx, workspace, sessionID, nil, nil, limit)
	if err != nil || len(observations) == 0 {
		return "", sessionID, 0
	}
	var text strings.Builder
	text.WriteString("Session: ")
	text.WriteString(sessionID)
	text.WriteByte('\n')
	count := 0
	for _, observation := range observations {
		line := strings.TrimSpace(observation.Summary)
		if line == "" || count >= limit {
			continue
		}
		text.WriteString("- ")
		text.WriteString(observation.OccurredAt.UTC().Format(time.RFC3339))
		text.WriteByte(' ')
		text.WriteString(engine.ClipString(line, 240))
		text.WriteByte('\n')
		count++
	}
	return strings.TrimSpace(text.String()), sessionID, count
}
