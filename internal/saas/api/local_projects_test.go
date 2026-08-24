package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type localProjectFixture struct {
	projects   []workspace.ListItem
	input      LocalProjectStudyInput
	feedback   []core.RetrievalRequestLog
	score      LocalProjectFeedbackInput
	search     LocalProjectSearchInput
	browse     LocalProjectBrowseInput
	detailID   string
	memories   []core.MemoryEntry
	solutions  []application.SolutionActivityEpisode
	detail     application.SolutionActivityDetail
	review     LocalProjectSolutionReviewInput
	start      LocalProjectSolutionStartInput
	step       LocalProjectSolutionStepInput
	checkpoint LocalProjectSolutionCheckpointInput
	transition LocalProjectSolutionTransitionInput
	handoff    LocalProjectSolutionHandoffInput
	finalize   LocalProjectSolutionFinalizeInput
	recall     LocalProjectSolutionRecallInput
	export     LocalProjectSolutionExportInput
}

func (fixture *localProjectFixture) StartSolutionEpisode(_ context.Context, input LocalProjectSolutionStartInput) (core.SolutionEpisode, bool, error) {
	fixture.start = input
	return core.SolutionEpisode{ID: "episode-1", Workspace: input.Workspace, PrincipalID: input.PrincipalID}, false, nil
}

func (fixture *localProjectFixture) AppendSolutionStep(_ context.Context, input LocalProjectSolutionStepInput) (core.SolutionStep, bool, error) {
	fixture.step = input
	return core.SolutionStep{ID: "step-1", EpisodeID: input.EpisodeID}, false, nil
}

func (fixture *localProjectFixture) CheckpointSolutionEpisode(_ context.Context, input LocalProjectSolutionCheckpointInput) (core.SolutionWorkingState, error) {
	fixture.checkpoint = input
	return core.SolutionWorkingState{EpisodeID: input.EpisodeID, Workspace: input.Workspace, PrincipalID: input.PrincipalID}, nil
}

func (fixture *localProjectFixture) TransitionSolutionEpisode(_ context.Context, input LocalProjectSolutionTransitionInput) (core.SolutionEpisode, error) {
	fixture.transition = input
	return core.SolutionEpisode{ID: input.EpisodeID, Workspace: input.Workspace, PrincipalID: input.PrincipalID}, nil
}

func (fixture *localProjectFixture) HandoffSolutionEpisode(_ context.Context, input LocalProjectSolutionHandoffInput) (core.SolutionEpisode, error) {
	fixture.handoff = input
	return core.SolutionEpisode{ID: input.EpisodeID, Workspace: input.Workspace, PrincipalID: input.TargetPrincipalID}, nil
}

func (fixture *localProjectFixture) FinalizeSolutionEpisode(_ context.Context, input LocalProjectSolutionFinalizeInput) (core.SolutionSummary, error) {
	fixture.finalize = input
	return core.SolutionSummary{ID: "summary-1", EpisodeID: input.EpisodeID}, nil
}

func (fixture *localProjectFixture) RecallSolutionPaths(_ context.Context, input LocalProjectSolutionRecallInput) (engine.HowRecallResult, error) {
	fixture.recall = input
	return engine.HowRecallResult{RequestID: "request-1"}, nil
}

func (fixture *localProjectFixture) ExportSolutionEpisode(_ context.Context, input LocalProjectSolutionExportInput) (LocalProjectSolutionExport, error) {
	fixture.export = input
	return LocalProjectSolutionExport{Detail: fixture.detail}, nil
}

func (fixture *localProjectFixture) ListSolutionEpisodes(_ context.Context, workspaceName string, _ int) ([]application.SolutionActivityEpisode, error) {
	if workspaceName != "agent-memory" {
		return nil, fmt.Errorf("unknown registered project")
	}
	return fixture.solutions, nil
}

func (fixture *localProjectFixture) GetSolutionEpisode(_ context.Context, workspaceName, episodeID string) (application.SolutionActivityDetail, error) {
	if workspaceName != "agent-memory" || episodeID != fixture.detail.Episode.ID {
		return application.SolutionActivityDetail{}, fmt.Errorf("not found")
	}
	return fixture.detail, nil
}

func (fixture *localProjectFixture) ReviewSolutionEpisode(_ context.Context, input LocalProjectSolutionReviewInput) error {
	fixture.review = input
	if input.Workspace != "agent-memory" {
		return fmt.Errorf("unknown registered project")
	}
	return nil
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
		{SubjectID: "subject-1", AccountID: "account-1", TenantID: "tenant-1", Role: "owner", CredentialID: "credential-1", SessionID: "session-1", Capabilities: map[string]struct{}{"memory:read": {}}},
		{SubjectID: "subject-1", AccountID: "account-1", TenantID: "tenant-1", Role: "owner", SessionID: "session-1", Capabilities: map[string]struct{}{"memory:write": {}}},
		{SubjectID: "subject-1", AccountID: "account-1", TenantID: "tenant-2", Role: "member", SessionID: "session-2", Capabilities: map[string]struct{}{"memory:read": {}}},
		{SessionID: "session-1", Capabilities: map[string]struct{}{"memory:read": {}}},
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
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{SubjectID: "subject-1", AccountID: "account-1", TenantID: "tenant-1", Role: "owner", SessionID: "session-1", Capabilities: map[string]struct{}{"memory:read": {}}}))
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

func TestLocalProjectBrowseAcceptsUngroupedMemoryMode(t *testing.T) {
	fixture := &localProjectFixture{memories: []core.MemoryEntry{{ID: "memory-1", Workspace: "agent-memory", Content: "Standalone knowledge"}}}
	recorder := httptest.NewRecorder()
	browseLocalProject(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-projects/memories?workspace=agent-memory&mode=ungrouped&limit=20&cursor=0", nil))
	if recorder.Code != http.StatusOK || fixture.browse.Mode != "ungrouped" {
		t.Fatalf("unexpected ungrouped browse: status=%d input=%+v body=%s", recorder.Code, fixture.browse, recorder.Body.String())
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

func TestLocalProjectSolutionsListDetailReviewAndRejectPathIdentity(t *testing.T) {
	episode := core.SolutionEpisode{ID: "episode-1", Workspace: "agent-memory", PrincipalID: "agent-1", GoalSummary: "Inspect a path."}
	fixture := &localProjectFixture{solutions: []application.SolutionActivityEpisode{{Episode: episode, StepCount: 2}}, detail: application.SolutionActivityDetail{Episode: episode}}
	recorder := httptest.NewRecorder()
	listLocalProjectSolutions(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-project-solutions?workspace=agent-memory&limit=20", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"episode-1"`) {
		t.Fatalf("unexpected list: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	listLocalProjectSolutions(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-project-solutions?workspace=agent-memory&episode_id=episode-1", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"Inspect a path."`) {
		t.Fatalf("unexpected detail: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	reviewLocalProjectSolution(fixture)(recorder, httptest.NewRequest(http.MethodPost, "/v1/local-project-solutions/review", strings.NewReader(`{"workspace":"agent-memory","episode_id":"episode-1","action":"pin","pinned":true}`)))
	if recorder.Code != http.StatusOK || fixture.review.EpisodeID != "episode-1" || !fixture.review.Pinned {
		t.Fatalf("unexpected review: %d %+v %s", recorder.Code, fixture.review, recorder.Body.String())
	}
	for _, workspaceName := range []string{"../agent-memory", "/tmp/agent-memory"} {
		recorder = httptest.NewRecorder()
		listLocalProjectSolutions(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-project-solutions?workspace="+workspaceName, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("path-shaped workspace %q was accepted: %d", workspaceName, recorder.Code)
		}
	}
}

func TestLocalProjectSolutionLifecycleRoutesPreserveWorkspaceAndPrincipalScope(t *testing.T) {
	fixture := &localProjectFixture{detail: application.SolutionActivityDetail{Episode: core.SolutionEpisode{ID: "episode-1", Workspace: "agent-memory", PrincipalID: "principal-1"}}}
	tests := []struct {
		name    string
		handler http.HandlerFunc
		path    string
		body    string
		check   func() bool
	}{
		{"start", startLocalProjectSolution(fixture), "/v1/local-project-solutions/start", `{"workspace":"agent-memory","session_id":"session-1","principal_id":"principal-1","client_id":"client-1","goal_summary":"Ship safely","capture_policy":"structured","retention_class":"standard","idempotency_key":"start-key"}`, func() bool {
			return fixture.start.Workspace == "agent-memory" && fixture.start.PrincipalID == "principal-1"
		}},
		{"step", appendLocalProjectSolutionStep(fixture), "/v1/local-project-solutions/steps", `{"workspace":"agent-memory","principal_id":"principal-1","episode_id":"episode-1","kind":"action","status":"completed","summary":"Ran tests","sensitivity":"internal","idempotency_key":"step-key"}`, func() bool { return fixture.step.EpisodeID == "episode-1" && fixture.step.PrincipalID == "principal-1" }},
		{"checkpoint", checkpointLocalProjectSolution(fixture), "/v1/local-project-solutions/checkpoint", `{"workspace":"agent-memory","principal_id":"principal-1","episode_id":"episode-1","goal_summary":"Ship safely","ttl_seconds":3600}`, func() bool { return fixture.checkpoint.TTLSeconds == 3600 }},
		{"transition", transitionLocalProjectSolution(fixture), "/v1/local-project-solutions/transition", `{"workspace":"agent-memory","principal_id":"principal-1","episode_id":"episode-1","expected_version":1,"status":"completed","idempotency_key":"transition-key"}`, func() bool { return fixture.transition.ExpectedVersion == 1 }},
		{"handoff", handoffLocalProjectSolution(fixture), "/v1/local-project-solutions/handoff", `{"workspace":"agent-memory","principal_id":"principal-1","episode_id":"episode-1","expected_version":1,"target_principal_id":"principal-2","target_session_id":"session-2","idempotency_key":"handoff-key"}`, func() bool { return fixture.handoff.TargetPrincipalID == "principal-2" }},
		{"finalize", finalizeLocalProjectSolution(fixture), "/v1/local-project-solutions/finalize", `{"workspace":"agent-memory","principal_id":"principal-1","episode_id":"episode-1","expected_version":2,"idempotency_key":"finalize-key"}`, func() bool { return fixture.finalize.ExpectedVersion == 2 }},
		{"recall", recallLocalProjectSolutions(fixture), "/v1/local-project-solutions/recall", `{"workspace":"agent-memory","principal_id":"principal-1","session_id":"session-1","task":"Ship safely","token_budget":800}`, func() bool { return fixture.recall.Task == "Ship safely" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.handler(recorder, httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body)))
			if recorder.Code != http.StatusOK || !test.check() {
				t.Fatalf("unexpected operation: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	exportLocalProjectSolution(fixture)(recorder, httptest.NewRequest(http.MethodGet, "/v1/local-project-solutions/export?workspace=agent-memory&principal_id=principal-1&episode_id=episode-1", nil))
	if recorder.Code != http.StatusOK || fixture.export.PrincipalID != "principal-1" {
		t.Fatalf("unexpected export: status=%d input=%+v body=%s", recorder.Code, fixture.export, recorder.Body.String())
	}
}

func TestLocalProjectSolutionOperationsRejectArbitraryPathsBeforeServiceAccess(t *testing.T) {
	fixture := &localProjectFixture{}
	for _, workspaceName := range []string{"../other", "/tmp/other"} {
		recorder := httptest.NewRecorder()
		body := fmt.Sprintf(`{"workspace":%q,"session_id":"session-1","principal_id":"principal-1","client_id":"client-1","goal_summary":"x","idempotency_key":"key"}`, workspaceName)
		startLocalProjectSolution(fixture)(recorder, httptest.NewRequest(http.MethodPost, "/v1/local-project-solutions/start", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest || fixture.start.Workspace != "" {
			t.Fatalf("path-shaped workspace reached service: workspace=%q status=%d", workspaceName, recorder.Code)
		}
	}
}
