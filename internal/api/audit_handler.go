package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func auditHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspaceName := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if workspaceName == "" {
			workspaceName = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), workspaceName)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		filter := sqlite.AuditFilter{Workspace: workspaceName, Operation: strings.TrimSpace(r.URL.Query().Get("operation")), Actor: strings.TrimSpace(r.URL.Query().Get("actor")), RequestID: strings.TrimSpace(r.URL.Query().Get("request_id")), Limit: parseIntOrDefault(r.URL.Query().Get("limit"), 100)}
		for key, target := range map[string]**time.Time{"from": &filter.From, "to": &filter.To, "before": &filter.To} {
			if raw := strings.TrimSpace(r.URL.Query().Get(key)); raw != "" {
				parsed, ok := parseTimeFlexible(raw)
				if !ok {
					writeErr(w, http.StatusBadRequest, "validation", "invalid "+key)
					return
				}
				*target = &parsed
			}
		}
		events, err := assets.Store.ListAuditEvents(r.Context(), filter)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "ndjson") {
			w.Header().Set("Content-Type", "application/x-ndjson")
			encoder := json.NewEncoder(w)
			for _, event := range events {
				if err := encoder.Encode(event); err != nil {
					return
				}
			}
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"workspace": workspaceName, "events": events, "count": len(events), "limit": filter.Limit})
	}
}
