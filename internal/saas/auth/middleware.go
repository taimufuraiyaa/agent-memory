// Package auth resolves the immutable authorization context for hosted requests.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrTenantUnavailable = errors.New("tenant unavailable")
var ErrCrossSiteSessionRequest = errors.New("cross-site browser session request")

type TokenSource func(*http.Request) (string, error)

type Identity struct {
	SubjectID    string
	SessionID    string
	CredentialID string
	Membership   *Membership
}

type Membership struct {
	AccountID    string
	TenantID     string
	Role         string
	Capabilities []string
}

type Authenticator interface {
	Verify(ctx context.Context, bearerToken string) (Identity, error)
}

type MembershipResolver interface {
	Resolve(ctx context.Context, subjectID, selectedTenantID string) (Membership, error)
}

type RequestContext struct {
	AccountID    string
	SubjectID    string
	TenantID     string
	Role         string
	Capabilities map[string]struct{}
	SessionID    string
	CredentialID string
	RequestID    string
	TraceID      string
}

func (c RequestContext) Can(capability string) bool {
	_, ok := c.Capabilities[capability]
	return ok
}

type contextKey struct{}

type DenialObserver interface {
	AuthorizationDenied(ctx context.Context, subjectID, selectedTenantID, requestID, traceID string)
}
type RequestGate interface {
	Allow(context.Context, string, time.Time) (bool, error)
}

func FromContext(ctx context.Context) (RequestContext, bool) {
	value, ok := ctx.Value(contextKey{}).(RequestContext)
	return value, ok
}

// WithRequestContext is reserved for trusted transport and worker boundaries
// after identity, membership, and capability verification has completed.
func WithRequestContext(ctx context.Context, value RequestContext) context.Context {
	return context.WithValue(ctx, contextKey{}, value)
}

func Middleware(authenticator Authenticator, memberships MembershipResolver) func(http.Handler) http.Handler {
	return MiddlewareWithObserver(authenticator, memberships, nil)
}

func MiddlewareWithObserver(authenticator Authenticator, memberships MembershipResolver, observer DenialObserver) func(http.Handler) http.Handler {
	return MiddlewareWithGuards(authenticator, memberships, observer, nil)
}

func MiddlewareWithGuards(authenticator Authenticator, memberships MembershipResolver, observer DenialObserver, gate RequestGate) func(http.Handler) http.Handler {
	return MiddlewareWithGuardsAndTokenSource(authenticator, memberships, observer, gate, bearerHeaderTokenSource)
}

func MiddlewareWithGuardsAndTokenSource(authenticator Authenticator, memberships MembershipResolver, observer DenialObserver, gate RequestGate, tokenSource TokenSource) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := uuid.NewString()
			w.Header().Set("X-Request-ID", requestID)
			if tokenSource == nil {
				tokenSource = bearerHeaderTokenSource
			}
			token, tokenErr := tokenSource(r)
			if errors.Is(tokenErr, ErrCrossSiteSessionRequest) {
				writeError(w, http.StatusForbidden, requestID, "cross_site_request", "The browser session cannot authorize this request.")
				return
			}
			if tokenErr != nil || strings.TrimSpace(token) == "" {
				writeError(w, http.StatusUnauthorized, requestID, "unauthenticated", "Authentication is required.")
				return
			}
			identity, err := authenticator.Verify(r.Context(), token)
			if err != nil || strings.TrimSpace(identity.SubjectID) == "" {
				writeError(w, http.StatusUnauthorized, requestID, "unauthenticated", "Authentication is required.")
				return
			}
			selection := strings.TrimSpace(r.Header.Get("X-Agent-Memory-Tenant"))
			var membership Membership
			if identity.Membership != nil {
				membership = *identity.Membership
			} else {
				membership, err = memberships.Resolve(r.Context(), identity.SubjectID, selection)
			}
			if err != nil || strings.TrimSpace(membership.TenantID) == "" || (selection != "" && selection != membership.TenantID) {
				if observer != nil && selection != "" {
					observer.AuthorizationDenied(r.Context(), identity.SubjectID, selection, requestID, uuid.NewString())
				}
				writeError(w, http.StatusNotFound, requestID, "resource_not_found", "The requested resource was not found.")
				return
			}
			capabilities := make(map[string]struct{}, len(membership.Capabilities))
			for _, capability := range membership.Capabilities {
				if capability = strings.TrimSpace(capability); capability != "" {
					capabilities[capability] = struct{}{}
				}
			}
			resolved := RequestContext{
				AccountID:    membership.AccountID,
				SubjectID:    identity.SubjectID,
				TenantID:     membership.TenantID,
				Role:         membership.Role,
				Capabilities: capabilities,
				SessionID:    identity.SessionID,
				CredentialID: identity.CredentialID,
				RequestID:    requestID,
				TraceID:      uuid.NewString(),
			}
			if gate != nil {
				allowed, gateErr := gate.Allow(r.Context(), membership.TenantID, time.Now().UTC())
				if gateErr != nil || !allowed {
					writeError(w, http.StatusTooManyRequests, requestID, "rate_limited", "The tenant is temporarily restricted.")
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(WithRequestContext(r.Context(), resolved)))
		})
	}
}

func bearerHeaderTokenSource(r *http.Request) (string, error) {
	token, _ := bearerToken(r.Header.Get("Authorization"))
	return token, nil
}

func LocalBrowserTokenSource(cookieName string) TokenSource {
	cookieName = strings.TrimSpace(cookieName)
	return func(r *http.Request) (string, error) {
		if token, ok := bearerToken(r.Header.Get("Authorization")); ok {
			return token, nil
		}
		cookie, err := r.Cookie(cookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			return "", nil
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !SameOriginBrowserRequest(r) {
			return "", ErrCrossSiteSessionRequest
		}
		return strings.TrimSpace(cookie.Value), nil
	}
}

// SameOriginBrowserRequest reports whether browser metadata proves that a
// state-changing request came from the same origin as the API.
func SameOriginBrowserRequest(r *http.Request) bool {
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		parsed, err := url.Parse(origin)
		return err == nil && strings.EqualFold(parsed.Host, r.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https")
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	returnValue := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		returnValue = strings.TrimSpace(parts[1])
	}
	return returnValue, returnValue != ""
}

func writeError(w http.ResponseWriter, status int, requestID, code, message string) {
	type safeError struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	}
	type envelope struct {
		OK        bool      `json:"ok"`
		Version   string    `json:"version"`
		RequestID string    `json:"request_id"`
		Error     safeError `json:"error"`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{
		OK:        false,
		Version:   "v1",
		RequestID: requestID,
		Error:     safeError{Code: code, Message: message, Retryable: false},
	})
}
