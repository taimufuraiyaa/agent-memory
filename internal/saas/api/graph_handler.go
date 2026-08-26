package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	baseobservability "github.com/taimufuraiyaa/agent-memory/internal/observability"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/memory"
)

type GraphWorkspaceAuthorizer interface {
	AuthorizeGraphWorkspace(*http.Request, auth.RequestContext, string, string) error
}

type GraphExperienceStore interface {
	contracts.GraphQueryStore
	ReviewGraphRecord(context.Context, core.GraphReview) error
	RecordGraphFeedback(context.Context, core.GraphFeedback) error
}

type GraphObserver interface {
	RecordGraph(baseobservability.GraphObservation) error
}

func hostedGraphReadiness(controller application.GraphOperationController, authorizer GraphWorkspaceAuthorizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, workspaceID, configurationID, ok := authorizeHostedGraphRead(r, authorizer)
		if !ok {
			writeError(w, http.StatusForbidden, requestID(r), "graph_forbidden", "Graph readiness is not authorized.")
			return
		}
		readiness, err := controller.Readiness(r.Context(), core.GraphScope{TenantID: caller.TenantID, WorkspaceID: workspaceID}, configurationID)
		if err != nil {
			writeHostedGraphError(w, r, err)
			return
		}
		writeSuccess(w, http.StatusOK, caller.RequestID, readiness)
	}
}

func hostedGraphStatus(controller application.GraphOperationController, authorizer GraphWorkspaceAuthorizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, workspaceID, configurationID, ok := authorizeHostedGraphRead(r, authorizer)
		if !ok {
			writeError(w, http.StatusForbidden, requestID(r), "graph_forbidden", "Graph status is not authorized.")
			return
		}
		status, err := controller.Status(r.Context(), core.GraphScope{TenantID: caller.TenantID, WorkspaceID: workspaceID}, configurationID)
		if err != nil {
			writeHostedGraphError(w, r, err)
			return
		}
		writeSuccess(w, http.StatusOK, caller.RequestID, status)
	}
}

func hostedGraphExplorer(store GraphExperienceStore, authorizer GraphWorkspaceAuthorizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, workspaceID, _, ok := authorizeHostedGraphRead(r, authorizer)
		if !ok {
			writeError(w, http.StatusForbidden, requestID(r), "graph_forbidden", "Graph explorer is not authorized.")
			return
		}
		snapshot, err := store.LoadActiveGraphSnapshot(r.Context(), core.GraphScope{TenantID: caller.TenantID, WorkspaceID: workspaceID}, hostedGraphLimit(r, "max_nodes", 512, 4096), hostedGraphLimit(r, "max_edges", 2048, 16384), hostedGraphLimit(r, "max_communities", 128, 2048))
		if err != nil {
			writeHostedGraphError(w, r, err)
			return
		}
		writeSuccess(w, http.StatusOK, caller.RequestID, snapshot)
	}
}

type hostedGraphRecallResponse struct {
	RequestID         string                            `json:"request_id"`
	GraphRoute        graphretrieval.GraphRouteDecision `json:"graph_route"`
	GraphContext      *application.RecallGraphContext   `json:"graph_context,omitempty"`
	BasicMemories     []memory.SearchItem               `json:"basic_memories"`
	CanonicalMemories []core.MemoryEntry                `json:"canonical_memories,omitempty"`
}

func hostedGraphRecall(store GraphExperienceStore, search MemorySearchService, authorizer GraphWorkspaceAuthorizer, auditor *audit.Service, observer GraphObserver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		caller, ok := auth.FromContext(r.Context())
		if !ok || !caller.Can("graph:read") || !caller.Can("memory:read") {
			writeError(w, http.StatusForbidden, requestID(r), "graph_forbidden", "Graph recall is not authorized.")
			return
		}
		var body struct {
			WorkspaceID string                        `json:"workspace_id"`
			Query       string                        `json:"query"`
			Mode        graphretrieval.GraphQueryMode `json:"mode"`
			Required    bool                          `json:"required"`
			AllowStale  bool                          `json:"allow_stale"`
			Limit       int                           `json:"limit"`
		}
		if !decodeHostedGraphBody(w, r, caller.RequestID, &body) {
			return
		}
		body.WorkspaceID, body.Query = strings.TrimSpace(body.WorkspaceID), strings.TrimSpace(body.Query)
		if body.Query == "" || authorizer.AuthorizeGraphWorkspace(r, caller, body.WorkspaceID, "graph:read") != nil {
			writeError(w, http.StatusForbidden, caller.RequestID, "graph_forbidden", "Graph recall is not authorized.")
			return
		}
		if body.Limit == 0 {
			body.Limit = 50
		}
		if body.Limit < 1 || body.Limit > 200 {
			writeError(w, http.StatusBadRequest, caller.RequestID, "invalid_graph_request", "The graph recall limit is invalid.")
			return
		}
		basic, err := search.Search(r.Context(), memory.SearchCommand{WorkspaceID: body.WorkspaceID, Query: body.Query, Limit: body.Limit})
		if err != nil {
			writeError(w, http.StatusNotFound, caller.RequestID, "graph_recall_unavailable", "Grounded Basic retrieval is unavailable.")
			return
		}
		availability := graphretrieval.GraphRouteAvailability{}
		var snapshot contracts.GraphQuerySnapshot
		if body.Mode != "" && body.Mode != graphretrieval.GraphQueryBasic {
			snapshot, err = store.LoadActiveGraphSnapshot(r.Context(), core.GraphScope{TenantID: caller.TenantID, WorkspaceID: body.WorkspaceID}, 4096, 16384, 2048)
			if err == nil {
				availability = graphretrieval.GraphRouteAvailability{Readable: true, Fresh: snapshot.Fresh, ActiveRevisionID: snapshot.RevisionID}
			} else if !errors.Is(err, sql.ErrNoRows) && body.Required {
				writeError(w, http.StatusServiceUnavailable, caller.RequestID, "graph_recall_unavailable", "The required graph index could not be read.")
				return
			}
		}
		decision, err := graphretrieval.NewGraphRouter().Route(graphretrieval.GraphRouteRequest{Mode: body.Mode, Query: body.Query, RequireGraph: body.Required, Policy: graphretrieval.GraphRoutePolicy{GraphEnabled: true, AllowLocal: true, AllowGlobal: true, AllowStale: body.AllowStale}, Availability: availability})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, caller.RequestID, "graph_route_required", "The required graph route is unavailable.")
			return
		}
		response := hostedGraphRecallResponse{RequestID: caller.RequestID, GraphRoute: decision, BasicMemories: basic.Items}
		if decision.SelectedMode == graphretrieval.GraphQueryLocal || decision.SelectedMode == graphretrieval.GraphQueryGlobal {
			response.GraphContext, response.CanonicalMemories, err = buildHostedGraphContext(r.Context(), store, body.Query, basic.Items, snapshot, decision.SelectedMode)
			if err != nil {
				if body.Required {
					writeError(w, http.StatusServiceUnavailable, caller.RequestID, "graph_recall_unavailable", "The required graph context could not be resolved.")
					return
				}
				response.GraphRoute.SelectedMode, response.GraphRoute.Fallback, response.GraphRoute.Degraded, response.GraphRoute.ReasonCode = graphretrieval.GraphQueryBasic, true, true, graphretrieval.GraphReasonReadFailed
				response.GraphContext, response.CanonicalMemories = &application.RecallGraphContext{DegradedReason: graphretrieval.GraphReasonReadFailed}, nil
			}
		}
		if auditor != nil {
			if err := auditor.Record(r.Context(), caller, "retrieval", "graph.recall", "success", "workspace", body.WorkspaceID, string(response.GraphRoute.ReasonCode), map[string]any{"route": response.GraphRoute.SelectedMode, "fallback": response.GraphRoute.Fallback, "basic_count": len(response.BasicMemories), "graph_count": len(response.CanonicalMemories)}); err != nil {
				writeError(w, http.StatusServiceUnavailable, caller.RequestID, "audit_unavailable", "The graph recall could not be durably audited.")
				return
			}
		}
		if observer != nil {
			_ = observer.RecordGraph(hostedGraphRecallObservation(started, response.GraphRoute, len(response.BasicMemories)+len(response.CanonicalMemories)))
		}
		writeSuccess(w, http.StatusOK, caller.RequestID, response)
	}
}

func hostedGraphRecallObservation(started time.Time, decision graphretrieval.GraphRouteDecision, records int) baseobservability.GraphObservation {
	mode, route := string(decision.SelectedMode), string(decision.SelectedMode)
	if mode == "" {
		mode, route = string(graphretrieval.GraphQueryBasic), string(graphretrieval.GraphQueryBasic)
	}
	if decision.Fallback {
		switch decision.RequestedMode {
		case graphretrieval.GraphQueryLocal, graphretrieval.GraphQueryGlobal:
			route = string(decision.RequestedMode)
		case graphretrieval.GraphQueryAuto:
			if decision.Intent == graphretrieval.GraphIntentGlobal {
				route = string(graphretrieval.GraphQueryGlobal)
			} else if decision.Intent == graphretrieval.GraphIntentRelational {
				route = string(graphretrieval.GraphQueryLocal)
			}
		}
	}
	result := baseobservability.GraphObservation{Stage: "query", Mode: mode, Route: route, Outcome: "completed", Duration: time.Since(started), Records: int64(records)}
	if decision.Fallback {
		result.Outcome, result.Fallback, result.Reason = "fallback", true, hostedGraphFallbackReason(decision.ReasonCode)
	}
	if decision.ActiveRevisionID != "" {
		result.Freshness = "fresh"
		if !decision.Fresh {
			result.Freshness = "stale"
		}
	}
	return result
}

func hostedGraphFallbackReason(reason graphretrieval.GraphRouteReason) string {
	switch reason {
	case graphretrieval.GraphReasonPolicyDisabled:
		return "policy_disabled"
	case graphretrieval.GraphReasonModeDisallowed:
		return "mode_disallowed"
	case graphretrieval.GraphReasonIndexUnavailable:
		return "index_unavailable"
	case graphretrieval.GraphReasonIndexStale:
		return "index_stale"
	default:
		return "read_failed"
	}
}

func buildHostedGraphContext(ctx context.Context, store GraphExperienceStore, query string, basic []memory.SearchItem, snapshot contracts.GraphQuerySnapshot, mode graphretrieval.GraphQueryMode) (*application.RecallGraphContext, []core.MemoryEntry, error) {
	evidence := make([]core.GraphEvidence, 0)
	for _, node := range snapshot.Nodes {
		evidence = append(evidence, node.Evidence...)
	}
	for _, edge := range snapshot.Edges {
		evidence = append(evidence, edge.Evidence...)
	}
	memories, authorized, err := store.ResolveGraphCanonicalMemories(ctx, snapshot.Scope, evidence)
	if err != nil {
		return nil, nil, err
	}
	contextResult := &application.RecallGraphContext{RevisionID: snapshot.RevisionID, Fresh: snapshot.Fresh}
	selected := map[string]struct{}{}
	if mode == graphretrieval.GraphQueryLocal {
		direct := make([]engine.RetrievalHit, 0, len(basic))
		for _, item := range basic {
			direct = append(direct, engine.RetrievalHit{Memory: core.MemoryEntry{ID: item.ID}, Score: item.Score})
		}
		local, expandErr := application.ExpandRecallLocalGraph(direct, nil, snapshot, authorized)
		if expandErr != nil {
			return nil, nil, expandErr
		}
		contextResult.Local = &local
		for _, path := range local.Paths {
			for _, item := range path.Evidence {
				if item.CanonicalKind == "memory" {
					selected[item.CanonicalID] = struct{}{}
				}
			}
		}
	} else {
		global, selectErr := application.SelectRecallGlobalGraph(query, snapshot, authorized)
		if selectErr != nil {
			return nil, nil, selectErr
		}
		contextResult.Global = &global
		for _, community := range global.Communities {
			for _, item := range community.Evidence {
				if item.CanonicalKind == "memory" {
					selected[item.CanonicalID] = struct{}{}
				}
			}
		}
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	contextResult.CanonicalIDs = ids
	result := make([]core.MemoryEntry, 0, len(ids))
	for _, id := range ids {
		if value, ok := memories[id]; ok {
			result = append(result, value)
		}
	}
	return contextResult, result, nil
}

func hostedGraphReview(store GraphExperienceStore, authorizer GraphWorkspaceAuthorizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.FromContext(r.Context())
		if !ok || !caller.Can("graph:review") {
			writeError(w, http.StatusForbidden, requestID(r), "graph_forbidden", "Graph review is not authorized.")
			return
		}
		var review core.GraphReview
		if !decodeHostedGraphBody(w, r, caller.RequestID, &review) {
			return
		}
		review.Scope.TenantID = caller.TenantID
		review.Scope.WorkspaceID = strings.TrimSpace(review.Scope.WorkspaceID)
		review.ReviewerID = caller.AccountID
		if authorizer.AuthorizeGraphWorkspace(r, caller, review.Scope.WorkspaceID, "graph:review") != nil {
			writeError(w, http.StatusForbidden, caller.RequestID, "graph_forbidden", "Graph review is not authorized.")
			return
		}
		if review.ID == "" {
			review.ID = uuid.NewString()
		}
		if review.CreatedAt.IsZero() {
			review.CreatedAt = time.Now().UTC()
		}
		if err := store.ReviewGraphRecord(r.Context(), review); err != nil {
			if strings.Contains(err.Error(), "version conflict") {
				writeError(w, http.StatusConflict, caller.RequestID, "graph_review_conflict", "The graph record changed; refresh and retry.")
			} else {
				writeError(w, http.StatusBadRequest, caller.RequestID, "invalid_graph_review", "The graph review request is invalid.")
			}
			return
		}
		writeSuccess(w, http.StatusOK, caller.RequestID, map[string]any{"reviewed": true, "review_id": review.ID})
	}
}

func hostedGraphFeedback(store GraphExperienceStore, authorizer GraphWorkspaceAuthorizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.FromContext(r.Context())
		if !ok || !caller.Can("graph:read") {
			writeError(w, http.StatusForbidden, requestID(r), "graph_forbidden", "Graph feedback is not authorized.")
			return
		}
		var feedback core.GraphFeedback
		if !decodeHostedGraphBody(w, r, caller.RequestID, &feedback) {
			return
		}
		feedback.Scope.TenantID = caller.TenantID
		feedback.Scope.WorkspaceID = strings.TrimSpace(feedback.Scope.WorkspaceID)
		if authorizer.AuthorizeGraphWorkspace(r, caller, feedback.Scope.WorkspaceID, "graph:read") != nil {
			writeError(w, http.StatusForbidden, caller.RequestID, "graph_forbidden", "Graph feedback is not authorized.")
			return
		}
		if feedback.ID == "" {
			feedback.ID = uuid.NewString()
		}
		if feedback.CreatedAt.IsZero() {
			feedback.CreatedAt = time.Now().UTC()
		}
		if err := store.RecordGraphFeedback(r.Context(), feedback); err != nil {
			writeError(w, http.StatusBadRequest, caller.RequestID, "invalid_graph_feedback", "The graph feedback request is invalid.")
			return
		}
		writeSuccess(w, http.StatusOK, caller.RequestID, map[string]any{"recorded": true, "feedback_id": feedback.ID})
	}
}

func authorizeHostedGraphRead(r *http.Request, authorizer GraphWorkspaceAuthorizer) (auth.RequestContext, string, string, bool) {
	caller, ok := auth.FromContext(r.Context())
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	configurationID := strings.TrimSpace(r.URL.Query().Get("configuration_id"))
	if !ok || !caller.Can("graph:read") || authorizer.AuthorizeGraphWorkspace(r, caller, workspaceID, "graph:read") != nil {
		return auth.RequestContext{}, "", "", false
	}
	return caller, workspaceID, configurationID, true
}

func hostedGraphLimit(r *http.Request, name string, fallback, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(name)))
	if err != nil || value < 1 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func decodeHostedGraphBody(w http.ResponseWriter, r *http.Request, request string, output any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		writeError(w, http.StatusBadRequest, request, "invalid_graph_request", "The graph request is invalid.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, request, "invalid_graph_request", "The graph request is invalid.")
		return false
	}
	return true
}

func hostedGraphOperation(controller application.GraphOperationController, authorizer GraphWorkspaceAuthorizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.FromContext(r.Context())
		if !ok || !caller.Can("graph:operate") {
			writeError(w, http.StatusForbidden, requestID(r), "graph_forbidden", "Graph operation is not authorized.")
			return
		}
		var body struct {
			WorkspaceID      string                           `json:"workspace_id"`
			ConfigurationID  string                           `json:"configuration_id"`
			Action           application.GraphOperationAction `json:"action"`
			IdempotencyKey   string                           `json:"idempotency_key,omitempty"`
			ExpectedRevision string                           `json:"expected_revision,omitempty"`
			JobID            string                           `json:"job_id,omitempty"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, caller.RequestID, "invalid_graph_operation", "The graph operation request is invalid.")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeError(w, http.StatusBadRequest, caller.RequestID, "invalid_graph_operation", "The graph operation request is invalid.")
			return
		}
		body.WorkspaceID = strings.TrimSpace(body.WorkspaceID)
		if authorizer.AuthorizeGraphWorkspace(r, caller, body.WorkspaceID, "graph:operate") != nil {
			writeError(w, http.StatusForbidden, caller.RequestID, "graph_forbidden", "Graph operation is not authorized.")
			return
		}
		result, err := controller.Operate(r.Context(), application.GraphOperationRequest{Scope: core.GraphScope{TenantID: caller.TenantID, WorkspaceID: body.WorkspaceID}, ConfigurationID: body.ConfigurationID, Action: body.Action, IdempotencyKey: body.IdempotencyKey, ExpectedRevision: body.ExpectedRevision, JobID: body.JobID, Actor: caller.AccountID})
		if err != nil {
			writeHostedGraphError(w, r, err)
			return
		}
		writeSuccess(w, http.StatusAccepted, caller.RequestID, result)
	}
}

func writeHostedGraphError(w http.ResponseWriter, r *http.Request, err error) {
	request := requestID(r)
	switch {
	case errors.Is(err, application.ErrGraphOperationInvalid):
		writeError(w, http.StatusBadRequest, request, "invalid_graph_operation", "The graph operation request is invalid.")
	case errors.Is(err, application.ErrGraphOperationNotFound):
		writeError(w, http.StatusNotFound, request, "graph_not_found", "The graph operation target was not found.")
	case errors.Is(err, application.ErrGraphOperationConflict):
		writeError(w, http.StatusConflict, request, "graph_conflict", "The graph state changed; refresh and retry.")
	case errors.Is(err, application.ErrGraphOperationDisabled):
		writeError(w, http.StatusServiceUnavailable, request, "graph_disabled", "Graph indexing is disabled.")
	default:
		writeError(w, http.StatusInternalServerError, request, "graph_failed", "The graph operation failed.")
	}
}
