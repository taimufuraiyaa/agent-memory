package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

// memoriesRecentHandler implements GET /api/v1/memories/recent: returns the
// most recently created/updated memories for a workspace.
func memoriesRecentHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		limit := 25
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "validation", "invalid limit")
				return
			}
			limit = v
		}
		if limit <= 0 {
			limit = 25
		}
		if limit > 200 {
			limit = 200
		}
		results, err := assets.Store.ListRecentMemoriesByWorkspace(r.Context(), ws, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if r.URL.Query().Get("ungrouped") == "true" {
			grouped, groupErr := assets.Store.ListPublishedSolutionPromotionMemoryIDs(r.Context(), ws)
			if groupErr != nil {
				writeErr(w, http.StatusInternalServerError, "runtime", groupErr.Error())
				return
			}
			filtered := results[:0]
			for _, memory := range results {
				if _, linked := grouped[memory.ID]; !linked {
					filtered = append(filtered, memory)
				}
			}
			results = filtered
		}
		writeOK(w, http.StatusOK, map[string]any{
			"results":   results,
			"workspace": ws,
			"limit":     limit,
		})
	}
}

// memoriesRecallHandler implements POST /api/v1/memories/recall: runs the
// shared recall pipeline (see runRecallPipeline) and returns a context block
// plus the memories/observations included in it.
func memoriesRecallHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Workspace       string `json:"workspace"`
			TaskDescription string `json:"task_description"`
			TokenBudget     int    `json:"token_budget"`

			Task    string `json:"task"`
			TopK    int    `json:"top_k"`
			Budget  int    `json:"budget"`
			Format  string `json:"format"`
			Explain bool   `json:"explain"`

			IncludeObservations bool   `json:"include_observations"`
			ObservationLimit    int    `json:"observation_limit"`
			ObservationSession  string `json:"observation_session_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		if !memoryEnabled() {
			task := strings.TrimSpace(req.TaskDescription)
			if task == "" {
				task = strings.TrimSpace(req.Task)
			}
			contextBlock := engine.AssembleRecallSections(task, nil)
			disabledTokens := len(strings.Fields(contextBlock))
			if assets, err := svc.resolve(r.Context(), ws); err == nil && assets.Store != nil {
				_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "recall", disabledTokens, disabledTokens, engine.RunLabel(), false)
			}
			data := map[string]any{
				"disabled":           true,
				"context_block":      contextBlock,
				"tokens_used":        disabledTokens,
				"baseline_tokens":    disabledTokens,
				"tokens_budget":      disabledTokens,
				"memories_used":      []any{},
				"hits":               []any{},
				"observation_tokens": 0,
			}
			if strings.EqualFold(strings.TrimSpace(req.Format), "raw") {
				data["text"] = contextBlock
			}
			writeOK(w, http.StatusOK, data)
			return
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		task := strings.TrimSpace(req.TaskDescription)
		if task == "" {
			task = strings.TrimSpace(req.Task)
		}
		budget := req.TokenBudget
		if budget <= 0 {
			budget = req.Budget
		}
		result, err := runRecallPipeline(r.Context(), assets, recallParams{
			workspace:           ws,
			task:                task,
			topK:                req.TopK,
			budget:              budget,
			includeObservations: req.IncludeObservations,
			observationSession:  req.ObservationSession,
			observationLimit:    req.ObservationLimit,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		var howResult *engine.HowRecallResult
		if engine.IsHowOrientedTask(task) {
			how, howErr := engine.NewHowRecallService(assets.Store).Recall(r.Context(), engine.HowRecallInput{
				Workspace: ws, SessionID: req.ObservationSession, Task: task, TokenBudget: budget,
			})
			if howErr != nil {
				writeErr(w, http.StatusBadRequest, "runtime", howErr.Error())
				return
			}
			result.contextBlock = engine.AppendHowRecallContext(result.contextBlock, how)
			howResult = &how
		}
		data := map[string]any{
			"request_id":             result.requestID,
			"context_block":          result.contextBlock,
			"tokens_used":            result.clip.UsedTokens + result.observationTokens,
			"tokens_budget":          result.clip.Budget + result.observationTokens,
			"memories_used":          result.included,
			"weak_memories":          result.retrieved.WeakHits,
			"suppressed_memories":    result.retrieved.SuppressedHits,
			"clipping":               result.clip,
			"workspace":              ws,
			"requested_top_k":        result.topK,
			"requested_budget":       result.clip.Budget + result.observationTokens,
			"retrieval_policy":       result.retrieved.Policy,
			"observations_included":  result.includeObservations && observeEnabled(),
			"observation_session_id": result.observationSessionID,
			"observation_count":      result.observationCount,
			"observation_tokens":     result.observationTokens,
			"retrieval_mode":         result.retrieved.Mode,
			"retrieved_hit_count":    len(result.retrieved.Hits),
			"retrieval_strategy":     result.decision.Strategy,
			"recall_trigger":         result.decision.Trigger,
			"search_sufficient":      result.decision.SearchSufficient,
			"search_probe":           result.decision.Probe,
			"deep_recall_used":       result.decision.Strategy != engine.RecallStrategySearchSatisfied,
			"reconstruction":         result.reconstruction,
		}
		if strings.EqualFold(strings.TrimSpace(req.Format), "raw") {
			data["text"] = result.contextBlock
		}
		if howResult != nil {
			data["how_recall"] = howResult
			data["how_request_id"] = howResult.RequestID
		}
		writeOK(w, http.StatusOK, data)
	}
}

// memoriesRecallPreviewHandler implements POST /api/v1/memories/recall/preview
// and /api/v1/memories/recall-preview: runs the same recall pipeline as
// memoriesRecallHandler but returns a more detailed, debugging-oriented
// breakdown (per-memory scores, clipped items, tier distribution, and
// optionally full memory bodies).
func memoriesRecallPreviewHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Workspace       string `json:"workspace"`
			TaskDescription string `json:"task_description"`
			TokenBudget     int    `json:"token_budget"`

			Task            string `json:"task"`
			TopK            int    `json:"top_k"`
			Budget          int    `json:"budget"`
			Explain         bool   `json:"explain"`
			IncludeMemories bool   `json:"include_memories"`

			IncludeObservations bool   `json:"include_observations"`
			ObservationLimit    int    `json:"observation_limit"`
			ObservationSession  string `json:"observation_session_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		if !memoryEnabled() {
			task := strings.TrimSpace(req.TaskDescription)
			if task == "" {
				task = strings.TrimSpace(req.Task)
			}
			if assets, err := svc.resolve(r.Context(), ws); err == nil && assets.Store != nil {
				_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "recall", 0, 0, engine.RunLabel(), false)
			}
			writeOK(w, http.StatusOK, map[string]any{
				"disabled":      true,
				"context_block": engine.AssembleRecallSections(task, nil),
				"tokens_used":   0,
				"tokens_budget": 0,
				"workspace":     ws,
			})
			return
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		task := strings.TrimSpace(req.TaskDescription)
		if task == "" {
			task = strings.TrimSpace(req.Task)
		}
		budget := req.TokenBudget
		if budget <= 0 {
			budget = req.Budget
		}
		result, err := runRecallPipeline(r.Context(), assets, recallParams{
			workspace:           ws,
			task:                task,
			topK:                req.TopK,
			budget:              budget,
			includeObservations: req.IncludeObservations,
			observationSession:  req.ObservationSession,
			observationLimit:    req.ObservationLimit,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		var howResult *engine.HowRecallResult
		if engine.IsHowOrientedTask(task) {
			how, howErr := engine.NewHowRecallService(assets.Store).Recall(r.Context(), engine.HowRecallInput{
				Workspace: ws, SessionID: req.ObservationSession, Task: task, TokenBudget: budget,
			})
			if howErr != nil {
				writeErr(w, http.StatusBadRequest, "runtime", howErr.Error())
				return
			}
			result.contextBlock = engine.AppendHowRecallContext(result.contextBlock, how)
			howResult = &how
		}
		tierDist := make(map[string]int)
		mems := make([]map[string]any, 0, len(result.included))
		var fullMems []core.MemoryEntry
		if req.IncludeMemories {
			fullMems = make([]core.MemoryEntry, 0, len(result.included))
		}
		for _, h := range result.included {
			tier := string(h.Memory.StorageTier)
			tierDist[tier]++
			item := map[string]any{
				"id":     h.Memory.ID,
				"type":   h.Memory.Type,
				"tier":   h.Memory.StorageTier,
				"score":  h.Score,
				"tokens": len(strings.Fields(h.Memory.Content)),
			}
			if req.Explain {
				item["score_breakdown"] = scoreBreakdownForHit(h)
			}
			mems = append(mems, item)
			if req.IncludeMemories {
				fullMems = append(fullMems, h.Memory)
			}
		}
		out := map[string]any{
			"context_block":          result.contextBlock,
			"tokens_used":            result.clip.UsedTokens + result.observationTokens,
			"tokens_budget":          result.clip.Budget + result.observationTokens,
			"memories_included":      mems,
			"weak_memories":          renderSearchResults(result.retrieved.WeakHits, req.Explain),
			"suppressed_memories":    renderSearchResults(result.retrieved.SuppressedHits, req.Explain),
			"memories_clipped":       renderClipped(result.clip),
			"tier_distribution":      tierDist,
			"clipping":               result.clip,
			"workspace":              ws,
			"requested_task":         result.task,
			"requested_explain":      req.Explain,
			"requested_top_k":        result.topK,
			"requested_budget":       result.originalBudget,
			"observations_included":  result.includeObservations && observeEnabled(),
			"observation_session_id": result.observationSessionID,
			"observation_count":      result.observationCount,
			"observation_tokens":     result.observationTokens,
			"retrieval_mode":         result.retrieved.Mode,
			"retrieval_weights":      result.retrieved.Weights,
			"retrieval_policy":       result.retrieved.Policy,
			"retrieved_hit_count":    len(result.retrieved.Hits),
			"retrieval_strategy":     result.decision.Strategy,
			"recall_trigger":         result.decision.Trigger,
			"search_sufficient":      result.decision.SearchSufficient,
			"search_probe":           result.decision.Probe,
			"deep_recall_used":       result.decision.Strategy != engine.RecallStrategySearchSatisfied,
			"reconstruction":         result.reconstruction,
		}
		if req.IncludeMemories {
			out["memories_included_full"] = fullMems
		}
		if howResult != nil {
			out["how_recall"] = howResult
			out["how_request_id"] = howResult.RequestID
		}
		writeOK(w, http.StatusOK, out)
	}
}
