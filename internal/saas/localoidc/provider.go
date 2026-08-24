// Package localoidc provides an ephemeral development-only OIDC boundary.
package localoidc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Issuer      string
	Audience    string
	Subject     string
	Email       string
	DisplayName string
	Now         func() time.Time
}

type signingKey struct {
	private *rsa.PrivateKey
	kid     string
}

type Provider struct {
	issuer      string
	audience    string
	subject     string
	email       string
	displayName string
	now         func() time.Time
	mu          sync.RWMutex
	current     signingKey
	previous    *signingKey
}

func New(cfg Config) (*Provider, error) {
	issuer := strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("local OIDC issuer must be an HTTP or HTTPS URL")
	}
	audience := bounded(cfg.Audience)
	subject := bounded(cfg.Subject)
	email := strings.ToLower(bounded(cfg.Email))
	if audience == "" || subject == "" {
		return nil, errors.New("local OIDC audience and subject are required")
	}
	if !strings.Contains(email, "@") || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return nil, errors.New("local OIDC synthetic email is invalid")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	provider := &Provider{
		issuer: issuer, audience: audience, subject: subject, email: email,
		displayName: bounded(cfg.DisplayName), now: now,
	}
	if err := provider.Rotate(); err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *Provider) Rotate() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return errors.New("generate local OIDC signing key")
	}
	digest := sha256.Sum256(key.PublicKey.N.Bytes())
	next := signingKey{private: key, kid: "local-" + hex.EncodeToString(digest[:8])}
	p.mu.Lock()
	if p.current.private != nil {
		prior := p.current
		p.previous = &prior
	}
	p.current = next
	p.mu.Unlock()
	return nil
}

func (p *Provider) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_local/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"service": "local-oidc", "status": "ok"})
	})
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.keys)
	mux.HandleFunc("GET /token", p.token)
	mux.HandleFunc("POST /rotate", p.rotate)
	return mux
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer": p.issuer, "jwks_uri": p.issuer + "/keys",
		"authorization_endpoint":                p.issuer + "/authorize",
		"token_endpoint":                        p.issuer + "/token",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *Provider) keys(w http.ResponseWriter, _ *http.Request) {
	p.mu.RLock()
	keys := []map[string]string{publicJWK(p.current)}
	if p.previous != nil {
		keys = append(keys, publicJWK(*p.previous))
	}
	p.mu.RUnlock()
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, map[string]any{"keys": keys})
}

func (p *Provider) token(w http.ResponseWriter, _ *http.Request) {
	token, err := p.signToken()
	if err != nil {
		http.Error(w, "local identity unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]string{"token": token})
}

func (p *Provider) rotate(w http.ResponseWriter, _ *http.Request) {
	if err := p.Rotate(); err != nil {
		http.Error(w, "local identity unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]bool{"rotated": true})
}

func (p *Provider) signToken() (string, error) {
	p.mu.RLock()
	key := p.current
	p.mu.RUnlock()
	now := p.now().UTC()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": key.kid})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"iss": p.issuer, "aud": p.audience, "sub": p.subject,
		"email": p.email, "email_verified": true, "name": p.displayName,
		"sid": "local-oidc-session", "iat": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(),
	})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key.private, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func publicJWK(key signingKey) map[string]string {
	return map[string]string{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": key.kid,
		"n": base64.RawURLEncoding.EncodeToString(key.private.PublicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.private.PublicKey.E)).Bytes()),
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(value)
}

func bounded(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	if len(value) > 256 {
		return value[:256]
	}
	return value
}
