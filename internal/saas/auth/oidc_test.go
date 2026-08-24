package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOIDCAuthenticatorDiscoversProviderAndValidatesIdentity(t *testing.T) {
	provider := newOIDCTestProvider(t)
	authenticator, err := NewOIDCAuthenticator(context.Background(), provider.URL(), "agent-memory-web")
	if err != nil {
		t.Fatal(err)
	}
	token := provider.Sign(t, oidcTestClaims(provider.URL(), "agent-memory-web"))

	identity, err := authenticator.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.SubjectID != "provider|member" || identity.SessionID != "provider-session" {
		t.Fatalf("identity=%+v", identity)
	}
	profile, err := authenticator.Profile(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Email != "member@example.test" || profile.DisplayName != "Member Name" {
		t.Fatalf("profile=%+v", profile)
	}
}

func TestOIDCAuthenticatorRejectsInvalidTrustClaims(t *testing.T) {
	provider := newOIDCTestProvider(t)
	authenticator, err := NewOIDCAuthenticator(context.Background(), provider.URL(), "agent-memory-web")
	if err != nil {
		t.Fatal(err)
	}
	valid := oidcTestClaims(provider.URL(), "agent-memory-web")
	cases := map[string]map[string]any{
		"wrong audience":   cloneClaims(valid, "aud", "another-client"),
		"expired":          cloneClaims(valid, "exp", time.Now().Add(-time.Minute).Unix()),
		"unverified email": cloneClaims(valid, "email_verified", false),
		"missing email":    cloneClaims(valid, "email", ""),
		"missing subject":  cloneClaims(valid, "sub", ""),
	}
	for name, claims := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := authenticator.Verify(context.Background(), provider.Sign(t, claims)); err == nil {
				t.Fatal("expected token rejection")
			}
		})
	}
}

func TestOIDCAuthenticatorRefreshesRotatedJWKSKey(t *testing.T) {
	provider := newOIDCTestProvider(t)
	authenticator, err := NewOIDCAuthenticator(context.Background(), provider.URL(), "agent-memory-web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.Verify(context.Background(), provider.Sign(t, oidcTestClaims(provider.URL(), "agent-memory-web"))); err != nil {
		t.Fatal(err)
	}
	provider.Rotate(t)
	if _, err := authenticator.Verify(context.Background(), provider.Sign(t, oidcTestClaims(provider.URL(), "agent-memory-web"))); err != nil {
		t.Fatalf("rotated key was not refreshed: %v", err)
	}
}

func TestOIDCAuthenticatorFailsClosedWhenDiscoveryFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	if _, err := NewOIDCAuthenticator(context.Background(), server.URL, "agent-memory-web"); err == nil {
		t.Fatal("expected discovery failure")
	}
}

type oidcTestProvider struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.RWMutex
	key    *rsa.PrivateKey
	kid    string
}

func newOIDCTestProvider(t *testing.T) *oidcTestProvider {
	t.Helper()
	provider := &oidcTestProvider{t: t}
	provider.Rotate(t)
	provider.server = httptest.NewServer(http.HandlerFunc(provider.serveHTTP))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *oidcTestProvider) URL() string { return p.server.URL }

func (p *oidcTestProvider) Rotate(t *testing.T) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.key = key
	p.kid = fmt.Sprintf("key-%d", time.Now().UnixNano())
	p.mu.Unlock()
}

func (p *oidcTestProvider) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.server.URL, "jwks_uri": p.server.URL + "/keys",
			"authorization_endpoint": p.server.URL + "/authorize", "token_endpoint": p.server.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/keys":
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": p.kid,
			"n": base64.RawURLEncoding.EncodeToString(p.key.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.key.PublicKey.E)).Bytes()),
		}}})
	default:
		http.NotFound(w, r)
	}
}

func (p *oidcTestProvider) Sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	p.mu.RLock()
	defer p.mu.RUnlock()
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": p.kid})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func oidcTestClaims(issuer, audience string) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": audience, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Add(-time.Minute).Unix(),
		"sub": "provider|member", "email": "MEMBER@EXAMPLE.TEST", "email_verified": true,
		"name": "Member Name", "sid": "provider-session",
	}
}

func cloneClaims(source map[string]any, key string, value any) map[string]any {
	result := make(map[string]any, len(source))
	for name, current := range source {
		result[name] = current
	}
	if strings.TrimSpace(key) != "" {
		result[key] = value
	}
	return result
}
