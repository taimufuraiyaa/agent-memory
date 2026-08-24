package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
)

const localSessionCookieName = "agent_memory_local_session"

type LocalOwnerService interface {
	Status(context.Context) (control.LocalOwnerStatus, error)
	Signup(context.Context, control.LocalOwnerSignup) (control.PersonalAccount, error)
}

type localSessionView struct {
	State       string `json:"state"`
	TenantID    string `json:"tenant_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

func localSessionStatus(service LocalOwnerService, sessionToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		status, err := service.Status(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, requestID, "local_session_unavailable", "The local session is temporarily unavailable.")
			return
		}
		view := localSessionView{State: status.State}
		if status.State == "authenticated" {
			view.TenantID, view.WorkspaceID = status.Account.TenantID, status.Account.WorkspaceID
			setLocalSessionCookie(w, r, sessionToken, 30*24*time.Hour)
		}
		writeSuccess(w, http.StatusOK, requestID, view)
	}
}

func localOwnerSignup(service LocalOwnerService, sessionToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		if !auth.SameOriginBrowserRequest(r) {
			writeError(w, http.StatusForbidden, requestID, "cross_site_request", "Cross-site local signup is not allowed.")
			return
		}
		var input control.LocalOwnerSignup
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, requestID, "invalid_request", "The signup details are invalid.")
			return
		}
		account, err := service.Signup(r.Context(), input)
		if errors.Is(err, control.ErrInvalidLocalOwnerSignup) {
			writeError(w, http.StatusBadRequest, requestID, "invalid_signup", "Enter a valid name and email, then confirm this private installation.")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, requestID, "signup_failed", "The local owner could not be created.")
			return
		}
		setLocalSessionCookie(w, r, sessionToken, 30*24*time.Hour)
		writeSuccess(w, http.StatusCreated, requestID, localSessionView{State: "authenticated", TenantID: account.TenantID, WorkspaceID: account.WorkspaceID})
	}
}

func localSessionLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: localSessionCookieName, Value: "", Path: "/v1", MaxAge: -1, Expires: time.Unix(1, 0).UTC(), HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	w.WriteHeader(http.StatusNoContent)
}

func setLocalSessionCookie(w http.ResponseWriter, r *http.Request, token string, lifetime time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: localSessionCookieName, Value: token, Path: "/v1", MaxAge: int(lifetime.Seconds()), Expires: time.Now().UTC().Add(lifetime), HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
}
