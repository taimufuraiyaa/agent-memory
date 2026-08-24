package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
)

const clientProfilesPath = "/api/v1/client-profiles/"

type clientProfileUpdateRequest struct {
	DisplayName      string `json:"display_name"`
	ClientKind       string `json:"client_kind"`
	ToolProfile      string `json:"tool_profile"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func ConfigureLocalClientProfiles(svc *Service) error {
	if svc == nil {
		return errors.New("service is required")
	}
	store, err := clientprofile.Open(svc.BaseDir, time.Now)
	if err != nil {
		return err
	}
	svc.ClientProfiles = store
	return nil
}

func clientProfilesHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store, ok := resolveClientProfileStore(w, svc)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeOK(w, http.StatusOK, map[string]any{"profiles": store.List()})
		case http.MethodPost:
			var input clientprofile.Input
			if !decodeClientProfileJSON(w, r, &input) {
				return
			}
			profile, err := store.Create(input)
			if err != nil {
				writeClientProfileError(w, err)
				return
			}
			writeOK(w, http.StatusCreated, map[string]any{"profile": profile})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	}
}

func clientProfileHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store, ok := resolveClientProfileStore(w, svc)
		if !ok {
			return
		}
		id, ok := parseClientProfileID(w, r.URL.Path)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			profile, err := store.Get(id)
			if err != nil {
				writeClientProfileError(w, err)
				return
			}
			writeOK(w, http.StatusOK, map[string]any{"profile": profile})
		case http.MethodPut:
			var request clientProfileUpdateRequest
			if !decodeClientProfileJSON(w, r, &request) {
				return
			}
			profile, err := store.Update(id, request.ExpectedRevision, clientprofile.Input{
				DisplayName: request.DisplayName,
				ClientKind:  request.ClientKind,
				ToolProfile: request.ToolProfile,
			})
			if err != nil {
				writeClientProfileError(w, err)
				return
			}
			writeOK(w, http.StatusOK, map[string]any{"profile": profile})
		case http.MethodDelete:
			revision, err := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 64)
			if err != nil || revision < 1 {
				writeErr(w, http.StatusBadRequest, "client_profile_validation", "expected_revision must be a positive integer")
				return
			}
			if err := store.Delete(id, revision); err != nil {
				writeClientProfileError(w, err)
				return
			}
			writeOK(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	}
}

func resolveClientProfileStore(w http.ResponseWriter, svc *Service) (*clientprofile.Store, bool) {
	if svc == nil || svc.ClientProfiles == nil {
		writeErr(w, http.StatusServiceUnavailable, "client_profiles_unavailable", "client profiles are not configured")
		return nil, false
	}
	return svc.ClientProfiles, true
}

func parseClientProfileID(w http.ResponseWriter, path string) (string, bool) {
	rawID := strings.TrimPrefix(path, clientProfilesPath)
	if rawID == path || rawID == "" || strings.Contains(rawID, "/") {
		writeErr(w, http.StatusBadRequest, "client_profile_path", "client profile path must contain exactly one client id")
		return "", false
	}
	id, err := url.PathUnescape(rawID)
	if err != nil || id != rawID || clientprofile.ValidateID(id) != nil {
		writeErr(w, http.StatusBadRequest, "client_profile_path", "client profile path contains an invalid client id")
		return "", false
	}
	return id, true
}

func decodeClientProfileJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeErr(w, http.StatusBadRequest, "client_profile_validation", "invalid client profile request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "client_profile_validation", "request must contain one JSON object")
		return false
	}
	return true
}

func writeClientProfileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, clientprofile.ErrValidation):
		writeErr(w, http.StatusBadRequest, "client_profile_validation", err.Error())
	case errors.Is(err, clientprofile.ErrNotFound):
		writeErr(w, http.StatusNotFound, "client_profile_not_found", "client profile was not found")
	case errors.Is(err, clientprofile.ErrConflict):
		writeErr(w, http.StatusConflict, "client_profile_conflict", "client profile already exists")
	case errors.Is(err, clientprofile.ErrRevisionConflict):
		writeErr(w, http.StatusConflict, "client_profile_revision_conflict", "client profile changed; reload and try again")
	default:
		writeErr(w, http.StatusServiceUnavailable, "client_profiles_unavailable", "client profiles are temporarily unavailable")
	}
}
