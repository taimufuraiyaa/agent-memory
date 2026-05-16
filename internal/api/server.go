package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
	"github.com/time/timebooks/agent-memory/internal/workspace"
)

type Service struct {
	Workspace string
	Writer    *engine.WritePipeline
	Retrieval *engine.RetrievalEngine
	Clipper   *engine.TokenClipper
	Store     *sqlite.Store
	BaseDir   string
}

func NewMux(svc *Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/dashboard/", dashboardHandler())
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/", http.StatusPermanentRedirect)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	writeMemoryHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
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
		src := core.MemorySource{Type: core.SourceUserInput}
		if req.Source != nil && req.Source.Type != "" {
			src = *req.Source
		}
		out, err := svc.Writer.Write(r.Context(), engine.WriteInput{
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

	mux.HandleFunc("/api/v1/memories/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		started := time.Now()
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
				Type          []core.MemoryType `json:"type"`
				Tiers         []string          `json:"tiers"`
				OutcomeResult string            `json:"outcome_result"`
				MinConfidence *float64          `json:"min_confidence"`
				MinDecayScore *float64          `json:"min_decay_score"`
				Entities      []string          `json:"entities"`
				DateRange     *struct {
					From string `json:"from"`
					To   string `json:"to"`
				} `json:"date_range"`
			} `json:"filters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.TrimSpace(req.Query) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "query is required")
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
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
		}
		out, err := svc.Retrieval.Retrieve(r.Context(), engine.RetrievalOptions{
			Workspace: opt.Workspace,
			Query:     opt.Query,
			TopK:      opt.TopK,
			Mode:      opt.Mode,
			Filters:   opt.Filters,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		hits := out.Hits
		if svc.Store != nil {
			_ = svc.Store.AddTokenMetric(r.Context(), ws, "search", sumHitTokens(hits), sumHitTokens(hits))
		}
		results := renderSearchResults(hits, req.Explain)
		writeOK(w, http.StatusOK, map[string]any{
			"results":         results,
			"total_tokens":    sumResultTokens(results),
			"search_time_ms":  time.Since(started).Milliseconds(),
			"workspace":       ws,
			"requested_query": req.Query,
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
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
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
		topK := req.TopK
		if topK <= 0 {
			topK = 50
		}
		retrieved, err := svc.Retrieval.Retrieve(r.Context(), engine.RetrievalOptions{
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
		included, meta := svc.Clipper.Clip(rebalanced, budget)
		contextBlock := engine.AssembleRecallSections(task, included)
		data := map[string]any{
			"context_block":    contextBlock,
			"tokens_used":      meta.UsedTokens,
			"tokens_budget":    meta.Budget,
			"memories_used":    included,
			"clipping":         meta,
			"workspace":        ws,
			"requested_top_k":  topK,
			"requested_budget": budget,
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

			Task    string `json:"task"`
			TopK    int    `json:"top_k"`
			Budget  int    `json:"budget"`
			Explain bool   `json:"explain"`
			IncludeMemories bool `json:"include_memories"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
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
		topK := req.TopK
		if topK <= 0 {
			topK = 50
		}
		retrieved, err := svc.Retrieval.Retrieve(r.Context(), engine.RetrievalOptions{
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
		included, meta := svc.Clipper.Clip(rebalanced, budget)
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
			"context_block":       engine.AssembleRecallSections(task, included),
			"tokens_used":         meta.UsedTokens,
			"tokens_budget":       meta.Budget,
			"memories_included":   mems,
			"memories_clipped":    renderClipped(meta),
			"tier_distribution":   tierDist,
			"clipping":            meta,
			"workspace":           ws,
			"requested_task":      task,
			"requested_explain":   req.Explain,
			"requested_top_k":     topK,
			"requested_budget":    budget,
			"retrieval_mode":      retrieved.Mode,
			"retrieval_weights":   retrieved.Weights,
			"retrieved_hit_count": len(retrieved.Hits),
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
		extractor := engine.NewSessionEndExtractor(svc.Writer)
		out, err := extractor.ExtractAndStore(r.Context(), svc.Workspace, req.Transcript)
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
		extractor := engine.NewSessionEndExtractor(svc.Writer)
		out, err := extractor.ExtractAndStore(r.Context(), svc.Workspace, req.Transcript)
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
		if svc.Store == nil {
			writeErr(w, http.StatusBadRequest, "runtime", "store unavailable")
			return
		}
		format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
		if format == "" {
			format = "json"
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		memories, err := svc.Store.ListMemoriesByWorkspace(r.Context(), ws)
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
		if svc.Store == nil {
			writeErr(w, http.StatusBadRequest, "runtime", "store unavailable")
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
				m.Workspace = svc.Workspace
			}
			mm := m
			if err := svc.Store.UpsertMemory(r.Context(), &mm); err != nil {
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
		if svc.Store == nil {
			writeErr(w, http.StatusBadRequest, "runtime", "store unavailable")
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
		re := engine.NewReconstructionEngine(svc.Store, svc.Writer)
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		out, err := re.Reconstruct(r.Context(), ws, req.Query, req.Confirm)
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
		if svc.Store == nil {
			writeErr(w, http.StatusBadRequest, "runtime", "store unavailable")
			return
		}
		workspace := workspaceFromRequest(r, svc.Workspace)
		memories, err := svc.Store.ListMemoriesByWorkspace(r.Context(), workspace)
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
		if svc.Store == nil {
			writeErr(w, http.StatusBadRequest, "runtime", "store unavailable")
			return
		}
		workspace := workspaceFromRequest(r, svc.Workspace)
		memories, err := svc.Store.ListMemoriesByWorkspace(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		tokenTotals, err := svc.Store.AggregateTokenMetrics(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		var dbSize int64
		if svc.BaseDir != "" {
			dbPath := filepath.Join(svc.BaseDir, workspace+".db")
			if st, statErr := os.Stat(dbPath); statErr == nil {
				dbSize = st.Size()
			}
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace":             workspace,
			"memory_count":          len(memories),
			"db_size_bytes":         dbSize,
			"token_metrics":         tokenTotals,
			"token_savings_percent": percentSaved(tokenTotals.BaselineTokens, tokenTotals.SavedTokens),
		})
	})
	return mux
}

func percentSaved(baseline, saved int) float64 {
	if baseline <= 0 || saved <= 0 {
		return 0
	}
	return (float64(saved) / float64(baseline)) * 100
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
	Tier           string             `json:"tier,omitempty"`
	Score          float64            `json:"score,omitempty"`
	ScoreBreakdown map[string]float64 `json:"score_breakdown,omitempty"`
	MatchReason    string             `json:"match_reason,omitempty"`
	TombstoneHint  any                `json:"tombstone_hint,omitempty"`
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
