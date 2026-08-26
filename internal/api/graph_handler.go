package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const graphOperationRequestLimit = 32 << 10

func graphController(r *http.Request, svc *Service, workspace string) (application.GraphOperationController, error) {
	if svc.GraphOperations != nil {
		return svc.GraphOperations, nil
	}
	assets, err := svc.resolve(r.Context(), workspace)
	if err != nil {
		return nil, err
	}
	return application.NewGraphOperationService(assets.Store), nil
}

func graphIndexReadinessHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
			return
		}
		workspace, configurationID := strings.TrimSpace(r.URL.Query().Get("workspace")), strings.TrimSpace(r.URL.Query().Get("configuration_id"))
		controller, err := graphController(r, svc, workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		out, err := controller.Readiness(r.Context(), core.GraphScope{WorkspaceID: workspace}, configurationID)
		if err != nil {
			writeGraphOperationError(w, err)
			return
		}
		writeOK(w, http.StatusOK, out)
	}
}

func graphIndexStatusHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
			return
		}
		workspace, configurationID := strings.TrimSpace(r.URL.Query().Get("workspace")), strings.TrimSpace(r.URL.Query().Get("configuration_id"))
		controller, err := graphController(r, svc, workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		out, err := controller.Status(r.Context(), core.GraphScope{WorkspaceID: workspace}, configurationID)
		if err != nil {
			writeGraphOperationError(w, err)
			return
		}
		writeOK(w, http.StatusOK, out)
	}
}

func graphIndexOperationHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
			return
		}
		var request application.GraphOperationRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, graphOperationRequestLimit))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid graph operation request")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeErr(w, http.StatusBadRequest, "invalid_request", "request must contain one JSON object")
			return
		}
		request.Scope.TenantID = ""
		request.Scope.WorkspaceID = strings.TrimSpace(request.Scope.WorkspaceID)
		request.Actor = "api"
		controller, err := graphController(r, svc, request.Scope.WorkspaceID)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		out, err := controller.Operate(r.Context(), request)
		if err != nil {
			writeGraphOperationError(w, err)
			return
		}
		writeOK(w, http.StatusAccepted, out)
	}
}

func writeGraphOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrGraphOperationInvalid):
		writeErr(w, http.StatusBadRequest, "invalid_graph_operation", err.Error())
	case errors.Is(err, application.ErrGraphOperationNotFound):
		writeErr(w, http.StatusNotFound, "graph_not_found", "graph operation target not found")
	case errors.Is(err, application.ErrGraphOperationConflict):
		writeErr(w, http.StatusConflict, "graph_conflict", "graph operation conflicts with current state")
	case errors.Is(err, application.ErrGraphOperationDisabled):
		writeErr(w, http.StatusServiceUnavailable, "graph_disabled", "graph indexing is disabled")
	default:
		writeErr(w, http.StatusInternalServerError, "graph_runtime", "graph operation failed")
	}
}
