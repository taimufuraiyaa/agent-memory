package core

import (
	"testing"
	"time"
)

func TestEvidenceVerificationValidate(t *testing.T) {
	verification := EvidenceVerification{
		ID:                  "verification-1",
		SubjectID:           "contribution-1",
		CitationID:          "citation-1",
		Verdict:             VerdictSupports,
		Method:              VerificationExactMatch,
		EvidenceFingerprint: "sha256:evidence",
		SubjectFingerprint:  FingerprintText("Practice is not play."),
		VerifierID:          "deterministic:quote-match",
		VerifierVersion:     "v1",
		VerifiedAt:          time.Now().UTC(),
	}
	if err := verification.Validate(); err != nil {
		t.Fatalf("expected verification to be valid: %v", err)
	}

	verification.VerifierVersion = ""
	if err := verification.Validate(); err == nil {
		t.Fatal("expected versionless verification to fail")
	}
}

func TestExactMatchVerificationCannotUseUnsupportedVerdict(t *testing.T) {
	verification := EvidenceVerification{
		ID:                  "verification-1",
		SubjectID:           "contribution-1",
		CitationID:          "citation-1",
		Verdict:             VerdictInsufficient,
		Method:              VerificationExactMatch,
		EvidenceFingerprint: "sha256:evidence",
		SubjectFingerprint:  FingerprintText("quote"),
		VerifierID:          "deterministic:quote-match",
		VerifierVersion:     "v1",
		VerifiedAt:          time.Now().UTC(),
	}
	if err := verification.Validate(); err == nil {
		t.Fatal("expected exact-match verification with insufficient verdict to fail")
	}
}
