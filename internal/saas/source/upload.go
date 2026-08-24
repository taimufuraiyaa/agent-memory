// Package source owns hosted source upload custody and ingestion state.
package source

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var checksumPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var allowedMedia = map[string]struct{}{"application/pdf": {}, "application/epub+zip": {}, "text/markdown": {}, "text/plain": {}}
var allowedRights = map[string]struct{}{"author_owned": {}, "licensed": {}, "public_domain_or_open": {}, "lawfully_acquired_private_use": {}}

type GrantRequest struct {
	WorkspaceID    string `json:"workspace_id"`
	Filename       string `json:"filename"`
	MediaType      string `json:"media_type"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	RightsBasis    string `json:"rights_basis"`
}
type Grant struct {
	ID           string    `json:"id"`
	SourceID     string    `json:"source_id"`
	TenantID     string    `json:"-"`
	UploadPath   string    `json:"upload_path"`
	ExpiresAt    time.Time `json:"expires_at"`
	ExpectedSize int64     `json:"expected_size"`
	MediaType    string    `json:"media_type"`
	token        string
}
type UploadClaim struct {
	GrantID, SourceID, TenantID, ObjectKey, MediaType, Checksum string
	ExpectedSize                                                int64
}
type Repository interface {
	Issue(context.Context, auth.RequestContext, GrantRequest, attestation.Receipt, []byte, string, time.Time) (Grant, error)
	ClaimUpload(context.Context, string, string, []byte, time.Time) (UploadClaim, error)
	CompleteUpload(context.Context, UploadClaim, time.Time) error
	FailUpload(context.Context, UploadClaim, string, time.Time) error
}
type QuarantineStore interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Delete(context.Context, string) error
}
type Service struct {
	repository   Repository
	attestations *attestation.Service
	objects      QuarantineStore
	now          func() time.Time
	rollout      interface {
		FeatureEnabled(context.Context, auth.RequestContext, string) (bool, error)
	}
}

func NewService(repository Repository, attestations *attestation.Service, objects QuarantineStore, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, attestations: attestations, objects: objects, now: now}
}

func (s *Service) SetRolloutGate(gate interface {
	FeatureEnabled(context.Context, auth.RequestContext, string) (bool, error)
}) {
	s.rollout = gate
}
func (s *Service) Issue(ctx context.Context, input GrantRequest) (Grant, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("source:write") {
		return Grant{}, auth.ErrTenantUnavailable
	}
	if s.rollout != nil {
		enabled, err := s.rollout.FeatureEnabled(ctx, request, "source_upload")
		if err != nil || !enabled {
			return Grant{}, errors.New("source uploads are paused by rollout policy")
		}
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Filename = filepath.Base(strings.TrimSpace(input.Filename))
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.ChecksumSHA256 = strings.ToLower(strings.TrimSpace(input.ChecksumSHA256))
	input.RightsBasis = strings.TrimSpace(input.RightsBasis)
	if input.WorkspaceID == "" || input.Filename == "." || input.Filename == "" || len(input.Filename) > 512 || input.SizeBytes < 1 || !checksumPattern.MatchString(input.ChecksumSHA256) {
		return Grant{}, errors.New("invalid upload grant request")
	}
	if _, ok := allowedMedia[input.MediaType]; !ok {
		return Grant{}, errors.New("unsupported source media type")
	}
	if _, ok := allowedRights[input.RightsBasis]; !ok {
		return Grant{}, errors.New("invalid rights basis")
	}
	receipt, err := s.attestations.RequireActive(ctx, request.AccountID)
	if err != nil {
		return Grant{}, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Grant{}, err
	}
	secretPart := base64.RawURLEncoding.EncodeToString(secret)
	verifier := sha256.Sum256([]byte(secretPart))
	grantID := uuid.NewString()
	objectKey := "quarantine/" + request.TenantID + "/" + grantID
	grant, err := s.repository.Issue(ctx, request, input, receipt, verifier[:], objectKey, s.now().UTC())
	if err != nil {
		return Grant{}, err
	}
	grant.token = "source_upload_" + request.TenantID + "_" + grant.ID + "_" + secretPart
	grant.UploadPath = "/v1/source-uploads/" + grant.ID + "/content?token=" + grant.token
	return grant, nil
}
func (s *Service) Upload(ctx context.Context, grantID, token, mediaType string, size int64, body io.Reader) error {
	parts := strings.SplitN(strings.TrimSpace(token), "_", 5)
	if len(parts) != 5 || parts[0] != "source" || parts[1] != "upload" || parts[3] != grantID {
		return auth.ErrTenantUnavailable
	}
	tenantID, secret := parts[2], parts[4]
	if _, err := uuid.Parse(tenantID); err != nil {
		return auth.ErrTenantUnavailable
	}
	verifier := sha256.Sum256([]byte(secret))
	claim, err := s.repository.ClaimUpload(ctx, tenantID, grantID, verifier[:], s.now().UTC())
	if err != nil {
		return err
	}
	if size != claim.ExpectedSize || mediaType != claim.MediaType {
		_ = s.repository.FailUpload(ctx, claim, "upload_contract_mismatch", s.now().UTC())
		return errors.New("upload does not match grant")
	}
	if err := s.objects.Put(ctx, claim.ObjectKey, io.LimitReader(body, size+1), size, mediaType); err != nil {
		_ = s.objects.Delete(ctx, claim.ObjectKey)
		_ = s.repository.FailUpload(ctx, claim, "object_write_failed", s.now().UTC())
		return fmt.Errorf("write quarantine object: %w", err)
	}
	if err := s.repository.CompleteUpload(ctx, claim, s.now().UTC()); err != nil {
		_ = s.objects.Delete(ctx, claim.ObjectKey)
		return err
	}
	return nil
}
