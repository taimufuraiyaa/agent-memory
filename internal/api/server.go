package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/time/timebooks/agent-memory/internal/api/dashboard"
	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/observability"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
	"github.com/time/timebooks/agent-memory/internal/workspace"
)

type Service struct {
	Workspace         string
	BaseDir           string
	EmbeddingProvider embeddings.Provider
	Scheduler         Scheduler

	mu     sync.RWMutex
	stores map[string]*workspaceAssets
}

const allProjectsScope = "__all_projects__"

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
	
	// Create shared cache for efficient query reuse
	cache := engine.NewQueryCache(engine.DefaultQueryCacheConfig())
	searcher := engine.NewVectorSearcher(store, s.EmbeddingProvider)
	retrieval := engine.NewRetrievalEngineWithSharedCache(searcher, cache)
	
	assets = &workspaceAssets{
		Store:     store,
		Writer:    engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{
			Embedder: s.EmbeddingProvider,
			Cache:    cache,
		}),
		Retrieval: retrieval,
		Clipper:   engine.NewTokenClipper(nil),
	}
	if s.stores == nil {
		s.stores = make(map[string]*workspaceAssets)
	}
	s.stores[ws] = assets
	return assets, nil
}

func (s *Service) listProjectNames(ctx context.Context) ([]string, error) {
	mgr, err := workspace.NewManager(s.BaseDir)
	if err != nil {
		return nil, err
	}
	items, err := mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		names = append(names, item.Name)
	}
	sort.Strings(names)
	return names, nil
}

func rankRetrievalHits(hits []engine.RetrievalHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		left := hits[i].Memory
		right := hits[j].Memory
		if left.AccessCount != right.AccessCount {
			return left.AccessCount > right.AccessCount
		}
		if left.UpdatedAt != right.UpdatedAt {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if left.Workspace != right.Workspace {
			return left.Workspace < right.Workspace
		}
		return left.ID < right.ID
	})
}

func trimRetrievalHits(hits []engine.RetrievalHit, limit int) []engine.RetrievalHit {
	if limit <= 0 || len(hits) <= limit {
		return hits
	}
	return hits[:limit]
}

// instrumentedResponseWriter wraps http.ResponseWriter to capture status code and bytes written.
type instrumentedResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *instrumentedResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *instrumentedResponseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// NewMux returns the HTTP ServeMux with all route handlers registered.
// Wrap the returned mux with InstrumentedHandler to add Prometheus metrics.
func NewMux(svc *Service) *http.ServeMux {
	mux := http.NewServeMux()
	// Prometheus metrics scrape endpoint
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		var memoryCount int
		var lastLifecycleRun string
		var dbSizeMB float64

		ws := svc.Workspace
		if ws == "" {
			ws = "agent-memory" // fallback if empty
		}

		assets, err := svc.resolve(r.Context(), ws)
		if err == nil && assets.Store != nil {
			if summary, err := assets.Store.GetWorkspaceActivitySummary(r.Context(), ws); err == nil {
				memoryCount = summary.MemoryCount
			}
			if state, err := assets.Store.GetSchedulerWorkspaceState(r.Context(), ws); err == nil && state != nil && !state.LastCompletedAt.IsZero() {
				lastLifecycleRun = state.LastCompletedAt.Format(time.RFC3339)
			}
		}

		dbPath := filepath.Join(svc.BaseDir, ws+".db")
		if fi, err := os.Stat(dbPath); err == nil {
			dbSizeMB = float64(fi.Size()) / (1024 * 1024)
		}

		providerName := "unknown"
		providerVersion := "unknown"
		onnxAvailable := false
		if svc.EmbeddingProvider != nil {
			providerName = svc.EmbeddingProvider.Name()
			providerVersion = svc.EmbeddingProvider.ModelVersion()
			onnxAvailable = (providerName == "onnx-minilm-l6-v2")
		}

		// Round dbSizeMB to two decimal places
		dbSizeMB = math.Round(dbSizeMB*100) / 100

		writeOK(w, http.StatusOK, map[string]any{
			"status":                  status,
			"db_size_mb":              dbSizeMB,
			"memory_count":            memoryCount,
			"last_lifecycle_run":      lastLifecycleRun,
			"embedding_provider":      providerName,
			"embedding_model_version": providerVersion,
			"onnx_runtime_available":   onnxAvailable,
		})
	})
	mux.HandleFunc("/ops/dashboard", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(OperatorDashboardHTML))
	})
	mux.HandleFunc("/api/v1/scheduler/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if svc.Scheduler == nil {
			writeOK(w, http.StatusOK, &SchedulerStatus{Enabled: false, Workspaces: []SchedulerWorkspaceStatus{}})
			return
		}
		status, err := svc.Scheduler.Status(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, status)
	})
	mux.HandleFunc("/api/v1/scheduler/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if svc.Scheduler == nil {
			writeOK(w, http.StatusOK, map[string]any{
				"workspace": strings.TrimSpace(r.URL.Query().Get("workspace")),
				"limit":     clamp(parseIntOrDefault(r.URL.Query().Get("limit"), 30), 1, 200),
				"runs":      []SchedulerRun{},
			})
			return
		}
		workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
		limit := clamp(parseIntOrDefault(r.URL.Query().Get("limit"), 30), 1, 200)
		runs, err := svc.Scheduler.History(r.Context(), workspace, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace": workspace,
			"limit":     limit,
			"runs":      runs,
		})
	})
	mux.HandleFunc("/api/v1/scheduler/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if svc.Scheduler == nil {
			writeErr(w, http.StatusNotFound, "not_found", "scheduler not available")
			return
		}
		workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if workspace == "" {
			workspace = workspaceFromRequest(r, svc.Workspace)
		}
		force := strings.TrimSpace(r.URL.Query().Get("force"))
		run, err := svc.Scheduler.RunNow(r.Context(), workspace, force == "1" || strings.EqualFold(force, "true"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, run)
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
	mux.HandleFunc("/api/v1/memories/pin", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Workspace string `json:"workspace"`
			MemoryID  string `json:"memory_id"`
			Pinned    bool   `json:"pinned"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		if strings.TrimSpace(req.MemoryID) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "memory_id is required")
			return
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
		updated, err := assets.Store.SetPinned(r.Context(), strings.TrimSpace(req.MemoryID), req.Pinned)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "not_found", "memory not found")
				return
			}
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace":      ws,
			"memory_id":      req.MemoryID,
			"pinned":         req.Pinned,
			"updated_memory": updated,
		})
	})
	mux.HandleFunc("/api/v1/memories/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Workspace string   `json:"workspace"`
			MemoryIDs []string `json:"memory_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		ids := make([]string, 0, len(req.MemoryIDs))
		seen := make(map[string]struct{}, len(req.MemoryIDs))
		for _, raw := range req.MemoryIDs {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			writeErr(w, http.StatusBadRequest, "validation", "memory_ids is required")
			return
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
		if err := assets.Store.DeleteByIDs(r.Context(), ids); err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace":     ws,
			"memory_ids":    ids,
			"deleted_count": len(ids),
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
		data := map[string]any{
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
		out, err := engine.RunSessionEndLifecycle(r.Context(), ws, req.Transcript, assets.Store, assets.Writer)
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
		out, err := engine.RunSessionEndLifecycle(r.Context(), ws, req.Transcript, assets.Store, assets.Writer)
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
		filter := engine.NewRegexSecurityFilter()
		imported := 0
		skipped := make([]map[string]any, 0)
		for _, m := range req.Memories {
			if strings.TrimSpace(m.Workspace) == "" {
				m.Workspace = ws
			}
			if reason := sanitizeImportedMemory(r.Context(), &m, filter); reason != "" {
				skipped = append(skipped, map[string]any{
					"id":        m.ID,
					"workspace": m.Workspace,
					"reason":    reason,
				})
				continue
			}
			if err := assets.Store.UpsertMemory(r.Context(), &m); err != nil {
				writeErr(w, http.StatusBadRequest, "runtime", err.Error())
				return
			}
			imported++
		}
		writeOK(w, http.StatusOK, map[string]any{
			"version":  req.Version,
			"imported": imported,
			"skipped":  skipped,
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

	mux.HandleFunc("/api/v1/benchmark/ingest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req sqlite.BenchmarkRun
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		if strings.TrimSpace(req.RunID) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "run_id is required")
			return
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
		req.Workspace = ws
		stored, err := assets.Store.InsertBenchmarkRun(r.Context(), req)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"stored":    true,
			"workspace": ws,
			"run":       stored,
		})
	})

	mux.HandleFunc("/api/v1/benchmark/runs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
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
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		limit := parseIntOrDefault(r.URL.Query().Get("limit"), 10)
		runs, err := assets.Store.ListBenchmarkRuns(r.Context(), ws, limit)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace": ws,
			"limit":     clamp(limit, 1, 200),
			"runs":      runs,
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
	mux.HandleFunc("/api/v1/graph", func(w http.ResponseWriter, r *http.Request) {
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
		relations, err := assets.Store.ListWorkspaceRelations(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}

		type GraphNode struct {
			ID          string           `json:"id"`
			Content     string           `json:"content"`
			Type        core.MemoryType  `json:"type"`
			StorageTier core.StorageTier `json:"storage_tier"`
		}

		nodes := make([]GraphNode, 0, len(memories))
		for _, m := range memories {
			nodes = append(nodes, GraphNode{
				ID:          m.ID,
				Content:     m.Content,
				Type:        m.Type,
				StorageTier: m.StorageTier,
			})
		}

		writeOK(w, http.StatusOK, map[string]any{
			"workspace": workspace,
			"nodes":     nodes,
			"edges":     relations,
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

		var schedulerSummary any
		if svc.Scheduler != nil {
			if status, err := svc.Scheduler.Status(r.Context()); err == nil && status != nil {
				schedulerSummary = schedulerSummaryForWorkspace(status, workspace)
			}
		} else {
			schedulerSummary = externalSchedulerSummary(r.Context(), svc.BaseDir, workspace)
		}
		cacheStats := assets.Retrieval.CacheStats()
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
			"scheduler":                     schedulerSummary,
			"cache": map[string]any{
				"enabled":            cacheStats.Enabled,
				"embedding_entries":  cacheStats.EmbeddingEntries,
				"result_entries":     cacheStats.ResultEntries,
				"embedding_hits":     cacheStats.EmbeddingHits,
				"embedding_misses":   cacheStats.EmbeddingMisses,
				"result_hits":        cacheStats.ResultHits,
				"result_misses":      cacheStats.ResultMisses,
				"embedding_hit_rate": cacheStats.EmbeddingHitRate(),
				"result_hit_rate":    cacheStats.ResultHitRate(),
			},
		})
	})

	// Visualization endpoints
	mux.HandleFunc("/api/v1/visualizations/graph", handleMemoryGraph(svc))
	mux.HandleFunc("/api/v1/visualizations/decay-timeline", handleDecayTimeline(svc))
	mux.HandleFunc("/api/v1/visualizations/entity-network", handleEntityNetwork(svc))

	// Serve embedded dashboard assets
	mux.Handle("/dashboard/", serveDashboard())

	return mux
}

// InstrumentedHandler wraps the given handler with Prometheus HTTP instrumentation
// middleware that records request count, duration, size, and in-flight gauges.
func InstrumentedHandler(h http.Handler) http.Handler {
	metrics := observability.GetRegistry()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.HTTPRequestsInFlight.Inc()
		defer metrics.HTTPRequestsInFlight.Dec()

		irw := &instrumentedResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		timer := observability.NewTimer()

		// Record request size
		if r.ContentLength > 0 {
			metrics.HTTPRequestSize.WithLabelValues(r.Method, r.URL.Path).Observe(float64(r.ContentLength))
		}

		h.ServeHTTP(irw, r)

		duration := timer.Duration()
		statusStr := fmt.Sprintf("%d", irw.statusCode)
		metrics.HTTPRequestsTotal.WithLabelValues(r.Method, r.URL.Path, statusStr).Inc()
		timer.ObserveDuration(metrics.HTTPRequestDuration.WithLabelValues(r.Method, r.URL.Path))
		if irw.bytesWritten > 0 {
			metrics.HTTPResponseSize.WithLabelValues(r.Method, r.URL.Path).Observe(float64(irw.bytesWritten))
		}
		_ = duration
	})
}

// serveDashboard returns an HTTP handler that serves the embedded dashboard
// assets (built via `make build-dashboard`/`make embed-dashboard` and
// embedded into the binary with go:embed in internal/api/dashboard).
// If this binary was built without embedded assets, it returns a handler
// that explains how to build them instead of a bare 404.
func serveDashboard() http.Handler {
	if !dashboard.HasEmbeddedAssets() {
		return http.StripPrefix("/dashboard/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Dashboard assets not embedded in this binary. Run: make build-with-dashboard", http.StatusNotFound)
		}))
	}
	return http.StripPrefix("/dashboard/", dashboard.GetEmbeddedHandler())
}

