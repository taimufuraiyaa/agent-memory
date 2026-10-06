package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	baseobservability "github.com/taimufuraiyaa/agent-memory/internal/observability"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

type RecallOptions struct {
	Workspace           string
	Task                string
	TopK                int
	Budget              int
	IncludeObservations bool
	ObservationSession  string
	ObservationLimit    int
	GraphMode           graphretrieval.GraphQueryMode
	GraphRequired       bool
	GraphPolicy         graphretrieval.GraphRoutePolicy
	GraphAvailability   graphretrieval.GraphRouteAvailability
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
	GraphRoute           graphretrieval.GraphRouteDecision
	GraphContext         *RecallGraphContext
}

func (s *MemoryService) Recall(ctx context.Context, options RecallOptions) (*RecallResult, error) {
	var snapshot *contracts.GraphQuerySnapshot
	var graphReadErr error
	if s.store != nil && options.GraphMode != "" && options.GraphMode != graphretrieval.GraphQueryBasic {
		loaded, err := s.store.LoadActiveGraphSnapshot(ctx, core.GraphScope{WorkspaceID: options.Workspace}, 4096, 16384, 256)
		if err == nil {
			snapshot = &loaded
			options.GraphAvailability = graphretrieval.GraphRouteAvailability{Readable: true, Fresh: loaded.Fresh, ActiveRevisionID: loaded.RevisionID}
		} else if !errors.Is(err, sql.ErrNoRows) {
			graphReadErr = err
		}
	}
	graphRoute, err := ResolveRecallGraphRoute(options)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	completed := false
	var result *RecallResult
	defer func() {
		if s.graphObserve != nil {
			_ = s.graphObserve(graphRecallObservation(started, completed, graphRoute, result))
		}
	}()
	result = &RecallResult{
		RequestID:           uuid.NewString(),
		Task:                strings.TrimSpace(options.Task),
		TopK:                options.TopK,
		IncludeObservations: options.IncludeObservations,
		GraphRoute:          graphRoute,
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

	graphCacheIdentity := ""
	if snapshot != nil {
		graphCacheIdentity = snapshot.CacheIdentity
	}
	retrieved, decision, err := s.retrieveForRecall(ctx, options.Workspace, result.Task, result.TopK, graphCacheIdentity)
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
	if graphReadErr != nil {
		if options.GraphRequired {
			return nil, fmt.Errorf("required graph read failed: %w", graphReadErr)
		}
		result.GraphRoute = degradedGraphRoute(result.GraphRoute, graphretrieval.GraphReasonReadFailed)
		result.GraphContext = &RecallGraphContext{DegradedReason: graphretrieval.GraphReasonReadFailed}
	} else if snapshot != nil && (result.GraphRoute.SelectedMode == graphretrieval.GraphQueryLocal || result.GraphRoute.SelectedMode == graphretrieval.GraphQueryGlobal) {
		hybrid, graphContext, enrichErr := s.enrichRecallWithGraph(ctx, result.Task, result.Rebalanced, *snapshot, result.GraphRoute.SelectedMode)
		if enrichErr != nil {
			if options.GraphRequired {
				return nil, fmt.Errorf("required graph enrichment failed: %w", enrichErr)
			}
			result.GraphRoute = degradedGraphRoute(result.GraphRoute, graphretrieval.GraphReasonReadFailed)
			result.GraphContext = &RecallGraphContext{RevisionID: snapshot.RevisionID, Fresh: snapshot.Fresh, DegradedReason: graphretrieval.GraphReasonReadFailed}
		} else {
			result.Rebalanced = hybrid
			result.GraphContext = graphContext
		}
	}
	result.Included, result.Clip = engine.NewTokenClipper(nil).Clip(result.Rebalanced, budget)
	result.ContextBlock = engine.AssembleRecallSectionsWithObservations(result.Task, result.ObservationBlock, result.Included)
	if s.store != nil {
		used := result.Clip.UsedTokens + result.ObservationTokens
		baseline := hitTokens(result.Rebalanced) + result.ObservationTokens
		_ = s.store.AddTokenMetricV2(ctx, options.Workspace, "recall", used, baseline, engine.RunLabel(), engine.MemoryEnabled())
	}
	completed = true
	return result, nil
}

func graphRecallObservation(started time.Time, completed bool, initial graphretrieval.GraphRouteDecision, result *RecallResult) baseobservability.GraphObservation {
	decision := initial
	records := int64(0)
	if result != nil {
		decision = result.GraphRoute
		records = int64(len(result.Rebalanced))
	}
	mode := string(decision.SelectedMode)
	if mode == "" {
		mode = string(graphretrieval.GraphQueryBasic)
	}
	route := mode
	if decision.Fallback {
		switch decision.RequestedMode {
		case graphretrieval.GraphQueryLocal, graphretrieval.GraphQueryGlobal:
			route = string(decision.RequestedMode)
		case graphretrieval.GraphQueryAuto:
			if decision.Intent == graphretrieval.GraphIntentGlobal {
				route = string(graphretrieval.GraphQueryGlobal)
			} else if decision.Intent == graphretrieval.GraphIntentRelational {
				route = string(graphretrieval.GraphQueryLocal)
			}
		}
	}
	observation := baseobservability.GraphObservation{Stage: "query", Mode: mode, Route: route, Outcome: "failed", Duration: time.Since(started), Records: records}
	if completed {
		observation.Outcome = "completed"
	}
	if decision.Fallback {
		observation.Outcome = "fallback"
		observation.Fallback = true
		observation.Reason = graphMetricFallbackReason(decision.ReasonCode)
	}
	if decision.ActiveRevisionID != "" {
		observation.Freshness = "fresh"
		if !decision.Fresh {
			observation.Freshness = "stale"
		}
	}
	return observation
}

func graphMetricFallbackReason(reason graphretrieval.GraphRouteReason) string {
	switch reason {
	case graphretrieval.GraphReasonPolicyDisabled:
		return "policy_disabled"
	case graphretrieval.GraphReasonModeDisallowed:
		return "mode_disallowed"
	case graphretrieval.GraphReasonIndexUnavailable:
		return "index_unavailable"
	case graphretrieval.GraphReasonIndexStale:
		return "index_stale"
	case graphretrieval.GraphReasonReadFailed:
		return "read_failed"
	default:
		return "read_failed"
	}
}

// ResolveRecallGraphRoute plans enrichment around the existing Basic recall.
// Later retrieval stages consume the selected normalized route; this function
// never calls the indexing adapter or an upstream GraphRAG query API.
func ResolveRecallGraphRoute(options RecallOptions) (graphretrieval.GraphRouteDecision, error) {
	return graphretrieval.NewGraphRouter().Route(graphretrieval.GraphRouteRequest{
		Mode: options.GraphMode, Query: strings.TrimSpace(options.Task), RequireGraph: options.GraphRequired,
		Policy: options.GraphPolicy, Availability: options.GraphAvailability,
	})
}

func (s *MemoryService) retrieveForRecall(ctx context.Context, workspace, task string, topK int, graphCacheIdentity string) (*engine.RetrievalResult, engine.RecallGateDecision, error) {
	if engine.IsContinuationPrompt(task) {
		decision := engine.DecideRecallGate(task, nil)
		retrieved, err := s.retrieval.Retrieve(ctx, engine.RetrievalOptions{Workspace: workspace, Query: task, GraphCacheIdentity: graphCacheIdentity, TopK: topK, Mode: engine.ModeRecall})
		return retrieved, decision, err
	}
	probe, err := s.retrieval.Retrieve(ctx, engine.RetrievalOptions{Workspace: workspace, Query: task, GraphCacheIdentity: graphCacheIdentity, TopK: topK, Mode: engine.ModeSearch})
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
	retrieved, err := s.retrieval.Retrieve(ctx, engine.RetrievalOptions{Workspace: workspace, Query: task, GraphCacheIdentity: graphCacheIdentity, TopK: topK, Mode: engine.ModeRecall})
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
