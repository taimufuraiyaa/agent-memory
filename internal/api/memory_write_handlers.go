package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/engine"
)

// writeMemoryHandler implements POST /api/v1/memories and
// /api/v1/memories/write: writes a single memory entry via the workspace's
// write pipeline.
func writeMemoryHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
}

// memoriesFeedbackHandler implements POST /api/v1/memories/feedback:
// records retrieval feedback (and optionally a reconsolidation action) for a
// memory.
func memoriesFeedbackHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

// memoriesPinHandler implements POST /api/v1/memories/pin: sets or clears
// the pinned flag on a memory.
func memoriesPinHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

// memoriesDeleteHandler implements POST /api/v1/memories/delete: deletes one
// or more memories by ID.
func memoriesDeleteHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}
