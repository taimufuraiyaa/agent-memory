// Package credential manages scoped, revocable hosted agent credentials.
package credential

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

var (
	ErrForbidden         = errors.New("credential management is forbidden")
	ErrInvalidCredential = errors.New("credential is invalid")
)

type Credential struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type Issued struct {
	Credential Credential `json:"credential"`
	Secret     string     `json:"secret"`
}

type Repository interface {
	Create(context.Context, auth.RequestContext, Credential, []byte) error
	List(context.Context, auth.RequestContext) ([]Credential, error)
	Revoke(context.Context, auth.RequestContext, string, time.Time) error
	Verify(context.Context, string, string, []byte, time.Time) (auth.Identity, auth.Membership, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
}

type TokenAuthenticator struct{ service *Service }

func NewTokenAuthenticator(service *Service) *TokenAuthenticator {
	return &TokenAuthenticator{service: service}
}

func (a *TokenAuthenticator) Verify(ctx context.Context, token string) (auth.Identity, error) {
	if a == nil || a.service == nil {
		return auth.Identity{}, ErrInvalidCredential
	}
	identity, _, err := a.service.Verify(ctx, token)
	return identity, err
}

func NewService(repository Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, now: now}
}

func (s *Service) Create(ctx context.Context, label string, scopes []string, expiresAt time.Time) (Issued, error) {
	request, err := authorize(ctx, scopes)
	if err != nil {
		return Issued{}, err
	}
	label = strings.TrimSpace(label)
	now := s.now().UTC()
	if label == "" || len(label) > 128 || !expiresAt.After(now) || expiresAt.After(now.Add(366*24*time.Hour)) {
		return Issued{}, ErrInvalidCredential
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return Issued{}, err
	}
	credential := Credential{ID: uuid.NewString(), Label: label, Scopes: normalized(scopes), ExpiresAt: expiresAt.UTC(), CreatedAt: now}
	secretPart := base64.RawURLEncoding.EncodeToString(secretBytes)
	secret := "am_sk_" + request.TenantID + "_" + credential.ID + "_" + secretPart
	verifier := sha256.Sum256([]byte(secretPart))
	if err := s.repository.Create(ctx, request, credential, verifier[:]); err != nil {
		return Issued{}, err
	}
	return Issued{Credential: credential, Secret: secret}, nil
}

func (s *Service) List(ctx context.Context) ([]Credential, error) {
	request, err := authorize(ctx, nil)
	if err != nil {
		return nil, err
	}
	return s.repository.List(ctx, request)
}

func (s *Service) Revoke(ctx context.Context, credentialID string) error {
	request, err := authorize(ctx, nil)
	if err != nil {
		return err
	}
	return s.repository.Revoke(ctx, request, strings.TrimSpace(credentialID), s.now().UTC())
}

// RevokeSelf lets an authenticated agent credential revoke itself without
// granting the broader credential:manage capability.
func (s *Service) RevokeSelf(ctx context.Context) error {
	request, ok := auth.FromContext(ctx)
	if !ok || request.TenantID == "" || request.AccountID == "" || request.CredentialID == "" {
		return ErrForbidden
	}
	return s.repository.Revoke(ctx, request, request.CredentialID, s.now().UTC())
}

func (s *Service) Rotate(ctx context.Context, credentialID string, expiresAt time.Time) (Issued, error) {
	credentials, err := s.List(ctx)
	if err != nil {
		return Issued{}, err
	}
	for _, current := range credentials {
		if current.ID != credentialID || current.RevokedAt != nil {
			continue
		}
		issued, err := s.Create(ctx, current.Label, current.Scopes, expiresAt)
		if err != nil {
			return Issued{}, err
		}
		if err := s.Revoke(ctx, current.ID); err != nil {
			_ = s.Revoke(ctx, issued.Credential.ID)
			return Issued{}, err
		}
		return issued, nil
	}
	return Issued{}, ErrInvalidCredential
}

func (s *Service) Verify(ctx context.Context, token string) (auth.Identity, auth.Membership, error) {
	token = strings.TrimSpace(token)
	parts := strings.SplitN(token, "_", 5)
	if len(parts) != 5 || parts[0] != "am" || parts[1] != "sk" {
		return auth.Identity{}, auth.Membership{}, ErrInvalidCredential
	}
	tenantID, credentialID, secretPart := parts[2], parts[3], parts[4]
	if _, err := uuid.Parse(tenantID); err != nil {
		return auth.Identity{}, auth.Membership{}, ErrInvalidCredential
	}
	if _, err := uuid.Parse(credentialID); err != nil {
		return auth.Identity{}, auth.Membership{}, ErrInvalidCredential
	}
	verifier := sha256.Sum256([]byte(secretPart))
	identity, membership, err := s.repository.Verify(ctx, tenantID, credentialID, verifier[:], s.now().UTC())
	if err == nil {
		identity.Membership = &membership
	}
	return identity, membership, err
}

func authorize(ctx context.Context, scopes []string) (auth.RequestContext, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || request.TenantID == "" || request.AccountID == "" || !request.Can("credential:manage") {
		return auth.RequestContext{}, ErrForbidden
	}
	for _, scope := range normalized(scopes) {
		if !request.Can(scope) {
			return auth.RequestContext{}, ErrForbidden
		}
	}
	return request, nil
}

func normalized(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func verifierEqual(expected, actual []byte) bool {
	return len(expected) == len(actual) && subtle.ConstantTimeCompare(expected, actual) == 1
}
