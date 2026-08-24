package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeAuthenticator struct {
	identity Identity
	err      error
}

func (f fakeAuthenticator) Verify(context.Context, string) (Identity, error) {
	return f.identity, f.err
}

type fakeMemberships struct {
	membership Membership
	err        error
}

func (f fakeMemberships) Resolve(context.Context, string, string) (Membership, error) {
	return f.membership, f.err
}

func TestMiddlewareResolvesCompleteAuthorizationContext(t *testing.T) {
	middleware := Middleware(
		fakeAuthenticator{identity: Identity{SubjectID: "subject-1", SessionID: "session-1"}},
		fakeMemberships{membership: Membership{AccountID: "account-1", TenantID: "tenant-1", Role: "owner", Capabilities: []string{"memory:read"}}},
	)
	var got RequestContext
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("X-Agent-Memory-Tenant", "tenant-1")
	request.Header.Set("X-Request-ID", "client-request")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
	if got.SubjectID != "subject-1" || got.AccountID != "account-1" || got.TenantID != "tenant-1" || got.SessionID != "session-1" {
		t.Fatalf("incomplete authorization context: %+v", got)
	}
	if got.RequestID == "" || got.TraceID == "" || got.RequestID == "client-request" {
		t.Fatalf("request identifiers must be server generated: %+v", got)
	}
	if !got.Can("memory:read") || got.Can("memory:write") {
		t.Fatalf("unexpected capabilities: %+v", got.Capabilities)
	}
}

func TestMiddlewareRejectsMissingBearerToken(t *testing.T) {
	handler := Middleware(fakeAuthenticator{}, fakeMemberships{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler must not run")
	}))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/whoami", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("rejection must include a request ID")
	}
}

func TestMiddlewareHidesUnauthorizedTenantExistence(t *testing.T) {
	middleware := Middleware(
		fakeAuthenticator{identity: Identity{SubjectID: "subject-1"}},
		fakeMemberships{err: ErrTenantUnavailable},
	)
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler must not run")
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("X-Agent-Memory-Tenant", "another-tenant")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want generic 404", recorder.Code)
	}
}

func TestMiddlewareRejectsInvalidCredential(t *testing.T) {
	middleware := Middleware(fakeAuthenticator{err: errors.New("provider details")}, fakeMemberships{})
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler must not run")
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/whoami", nil)
	request.Header.Set("Authorization", "Bearer invalid")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized || recorder.Body.String() != "{\"ok\":false,\"version\":\"v1\",\"request_id\":\""+recorder.Header().Get("X-Request-ID")+"\",\"error\":{\"code\":\"unauthenticated\",\"message\":\"Authentication is required.\",\"retryable\":false}}\n" {
		t.Fatalf("unsafe authentication error: %s", recorder.Body.String())
	}
}

func TestLocalBrowserTokenSourcePrefersBearerAndAcceptsSameOriginCookie(t *testing.T) {
	source := LocalBrowserTokenSource("agent_memory_local_session")
	bearerRequest := httptest.NewRequest(http.MethodPost, "http://localhost/v1/memories", nil)
	bearerRequest.Header.Set("Authorization", "Bearer agent-token")
	bearerRequest.AddCookie(&http.Cookie{Name: "agent_memory_local_session", Value: "browser-token"})
	if token, err := source(bearerRequest); err != nil || token != "agent-token" {
		t.Fatalf("bearer token=%q err=%v", token, err)
	}

	cookieRequest := httptest.NewRequest(http.MethodPost, "http://localhost/v1/memories", nil)
	cookieRequest.Header.Set("Origin", "http://localhost")
	cookieRequest.AddCookie(&http.Cookie{Name: "agent_memory_local_session", Value: "browser-token"})
	if token, err := source(cookieRequest); err != nil || token != "browser-token" {
		t.Fatalf("cookie token=%q err=%v", token, err)
	}
}

func TestLocalBrowserTokenSourceRejectsCrossSiteUnsafeCookieRequest(t *testing.T) {
	source := LocalBrowserTokenSource("agent_memory_local_session")
	request := httptest.NewRequest(http.MethodDelete, "http://localhost/v1/account", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.AddCookie(&http.Cookie{Name: "agent_memory_local_session", Value: "browser-token"})
	if _, err := source(request); !errors.Is(err, ErrCrossSiteSessionRequest) {
		t.Fatalf("error=%v, want cross-site rejection", err)
	}
}
