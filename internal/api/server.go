package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
	"github.com/time/timebooks/agent-memory/internal/workspace"
)

type Service struct {
	Workspace         string
	BaseDir           string
	EmbeddingProvider embeddings.Provider

	mu     sync.RWMutex
	stores map[string]*workspaceAssets
}

type workspaceAssets struct {
	Store     *sqlite.Store
	Writer    *engine.WritePipeline
	Retrieval *engine.RetrievalEngine
	Clipper   *engine.TokenClipper
}

func (s *Service) resolve(ctx context.Context, ws string) (*workspaceAssets, error) {
	if ws == "" {
		ws = s.Workspace
	}
	s.mu.RLock()
	assets, ok := s.stores[ws]
	s.mu.RUnlock()
	if ok {
		return assets, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if assets, ok := s.stores[ws]; ok {
		return assets, nil
	}

	dbPath := filepath.Join(s.BaseDir, ws+".db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	assets = &workspaceAssets{
		Store:     store,
		Writer:    engine.NewWritePipelineWithEmbedder(store, s.EmbeddingProvider),
		Retrieval: engine.NewRetrievalEngine(engine.NewVectorSearcher(store, s.EmbeddingProvider)),
		Clipper:   engine.NewTokenClipper(nil),
	}
	if s.stores == nil {
		s.stores = make(map[string]*workspaceAssets)
	}
	s.stores[ws] = assets
	return assets, nil
}

func NewMux(svc *Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	writeMemoryHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !memoryEnabled() {
			writeOK(w, http.StatusOK, map[string]any{
				"skipped": true,
				"reason":  "disabled",
			})
			return
		}
		var req struct {
			Workspace string             `json:"workspace"`
			Type      core.MemoryType    `json:"type"`
			Content   string             `json:"content"`
			Entities  []string           `json:"entities"`
			Tags      []string           `json:"tags"`
			Outcome   *core.Outcome      `json:"outcome"`
			Source    *core.MemorySource `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		src := core.MemorySource{Type: core.SourceUserInput}
		if req.Source != nil && req.Source.Type != "" {
			src = *req.Source
		}
		out, err := assets.Writer.Write(r.Context(), engine.WriteInput{
			Workspace: ws,
			Type:      req.Type,
			Content:   req.Content,
			Entities:  req.Entities,
			Tags:      req.Tags,
			Outcome:   req.Outcome,
			Source:    src,
			Mode:      engine.ExtractFast,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	}

	mux.HandleFunc("/api/v1/memories", writeMemoryHandler)
	mux.HandleFunc("/api/v1/memories/write", writeMemoryHandler)
	mux.HandleFunc("/api/v1/memories/feedback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req FeedbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if err := req.Validate(); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		at := time.Now().UTC()
		if raw := strings.TrimSpace(req.OccurredAt); raw != "" {
			if parsed, ok := parseTimeFlexible(raw); ok {
				at = parsed
			} else {
				writeErr(w, http.StatusBadRequest, "validation", "invalid occurred_at")
				return
			}
		}
		updated, err := assets.Store.ApplyRetrievalFeedback(r.Context(), req.MemoryID, req.Outcome, at)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		if strings.TrimSpace(string(req.ReconsolidationAction)) != "" {
			updated, err = assets.Store.ApplyReconsolidation(r.Context(), req.MemoryID, req.ReconsolidationAction, strings.TrimSpace(req.SuccessorMemoryID), at)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "runtime", err.Error())
				return
			}
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace":              ws,
			"memory_id":              req.MemoryID,
			"outcome":                req.Outcome,
			"validator":              strings.TrimSpace(req.Validator),
			"reason_category":        strings.TrimSpace(req.ReasonCategory),
			"reconsolidation_action": req.ReconsolidationAction,
			"updated_memory":         updated,
		})
	})

	mux.HandleFunc("/api/v1/memories/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Query       string            `json:"query"`
			Workspace   string            `json:"workspace"`
			TopK        int               `json:"top_k"`
			TokenBudget int               `json:"token_budget"`
			Mode        string            `json:"mode"`
			Explain     bool              `json:"explain"`
			Tiers       []string          `json:"tiers"`
			Types       []core.MemoryType `json:"types"`
			Filters     *struct {
				Type             []core.MemoryType `json:"type"`
				Tiers            []string          `json:"tiers"`
				OutcomeResult    string            `json:"outcome_result"`
				MinConfidence    *float64          `json:"min_confidence"`
				MinDecayScore    *float64          `json:"min_decay_score"`
				MinSemanticScore *float64          `json:"min_semantic_score"`
				MinTotalScore    *float64          `json:"min_total_score"`
				RelativeCutoff   *float64          `json:"relative_cutoff"`
				Entities         []string          `json:"entities"`
				DateRange        *struct {
					From string `json:"from"`
					To   string `json:"to"`
				} `json:"date_range"`
			} `json:"filters"`
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
			if assets, err := svc.resolve(r.Context(), ws); err == nil && assets.Store != nil {
				_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "search", 0, 0, engine.RunLabel(), false)
			}
			writeOK(w, http.StatusOK, map[string]any{
				"disabled": true,
				"results":  []any{},
			})
			return
		}
		started := time.Now()
		if strings.TrimSpace(req.Query) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "query is required")
			return
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		topK := req.TopK
		if topK <= 0 {
			topK = 10
		}
		mode := engine.RetrievalMode(strings.ToLower(strings.TrimSpace(req.Mode)))
		if mode == "" {
			mode = engine.ModeSearch
		}
		switch mode {
		case engine.ModeSearch, engine.ModeRecall, engine.ModeRelate, engine.ModeOutcomes:
		default:
			writeErr(w, http.StatusBadRequest, "validation", "invalid mode")
			return
		}
		types := req.Types
		tiers := req.Tiers
		var outcomeResult *core.OutcomeResult
		var minConfidence *float64
		var minDecayScore *float64
		var minSemanticScore *float64
		var minTotalScore *float64
		var relativeCutoff *float64
		var entities []string
		var dateFrom, dateTo *time.Time
		if req.Filters != nil {
			if len(req.Filters.Type) > 0 {
				types = req.Filters.Type
			}
			if len(req.Filters.Tiers) > 0 {
				tiers = req.Filters.Tiers
			}
			if strings.TrimSpace(req.Filters.OutcomeResult) != "" {
				v := core.OutcomeResult(strings.ToLower(strings.TrimSpace(req.Filters.OutcomeResult)))
				switch v {
				case core.OutcomeSuccess, core.OutcomeFailure, core.OutcomePartial:
				default:
					writeErr(w, http.StatusBadRequest, "validation", "invalid outcome_result")
					return
				}
				outcomeResult = &v
			}
			minConfidence = req.Filters.MinConfidence
			minDecayScore = req.Filters.MinDecayScore
			minSemanticScore = req.Filters.MinSemanticScore
			minTotalScore = req.Filters.MinTotalScore
			relativeCutoff = req.Filters.RelativeCutoff
			entities = req.Filters.Entities
			if req.Filters.DateRange != nil {
				fromRaw := strings.TrimSpace(req.Filters.DateRange.From)
				toRaw := strings.TrimSpace(req.Filters.DateRange.To)
				if fromRaw != "" {
					t, ok := parseTimeFlexible(fromRaw)
					if !ok {
						writeErr(w, http.StatusBadRequest, "validation", "invalid date_range.from")
						return
					}
					dateFrom = &t
				}
				if toRaw != "" {
					t, ok := parseTimeFlexible(toRaw)
					if !ok {
						writeErr(w, http.StatusBadRequest, "validation", "invalid date_range.to")
						return
					}
					dateTo = &t
				}
			}
		}
		for _, mt := range types {
			if strings.TrimSpace(string(mt)) == "" {
				continue
			}
			if !core.IsMemoryType(mt) {
				writeErr(w, http.StatusBadRequest, "validation", "invalid memory type")
				return
			}
		}
		parsedTiers, err := parseTiers(tiers)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		if minConfidence != nil && (*minConfidence < 0 || *minConfidence > 1) {
			writeErr(w, http.StatusBadRequest, "validation", "min_confidence must be between 0 and 1")
			return
		}
		if minDecayScore != nil && (*minDecayScore < 0 || *minDecayScore > 1) {
			writeErr(w, http.StatusBadRequest, "validation", "min_decay_score must be between 0 and 1")
			return
		}
		if minSemanticScore != nil && (*minSemanticScore < 0 || *minSemanticScore > 1) {
			writeErr(w, http.StatusBadRequest, "validation", "min_semantic_score must be between 0 and 1")
			return
		}
		if minTotalScore != nil && (*minTotalScore < 0 || *minTotalScore > 1) {
			writeErr(w, http.StatusBadRequest, "validation", "min_total_score must be between 0 and 1")
			return
		}
		if relativeCutoff != nil && (*relativeCutoff < 0 || *relativeCutoff > 1) {
			writeErr(w, http.StatusBadRequest, "validation", "relative_cutoff must be between 0 and 1")
			return
		}
		opt := engine.RetrievalOptions{
			Workspace: ws,
			Query:     req.Query,
			TopK:      topK,
			Mode:      mode,
			Filters: engine.RetrievalFilters{
				Types:         types,
				Tiers:         parsedTiers,
				OutcomeResult: outcomeResult,
				MinConfidence: minConfidence,
				MinDecayScore: minDecayScore,
				Entities:      entities,
				DateFrom:      dateFrom,
				DateTo:        dateTo,
			},
			Policy: engine.RetrievalPolicy{
				MinSemanticScore:    minSemanticScore,
				MinTotalScore:       minTotalScore,
				RelativeScoreCutoff: relativeCutoff,
			},
		}
		out, err := assets.Retrieval.Retrieve(r.Context(), engine.RetrievalOptions{
			Workspace: opt.Workspace,
			Query:     opt.Query,
			TopK:      opt.TopK,
			Mode:      opt.Mode,
			Filters:   opt.Filters,
			Policy:    opt.Policy,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		hits := out.Hits
		if assets.Store != nil {
			_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "search", sumHitTokens(hits), sumHitTokens(hits), engine.RunLabel(), engine.MemoryEnabled())
		}
		results := renderSearchResults(hits, req.Explain)
		strongResults := renderSearchResults(out.StrongHits, req.Explain)
		weakResults := renderSearchResults(out.WeakHits, req.Explain)
		suppressedResults := renderSearchResults(out.SuppressedHits, req.Explain)
		writeOK(w, http.StatusOK, map[string]any{
			"results":            results,
			"strong_results":     strongResults,
			"weak_results":       weakResults,
			"suppressed_results": suppressedResults,
			"result_bands": map[string]int{
				string(engine.BandStrongRecall):    len(strongResults),
				string(engine.BandWeakFamiliarity): len(weakResults),
				string(engine.BandSuppressed):      len(suppressedResults),
			},
			"retrieval_policy": out.Policy,
			"total_tokens":     sumResultTokens(results),
			"search_time_ms":   time.Since(started).Milliseconds(),
			"workspace":        ws,
			"requested_query":  req.Query,
		})
	})
	mux.HandleFunc("/api/v1/memories/recent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
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
		writeOK(w, http.StatusOK, map[string]any{
			"results":   results,
			"workspace": ws,
			"limit":     limit,
		})
	})
	mux.HandleFunc("/api/v1/memories/recall", func(w http.ResponseWriter, r *http.Request) {
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
			if assets, err := svc.resolve(r.Context(), ws); err == nil && assets.Store != nil {
				_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "recall", 0, 0, engine.RunLabel(), false)
			}
			contextBlock := engine.AssembleRecallSections(task, nil)
			data := map[string]any{
				"disabled":      true,
				"context_block": contextBlock,
				"tokens_used":   0,
				"tokens_budget": 0,
				"memories_used": []any{},
			}
			if strings.EqualFold(strings.TrimSpace(req.Format), "raw") {
				data["text"] = contextBlock
			}
			writeOK(w, http.StatusOK, data)
			return
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
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
		if budget <= 0 {
			budget = 4000
		}
		observationBlock := ""
		observationTokens := 0
		observationCount := 0
		observationSessionID := ""
		if req.IncludeObservations && observeEnabled() {
			block, sid, count := buildRecentObservationBlock(r.Context(), assets.Store, ws, strings.TrimSpace(req.ObservationSession), req.ObservationLimit)
			observationBlock = block
			observationSessionID = sid
			observationCount = count
			observationTokens = len(strings.Fields(observationBlock)) + len(strings.Fields("## Recent Observations"))
			if budget-observationTokens > 0 {
				budget = budget - observationTokens
			} else {
				budget = 0
			}
		}
		topK := req.TopK
		if topK <= 0 {
			topK = 50
		}
		retrieved, err := assets.Retrieval.Retrieve(r.Context(), engine.RetrievalOptions{
			Workspace: ws,
			Query:     task,
			TopK:      topK,
			Mode:      engine.ModeRecall,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		rebalanced := engine.RebalanceRecallHits(task, retrieved.Hits)
		included, meta := assets.Clipper.Clip(rebalanced, budget)
		contextBlock := engine.AssembleRecallSectionsWithObservations(task, observationBlock, included)
		tokensUsedTotal := meta.UsedTokens + observationTokens
		if assets.Store != nil {
			_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "recall", tokensUsedTotal, recallBaselineTokens(rebalanced, observationTokens), engine.RunLabel(), engine.MemoryEnabled())
		}
		data := map[string]any{
			"context_block":          contextBlock,
			"tokens_used":            tokensUsedTotal,
			"tokens_budget":          meta.Budget + observationTokens,
			"memories_used":          included,
			"weak_memories":          retrieved.WeakHits,
			"suppressed_memories":    retrieved.SuppressedHits,
			"clipping":               meta,
			"workspace":              ws,
			"requested_top_k":        topK,
			"requested_budget":       meta.Budget + observationTokens,
			"retrieval_policy":       retrieved.Policy,
			"observations_included":  req.IncludeObservations && observeEnabled(),
			"observation_session_id": observationSessionID,
			"observation_count":      observationCount,
			"observation_tokens":     observationTokens,
		}
		if strings.EqualFold(strings.TrimSpace(req.Format), "raw") {
			data["text"] = contextBlock
		}
		writeOK(w, http.StatusOK, data)
	})

	recallPreviewHandler := func(w http.ResponseWriter, r *http.Request) {
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
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
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
		if budget <= 0 {
			budget = 4000
		}
		observationBlock := ""
		observationTokens := 0
		observationCount := 0
		observationSessionID := ""
		originalBudget := budget
		if req.IncludeObservations && observeEnabled() {
			block, sid, count := buildRecentObservationBlock(r.Context(), assets.Store, ws, strings.TrimSpace(req.ObservationSession), req.ObservationLimit)
			observationBlock = block
			observationSessionID = sid
			observationCount = count
			observationTokens = len(strings.Fields(observationBlock)) + len(strings.Fields("## Recent Observations"))
			if budget-observationTokens > 0 {
				budget = budget - observationTokens
			} else {
				budget = 0
			}
		}
		topK := req.TopK
		if topK <= 0 {
			topK = 50
		}
		retrieved, err := assets.Retrieval.Retrieve(r.Context(), engine.RetrievalOptions{
			Workspace: ws,
			Query:     task,
			TopK:      topK,
			Mode:      engine.ModeRecall,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		rebalanced := engine.RebalanceRecallHits(task, retrieved.Hits)
		included, meta := assets.Clipper.Clip(rebalanced, budget)
		tierDist := make(map[string]int)
		mems := make([]map[string]any, 0, len(included))
		var fullMems []core.MemoryEntry
		if req.IncludeMemories {
			fullMems = make([]core.MemoryEntry, 0, len(included))
		}
		for _, h := range included {
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
			"context_block":          engine.AssembleRecallSectionsWithObservations(task, observationBlock, included),
			"tokens_used":            meta.UsedTokens + observationTokens,
			"tokens_budget":          meta.Budget + observationTokens,
			"memories_included":      mems,
			"weak_memories":          renderSearchResults(retrieved.WeakHits, req.Explain),
			"suppressed_memories":    renderSearchResults(retrieved.SuppressedHits, req.Explain),
			"memories_clipped":       renderClipped(meta),
			"tier_distribution":      tierDist,
			"clipping":               meta,
			"workspace":              ws,
			"requested_task":         task,
			"requested_explain":      req.Explain,
			"requested_top_k":        topK,
			"requested_budget":       originalBudget,
			"observations_included":  req.IncludeObservations && observeEnabled(),
			"observation_session_id": observationSessionID,
			"observation_count":      observationCount,
			"observation_tokens":     observationTokens,
			"retrieval_mode":         retrieved.Mode,
			"retrieval_weights":      retrieved.Weights,
			"retrieval_policy":       retrieved.Policy,
			"retrieved_hit_count":    len(retrieved.Hits),
		}
		if assets.Store != nil {
			_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "recall", meta.UsedTokens+observationTokens, recallBaselineTokens(rebalanced, observationTokens), engine.RunLabel(), engine.MemoryEnabled())
		}
		if req.IncludeMemories {
			out["memories_included_full"] = fullMems
		}
		writeOK(w, http.StatusOK, out)
	}

	mux.HandleFunc("/api/v1/memories/recall/preview", recallPreviewHandler)
	mux.HandleFunc("/api/v1/memories/recall-preview", recallPreviewHandler)

	mux.HandleFunc("/api/v1/memories/session-end", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Transcript string `json:"transcript"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		extractor := engine.NewSessionEndExtractor(assets.Writer)
		out, err := extractor.ExtractAndStore(r.Context(), ws, req.Transcript)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	})
	mux.HandleFunc("/api/v1/sessions/end", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Transcript string `json:"transcript"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		extractor := engine.NewSessionEndExtractor(assets.Writer)
		out, err := extractor.ExtractAndStore(r.Context(), ws, req.Transcript)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	})
	mux.HandleFunc("/api/v1/projects/init", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			CWD         string `json:"cwd"`
			ProjectName string `json:"project_name"`
			Study       bool   `json:"study"`
			Reuse       bool   `json:"reuse"`
			Force       bool   `json:"force"`
			NoRule      bool   `json:"no_rule"`
			RulePath    string `json:"rule_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		out, err := mgr.Init(r.Context(), workspace.InitOptions{
			CWD:         req.CWD,
			ProjectName: req.ProjectName,
			Study:       req.Study,
			Reuse:       req.Reuse,
			Force:       req.Force,
			NoRule:      req.NoRule,
			RulePath:    req.RulePath,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	})
	mux.HandleFunc("/api/v1/projects/rename", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			CWD  string `json:"cwd"`
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		out, err := mgr.Rename(r.Context(), workspace.RenameOptions{CWD: req.CWD, From: req.From, To: req.To})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	})
	mux.HandleFunc("/api/v1/projects/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		out, err := mgr.List(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"projects": out})
	})
	mux.HandleFunc("/api/v1/projects/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			ProjectName string `json:"project_name"`
			KeepData    bool   `json:"keep_data"`
			Yes         bool   `json:"yes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		out, err := mgr.Delete(r.Context(), workspace.DeleteOptions{
			ProjectName: req.ProjectName,
			KeepData:    req.KeepData,
			Yes:         req.Yes,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	})
	mux.HandleFunc("/api/v1/memories/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
		if format == "" {
			format = "json"
		}
		memories, err := assets.Store.ListMemoriesByWorkspace(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		if format == "markdown" {
			writeOK(w, http.StatusOK, map[string]any{"markdown": engine.BuildMarkdownExport(ws, memories)})
			return
		}
		writeOK(w, http.StatusOK, engine.BuildExportBundle(ws, memories))
	})
	mux.HandleFunc("/api/v1/memories/import", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		var req engine.ExportBundle
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if req.Version == "" {
			req.Version = engine.ExportVersion
		}
		if req.Version != engine.ExportVersion {
			writeErr(w, http.StatusBadRequest, "validation", "unsupported export version")
			return
		}
		imported := 0
		for _, m := range req.Memories {
			if strings.TrimSpace(m.Workspace) == "" {
				m.Workspace = ws
			}
			mm := m
			if err := assets.Store.UpsertMemory(r.Context(), &mm); err != nil {
				writeErr(w, http.StatusBadRequest, "runtime", err.Error())
				return
			}
			imported++
		}
		writeOK(w, http.StatusOK, map[string]any{
			"version":  req.Version,
			"imported": imported,
		})
	})
	mux.HandleFunc("/api/v1/memories/reconstruct", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Query     string `json:"query"`
			Confirm   bool   `json:"confirm"`
			Workspace string `json:"workspace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		re := engine.NewReconstructionEngine(assets.Store, assets.Writer)
		out, err := re.Reconstruct(r.Context(), ws, req.Query, req.Confirm)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	})

	mux.HandleFunc("/api/v1/observe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !observeEnabled() {
			writeErr(w, http.StatusNotFound, "not_found", "route not enabled")
			return
		}
		var req ObserveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if err := req.Validate(); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.OccurredAt))
		if err != nil {
			occurredAt, err = time.Parse(time.RFC3339, strings.TrimSpace(req.OccurredAt))
			if err != nil {
				writeErr(w, http.StatusBadRequest, "validation", "invalid occurred_at")
				return
			}
		}
		summary := buildObservationSummary(req)
		summary = engine.RedactPrivateAndSecrets(summary)
		summary = engine.ClipString(summary, 1200)
		if strings.TrimSpace(summary) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "summary is empty after redaction")
			return
		}

		hash := computeObservationHash(ws, req.SessionID, req.Kind, req.ToolName, summary)
		obs, dedup, err := assets.Store.InsertObservationDedupWindow(r.Context(), sqlite.ObservationInsert{
			Workspace:   ws,
			SessionID:   req.SessionID,
			OccurredAt:  occurredAt,
			Kind:        strings.TrimSpace(req.Kind),
			ToolName:    strings.TrimSpace(req.ToolName),
			Summary:     summary,
			ContentHash: hash,
		}, 5*time.Minute)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if !dedup {
			_ = assets.Store.UpsertSessionFromObservation(r.Context(), sqlite.ObserveUpsertSessionInput{
				Workspace:   ws,
				SessionID:   req.SessionID,
				ProjectRoot: strings.TrimSpace(req.ProjectRoot),
				CWD:         strings.TrimSpace(req.CWD),
				OccurredAt:  occurredAt,
				Kind:        strings.TrimSpace(req.Kind),
			})
		}

		writeOK(w, http.StatusOK, map[string]any{
			"observation_id": obs.ID,
			"workspace":      ws,
			"session_id":     req.SessionID,
			"deduplicated":   dedup,
			"stored":         !dedup,
		})
	})

	mux.HandleFunc("/api/v1/observations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !observeEnabled() {
			writeErr(w, http.StatusNotFound, "not_found", "route not enabled")
			return
		}
		ws := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		limit := parseIntOrDefault(r.URL.Query().Get("limit"), 50)
		var from *time.Time
		if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				from = &t
			} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
				from = &t
			} else {
				writeErr(w, http.StatusBadRequest, "validation", "invalid from")
				return
			}
		}
		var to *time.Time
		if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				to = &t
			} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
				to = &t
			} else {
				writeErr(w, http.StatusBadRequest, "validation", "invalid to")
				return
			}
		}
		results, err := assets.Store.ListObservations(r.Context(), ws, sessionID, from, to, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace":    ws,
			"session_id":   sessionID,
			"limit":        clamp(limit, 1, 200),
			"observations": results,
		})
	})

	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !observeEnabled() {
			writeErr(w, http.StatusNotFound, "not_found", "route not enabled")
			return
		}
		ws := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		limit := parseIntOrDefault(r.URL.Query().Get("limit"), 50)
		sessions, err := assets.Store.ListSessions(r.Context(), ws, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace": ws,
			"limit":     clamp(limit, 1, 200),
			"sessions":  sessions,
		})
	})

	mux.HandleFunc("/api/v1/llm-usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Workspace        string `json:"workspace"`
			Provider         string `json:"provider"`
			Model            string `json:"model"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			TotalTokens      int    `json:"total_tokens"`
			RunLabel         string `json:"run_label"`
			MemoryEnabled    *bool  `json:"memory_enabled"`
			CreatedAt        string `json:"created_at"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		if strings.TrimSpace(req.Provider) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "provider is required")
			return
		}
		if req.PromptTokens < 0 || req.CompletionTokens < 0 || req.TotalTokens < 0 {
			writeErr(w, http.StatusBadRequest, "validation", "token counts must be non-negative")
			return
		}
		enabled := engine.MemoryEnabled()
		if req.MemoryEnabled != nil {
			enabled = *req.MemoryEnabled
		}
		label := strings.TrimSpace(req.RunLabel)
		if label == "" {
			label = engine.RunLabel()
		}
		var at time.Time
		if strings.TrimSpace(req.CreatedAt) != "" {
			t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.CreatedAt))
			if err != nil {
				t, err = time.Parse(time.RFC3339, strings.TrimSpace(req.CreatedAt))
				if err != nil {
					writeErr(w, http.StatusBadRequest, "validation", "invalid created_at")
					return
				}
			}
			at = t
		}
		if err := assets.Store.AddLLMUsageMetric(r.Context(), sqlite.LLMUsageInsert{
			Workspace:        ws,
			Provider:         strings.TrimSpace(req.Provider),
			Model:            strings.TrimSpace(req.Model),
			PromptTokens:     req.PromptTokens,
			CompletionTokens: req.CompletionTokens,
			TotalTokens:      req.TotalTokens,
			RunLabel:         label,
			MemoryEnabled:    enabled,
			CreatedAt:        at,
		}); err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"stored":         true,
			"workspace":      ws,
			"run_label":      label,
			"memory_enabled": enabled,
		})
	})

	mux.HandleFunc("/api/v1/observations/promote", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !observeEnabled() {
			writeErr(w, http.StatusNotFound, "not_found", "route not enabled")
			return
		}
		var req struct {
			Workspace string        `json:"workspace"`
			SessionID string        `json:"session_id"`
			From      string        `json:"from,omitempty"`
			To        string        `json:"to,omitempty"`
			MaxItems  int           `json:"max_items,omitempty"`
			Type      string        `json:"type,omitempty"`
			Outcome   *core.Outcome `json:"outcome,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		if strings.TrimSpace(req.SessionID) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "session_id is required")
			return
		}
		var from *time.Time
		if raw := strings.TrimSpace(req.From); raw != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				from = &t
			} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
				from = &t
			} else {
				writeErr(w, http.StatusBadRequest, "validation", "invalid from")
				return
			}
		}
		var to *time.Time
		if raw := strings.TrimSpace(req.To); raw != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				to = &t
			} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
				to = &t
			} else {
				writeErr(w, http.StatusBadRequest, "validation", "invalid to")
				return
			}
		}
		memType := core.EpisodicMemory
		if raw := strings.TrimSpace(req.Type); raw != "" {
			mt := core.MemoryType(strings.ToLower(raw))
			if !core.IsMemoryType(mt) {
				writeErr(w, http.StatusBadRequest, "validation", "invalid type")
				return
			}
			memType = mt
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		promoter := engine.NewObservationPromoter(assets.Store, assets.Writer)
		out, err := promoter.Promote(r.Context(), engine.PromoteRequest{
			Workspace:  ws,
			SessionID:  req.SessionID,
			From:       from,
			To:         to,
			MaxItems:   req.MaxItems,
			MemoryType: memType,
			Outcome:    req.Outcome,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	})

	mux.HandleFunc("/api/v1/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspace := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		memories, err := assets.Store.ListMemoriesByWorkspace(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace": workspace,
			"totals": map[string]any{
				"memory_count": len(memories),
			},
		})
	})
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspace := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		memories, err := assets.Store.ListMemoriesByWorkspace(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		typeCounts := map[string]int{}
		tierCounts := map[string]int{}
		diagramCount := 0
		pinnedCount := 0
		totalRetrieveCount := 0
		retrievedMemoryCount := 0
		var lastUpdatedAt time.Time
		var lastAccessedAt time.Time
		for _, m := range memories {
			typeCounts[string(m.Type)]++
			tierCounts[string(m.StorageTier)]++
			if m.Diagram != nil && strings.TrimSpace(m.Diagram.Code) != "" {
				diagramCount++
			}
			if m.Pinned {
				pinnedCount++
			}
			totalRetrieveCount += m.AccessCount
			if m.AccessCount > 0 {
				retrievedMemoryCount++
			}
			if m.UpdatedAt.After(lastUpdatedAt) {
				lastUpdatedAt = m.UpdatedAt
			}
			if m.LastAccessedAt.After(lastAccessedAt) {
				lastAccessedAt = m.LastAccessedAt
			}
		}
		tokenTotals, err := assets.Store.AggregateTokenMetrics(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		tokenGroups, err := assets.Store.AggregateTokenMetricsByGroup(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		tokenByOperation, err := assets.Store.AggregateTokenMetricsByOperation(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		recallTokenTotals := tokenTotalsForOperation(tokenByOperation, "recall")
		enabledGroups := make([]sqlite.TokenMetricGroupTotals, 0, len(tokenGroups))
		disabledGroups := make([]sqlite.TokenMetricGroupTotals, 0, len(tokenGroups))
		for _, g := range tokenGroups {
			if g.MemoryEnabled {
				enabledGroups = append(enabledGroups, g)
			} else {
				disabledGroups = append(disabledGroups, g)
			}
		}
		llmTotals, err := assets.Store.AggregateLLMUsageTotals(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		llmGroups, err := assets.Store.AggregateLLMUsageByGroup(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		llmEnabledGroups := make([]sqlite.LLMUsageGroupTotals, 0, len(llmGroups))
		llmDisabledGroups := make([]sqlite.LLMUsageGroupTotals, 0, len(llmGroups))
		for _, g := range llmGroups {
			if g.MemoryEnabled {
				llmEnabledGroups = append(llmEnabledGroups, g)
			} else {
				llmDisabledGroups = append(llmDisabledGroups, g)
			}
		}
		var dbSize int64
		if svc.BaseDir != "" {
			dbPath := filepath.Join(svc.BaseDir, workspace+".db")
			if st, statErr := os.Stat(dbPath); statErr == nil {
				dbSize = st.Size()
			}
		}
		lastActivity := lastUpdatedAt
		if lastAccessedAt.After(lastActivity) {
			lastActivity = lastAccessedAt
		}
		lastUpdated := ""
		if !lastUpdatedAt.IsZero() {
			lastUpdated = lastUpdatedAt.UTC().Format(time.RFC3339Nano)
		}
		lastAccessed := ""
		if !lastAccessedAt.IsZero() {
			lastAccessed = lastAccessedAt.UTC().Format(time.RFC3339Nano)
		}
		lastActivityStr := ""
		if !lastActivity.IsZero() {
			lastActivityStr = lastActivity.UTC().Format(time.RFC3339Nano)
		}
		neverReachedMemoryCount := len(memories) - retrievedMemoryCount
		retrievalCoveragePercent := 0.0
		neverReachedPercent := 0.0
		if len(memories) > 0 {
			retrievalCoveragePercent = (float64(retrievedMemoryCount) / float64(len(memories))) * 100
			neverReachedPercent = (float64(neverReachedMemoryCount) / float64(len(memories))) * 100
		}
		lowReachPercentile := 25
		lowReachThreshold, lowReachMemoryCount := computeLowReachStats(memories, lowReachPercentile)
		topRetrievedMemories := buildTopRetrievedMemories(memories, 5)
		writeOK(w, http.StatusOK, map[string]any{
			"workspace":                     workspace,
			"memory_count":                  len(memories),
			"db_size_bytes":                 dbSize,
			"memory_type_counts":            typeCounts,
			"storage_tier_counts":           tierCounts,
			"diagram_count":                 diagramCount,
			"pinned_count":                  pinnedCount,
			"retrieve_count_total":          totalRetrieveCount,
			"retrieved_memory_count":        retrievedMemoryCount,
			"never_reached_memory_count":    neverReachedMemoryCount,
			"retrieval_coverage_percent":    retrievalCoveragePercent,
			"never_reached_percent":         neverReachedPercent,
			"low_reach_percentile":          lowReachPercentile,
			"low_reach_threshold":           lowReachThreshold,
			"low_reach_memory_count":        lowReachMemoryCount,
			"top_retrieved_memories":        topRetrievedMemories,
			"last_memory_updated_at":        lastUpdated,
			"last_memory_accessed_at":       lastAccessed,
			"last_activity":                 lastActivityStr,
			"token_metrics":                 tokenTotals,
			"token_metrics_by_operation":    tokenByOperation,
			"token_metrics_by_group":        enabledGroups,
			"raw_token_metrics_by_group":    disabledGroups,
			"token_metrics_by_group_all":    tokenGroups,
			"recall_token_metrics":          recallTokenTotals,
			"llm_usage_totals":              llmTotals,
			"llm_usage_by_group":            llmEnabledGroups,
			"raw_llm_usage_by_group":        llmDisabledGroups,
			"llm_usage_by_group_all":        llmGroups,
			"overall_token_savings_percent": percentSaved(tokenTotals.BaselineTokens, tokenTotals.SavedTokens),
			"recall_token_savings_percent":  percentSaved(recallTokenTotals.BaselineTokens, recallTokenTotals.SavedTokens),
			"token_savings_percent":         percentSaved(recallTokenTotals.BaselineTokens, recallTokenTotals.SavedTokens),
		})
	})
	return mux
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

func observeEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_MEMORY_OBSERVE_ENABLED")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func memoryEnabled() bool {
	return engine.MemoryEnabled()
}

func buildObservationSummary(req ObserveRequest) string {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	switch kind {
	case "session_start":
		return "session_start"
	case "session_end":
		return "session_end"
	}
	tool := strings.TrimSpace(req.ToolName)
	prompt := strings.TrimSpace(req.Prompt)

	var b strings.Builder
	if kind != "" {
		b.WriteString(kind)
	}
	if tool != "" {
		if b.Len() > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("tool=")
		b.WriteString(tool)
	}
	if prompt != "" {
		if b.Len() > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("prompt=")
		b.WriteString(engine.ClipString(prompt, 240))
	}
	if req.ToolInput != nil {
		if input := stringifyJSON(req.ToolInput); strings.TrimSpace(input) != "" {
			if b.Len() > 0 {
				b.WriteString(" | ")
			}
			b.WriteString("input=")
			b.WriteString(engine.ClipString(input, 320))
		}
	}
	if b.Len() == 0 {
		return kind
	}
	return b.String()
}

func stringifyJSON(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func computeObservationHash(workspace, sessionID, kind, toolName, summary string) string {
	h := sha256.New()
	parts := []string{workspace, sessionID, kind, toolName, summary}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func parseIntOrDefault(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func percentSaved(baseline, saved int) float64 {
	if baseline <= 0 || saved <= 0 {
		return 0
	}
	return (float64(saved) / float64(baseline)) * 100
}

func tokenTotalsForOperation(items []sqlite.TokenMetricOperationTotals, operation string) sqlite.TokenMetricTotals {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Operation), operation) {
			return item.TokenMetricTotals
		}
	}
	return sqlite.TokenMetricTotals{}
}

type topRetrievedMemory struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	StorageTier    string `json:"storage_tier"`
	AccessCount    int    `json:"access_count"`
	LastAccessedAt string `json:"last_accessed_at,omitempty"`
	Pinned         bool   `json:"pinned"`
	Preview        string `json:"preview"`
}

func buildTopRetrievedMemories(memories []core.MemoryEntry, limit int) []topRetrievedMemory {
	if limit <= 0 {
		limit = 5
	}
	items := make([]core.MemoryEntry, 0, len(memories))
	for _, m := range memories {
		if m.AccessCount <= 0 {
			continue
		}
		items = append(items, m)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].AccessCount != items[j].AccessCount {
			return items[i].AccessCount > items[j].AccessCount
		}
		if !items[i].LastAccessedAt.Equal(items[j].LastAccessedAt) {
			return items[i].LastAccessedAt.After(items[j].LastAccessedAt)
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]topRetrievedMemory, 0, len(items))
	for _, m := range items {
		lastAccessed := ""
		if !m.LastAccessedAt.IsZero() {
			lastAccessed = m.LastAccessedAt.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, topRetrievedMemory{
			ID:             m.ID,
			Type:           string(m.Type),
			StorageTier:    string(m.StorageTier),
			AccessCount:    m.AccessCount,
			LastAccessedAt: lastAccessed,
			Pinned:         m.Pinned,
			Preview:        memoryPreview(m.Content, 96),
		})
	}
	return out
}

func computeLowReachStats(memories []core.MemoryEntry, percentile int) (threshold int, count int) {
	if percentile <= 0 {
		percentile = 25
	}
	reached := make([]int, 0, len(memories))
	for _, m := range memories {
		if m.AccessCount > 0 {
			reached = append(reached, m.AccessCount)
		}
	}
	if len(reached) == 0 {
		return 0, 0
	}
	sort.Ints(reached)
	rank := int(math.Ceil((float64(percentile) / 100) * float64(len(reached))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(reached) {
		rank = len(reached)
	}
	threshold = reached[rank-1]
	for _, hits := range reached {
		if hits <= threshold {
			count++
		}
	}
	return threshold, count
}

func memoryPreview(content string, limit int) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if clean == "" {
		return "-"
	}
	return engine.ClipString(clean, limit)
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

func workspaceFromRequest(r *http.Request, fallback string) string {
	ws := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if ws == "" {
		return fallback
	}
	return ws
}

type searchResult struct {
	core.MemoryEntry
	Tier             string                   `json:"tier,omitempty"`
	Score            float64                  `json:"score,omitempty"`
	ScoreBreakdown   map[string]float64       `json:"score_breakdown,omitempty"`
	MatchReason      string                   `json:"match_reason,omitempty"`
	TombstoneHint    any                      `json:"tombstone_hint,omitempty"`
	Band             string                   `json:"band,omitempty"`
	ExclusionReasons []engine.ExclusionReason `json:"exclusion_reasons,omitempty"`
}

func renderSearchResults(hits []engine.RetrievalHit, explain bool) []searchResult {
	out := make([]searchResult, 0, len(hits))
	for _, h := range hits {
		item := searchResult{
			MemoryEntry: h.Memory,
		}
		if explain {
			item.Tier = string(h.Memory.StorageTier)
			item.Score = h.Score
			item.ScoreBreakdown = scoreBreakdownForHit(h)
			item.MatchReason = matchReasonForHit(h)
			item.TombstoneHint = nil
			item.Band = string(h.Band)
			item.ExclusionReasons = h.ExclusionReasons
		}
		out = append(out, item)
	}
	return out
}

func scoreBreakdownForHit(h engine.RetrievalHit) map[string]float64 {
	return map[string]float64{
		"semantic_similarity": h.Breakdown.Semantic,
		"recency":             h.Breakdown.Recency,
		"outcome_boost":       h.Breakdown.Outcome,
		"decay_weight":        h.Breakdown.Decay,
		"tier_bias":           h.Breakdown.TierBias,
		"salience":            h.Breakdown.Salience,
		"suppression":         h.Breakdown.Suppression,
		"activation":          h.Breakdown.Activation,
		"relative_to_best":    h.Breakdown.RelativeToBest,
		"total":               h.Breakdown.Total,
	}
}

func matchReasonForHit(h engine.RetrievalHit) string {
	sb := scoreBreakdownForHit(h)
	bestK := ""
	bestV := -1.0
	for k, v := range sb {
		if v > bestV {
			bestV = v
			bestK = k
		}
	}
	switch bestK {
	case "semantic_similarity":
		return "High semantic similarity"
	case "recency":
		return "Recently updated"
	case "outcome_boost":
		return "Outcome memory boost"
	case "decay_weight":
		return "Low decay (still relevant)"
	case "tier_bias":
		return "Tier bias applied"
	default:
		return "Ranked by combined signals"
	}
}

func sumResultTokens(results []searchResult) int {
	total := 0
	for _, r := range results {
		total += len(strings.Fields(r.Content))
	}
	return total
}

func parseTiers(in []string) ([]core.StorageTier, error) {
	out := make([]core.StorageTier, 0, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(strings.ToLower(raw))
		if v == "" {
			continue
		}
		t := core.StorageTier(v)
		if !core.IsStorageTier(t) {
			return nil, fmt.Errorf("invalid tier: %s", v)
		}
		out = append(out, t)
	}
	return out, nil
}

func parseTimeFlexible(s string) (time.Time, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func renderClipped(meta engine.ClipMetadata) []map[string]any {
	out := make([]map[string]any, 0, len(meta.ClippedDetails))
	for _, c := range meta.ClippedDetails {
		out = append(out, map[string]any{
			"id":               c.ID,
			"reason":           string(c.Reason),
			"would_add_tokens": c.Tokens,
		})
	}
	return out
}
