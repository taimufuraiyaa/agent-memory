package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type noteIdentityRequest struct {
	Workspace string `json:"workspace"`
	NoteID    string `json:"note_id"`
}

func notesListHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspaceName := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspaceName)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		includeDeleted := r.URL.Query().Get("include_deleted") == "true"
		notes, err := assets.Notes.List(r.Context(), workspaceName, includeDeleted)
		if err != nil {
			writeNoteError(w, err)
			return
		}
		for index := range notes {
			notes[index].Body = ""
		}
		writeOK(w, http.StatusOK, map[string]any{"workspace": workspaceName, "notes": notes})
	}
}

func noteGetHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspaceName := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspaceName)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		note, err := assets.Notes.Get(r.Context(), workspaceName, r.URL.Query().Get("note_id"))
		if err != nil {
			writeNoteError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"note": note})
	}
}

func noteCreateHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var input core.CreateNoteInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.TrimSpace(input.Workspace) == "" {
			input.Workspace = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), input.Workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		note, err := assets.Notes.Create(r.Context(), input)
		if err != nil {
			writeNoteError(w, err)
			return
		}
		scheduleNoteIndex(assets, input.Workspace, note.ID)
		writeOK(w, http.StatusCreated, map[string]any{"note": note})
	}
}

func noteUpdateHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var input core.UpdateNoteInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.TrimSpace(input.Workspace) == "" {
			input.Workspace = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), input.Workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		note, err := assets.Notes.Update(r.Context(), input)
		if err != nil {
			writeNoteError(w, err)
			return
		}
		scheduleNoteIndex(assets, input.Workspace, note.ID)
		writeOK(w, http.StatusOK, map[string]any{"note": note})
	}
}

func noteTrashHandler(svc *Service) http.HandlerFunc {
	return noteIdentityMutationHandler(svc, func(r *http.Request, assets *workspaceAssets, input noteIdentityRequest) (any, error) {
		return assets.Notes.Trash(r.Context(), input.Workspace, input.NoteID)
	})
}

func noteRestoreHandler(svc *Service) http.HandlerFunc {
	return noteIdentityMutationHandler(svc, func(r *http.Request, assets *workspaceAssets, input noteIdentityRequest) (any, error) {
		note, err := assets.Notes.Restore(r.Context(), input.Workspace, input.NoteID)
		if err == nil {
			scheduleNoteIndex(assets, input.Workspace, input.NoteID)
		}
		return note, err
	})
}

func noteDeleteHandler(svc *Service) http.HandlerFunc {
	return noteIdentityMutationHandler(svc, func(r *http.Request, assets *workspaceAssets, input noteIdentityRequest) (any, error) {
		if err := assets.Notes.DeletePermanently(r.Context(), input.Workspace, input.NoteID); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "note_id": input.NoteID}, nil
	})
}

func noteIdentityMutationHandler(
	svc *Service,
	operation func(*http.Request, *workspaceAssets, noteIdentityRequest) (any, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var input noteIdentityRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.TrimSpace(input.Workspace) == "" {
			input.Workspace = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), input.Workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		result, err := operation(r, assets, input)
		if err != nil {
			writeNoteError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"note": result})
	}
}

func noteRevisionsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspaceName := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspaceName)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		revisions, err := assets.Notes.Revisions(r.Context(), workspaceName, r.URL.Query().Get("note_id"))
		if err != nil {
			writeNoteError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"revisions": revisions})
	}
}

func noteRevisionRestoreHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var input struct {
			Workspace        string `json:"workspace"`
			NoteID           string `json:"note_id"`
			Revision         int    `json:"revision"`
			ExpectedRevision int    `json:"expected_revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.TrimSpace(input.Workspace) == "" {
			input.Workspace = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), input.Workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		note, err := assets.Notes.RestoreRevision(r.Context(), input.Workspace, input.NoteID, input.Revision, input.ExpectedRevision)
		if err != nil {
			writeNoteError(w, err)
			return
		}
		scheduleNoteIndex(assets, input.Workspace, input.NoteID)
		writeOK(w, http.StatusOK, map[string]any{"note": note})
	}
}

func noteIndexRetryHandler(svc *Service) http.HandlerFunc {
	return noteIdentityMutationHandler(svc, func(_ *http.Request, assets *workspaceAssets, input noteIdentityRequest) (any, error) {
		note, err := assets.Notes.Get(context.Background(), input.Workspace, input.NoteID)
		if err != nil {
			return nil, err
		}
		scheduleNoteIndex(assets, input.Workspace, input.NoteID)
		return note, nil
	})
}

func scheduleNoteIndex(assets *workspaceAssets, workspaceName, noteID string) {
	assets.Notes.ScheduleIndex(workspaceName, noteID, 250*time.Millisecond)
}

func noteBacklinksHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspaceName := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspaceName)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		links, err := assets.Notes.Backlinks(r.Context(), workspaceName, r.URL.Query().Get("note_id"))
		if err != nil {
			writeNoteError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"backlinks": links})
	}
}

func writeNoteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sqlite.ErrNoteRevisionConflict):
		writeErr(w, http.StatusConflict, "revision_conflict", err.Error())
	case errors.Is(err, sqlite.ErrNotePathConflict):
		writeErr(w, http.StatusConflict, "path_conflict", err.Error())
	case errors.Is(err, sqlite.ErrNoteNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	default:
		writeErr(w, http.StatusBadRequest, "validation", err.Error())
	}
}
