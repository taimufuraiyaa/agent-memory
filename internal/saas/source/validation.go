package source

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

type ValidationClaim struct {
	UploadClaim
	Filename string
}
type ValidationRepository interface {
	ActiveTenantIDs(context.Context) ([]string, error)
	ClaimValidation(context.Context, string, time.Time) (*ValidationClaim, error)
	Promote(context.Context, ValidationClaim, string, time.Time) error
	Reject(context.Context, ValidationClaim, string, time.Time) error
	ExpireGrants(context.Context, string, time.Time) ([]string, error)
	ConfirmQuarantineDeleted(context.Context, string, string) error
}
type ValidationObjects interface {
	Get(context.Context, string) ([]byte, error)
	PutVault(context.Context, string, []byte) error
	Delete(context.Context, string) error
}
type VaultReferenceRepository interface {
	ActiveTenantIDs(context.Context) ([]string, error)
	VaultReferences(context.Context, string) (map[string]int, error)
}
type VaultInventory interface {
	ListVault(context.Context, string) ([]string, error)
}
type VaultFinding struct {
	TenantID   string `json:"tenant_id"`
	ObjectKey  string `json:"object_key"`
	Kind       string `json:"kind"`
	References int    `json:"references"`
}
type VaultReconciler struct {
	repository VaultReferenceRepository
	objects    VaultInventory
}

func NewVaultReconciler(repository VaultReferenceRepository, objects VaultInventory) *VaultReconciler {
	return &VaultReconciler{repository: repository, objects: objects}
}
func (r *VaultReconciler) RunOnce(ctx context.Context) ([]VaultFinding, error) {
	tenants, err := r.repository.ActiveTenantIDs(ctx)
	if err != nil {
		return nil, err
	}
	findings := []VaultFinding{}
	for _, tenant := range tenants {
		references, err := r.repository.VaultReferences(ctx, tenant)
		if err != nil {
			return findings, err
		}
		objects, err := r.objects.ListVault(ctx, "vault/"+tenant+"/")
		if err != nil {
			return findings, err
		}
		seen := map[string]struct{}{}
		for _, key := range objects {
			seen[key] = struct{}{}
			count := references[key]
			if count == 0 {
				findings = append(findings, VaultFinding{TenantID: tenant, ObjectKey: key, Kind: "orphan"})
			}
		}
		for key, count := range references {
			if count > 1 {
				findings = append(findings, VaultFinding{TenantID: tenant, ObjectKey: key, Kind: "multiply_referenced", References: count})
			}
			if _, ok := seen[key]; !ok {
				findings = append(findings, VaultFinding{TenantID: tenant, ObjectKey: key, Kind: "missing", References: count})
			}
		}
	}
	return findings, nil
}
func (r *VaultReconciler) Run(ctx context.Context, poll time.Duration, report func([]VaultFinding, error)) {
	if poll <= 0 {
		poll = time.Minute
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		findings, err := r.RunOnce(ctx)
		if report != nil && (err != nil || len(findings) > 0) {
			report(findings, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type Processor struct {
	repository ValidationRepository
	objects    ValidationObjects
	key        []byte
	now        func() time.Time
}

func NewProcessor(repository ValidationRepository, objects ValidationObjects, encryptionSecret string, now func() time.Time) (*Processor, error) {
	if repository == nil || objects == nil || encryptionSecret == "" {
		return nil, errors.New("source validation processor is not configured")
	}
	if now == nil {
		now = time.Now
	}
	sum := sha256.Sum256([]byte(encryptionSecret))
	return &Processor{repository: repository, objects: objects, key: sum[:], now: now}, nil
}
func (p *Processor) ProcessOnce(ctx context.Context) (int, error) {
	tenants, err := p.repository.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	promoted := 0
	var failures []error
	for _, tenant := range tenants {
		expired, err := p.repository.ExpireGrants(ctx, tenant, p.now().UTC())
		if err != nil {
			failures = append(failures, err)
		}
		for _, key := range expired {
			if err := p.objects.Delete(ctx, key); err != nil {
				failures = append(failures, err)
			}
		}
		claim, err := p.repository.ClaimValidation(ctx, tenant, p.now().UTC())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if claim == nil {
			continue
		}
		value, err := p.objects.Get(ctx, claim.ObjectKey)
		if err != nil {
			_ = p.repository.Reject(ctx, *claim, "quarantine_read_failed", p.now().UTC())
			failures = append(failures, err)
			continue
		}
		code := validateContent(*claim, value)
		if code != "" {
			_ = p.objects.Delete(ctx, claim.ObjectKey)
			if err := p.repository.Reject(ctx, *claim, code, p.now().UTC()); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		encrypted, err := encryptVault(p.key, value)
		if err != nil {
			_ = p.repository.Reject(ctx, *claim, "encryption_failed", p.now().UTC())
			failures = append(failures, err)
			continue
		}
		vaultKey := "vault/" + claim.TenantID + "/" + claim.SourceID + "/1.aesgcm"
		if err := p.objects.PutVault(ctx, vaultKey, encrypted); err != nil {
			_ = p.repository.Reject(ctx, *claim, "vault_write_failed", p.now().UTC())
			failures = append(failures, err)
			continue
		}
		if err := p.repository.Promote(ctx, *claim, vaultKey, p.now().UTC()); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := p.objects.Delete(ctx, claim.ObjectKey); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := p.repository.ConfirmQuarantineDeleted(ctx, claim.TenantID, claim.GrantID); err != nil {
			failures = append(failures, err)
			continue
		}
		promoted++
	}
	return promoted, errors.Join(failures...)
}
func (p *Processor) Run(ctx context.Context, poll time.Duration, report func(error)) {
	if poll <= 0 {
		poll = time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if _, err := p.ProcessOnce(ctx); err != nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func validateContent(claim ValidationClaim, value []byte) string {
	if int64(len(value)) != claim.ExpectedSize {
		return "size_mismatch"
	}
	sum := sha256.Sum256(value)
	if hex.EncodeToString(sum[:]) != claim.Checksum {
		return "checksum_mismatch"
	}
	if bytes.Contains(bytes.ToUpper(value), []byte("EICAR-STANDARD-ANTIVIRUS-TEST-FILE")) {
		return "malware_detected"
	}
	switch claim.MediaType {
	case "application/pdf":
		if !bytes.HasPrefix(value, []byte("%PDF-")) {
			return "signature_mismatch"
		}
	case "application/epub+zip":
		if !bytes.HasPrefix(value, []byte{'P', 'K', 3, 4}) {
			return "signature_mismatch"
		}
	case "text/markdown", "text/plain":
		if !utf8.Valid(value) || strings.IndexByte(string(value), 0) >= 0 {
			return "text_invalid"
		}
	default:
		return "unsupported_media_type"
	}
	return ""
}
func encryptVault(key, plain []byte) ([]byte, error) {
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
