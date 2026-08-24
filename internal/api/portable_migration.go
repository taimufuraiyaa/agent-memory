package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/taimufuraiyaa/agent-memory/internal/portable"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

const portableMigrationRequestLimit = 4096

type portableMigrationRequest struct {
	Workspace  string `json:"workspace"`
	Passphrase string `json:"passphrase"`
}

func portableMigrationExportHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var request portableMigrationRequest
		decoder := json.NewDecoder(io.LimitReader(r.Body, portableMigrationRequestLimit+1))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "invalid portable export request")
			return
		}
		if len(request.Passphrase) < 12 || len(request.Passphrase) > 1024 {
			writeErr(w, http.StatusBadRequest, "validation", "passphrase must contain between 12 and 1024 characters")
			return
		}
		workspace := strings.TrimSpace(request.Workspace)
		if workspace == "" {
			workspace = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		bundle, err := portable.BuildLocal(r.Context(), assets.Store, portable.Selection{Workspace: workspace})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "portable_export_failed", "could not build portable export")
			return
		}
		plain, err := json.Marshal(bundle)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "portable_export_failed", "could not encode portable export")
			return
		}
		encrypted, err := exportservice.EncryptPortable(request.Passphrase, plain)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "portable_export_failed", "could not encrypt portable export")
			return
		}
		_, err = assets.Store.AppendAuditEvent(r.Context(), sqlite.AuditEventInput{
			Workspace: workspace, Operation: "portable_export", Outcome: "success", Actor: "http", Source: "dashboard",
			TargetType: "migration_bundle", TargetCount: len(bundle.Memories) + len(bundle.Notes),
			Reason:   "copy-first browser migration export",
			Metadata: map[string]any{"memory_count": len(bundle.Memories), "note_count": len(bundle.Notes), "source_originals_included": false},
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "portable_export_failed", "could not record portable export")
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="agent-memory-%s.ampb2"`, portableFilenamePart(workspace)))
		w.Header().Set("X-Agent-Memory-Memories", fmt.Sprint(len(bundle.Memories)))
		w.Header().Set("X-Agent-Memory-Notes", fmt.Sprint(len(bundle.Notes)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encrypted)
	}
}

func portableFilenamePart(value string) string {
	var result strings.Builder
	for _, character := range strings.TrimSpace(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' {
			result.WriteRune(character)
		} else {
			result.WriteByte('-')
		}
	}
	if result.Len() == 0 {
		return "workspace"
	}
	return result.String()
}
