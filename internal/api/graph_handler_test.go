package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type fakeGraphOperations struct {
	requests []application.GraphOperationRequest
}

func (f *fakeGraphOperations) Readiness(context.Context, core.GraphScope, string) (application.GraphIndexReadiness, error) {
	return application.GraphIndexReadiness{Ready: true, Enabled: true, State: "ready"}, nil
}
func (f *fakeGraphOperations) Status(context.Context, core.GraphScope, string) (application.GraphIndexStatus, error) {
	return application.GraphIndexStatus{Enabled: true, State: "stale", PendingChanges: 3}, nil
}
func (f *fakeGraphOperations) Operate(_ context.Context, request application.GraphOperationRequest) (application.GraphOperationResult, error) {
	f.requests = append(f.requests, request)
	return application.GraphOperationResult{Action: request.Action, Accepted: true}, nil
}

func TestGraphStatusAPIReportsFreshnessAndPendingChanges(t *testing.T) {
	fake := &fakeGraphOperations{}
	mux := NewMux(&Service{GraphOperations: fake})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/graph-index/status?workspace=ws&configuration_id=default", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data application.GraphIndexStatus `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.PendingChanges != 3 || envelope.Data.State != "stale" {
		t.Fatalf("unexpected status: %#v", envelope.Data)
	}
}

func TestGraphIndexOperationAPIRejectsUnknownStorageRootAndNormalizesAuthority(t *testing.T) {
	fake := &fakeGraphOperations{}
	mux := NewMux(&Service{GraphOperations: fake})
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/graph-index/operations", strings.NewReader(`{"scope":{"workspace_id":"ws"},"configuration_id":"default","action":"update","idempotency_key":"key","artifact_root":"/tmp/escape"}`))
	bad.Header.Set("Content-Type", "application/json")
	badResponse := httptest.NewRecorder()
	mux.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", badResponse.Code, badResponse.Body.String())
	}

	good := httptest.NewRequest(http.MethodPost, "/api/v1/graph-index/operations", strings.NewReader(`{"scope":{"tenant_id":"attacker","workspace_id":"ws"},"configuration_id":"default","action":"update","idempotency_key":"key"}`))
	goodResponse := httptest.NewRecorder()
	mux.ServeHTTP(goodResponse, good)
	if goodResponse.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", goodResponse.Code, goodResponse.Body.String())
	}
	if len(fake.requests) != 1 || fake.requests[0].Scope.TenantID != "" || fake.requests[0].Actor != "api" {
		t.Fatalf("authority was not normalized: %#v", fake.requests)
	}
}

func TestGraphIndexOperationAPIMapsConflictsSafely(t *testing.T) {
	controller := &conflictingGraphOperations{}
	mux := NewMux(&Service{GraphOperations: controller})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/graph-index/operations", strings.NewReader(`{"scope":{"workspace_id":"ws"},"configuration_id":"default","action":"rollback","expected_revision":"stale"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), "SELECT") {
		t.Fatalf("unsafe conflict response: %d %s", response.Code, response.Body.String())
	}
}

type conflictingGraphOperations struct{ fakeGraphOperations }

func (conflictingGraphOperations) Operate(context.Context, application.GraphOperationRequest) (application.GraphOperationResult, error) {
	return application.GraphOperationResult{}, application.ErrGraphOperationConflict
}
