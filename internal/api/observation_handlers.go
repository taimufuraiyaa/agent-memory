package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// observeHandler implements POST /api/v1/observe: records a single
// observation event (deduplicated within a short time window), gated behind
// observeEnabled().
func observeHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

// observationsHandler implements GET /api/v1/observations: lists recorded
// observations for a workspace/session, gated behind observeEnabled().
func observationsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

// sessionsHandler implements GET /api/v1/sessions: lists recent sessions for
// a workspace, gated behind observeEnabled().
func sessionsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

// observationsPromoteHandler implements POST /api/v1/observations/promote:
// promotes recent observations into durable memories, gated behind
// observeEnabled().
func observationsPromoteHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}
