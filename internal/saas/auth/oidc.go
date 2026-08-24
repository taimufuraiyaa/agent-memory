package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

var ErrInvalidIdentityToken = errors.New("invalid identity token")

// OIDCAuthenticator verifies managed identity tokens using one long-lived
// discovery-backed verifier. The underlying remote key set caches keys and
// refreshes when the provider rotates to an unknown key ID.
type OIDCAuthenticator struct {
	verifier *oidc.IDTokenVerifier
}

func NewOIDCAuthenticator(ctx context.Context, issuer, audience string) (*OIDCAuthenticator, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	audience = strings.TrimSpace(audience)
	if issuer == "" || audience == "" {
		return nil, errors.New("OIDC issuer and audience are required")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	verifier := provider.VerifierContext(context.Background(), &oidc.Config{
		ClientID: audience,
		SupportedSigningAlgs: []string{
			oidc.RS256, oidc.RS384, oidc.RS512,
			oidc.PS256, oidc.PS384, oidc.PS512,
			oidc.ES256, oidc.ES384, oidc.ES512,
			oidc.EdDSA,
		},
	})
	return &OIDCAuthenticator{verifier: verifier}, nil
}

func (a *OIDCAuthenticator) Verify(ctx context.Context, bearerToken string) (Identity, error) {
	claims, err := a.verify(ctx, bearerToken)
	if err != nil {
		return Identity{}, err
	}
	return Identity{SubjectID: claims.SubjectID, SessionID: claims.SessionID}, nil
}

func (a *OIDCAuthenticator) Profile(ctx context.Context, bearerToken string) (VerifiedProfile, error) {
	claims, err := a.verify(ctx, bearerToken)
	if err != nil {
		return VerifiedProfile{}, err
	}
	return VerifiedProfile{SubjectID: claims.SubjectID, Email: claims.Email, DisplayName: claims.DisplayName}, nil
}

type oidcIdentityClaims struct {
	SubjectID     string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	DisplayName   string `json:"name"`
	SessionID     string `json:"sid"`
}

func (a *OIDCAuthenticator) verify(ctx context.Context, bearerToken string) (oidcIdentityClaims, error) {
	if a == nil || a.verifier == nil || strings.TrimSpace(bearerToken) == "" {
		return oidcIdentityClaims{}, ErrInvalidIdentityToken
	}
	token, err := a.verifier.Verify(ctx, strings.TrimSpace(bearerToken))
	if err != nil {
		return oidcIdentityClaims{}, ErrInvalidIdentityToken
	}
	var claims oidcIdentityClaims
	if err := token.Claims(&claims); err != nil {
		return oidcIdentityClaims{}, ErrInvalidIdentityToken
	}
	claims.SubjectID = strings.TrimSpace(claims.SubjectID)
	claims.Email = strings.ToLower(strings.TrimSpace(claims.Email))
	claims.DisplayName = strings.TrimSpace(claims.DisplayName)
	claims.SessionID = strings.TrimSpace(claims.SessionID)
	if claims.SubjectID == "" || claims.Email == "" || !claims.EmailVerified || token.Subject != claims.SubjectID {
		return oidcIdentityClaims{}, ErrInvalidIdentityToken
	}
	return claims, nil
}
