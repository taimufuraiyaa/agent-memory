package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/engine"
)

// searchHandler implements POST /api/v1/memories/search: runs a
// semantic/multi-signal retrieval query against a single workspace, or (when
// workspace == allProjectsScope) fans the query out across every known
// project and merges the results.
func searchHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			Depth       int               `json:"depth"`
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
			if ws != allProjectsScope {
				if assets, err := svc.resolve(r.Context(), ws); err == nil && assets.Store != nil {
					_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "search", 0, 0, engine.RunLabel(), false)
				}
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
		var assets *workspaceAssets
		var err error
		if ws != allProjectsScope {
			assets, err = svc.resolve(r.Context(), ws)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
				return
			}
		}
		requestID := uuid.New().String()
		if ws != allProjectsScope && assets != nil && assets.Store != nil {
			_ = assets.Store.LogRetrievalRequest(r.Context(), requestID, ws, "search", req.Query)
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
		case engine.ModeSearch, engine.ModeRecall, engine.ModeRelate, engine.ModeOutcomes, engine.ModeGraphExpand:
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
			Depth:     req.Depth,
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
		var (
			hits           []engine.RetrievalHit
			strongHits     []engine.RetrievalHit
			weakHits       []engine.RetrievalHit
			suppressedHits []engine.RetrievalHit
			policySnapshot engine.RetrievalPolicySnapshot
			tokenTotal     int
		)
		if ws == allProjectsScope {
			projectNames, err := svc.listProjectNames(r.Context())
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
				return
			}
			for _, projectName := range projectNames {
				projectAssets, err := svc.resolve(r.Context(), projectName)
				if err != nil {
					log.Printf("[agent-memory] failed to resolve project %q during all-projects search: %v", projectName, err)
					continue
				}
				projectOut, err := projectAssets.Retrieval.Retrieve(r.Context(), engine.RetrievalOptions{
					Workspace: projectName,
					Query:     opt.Query,
					TopK:      opt.TopK,
					Mode:      opt.Mode,
					Depth:     opt.Depth,
					Filters:   opt.Filters,
					Policy:    opt.Policy,
				})
				if err != nil {
					log.Printf("[agent-memory] failed to retrieve memories from project %q during all-projects search: %v", projectName, err)
					continue
				}
				hits = append(hits, projectOut.Hits...)
				strongHits = append(strongHits, projectOut.StrongHits...)
				weakHits = append(weakHits, projectOut.WeakHits...)
				suppressedHits = append(suppressedHits, projectOut.SuppressedHits...)
				policySnapshot = projectOut.Policy
				projectTokens := sumHitTokens(projectOut.Hits)
				tokenTotal += projectTokens
				if projectAssets.Store != nil {
					_ = projectAssets.Store.AddTokenMetricV2(r.Context(), projectName, "search", projectTokens, projectTokens, engine.RunLabel(), engine.MemoryEnabled())
				}
			}
			rankRetrievalHits(hits)
			rankRetrievalHits(strongHits)
			rankRetrievalHits(weakHits)
			rankRetrievalHits(suppressedHits)
			hits = trimRetrievalHits(hits, topK)
			strongHits = trimRetrievalHits(strongHits, topK)
			weakHits = trimRetrievalHits(weakHits, topK)
			suppressedHits = trimRetrievalHits(suppressedHits, topK)
		} else {
			out, err := assets.Retrieval.Retrieve(r.Context(), engine.RetrievalOptions{
				Workspace: opt.Workspace,
				Query:     opt.Query,
				TopK:      opt.TopK,
				Mode:      opt.Mode,
				Depth:     opt.Depth,
				Filters:   opt.Filters,
				Policy:    opt.Policy,
			})
			if err != nil {
				writeErr(w, http.StatusBadRequest, "runtime", err.Error())
				return
			}
			hits = out.Hits
			strongHits = out.StrongHits
			weakHits = out.WeakHits
			suppressedHits = out.SuppressedHits
			policySnapshot = out.Policy
			tokenTotal = sumHitTokens(hits)
			if assets.Store != nil {
				_ = assets.Store.AddTokenMetricV2(r.Context(), ws, "search", tokenTotal, tokenTotal, engine.RunLabel(), engine.MemoryEnabled())
			}
		}
		results := renderSearchResults(hits, req.Explain)
		strongResults := renderSearchResults(strongHits, req.Explain)
		weakResults := renderSearchResults(weakHits, req.Explain)
		suppressedResults := renderSearchResults(suppressedHits, req.Explain)
		writeOK(w, http.StatusOK, map[string]any{
			"request_id":         requestID,
			"results":            results,
			"strong_results":     strongResults,
			"weak_results":       weakResults,
			"suppressed_results": suppressedResults,
			"result_bands": map[string]int{
				string(engine.BandStrongRecall):    len(strongResults),
				string(engine.BandWeakFamiliarity): len(weakResults),
				string(engine.BandSuppressed):      len(suppressedResults),
			},
			"retrieval_policy": policySnapshot,
			"total_tokens":     tokenTotal,
			"search_time_ms":   time.Since(started).Milliseconds(),
			"workspace":        ws,
			"requested_query":  req.Query,
		})
	}
}
