package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
)

type localOwnerFixture struct {
	status  control.LocalOwnerStatus
	signups int
}

func (f *localOwnerFixture) Status(context.Context) (control.LocalOwnerStatus, error) {
	return f.status, nil
}

func (f *localOwnerFixture) Signup(_ context.Context, input control.LocalOwnerSignup) (control.PersonalAccount, error) {
	f.signups++
	if !input.PrivateInstallationConfirmed || !strings.Contains(input.Email, "@") {
		return control.PersonalAccount{}, control.ErrInvalidLocalOwnerSignup
	}
	f.status = control.LocalOwnerStatus{State: "authenticated", Account: control.PersonalAccount{AccountID: "account", TenantID: "tenant", WorkspaceID: "workspace", Role: "owner", State: "active", CreatedAt: time.Unix(100, 0).UTC()}}
	return f.status.Account, nil
}

func TestLocalSessionSignupSetsHttpOnlyCookieWithoutReturningCredential(t *testing.T) {
	fixture := &localOwnerFixture{status: control.LocalOwnerStatus{State: "signup_required"}}
	handler := localOwnerSignup(fixture, "server-development-secret")
	request := httptest.NewRequest(http.MethodPost, "http://localhost/v1/local-session/signup", strings.NewReader(`{"display_name":"Owner","email":"owner@example.test","private_installation_confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost")
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusCreated || fixture.signups != 1 {
		t.Fatalf("status=%d signups=%d body=%s", recorder.Code, fixture.signups, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != localSessionCookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/v1" {
		t.Fatalf("unsafe session cookie: %+v", cookies)
	}
	if strings.Contains(recorder.Body.String(), "server-development-secret") {
		t.Fatal("session credential leaked in response")
	}
	if !strings.Contains(recorder.Body.String(), `"tenant_id":"tenant"`) || !strings.Contains(recorder.Body.String(), `"workspace_id":"workspace"`) {
		t.Fatalf("routing context missing: %s", recorder.Body.String())
	}
}

func TestLocalSessionStatusResumesOwnerAndLogoutExpiresCookie(t *testing.T) {
	fixture := &localOwnerFixture{status: control.LocalOwnerStatus{State: "authenticated", Account: control.PersonalAccount{TenantID: "tenant", WorkspaceID: "workspace"}}}
	statusRecorder := httptest.NewRecorder()
	localSessionStatus(fixture, "server-secret")(statusRecorder, httptest.NewRequest(http.MethodGet, "http://localhost/v1/local-session", nil))
	if statusRecorder.Code != http.StatusOK || len(statusRecorder.Result().Cookies()) != 1 {
		t.Fatalf("status response=%d cookies=%v body=%s", statusRecorder.Code, statusRecorder.Result().Cookies(), statusRecorder.Body.String())
	}

	logoutRecorder := httptest.NewRecorder()
	localSessionLogout(logoutRecorder, httptest.NewRequest(http.MethodDelete, "http://localhost/v1/local-session", nil))
	cookies := logoutRecorder.Result().Cookies()
	if logoutRecorder.Code != http.StatusNoContent || len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("logout status=%d cookies=%+v", logoutRecorder.Code, cookies)
	}
}

func TestLocalSessionSignupRejectsInvalidInputWithoutCookie(t *testing.T) {
	fixture := &localOwnerFixture{status: control.LocalOwnerStatus{State: "signup_required"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/v1/local-session/signup", strings.NewReader(`{"display_name":"Owner","email":"invalid"}`))
	request.Header.Set("Origin", "http://localhost")
	localOwnerSignup(fixture, "server-secret")(recorder, request)
	if recorder.Code != http.StatusBadRequest || len(recorder.Result().Cookies()) != 0 || fixture.signups != 1 {
		t.Fatalf("status=%d cookies=%v signups=%d", recorder.Code, recorder.Result().Cookies(), fixture.signups)
	}
}

func TestLocalSessionSignupRejectsCrossSiteRequest(t *testing.T) {
	fixture := &localOwnerFixture{status: control.LocalOwnerStatus{State: "signup_required"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/v1/local-session/signup", strings.NewReader(`{"display_name":"Owner","email":"owner@example.test","private_installation_confirmed":true}`))
	request.Header.Set("Origin", "https://attacker.example")
	localOwnerSignup(fixture, "server-secret")(recorder, request)
	if recorder.Code != http.StatusForbidden || fixture.signups != 0 || len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("status=%d signups=%d cookies=%v", recorder.Code, fixture.signups, recorder.Result().Cookies())
	}
}
