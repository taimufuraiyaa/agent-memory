// Package auth resolves the immutable authorization context for hosted requests.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrTenantUnavailable = errors.New("tenant unavailable")

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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := uuid.NewString()
			w.Header().Set("X-Request-ID", requestID)
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
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
