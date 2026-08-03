package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type VerificationVerdict string

const (
	VerdictSupports     VerificationVerdict = "supports"
	VerdictPartial      VerificationVerdict = "partial"
	VerdictChallenges   VerificationVerdict = "challenges"
	VerdictContradicts  VerificationVerdict = "contradicts"
	VerdictInsufficient VerificationVerdict = "insufficient"
)

type VerificationMethod string

const (
	VerificationExactMatch VerificationMethod = "exact_match"
	VerificationEntailment VerificationMethod = "entailment"
	VerificationHuman      VerificationMethod = "human_review"
)

type EvidenceVerification struct {
	ID                  string              `json:"id"`
	SubjectID           string              `json:"subject_id"`
	CitationID          string              `json:"citation_id"`
	Verdict             VerificationVerdict `json:"verdict"`
	Method              VerificationMethod  `json:"method"`
	EvidenceFingerprint string              `json:"evidence_fingerprint"`
	SubjectFingerprint  string              `json:"subject_fingerprint"`
	VerifierID          string              `json:"verifier_id"`
	VerifierVersion     string              `json:"verifier_version"`
	VerifiedAt          time.Time           `json:"verified_at"`
}

func (v EvidenceVerification) Validate() error {
	if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.SubjectID) == "" || strings.TrimSpace(v.CitationID) == "" {
		return errors.New("verification requires id, subject, and citation")
	}
	switch v.Verdict {
	case VerdictSupports, VerdictPartial, VerdictChallenges, VerdictContradicts, VerdictInsufficient:
	default:
		return errors.New("invalid verification verdict")
	}
	switch v.Method {
	case VerificationExactMatch, VerificationEntailment, VerificationHuman:
	default:
		return errors.New("invalid verification method")
	}
	if v.Method == VerificationExactMatch && v.Verdict != VerdictSupports {
		return errors.New("exact match verification must use supports verdict")
	}
	if strings.TrimSpace(v.EvidenceFingerprint) == "" || strings.TrimSpace(v.SubjectFingerprint) == "" {
		return errors.New("verification fingerprints are required")
	}
	if strings.TrimSpace(v.VerifierID) == "" || strings.TrimSpace(v.VerifierVersion) == "" || v.VerifiedAt.IsZero() {
		return errors.New("verification identity, version, and time are required")
	}
	return nil
}

func FingerprintText(text string) string {
	digest := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(digest[:])
}
