package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

const (
	standaloneImportBodyLimit    = 16 << 20
	standaloneImportMemoryLimit  = 10_000
	standaloneImportContentLimit = 12 << 20
)

// memoriesExportHandler implements GET /api/v1/memories/export.
func memoriesExportHandler(svc *Service) http.HandlerFunc {
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
	}
}

// memoriesImportHandler implements POST /api/v1/memories/import: imports an
// export bundle, sanitizing each memory (see SanitizeImportedMemory) before
// persisting it.
func memoriesImportHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req engine.ExportBundle
		if status, err := decodeStandaloneImport(w, r, &req); err != nil {
			code := "bad_request"
			if status == http.StatusRequestEntityTooLarge {
				code = "payload_too_large"
			}
			writeErr(w, status, code, err.Error())
			return
		}
		if err := validateStandaloneImportBundle(req); err != nil {
			writeErr(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeWorkspaceResolveError(w, err)
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
			if reason := SanitizeImportedMemory(r.Context(), &m, filter); reason != "" {
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
		_, _ = assets.Store.AppendAuditEvent(r.Context(), sqlite.AuditEventInput{Workspace: ws, Operation: "import", Outcome: "success", Actor: "http", Source: "api", TargetType: "memory", TargetCount: imported, Reason: "memory bundle import", Metadata: map[string]any{"skipped": len(skipped)}})
		writeOK(w, http.StatusOK, map[string]any{
			"version":  req.Version,
			"imported": imported,
			"skipped":  skipped,
		})
	}
}

func decodeStandaloneImport(w http.ResponseWriter, r *http.Request, out *engine.ExportBundle) (int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, standaloneImportBodyLimit)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return http.StatusRequestEntityTooLarge, errors.New("memory import exceeds the request limit")
		}
		return http.StatusBadRequest, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return http.StatusBadRequest, errors.New("memory import must contain one JSON object")
	}
	return http.StatusOK, nil
}

func validateStandaloneImportBundle(bundle engine.ExportBundle) error {
	if len(bundle.Memories) > standaloneImportMemoryLimit {
		return fmt.Errorf("memory import exceeds the %d-record limit", standaloneImportMemoryLimit)
	}
	contentBytes := 0
	for _, memory := range bundle.Memories {
		contentBytes += len(memory.Content)
		if contentBytes > standaloneImportContentLimit {
			return errors.New("memory import exceeds the aggregate content limit")
		}
	}
	return nil
}

// memoriesReconstructHandler implements POST /api/v1/memories/reconstruct.
func memoriesReconstructHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			writeWorkspaceResolveError(w, err)
			return
		}
		re := engine.NewReconstructionEngine(assets.Store, assets.Writer)
		out, err := re.Reconstruct(r.Context(), ws, req.Query, req.Confirm)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	}
}
