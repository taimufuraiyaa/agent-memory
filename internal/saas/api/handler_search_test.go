package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/memory"
)

func TestSearchMemoriesReturnsAllowlistedResultAndContentFreeAudit(t *testing.T) {
	workspaceID := uuid.NewString()
	resultID := uuid.NewString()
	service := &memorySearchFixture{result: memory.SearchResult{Items: []memory.SearchItem{{
		ID: resultID, WorkspaceID: workspaceID, Type: core.SemanticMemory,
		Content: "private result content", SourceKind: core.SourceUserInput,
		StorageTier: core.TierVector, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Score: 0.7,
	}}}}
	auditor := &memorySearchAuditFixture{}
	request := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(`{"workspace_id":"`+workspaceID+`","query":"private query","limit":5}`))
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{
		TenantID: "tenant-one", AccountID: "account-one", RequestID: "request-one", TraceID: "trace-one",
		Capabilities: map[string]struct{}{"memory:read": {}},
	}))
	response := httptest.NewRecorder()

	searchMemories(service, auditor).ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "private result content") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.command.WorkspaceID != workspaceID || service.command.Query != "private query" || service.command.Limit != 5 {
		t.Fatalf("command=%+v", service.command)
	}
	encoded, err := json.Marshal(auditor.metadata)
	if err != nil {
		t.Fatal(err)
	}
	if auditor.operation != "memory.search" || auditor.targetID != workspaceID || string(encoded) != `{"has_next":false,"result_count":1}` {
		t.Fatalf("audit operation=%s target=%s metadata=%s", auditor.operation, auditor.targetID, encoded)
	}
	for _, forbidden := range []string{"private query", "private result content", resultID} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("audit metadata leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSearchMemoriesWithholdsResultsWhenAuditFails(t *testing.T) {
	workspaceID := uuid.NewString()
	service := &memorySearchFixture{result: memory.SearchResult{Items: []memory.SearchItem{{Content: "must not be returned"}}}}
	auditor := &memorySearchAuditFixture{err: errors.New("audit unavailable")}
	request := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(`{"workspace_id":"`+workspaceID+`","query":"fact"}`))
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{
		TenantID: "tenant-one", AccountID: "account-one", RequestID: "request-one",
		Capabilities: map[string]struct{}{"memory:read": {}},
	}))
	response := httptest.NewRecorder()

	searchMemories(service, auditor).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "must not be returned") || strings.Contains(response.Body.String(), "audit unavailable") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSearchMemoriesRejectsStrictInputAndConcealsAuthorization(t *testing.T) {
	workspaceID := uuid.NewString()
	for name, fixture := range map[string]struct {
		body       string
		serviceErr error
		want       int
	}{
		"unknown field": {body: `{"workspace_id":"` + workspaceID + `","query":"fact","extra":true}`, want: http.StatusBadRequest},
		"forbidden":     {body: `{"workspace_id":"` + workspaceID + `","query":"fact"}`, serviceErr: memory.ErrSearchForbidden, want: http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			service := &memorySearchFixture{err: fixture.serviceErr}
			request := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(fixture.body))
			request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{TenantID: "tenant-one", AccountID: "account-one", RequestID: "request-one"}))
			response := httptest.NewRecorder()
			searchMemories(service, &memorySearchAuditFixture{}).ServeHTTP(response, request)
			if response.Code != fixture.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

type memorySearchFixture struct {
	result  memory.SearchResult
	err     error
	command memory.SearchCommand
}

func (s *memorySearchFixture) Search(_ context.Context, command memory.SearchCommand) (memory.SearchResult, error) {
	s.command = command
	return s.result, s.err
}

type memorySearchAuditFixture struct {
	operation string
	targetID  string
	metadata  map[string]any
	err       error
}

func (a *memorySearchAuditFixture) Record(_ context.Context, _ auth.RequestContext, _, operation, _, _, targetID, _ string, metadata map[string]any) error {
	a.operation = operation
	a.targetID = targetID
	a.metadata = metadata
	return a.err
}
