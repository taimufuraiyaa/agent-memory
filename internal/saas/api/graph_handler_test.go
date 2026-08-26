package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/memory"
)

type hostedGraphControllerFixture struct {
	request application.GraphOperationRequest
}

func (f *hostedGraphControllerFixture) Readiness(context.Context, core.GraphScope, string) (application.GraphIndexReadiness, error) {
	return application.GraphIndexReadiness{}, nil
}
func (f *hostedGraphControllerFixture) Status(_ context.Context, scope core.GraphScope, _ string) (application.GraphIndexStatus, error) {
	return application.GraphIndexStatus{Enabled: true, State: scope.TenantID + ":ready"}, nil
}
func (f *hostedGraphControllerFixture) Operate(_ context.Context, request application.GraphOperationRequest) (application.GraphOperationResult, error) {
	f.request = request
	return application.GraphOperationResult{Action: request.Action, Accepted: true}, nil
}

type hostedGraphAuthorizerFixture struct{ tenant, workspace string }

func (a hostedGraphAuthorizerFixture) AuthorizeGraphWorkspace(_ *http.Request, caller auth.RequestContext, workspace, _ string) error {
	if caller.TenantID != a.tenant || workspace != a.workspace {
		return application.ErrGraphOperationNotFound
	}
	return nil
}

func TestGraphOperatorDerivesTenantAndRejectsPaths(t *testing.T) {
	controller := &hostedGraphControllerFixture{}
	authorizer := hostedGraphAuthorizerFixture{"tenant-a", "workspace-a"}
	handler := hostedGraphOperation(controller, authorizer)
	bad := hostedGraphRequest(http.MethodPost, `{"workspace_id":"workspace-a","configuration_id":"configuration-a","action":"update","idempotency_key":"key","artifact_path":"/tmp/forged"}`)
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusBadRequest {
		t.Fatalf("path field accepted: %d %s", badResponse.Code, badResponse.Body.String())
	}
	good := hostedGraphRequest(http.MethodPost, `{"workspace_id":"workspace-a","configuration_id":"configuration-a","action":"update","idempotency_key":"key"}`)
	goodResponse := httptest.NewRecorder()
	handler.ServeHTTP(goodResponse, good)
	if goodResponse.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", goodResponse.Code, goodResponse.Body.String())
	}
	if controller.request.Scope.TenantID != "tenant-a" || controller.request.Scope.WorkspaceID != "workspace-a" || controller.request.Actor != "account-a" {
		t.Fatalf("authority not derived: %#v", controller.request)
	}
}

func TestGraphOperatorRequiresCapabilityAndWorkspaceAuthorization(t *testing.T) {
	controller := &hostedGraphControllerFixture{}
	handler := hostedGraphOperation(controller, hostedGraphAuthorizerFixture{"tenant-a", "workspace-a"})
	request := httptest.NewRequest(http.MethodPost, "/v1/graph-index/operations", strings.NewReader(`{"workspace_id":"workspace-b","configuration_id":"configuration-a","action":"disable"}`))
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{TenantID: "tenant-a", AccountID: "account-a", RequestID: "request-a", Capabilities: map[string]struct{}{"graph:operate": {}}}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || controller.request.Action != "" {
		t.Fatalf("unauthorized workspace reached controller: %d %#v", response.Code, controller.request)
	}
}

type hostedGraphExperienceFixture struct{ snapshot contracts.GraphQuerySnapshot }

func (f hostedGraphExperienceFixture) LoadActiveGraphSnapshot(context.Context, core.GraphScope, int, int, int) (contracts.GraphQuerySnapshot, error) {
	return f.snapshot, nil
}
func (f hostedGraphExperienceFixture) ResolveGraphCanonicalMemories(_ context.Context, _ core.GraphScope, values []core.GraphEvidence) (map[string]core.MemoryEntry, map[string]struct{}, error) {
	memories, authorized := map[string]core.MemoryEntry{}, map[string]struct{}{}
	for _, value := range values {
		memories[value.CanonicalID] = core.MemoryEntry{ID: value.CanonicalID, Workspace: value.Scope.WorkspaceID, Content: value.CanonicalID}
		authorized[graphretrieval.GraphAuthorizationKey(value)] = struct{}{}
	}
	return memories, authorized, nil
}
func (hostedGraphExperienceFixture) ReviewGraphRecord(context.Context, core.GraphReview) error {
	return nil
}
func (hostedGraphExperienceFixture) RecordGraphFeedback(context.Context, core.GraphFeedback) error {
	return nil
}

type hostedGraphMemorySearchFixture struct{ item memory.SearchItem }

func (f hostedGraphMemorySearchFixture) Search(context.Context, memory.SearchCommand) (memory.SearchResult, error) {
	return memory.SearchResult{Items: []memory.SearchItem{f.item}}, nil
}

func TestHostedGraphRecallUsesNormalizedStoreAndCanonicalMemoryWithoutOnlineAdapter(t *testing.T) {
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	evidence := func(id string) core.GraphEvidence {
		return core.GraphEvidence{Scope: scope, CanonicalKind: "memory", CanonicalID: id, CanonicalFingerprint: "sha256:" + id, OccurrenceCount: 1}
	}
	snapshot := contracts.GraphQuerySnapshot{Scope: scope, RevisionID: "revision-a", CacheIdentity: "cache-a", Fresh: true,
		Nodes: []contracts.GraphQueryNode{
			{Entity: core.GraphEntity{ID: "book-node", Scope: scope, Trust: core.GraphTrustApproved}, Evidence: []core.GraphEvidence{evidence("book-a")}},
			{Entity: core.GraphEntity{ID: "day10-node", Scope: scope, Trust: core.GraphTrustReviewed}, Evidence: []core.GraphEvidence{evidence("day-10")}},
		},
		Edges: []contracts.GraphQueryEdge{{Edge: core.GraphEdge{ID: "membership", Scope: scope, SourceEntityID: "day10-node", TargetEntityID: "book-node", NormalizedKind: string(core.GraphRelationshipMembership), Trust: core.GraphTrustReviewed}, Version: core.GraphEdgeVersion{Weight: .9}, Evidence: []core.GraphEvidence{evidence("book-a"), evidence("day-10")}}},
	}
	handler := hostedGraphRecall(hostedGraphExperienceFixture{snapshot: snapshot}, hostedGraphMemorySearchFixture{item: memory.SearchItem{ID: "day-10", WorkspaceID: scope.WorkspaceID, Score: .9, CreatedAt: time.Now(), UpdatedAt: time.Now()}}, hostedGraphAuthorizerFixture{tenant: scope.TenantID, workspace: scope.WorkspaceID}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/graph-index/recall", strings.NewReader(`{"workspace_id":"workspace-a","query":"How is Day 10 related to Book A?","mode":"local_graph","required":true,"allow_stale":false,"limit":20}`))
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{TenantID: scope.TenantID, AccountID: "account-a", RequestID: "request-a", Capabilities: map[string]struct{}{"graph:read": {}, "memory:read": {}}}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"selected_mode":"local_graph"`) || !strings.Contains(response.Body.String(), `"canonical_memories"`) || !strings.Contains(response.Body.String(), `"id":"book-a"`) || strings.Contains(response.Body.String(), "graphrag endpoint") {
		t.Fatalf("hosted graph recall did not remain Agent Memory-owned: %d %s", response.Code, response.Body.String())
	}
}

func hostedGraphRequest(method, body string) *http.Request {
	request := httptest.NewRequest(method, "/v1/graph-index/operations", strings.NewReader(body))
	caller := auth.RequestContext{TenantID: "tenant-a", AccountID: "account-a", RequestID: "request-a", Capabilities: map[string]struct{}{"graph:operate": {}}}
	return request.WithContext(auth.WithRequestContext(request.Context(), caller))
}
