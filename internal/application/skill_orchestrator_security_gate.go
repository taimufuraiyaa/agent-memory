package application

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

const SkillSecurityReviewSchemaV1 = "agent-memory/skill-orchestrator-independent-security-review/v1"

type SkillSecurityControlID string

const (
	SkillSecurityRLS                 SkillSecurityControlID = "forced_rls"
	SkillSecurityAuthorization       SkillSecurityControlID = "authorization"
	SkillSecurityFilesystemCustody   SkillSecurityControlID = "filesystem_custody"
	SkillSecurityWorkerPrivilege     SkillSecurityControlID = "worker_privilege"
	SkillSecurityForgedInputs        SkillSecurityControlID = "forged_ids_tokens_signals"
	SkillSecurityTimingBehavior      SkillSecurityControlID = "timing_behavior"
	SkillSecurityPayloadLogRedaction SkillSecurityControlID = "payload_log_redaction"
	SkillSecurityEvaluatorPrivilege  SkillSecurityControlID = "least_privilege_evaluation"
)

var requiredSkillSecurityControls = []SkillSecurityControlID{
	SkillSecurityRLS, SkillSecurityAuthorization, SkillSecurityFilesystemCustody,
	SkillSecurityWorkerPrivilege, SkillSecurityForgedInputs, SkillSecurityTimingBehavior,
	SkillSecurityPayloadLogRedaction, SkillSecurityEvaluatorPrivilege,
}

type SkillSecurityControlEvidence struct {
	ID             SkillSecurityControlID `json:"id"`
	Passed         bool                   `json:"passed"`
	FindingCount   int                    `json:"finding_count"`
	EvidenceDigest string                 `json:"evidence_digest"`
}

type SkillIndependentSecurityReview struct {
	Schema                 string    `json:"schema"`
	Classification         string    `json:"classification"`
	ReviewID               string    `json:"review_id"`
	ReleaseID              string    `json:"release_id"`
	BuildDigest            string    `json:"build_digest"`
	MigrationDigest        string    `json:"migration_digest"`
	IsolationReceiptDigest string    `json:"isolation_receipt_digest"`
	ChaosCertificateDigest string    `json:"chaos_certificate_digest"`
	ReviewerRole           string    `json:"reviewer_role"`
	CompletedAt            time.Time `json:"completed_at"`
	ExpiresAt              time.Time `json:"expires_at"`
	SigningKeyID           string    `json:"signing_key_id"`
	Signature              string    `json:"signature"`
}

type skillIndependentSecurityUnsigned struct {
	Schema                 string    `json:"schema"`
	Classification         string    `json:"classification"`
	ReviewID               string    `json:"review_id"`
	ReleaseID              string    `json:"release_id"`
	BuildDigest            string    `json:"build_digest"`
	MigrationDigest        string    `json:"migration_digest"`
	IsolationReceiptDigest string    `json:"isolation_receipt_digest"`
	ChaosCertificateDigest string    `json:"chaos_certificate_digest"`
	ReviewerRole           string    `json:"reviewer_role"`
	CompletedAt            time.Time `json:"completed_at"`
	ExpiresAt              time.Time `json:"expires_at"`
	SigningKeyID           string    `json:"signing_key_id"`
}

type SkillSecurityGateConfig struct {
	ReleaseID              string
	BuildDigest            string
	MigrationDigest        string
	IsolationReceiptDigest string
	ChaosCertificateDigest string
	TrustedReviewKeys      map[string]ed25519.PublicKey
	MaximumReviewAge       time.Duration
}

type SkillSecurityGateReport struct {
	Ready           bool                           `json:"ready"`
	ReleaseID       string                         `json:"release_id"`
	BuildDigest     string                         `json:"build_digest"`
	MigrationDigest string                         `json:"migration_digest"`
	ReviewID        string                         `json:"review_id"`
	ReviewDigest    string                         `json:"review_digest"`
	Controls        []SkillSecurityControlEvidence `json:"controls"`
	Blockers        []string                       `json:"blockers"`
	VerifiedAt      time.Time                      `json:"verified_at"`
}

func RequiredSkillSecurityControls() []SkillSecurityControlID {
	return append([]SkillSecurityControlID(nil), requiredSkillSecurityControls...)
}

func EvaluateSkillSecurityGate(config SkillSecurityGateConfig, controls []SkillSecurityControlEvidence, review SkillIndependentSecurityReview, now time.Time) (SkillSecurityGateReport, error) {
	if strings.TrimSpace(config.ReleaseID) == "" || !validSHA256Digest(config.BuildDigest) || !validSHA256Digest(config.MigrationDigest) || !validSHA256Digest(config.IsolationReceiptDigest) || !validSHA256Digest(config.ChaosCertificateDigest) || config.MaximumReviewAge <= 0 || config.MaximumReviewAge > 90*24*time.Hour || now.IsZero() {
		return SkillSecurityGateReport{}, errors.New("skill security gate configuration is invalid")
	}
	report := SkillSecurityGateReport{
		ReleaseID: config.ReleaseID, BuildDigest: config.BuildDigest, MigrationDigest: config.MigrationDigest,
		ReviewID: review.ReviewID, Controls: append([]SkillSecurityControlEvidence(nil), controls...),
		Blockers: []string{}, VerifiedAt: now.UTC(),
	}
	sort.Slice(report.Controls, func(i, j int) bool { return report.Controls[i].ID < report.Controls[j].ID })
	if err := validateSkillSecurityControls(report.Controls); err != nil {
		report.Blockers = append(report.Blockers, "local_security_controls_unready")
	}
	unsigned := skillSecurityUnsigned(review)
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return SkillSecurityGateReport{}, err
	}
	digest := sha256.Sum256(payload)
	report.ReviewDigest = "sha256:" + hex.EncodeToString(digest[:])
	if review.Schema != SkillSecurityReviewSchemaV1 || review.Classification != "independent_external" || review.ReviewerRole != "independent_security" || strings.TrimSpace(review.ReviewID) == "" {
		report.Blockers = append(report.Blockers, "independent_review_identity_invalid")
	}
	if review.ReleaseID != config.ReleaseID || review.BuildDigest != config.BuildDigest || review.MigrationDigest != config.MigrationDigest || review.IsolationReceiptDigest != config.IsolationReceiptDigest || review.ChaosCertificateDigest != config.ChaosCertificateDigest {
		report.Blockers = append(report.Blockers, "independent_review_binding_mismatch")
	}
	if review.CompletedAt.IsZero() || review.ExpiresAt.IsZero() || review.ExpiresAt.Before(review.CompletedAt) || review.CompletedAt.After(now) || now.Sub(review.CompletedAt) > config.MaximumReviewAge || !now.Before(review.ExpiresAt) {
		report.Blockers = append(report.Blockers, "independent_review_stale")
	}
	publicKey, trusted := config.TrustedReviewKeys[review.SigningKeyID]
	signature, decodeErr := base64.StdEncoding.DecodeString(review.Signature)
	if !trusted || len(publicKey) != ed25519.PublicKeySize || decodeErr != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, payload, signature) {
		report.Blockers = append(report.Blockers, "independent_review_signature_invalid")
	}
	report.Ready = len(report.Blockers) == 0
	return report, nil
}

func validateSkillSecurityControls(controls []SkillSecurityControlEvidence) error {
	if len(controls) != len(requiredSkillSecurityControls) {
		return errors.New("skill security controls are incomplete")
	}
	required := make(map[SkillSecurityControlID]struct{}, len(requiredSkillSecurityControls))
	for _, id := range requiredSkillSecurityControls {
		required[id] = struct{}{}
	}
	for _, control := range controls {
		if _, ok := required[control.ID]; !ok || !control.Passed || control.FindingCount != 0 || !validSHA256Digest(control.EvidenceDigest) {
			return errors.New("skill security control is unsafe or duplicated")
		}
		delete(required, control.ID)
	}
	if len(required) != 0 {
		return errors.New("skill security controls are incomplete")
	}
	return nil
}

func skillSecurityUnsigned(review SkillIndependentSecurityReview) skillIndependentSecurityUnsigned {
	return skillIndependentSecurityUnsigned{
		Schema: review.Schema, Classification: review.Classification, ReviewID: review.ReviewID,
		ReleaseID: review.ReleaseID, BuildDigest: review.BuildDigest, MigrationDigest: review.MigrationDigest,
		IsolationReceiptDigest: review.IsolationReceiptDigest, ChaosCertificateDigest: review.ChaosCertificateDigest,
		ReviewerRole: review.ReviewerRole, CompletedAt: review.CompletedAt.UTC(), ExpiresAt: review.ExpiresAt.UTC(),
		SigningKeyID: review.SigningKeyID,
	}
}
