// Package export creates encrypted, time-limited tenant data exports.
package export

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type Operation struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"-"`
	WorkspaceID    string     `json:"workspace_id,omitempty"`
	State          string     `json:"state"`
	ChecksumSHA256 string     `json:"checksum_sha256,omitempty"`
	RequestedAt    time.Time  `json:"requested_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}
type Claimed struct {
	Operation
	AccountID string
}
type Bundle struct {
	Format              string           `json:"format"`
	Version             string           `json:"version"`
	MinReaderVersion    string           `json:"min_reader_version"`
	ExportedAt          time.Time        `json:"exported_at"`
	TenantID            string           `json:"tenant_id"`
	WorkspaceID         string           `json:"workspace_id,omitempty"`
	Memories            []map[string]any `json:"memories"`
	Notes               []map[string]any `json:"notes"`
	Sources             []map[string]any `json:"sources"`
	SourceVersions      []map[string]any `json:"source_versions"`
	Lineage             []map[string]any `json:"lineage"`
	Attestations        []map[string]any `json:"attestations"`
	Policies            []map[string]any `json:"policies"`
	SourceBytesIncluded bool             `json:"source_bytes_included"`
	SourceObjects       []SourceObject   `json:"source_objects,omitempty"`
	Manifest            BundleManifest   `json:"manifest"`
}
type SourceObject struct {
	SourceID       string `json:"source_id"`
	Filename       string `json:"filename"`
	MediaType      string `json:"media_type"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	BytesBase64    string `json:"bytes_base64"`
}
type BundleManifest struct {
	Algorithm     string         `json:"algorithm"`
	PayloadSHA256 string         `json:"payload_sha256"`
	Counts        map[string]int `json:"counts"`
}

func (b *Bundle) SealManifest() error {
	if b.Memories == nil {
		b.Memories = []map[string]any{}
	}
	if b.Notes == nil {
		b.Notes = []map[string]any{}
	}
	if b.Sources == nil {
		b.Sources = []map[string]any{}
	}
	if b.SourceVersions == nil {
		b.SourceVersions = []map[string]any{}
	}
	if b.Lineage == nil {
		b.Lineage = []map[string]any{}
	}
	if b.Attestations == nil {
		b.Attestations = []map[string]any{}
	}
	if b.Policies == nil {
		b.Policies = []map[string]any{}
	}
	if b.SourceObjects == nil {
		b.SourceObjects = []SourceObject{}
	}
	b.Manifest = BundleManifest{}
	payload, err := json.Marshal(struct {
		Memories, Notes, Sources, SourceVersions, Lineage, Attestations, Policies []map[string]any
		SourceObjects                                                             []SourceObject
	}{b.Memories, b.Notes, b.Sources, b.SourceVersions, b.Lineage, b.Attestations, b.Policies, b.SourceObjects})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	b.Manifest = BundleManifest{Algorithm: "sha256", PayloadSHA256: hex.EncodeToString(sum[:]), Counts: map[string]int{"memories": len(b.Memories), "notes": len(b.Notes), "sources": len(b.Sources), "source_versions": len(b.SourceVersions), "lineage": len(b.Lineage), "attestations": len(b.Attestations), "policies": len(b.Policies), "source_objects": len(b.SourceObjects)}}
	return nil
}
func (b Bundle) VerifyManifest() error {
	copy := b
	expected := b.Manifest
	if err := copy.SealManifest(); err != nil {
		return err
	}
	if expected.Algorithm != "sha256" || expected.PayloadSHA256 != copy.Manifest.PayloadSHA256 || !sameCounts(expected.Counts, copy.Manifest.Counts) {
		return errors.New("portable bundle manifest mismatch")
	}
	return nil
}

func sameCounts(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

type Repository interface {
	Request(context.Context, auth.RequestContext, string, time.Time) (Operation, error)
	Status(context.Context, auth.RequestContext, string) (Operation, error)
	Get(context.Context, auth.RequestContext, string, time.Time) (Operation, string, error)
	ActiveTenantIDs(context.Context) ([]string, error)
	Claim(context.Context, string, time.Time) (*Claimed, error)
	LoadBundle(context.Context, Claimed, time.Time) (Bundle, error)
	Complete(context.Context, Claimed, string, string, time.Time, time.Time) error
	Fail(context.Context, Claimed, string, time.Time) error
}
type ObjectStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
}
type Service struct {
	repository Repository
	objects    ObjectStore
	key        []byte
	now        func() time.Time
}

func NewService(repository Repository, objects ObjectStore, encryptionSecret string, now func() time.Time) (*Service, error) {
	if repository == nil || objects == nil || encryptionSecret == "" {
		return nil, errors.New("export service is not configured")
	}
	if now == nil {
		now = time.Now
	}
	sum := sha256.Sum256([]byte(encryptionSecret))
	return &Service{repository: repository, objects: objects, key: sum[:], now: now}, nil
}
func (s *Service) Request(ctx context.Context, workspaceID string) (Operation, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("tenant:export") {
		return Operation{}, auth.ErrTenantUnavailable
	}
	return s.repository.Request(ctx, request, workspaceID, s.now().UTC())
}
func (s *Service) Status(ctx context.Context, id string) (Operation, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("tenant:export") {
		return Operation{}, auth.ErrTenantUnavailable
	}
	return s.repository.Status(ctx, request, id)
}
func (s *Service) Download(ctx context.Context, id string) ([]byte, Operation, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("tenant:export") {
		return nil, Operation{}, auth.ErrTenantUnavailable
	}
	operation, key, err := s.repository.Get(ctx, request, id, s.now().UTC())
	if err != nil {
		return nil, Operation{}, err
	}
	encrypted, err := s.objects.Get(ctx, key)
	if err != nil {
		return nil, Operation{}, err
	}
	sum := sha256.Sum256(encrypted)
	if hex.EncodeToString(sum[:]) != operation.ChecksumSHA256 {
		return nil, Operation{}, errors.New("export checksum mismatch")
	}
	plain, err := decrypt(s.key, encrypted)
	return plain, operation, err
}
func (s *Service) ProcessOnce(ctx context.Context) (int, error) {
	tenants, err := s.repository.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	completed := 0
	var failures []error
	for _, tenant := range tenants {
		claim, err := s.repository.Claim(ctx, tenant, s.now().UTC())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if claim == nil {
			continue
		}
		bundle, err := s.repository.LoadBundle(ctx, *claim, s.now().UTC())
		if err != nil {
			_ = s.repository.Fail(ctx, *claim, "bundle_failed", s.now().UTC())
			failures = append(failures, err)
			continue
		}
		if err = bundle.SealManifest(); err != nil {
			_ = s.repository.Fail(ctx, *claim, "manifest_failed", s.now().UTC())
			failures = append(failures, err)
			continue
		}
		plain, err := json.Marshal(bundle)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		encrypted, err := encrypt(s.key, plain)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		key := "exports/" + claim.TenantID + "/" + claim.ID + ".json.aesgcm"
		if err := s.objects.Put(ctx, key, encrypted); err != nil {
			_ = s.repository.Fail(ctx, *claim, "object_write_failed", s.now().UTC())
			failures = append(failures, err)
			continue
		}
		sum := sha256.Sum256(encrypted)
		completedAt := s.now().UTC()
		if err := s.repository.Complete(ctx, *claim, key, hex.EncodeToString(sum[:]), completedAt, completedAt.Add(15*time.Minute)); err != nil {
			failures = append(failures, err)
			continue
		}
		completed++
	}
	return completed, errors.Join(failures...)
}
func (s *Service) Run(ctx context.Context, poll time.Duration, report func(error)) {
	if poll <= 0 {
		poll = time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if _, err := s.ProcessOnce(ctx); err != nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func encrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, aead.Seal(nil, nonce, plain, nil)...), nil
}
func decrypt(key, value []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(value) < aead.NonceSize() {
		return nil, errors.New("invalid encrypted export")
	}
	return aead.Open(nil, value[:aead.NonceSize()], value[aead.NonceSize():], nil)
}
