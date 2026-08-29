package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
)

func TestHostedSkillLifecycleBindsOwnerTenantWorkspaceAndActor(t *testing.T) {
	fixture := &hostedSkillLifecycleFixture{}
	owner := hostedOwnerFixture{status: control.LocalOwnerStatus{State: "authenticated", Account: control.PersonalAccount{TenantID: "tenant-1", AccountID: "account-1"}}}
	handler := localProjectOwnerBoundary(owner, "memory:write", localProjectSkillLifecycle(fixture))
	request := httptest.NewRequest(http.MethodPost, "/v1/local-project-skills/lifecycle", strings.NewReader(`{"workspace":"agent-memory","operation":"canary","payload":{"candidate_revision_id":"revision-2"}}`))
	request = request.WithContext(auth.WithRequestContext(request.Context(), hostedSkillCaller("tenant-1", "account-1", "memory:write")))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || fixture.input.Actor != "subject-1" || fixture.input.TenantID != "tenant-1" || fixture.input.Workspace != "agent-memory" {
		t.Fatalf("hosted scope was not bound: status=%d input=%+v body=%s", recorder.Code, fixture.input, recorder.Body.String())
	}

	fixture.called = false
	request = httptest.NewRequest(http.MethodPost, "/v1/local-project-skills/lifecycle", strings.NewReader(`{"workspace":"agent-memory","operation":"rollback","payload":{"revision_id":"revision-2"}}`))
	request = request.WithContext(auth.WithRequestContext(request.Context(), hostedSkillCaller("tenant-2", "account-2", "memory:write")))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || fixture.called || strings.Contains(recorder.Body.String(), "revision-2") {
		t.Fatalf("cross-tenant request leaked or reached service: status=%d called=%v body=%s", recorder.Code, fixture.called, recorder.Body.String())
	}
}

func TestHostedSkillLifecycleRejectsPathsAndUnknownProjectsBeforeMutation(t *testing.T) {
	fixture := &hostedSkillLifecycleFixture{}
	handler := localProjectSkillLifecycle(fixture)
	for _, body := range []string{`{"workspace":"../escape","operation":"approve","payload":{}}`, `{"workspace":"agent-memory","operation":"propose","payload":{"project_root":"/tmp/escape"}}`, `{"workspace":"agent-memory","operation":"disable","payload":{"workspace":"other","revision_id":"revision-1"}}`} {
		fixture.called = false
		request := httptest.NewRequest(http.MethodPost, "/v1/local-project-skills/lifecycle", strings.NewReader(body))
		request = request.WithContext(auth.WithRequestContext(request.Context(), hostedSkillCaller("tenant-1", "account-1", "memory:write")))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || fixture.called {
			t.Fatalf("unsafe payload reached service: status=%d called=%v body=%s", recorder.Code, fixture.called, recorder.Body.String())
		}
	}
	fixture.err = errors.New("unknown registered project")
	request := httptest.NewRequest(http.MethodGet, "/v1/local-project-skills/lifecycle?workspace=missing", nil)
	request = request.WithContext(auth.WithRequestContext(request.Context(), hostedSkillCaller("tenant-1", "account-1", "memory:read")))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown project status=%d", recorder.Code)
	}
}

func TestHostedSkillLifecycleListsAndInspectsBoundedRegistryState(t *testing.T) {
	fixture := &hostedSkillLifecycleFixture{skills: []core.LogicalSkill{{ID: "skill-1", Workspace: "agent-memory", Name: "safe-skill"}}}
	handler := localProjectSkillLifecycle(fixture)
	for _, target := range []string{"/v1/local-project-skills/lifecycle?workspace=agent-memory", "/v1/local-project-skills/lifecycle?workspace=agent-memory&skill_id=skill-1&environment=local"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request = request.WithContext(auth.WithRequestContext(request.Context(), hostedSkillCaller("tenant-1", "account-1", "memory:read")))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "skill-1") {
			t.Fatalf("lifecycle read failed: %d %s", recorder.Code, recorder.Body.String())
		}
	}
}

type hostedSkillLifecycleFixture struct {
	input  LocalProjectSkillLifecycleInput
	called bool
	err    error
	skills []core.LogicalSkill
}

func (f *hostedSkillLifecycleFixture) ListSkillLifecycle(context.Context, string) ([]core.LogicalSkill, error) {
	f.called = true
	return f.skills, f.err
}
func (f *hostedSkillLifecycleFixture) InspectSkillLifecycle(_ context.Context, workspaceName, skillID string, _ string) (LocalProjectSkillLifecycleView, error) {
	f.called = true
	if f.err != nil {
		return LocalProjectSkillLifecycleView{}, f.err
	}
	return LocalProjectSkillLifecycleView{Skill: core.LogicalSkill{ID: skillID, Workspace: workspaceName, Name: "safe-skill"}}, nil
}
func (f *hostedSkillLifecycleFixture) OperateSkillLifecycle(_ context.Context, input LocalProjectSkillLifecycleInput) (any, error) {
	f.called = true
	f.input = input
	return map[string]any{"accepted": true}, f.err
}

type hostedOwnerFixture struct {
	status control.LocalOwnerStatus
	err    error
}

func (f hostedOwnerFixture) Status(context.Context) (control.LocalOwnerStatus, error) {
	return f.status, f.err
}
func (hostedOwnerFixture) Signup(context.Context, control.LocalOwnerSignup) (control.PersonalAccount, error) {
	return control.PersonalAccount{}, errors.New("not implemented")
}
func hostedSkillCaller(tenant, account, capability string) auth.RequestContext {
	return auth.RequestContext{SubjectID: "subject-1", TenantID: tenant, AccountID: account, Role: "owner", SessionID: "session-1", Capabilities: map[string]struct{}{capability: {}}}
}
