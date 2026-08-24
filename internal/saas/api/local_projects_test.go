package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type localProjectFixture struct {
	projects []workspace.ListItem
	input    LocalProjectStudyInput
	feedback []core.RetrievalRequestLog
	score    LocalProjectFeedbackInput
	search   LocalProjectSearchInput
	browse   LocalProjectBrowseInput
	detailID string
	memories []core.MemoryEntry
}

type deniedSourceQueryFixture struct{ called bool }

func (fixture *deniedSourceQueryFixture) Query(context.Context, retrieval.Query) (retrieval.Result, error) {
	fixture.called = true
	return retrieval.Result{}, nil
}

func TestSourceQueryHandlerRequiresSourceRead(t *testing.T) {
	fixture := &deniedSourceQueryFixture{}
	request := httptest.NewRequest(http.MethodPost, "/v1/source-queries", strings.NewReader(`{"source_ids":["source-1"],"query":"test"}`))
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{Capabilities: map[string]struct{}{"memory:read": {}}}))
	recorder := httptest.NewRecorder()
	querySources(fixture, nil).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || fixture.called {
		t.Fatalf("source query was not denied before service access: status=%d called=%v", recorder.Code, fixture.called)
	}
}

func TestLocalProjectBoundaryRejectsAPICredentialsAndMissingCapability(t *testing.T) {
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) })
	for _, requestContext := range []auth.RequestContext{
		{CredentialID: "credential-1", SessionID: "session-1", Capabilities: map[string]struct{}{"memory:read": {}}},
		{SessionID: "session-1", Capabilities: map[string]struct{}{"memory:write": {}}},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/local-projects", nil)
		request = request.WithContext(auth.WithRequestContext(request.Context(), requestContext))
		localProjectBoundary("memory:read", next).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("expected forbidden request, got %d", recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/local-projects", nil)
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{SessionID: "session-1", Capabilities: map[string]struct{}{"memory:read": {}}}))
	localProjectBoundary("memory:read", next).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected browser owner session to pass, got %d", recorder.Code)
	}
}

func (fixture *localProjectFixture) Search(_ context.Context, input LocalProjectSearchInput) ([]LocalProjectMemoryResult, error) {
	fixture.search = input
	results := make([]LocalProjectMemoryResult, 0, len(fixture.memories))
	for _, memory := range fixture.memories {
		results = append(results, LocalProjectMemoryResult{Memory: memory, Score: 0.9, Explanation: "semantic match"})
	}
	return results, nil
}

func (fixture *localProjectFixture) Browse(_ context.Context, input LocalProjectBrowseInput) ([]core.MemoryEntry, error) {
	fixture.browse = input
	return fixture.memories, nil
}

func (fixture *localProjectFixture) GetMemory(_ context.Context, workspaceName, memoryID string) (*core.MemoryEntry, error) {
	fixture.detailID = workspaceName + ":" + memoryID
	if len(fixture.memories) == 0 {
		return nil, fmt.Errorf("memory not found")
	}
	return &fixture.memories[0], nil
}

func (fixture *localProjectFixture) ListFeedback(context.Context, string) ([]core.RetrievalRequestLog, error) {
	return fixture.feedback, nil
}

func (fixture *localProjectFixture) RecordFeedback(_ context.Context, input LocalProjectFeedbackInput) error {
	fixture.score = input
	return nil
}

func (fixture *localProjectFixture) List(context.Context) ([]workspace.ListItem, error) {
	return fixture.projects, nil
}

func (fixture *localProjectFixture) Study(_ context.Context, input LocalProjectStudyInput) (*engine.StudyResult, error) {
	fixture.input = input
	return &engine.StudyResult{ScannedFiles: 2, Extracted: 1, DryRun: input.DryRun}, nil
}

func TestLocalProjectsListReturnsRegisteredProjects(t *testing.T) {
	fixture := &localProjectFixture{projects: []workspace.ListItem{{Name: "agent-memory", WorkspaceRoot: "/workspace/agent-memory", MemoryCount: 78}}}
	recorder := httptest.NewRecorder()
	listLocalProjects(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-projects", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"name":"agent-memory"`) {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLocalProjectStudyUsesRegisteredNameAndBoundedOptions(t *testing.T) {
	fixture := &localProjectFixture{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/local-projects/study", strings.NewReader(`{"workspace":"agent-memory","depth":"medium","dry_run":true,"max_files":200,"offset":400}`))
	studyLocalProject(fixture)(recorder, request)
	if recorder.Code != http.StatusOK || fixture.input.Workspace != "agent-memory" || !fixture.input.DryRun || fixture.input.MaxFiles != engine.DefaultMaxFiles || fixture.input.Offset != 400 {
		t.Fatalf("unexpected study request: status=%d input=%+v body=%s", recorder.Code, fixture.input, recorder.Body.String())
	}
}

func TestLocalProjectStudyRejectsUnsafeFileLimit(t *testing.T) {
	fixture := &localProjectFixture{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/local-projects/study", strings.NewReader(`{"workspace":"agent-memory","depth":"deep","max_files":201}`))
	studyLocalProject(fixture)(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestLocalProjectStudyRejectsUnsafeOffset(t *testing.T) {
	for _, offset := range []int{-1, engine.MaxStudyOffset + 1} {
		fixture := &localProjectFixture{}
		recorder := httptest.NewRecorder()
		body := fmt.Sprintf(`{"workspace":"agent-memory","depth":"deep","max_files":20,"offset":%d}`, offset)
		request := httptest.NewRequest(http.MethodPost, "/v1/local-projects/study", strings.NewReader(body))
		studyLocalProject(fixture)(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("expected bad request for offset %d, got %d: %s", offset, recorder.Code, recorder.Body.String())
		}
	}
}

func TestLocalProjectFeedbackListsHistoricalRequests(t *testing.T) {
	fixture := &localProjectFixture{feedback: []core.RetrievalRequestLog{{ID: "request-1", Workspace: "agent-memory", RequestType: "search", Query: "feedback dashboard", Score: -1}}}
	recorder := httptest.NewRecorder()
	listLocalProjectFeedback(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-project-feedback?workspace=agent-memory", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"query":"feedback dashboard"`) {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLocalProjectFeedbackValidatesAndRecordsScore(t *testing.T) {
	fixture := &localProjectFixture{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/local-project-feedback", strings.NewReader(`{"workspace":"agent-memory","request_id":"request-1","score":2,"reason":"missed the requested component","useful_count":2,"total_count":8}`))
	recordLocalProjectFeedback(fixture)(recorder, request)
	if recorder.Code != http.StatusOK || fixture.score.RequestID != "request-1" || fixture.score.UsefulCount == nil || *fixture.score.UsefulCount != 2 {
		t.Fatalf("unexpected score request: status=%d input=%+v body=%s", recorder.Code, fixture.score, recorder.Body.String())
	}
}

func TestLocalProjectFeedbackRejectsLowScoreWithoutReason(t *testing.T) {
	fixture := &localProjectFixture{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/local-project-feedback", strings.NewReader(`{"workspace":"agent-memory","request_id":"request-1","score":3}`))
	recordLocalProjectFeedback(fixture)(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestLocalProjectSearchUsesRegisteredWorkspaceAndCursor(t *testing.T) {
	fixture := &localProjectFixture{memories: []core.MemoryEntry{{ID: "memory-1", Workspace: "agent-memory", Content: "Scoped result"}}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/local-projects/search", strings.NewReader(`{"workspace":"agent-memory","query":"scoped","limit":20,"cursor":"20"}`))
	searchLocalProject(fixture)(recorder, request)
	if recorder.Code != http.StatusOK || fixture.search.Workspace != "agent-memory" || fixture.search.Offset != 20 || !strings.Contains(recorder.Body.String(), `"memory-1"`) {
		t.Fatalf("unexpected search response: status=%d input=%+v body=%s", recorder.Code, fixture.search, recorder.Body.String())
	}
}

func TestLocalProjectBrowseAndDetailRemainWorkspaceScoped(t *testing.T) {
	fixture := &localProjectFixture{memories: []core.MemoryEntry{{ID: "memory-1", Workspace: "agent-memory", Content: "Scoped result", Pinned: true}}}
	recorder := httptest.NewRecorder()
	browseLocalProject(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-projects/memories?workspace=agent-memory&mode=pinned&limit=20&cursor=0", nil))
	if recorder.Code != http.StatusOK || fixture.browse.Workspace != "agent-memory" || fixture.browse.Mode != "pinned" {
		t.Fatalf("unexpected browse response: status=%d input=%+v body=%s", recorder.Code, fixture.browse, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	detailLocalProjectMemory(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-projects/memories/memory-1?workspace=agent-memory", nil), "memory-1")
	if recorder.Code != http.StatusOK || fixture.detailID != "agent-memory:memory-1" {
		t.Fatalf("unexpected detail response: status=%d detail=%q body=%s", recorder.Code, fixture.detailID, recorder.Body.String())
	}
}

func TestLocalProjectRetrievalRejectsPathShapedWorkspace(t *testing.T) {
	fixture := &localProjectFixture{}
	for _, workspaceName := range []string{"/tmp/other", "../other"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/local-projects/search", strings.NewReader(fmt.Sprintf(`{"workspace":%q,"query":"x","limit":20}`, workspaceName)))
		searchLocalProject(fixture)(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("expected path-shaped workspace %q to be rejected, got %d", workspaceName, recorder.Code)
		}
	}
}
