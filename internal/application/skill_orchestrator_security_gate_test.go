package application

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSkillSecurityGateRequiresCompleteLocalAndIndependentEvidence(t *testing.T) {
	now := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	config, controls, review := validSkillSecurityGateFixture(t, now)
	report, err := EvaluateSkillSecurityGate(config, controls, review, now)
	if err != nil || !report.Ready || len(report.Blockers) != 0 || report.ReviewDigest == "" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestSkillSecurityGateFailsClosedForLocalEvidenceAndExternalBinding(t *testing.T) {
	now := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*SkillSecurityGateConfig, *[]SkillSecurityControlEvidence, *SkillIndependentSecurityReview)
	}{
		{"missing_control", func(_ *SkillSecurityGateConfig, controls *[]SkillSecurityControlEvidence, _ *SkillIndependentSecurityReview) {
			*controls = (*controls)[:7]
		}},
		{"finding", func(_ *SkillSecurityGateConfig, controls *[]SkillSecurityControlEvidence, _ *SkillIndependentSecurityReview) {
			(*controls)[0].FindingCount = 1
		}},
		{"build_mismatch", func(_ *SkillSecurityGateConfig, _ *[]SkillSecurityControlEvidence, review *SkillIndependentSecurityReview) {
			review.BuildDigest = "sha256:" + strings.Repeat("9", 64)
		}},
		{"expired", func(_ *SkillSecurityGateConfig, _ *[]SkillSecurityControlEvidence, review *SkillIndependentSecurityReview) {
			review.ExpiresAt = now.Add(-time.Minute)
		}},
		{"wrong_role", func(_ *SkillSecurityGateConfig, _ *[]SkillSecurityControlEvidence, review *SkillIndependentSecurityReview) {
			review.ReviewerRole = "product_team"
		}},
		{"tampered_signature", func(_ *SkillSecurityGateConfig, _ *[]SkillSecurityControlEvidence, review *SkillIndependentSecurityReview) {
			review.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, controls, review := validSkillSecurityGateFixture(t, now)
			test.mutate(&config, &controls, &review)
			report, err := EvaluateSkillSecurityGate(config, controls, review, now)
			if err != nil {
				t.Fatal(err)
			}
			if report.Ready || len(report.Blockers) == 0 {
				t.Fatalf("unsafe evidence passed: %+v", report)
			}
		})
	}
}

func validSkillSecurityGateFixture(t *testing.T, now time.Time) (SkillSecurityGateConfig, []SkillSecurityControlEvidence, SkillIndependentSecurityReview) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	config := SkillSecurityGateConfig{
		ReleaseID: "release-1", BuildDigest: digest("a"), MigrationDigest: digest("b"),
		IsolationReceiptDigest: digest("c"), ChaosCertificateDigest: digest("d"),
		TrustedReviewKeys: map[string]ed25519.PublicKey{"independent-key": publicKey}, MaximumReviewAge: 14 * 24 * time.Hour,
	}
	controls := make([]SkillSecurityControlEvidence, 0, len(RequiredSkillSecurityControls()))
	for _, id := range RequiredSkillSecurityControls() {
		controls = append(controls, SkillSecurityControlEvidence{ID: id, Passed: true, EvidenceDigest: digest("e")})
	}
	review := SkillIndependentSecurityReview{
		Schema: SkillSecurityReviewSchemaV1, Classification: "independent_external", ReviewID: "review-1",
		ReleaseID: config.ReleaseID, BuildDigest: config.BuildDigest, MigrationDigest: config.MigrationDigest,
		IsolationReceiptDigest: config.IsolationReceiptDigest, ChaosCertificateDigest: config.ChaosCertificateDigest,
		ReviewerRole: "independent_security", CompletedAt: now.Add(-time.Hour), ExpiresAt: now.Add(7 * 24 * time.Hour),
		SigningKeyID: "independent-key",
	}
	payload, err := json.Marshal(skillSecurityUnsigned(review))
	if err != nil {
		t.Fatal(err)
	}
	review.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return config, controls, review
}
