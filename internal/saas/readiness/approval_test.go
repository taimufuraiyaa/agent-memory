package readiness

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVerifyApprovalsAcceptsScopedSignatures(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := TrustBundle{Schema: ApprovalTrustSchema, Keys: []TrustedApprover{{KeyID: "security-2026", Owner: "security-review", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"private_beta"}, Controls: []string{"security_review", "privacy_review"}}}}
	approvals := []SignedApproval{
		signApproval(t, private, approvalFixture(now, "security_review", "approved")),
		signApproval(t, private, approvalFixture(now.Add(time.Minute), "privacy_review", "approved")),
	}
	report, err := VerifyApprovals("private_beta", []string{"security_review", "privacy_review"}, bundle, approvals, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.Verified) != 2 || len(report.Missing) != 0 || report.Verified["security_review"].KeyID != "security-2026" {
		t.Fatalf("unexpected approval report: %+v", report)
	}
}

func TestVerifyApprovalsRejectsTamperingAndScopeEscalation(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	bundle := TrustBundle{Schema: ApprovalTrustSchema, Keys: []TrustedApprover{{KeyID: "security-2026", Owner: "security-review", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"private_beta"}, Controls: []string{"security_review"}}}}

	tests := map[string]func(SignedApproval) SignedApproval{
		"tampered evidence": func(value SignedApproval) SignedApproval {
			value.EvidenceSHA256 = strings.Repeat("b", 64)
			return value
		},
		"wrong owner":   func(value SignedApproval) SignedApproval { value.Owner = "legal-review"; return value },
		"wrong control": func(value SignedApproval) SignedApproval { value.Control = "legal_review"; return value },
		"wrong gate":    func(value SignedApproval) SignedApproval { value.Gate = "ga"; return value },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			approval := signApproval(t, private, approvalFixture(now, "security_review", "approved"))
			approval = mutate(approval)
			if _, err := VerifyApprovals("private_beta", []string{"security_review"}, bundle, []SignedApproval{approval}, now.Add(time.Minute)); err == nil {
				t.Fatal("expected altered approval to fail closed")
			}
		})
	}
}

func TestVerifyApprovalsUsesLatestSignedDecision(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	bundle := TrustBundle{Schema: ApprovalTrustSchema, Keys: []TrustedApprover{{KeyID: "operations-2026", Owner: "operations-review", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"ga"}, Controls: []string{"operations_review"}}}}
	approved := approvalFixture(now, "operations_review", "approved")
	approved.Gate, approved.Owner, approved.KeyID = "ga", "operations-review", "operations-2026"
	rejected := approved
	rejected.Decision = "rejected"
	rejected.IssuedAt = now.Add(time.Hour).Format(time.RFC3339Nano)
	rejected.ExpiresAt = now.Add(25 * time.Hour).Format(time.RFC3339Nano)
	report, err := VerifyApprovals("ga", []string{"operations_review"}, bundle, []SignedApproval{signApproval(t, private, approved), signApproval(t, private, rejected)}, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || len(report.Rejected) != 1 || report.Rejected[0] != "operations_review" {
		t.Fatalf("newer rejection must block release: %+v", report)
	}
}

func TestVerifyApprovalsRejectsExpiredFutureAndAmbiguousDecisions(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	bundle := TrustBundle{Schema: ApprovalTrustSchema, Keys: []TrustedApprover{{KeyID: "product-2026", Owner: "product-review", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"ga"}, Controls: []string{"product_review"}}}}
	base := approvalFixture(now, "product_review", "approved")
	base.Gate, base.Owner, base.KeyID = "ga", "product-review", "product-2026"

	expired := base
	expired.ExpiresAt = now.Add(time.Minute).Format(time.RFC3339Nano)
	report, err := VerifyApprovals("ga", []string{"product_review"}, bundle, []SignedApproval{signApproval(t, private, expired)}, now.Add(2*time.Minute))
	if err != nil || report.Ready || len(report.Expired) != 1 {
		t.Fatalf("expired report=%+v err=%v", report, err)
	}

	future := base
	future.IssuedAt = now.Add(10 * time.Minute).Format(time.RFC3339Nano)
	future.ExpiresAt = now.Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := VerifyApprovals("ga", []string{"product_review"}, bundle, []SignedApproval{signApproval(t, private, future)}, now); err == nil {
		t.Fatal("future approval must fail closed")
	}

	one := signApproval(t, private, base)
	two := signApproval(t, private, base)
	if _, err := VerifyApprovals("ga", []string{"product_review"}, bundle, []SignedApproval{one, two}, now.Add(time.Minute)); err == nil {
		t.Fatal("same-time duplicate decisions must fail closed")
	}
}

func TestLoadApprovalFilesRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	bundle := TrustBundle{Schema: ApprovalTrustSchema, Keys: []TrustedApprover{{KeyID: "security-2026", Owner: "security-review", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"private_beta"}, Controls: []string{"security_review"}}}}
	directory := t.TempDir()
	trustPath := directory + "/trust.json"
	writeJSONFile(t, trustPath, bundle)
	loaded, err := LoadTrustBundle(trustPath)
	if err != nil || len(loaded.Keys) != 1 {
		t.Fatalf("trust bundle=%+v err=%v", loaded, err)
	}
	approvalDirectory := directory + "/approvals"
	if err := os.Mkdir(approvalDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	approval := signApproval(t, private, approvalFixture(now, "security_review", "approved"))
	writeJSONFile(t, approvalDirectory+"/security.json", approval)
	approvals, err := LoadApprovals(approvalDirectory)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals=%+v err=%v", approvals, err)
	}

	if err := os.WriteFile(approvalDirectory+"/unknown.json", []byte(`{"schema":"agent-memory-release-approval-v1","unexpected":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovals(approvalDirectory); err == nil {
		t.Fatal("unknown approval fields must fail closed")
	}
	if err := os.Remove(approvalDirectory + "/unknown.json"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(approvalDirectory+"/security.json", approvalDirectory+"/linked.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovals(approvalDirectory); err == nil {
		t.Fatal("approval symlinks must fail closed")
	}
}

func TestApprovalFileIdentityChangeIsRejected(t *testing.T) {
	directory := t.TempDir()
	path := directory + "/approval.json"
	if err := os.WriteFile(path, []byte(`{"schema":"original"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	validated, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":"replacement"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOpenedEvidenceFile(validated, opened); err == nil {
		t.Fatal("a file replaced between path validation and open must fail closed")
	}
}

func TestDecodeStrictJSONFileRejectsPostOpenPathReplacement(t *testing.T) {
	directory := t.TempDir()
	path := directory + "/trust.json"
	if err := os.WriteFile(path, []byte(`{"schema":"original"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Schema string `json:"schema"`
	}
	err := decodeStrictJSONFileWithHook(path, &decoded, func() {
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"schema":"replacement"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("a path replaced after open must fail closed")
	}
}

func TestLoadApprovalsRejectsDirectoryMembershipChange(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	directory := t.TempDir()
	writeJSONFile(t, directory+"/approved.json", signApproval(t, private, approvalFixture(now, "security_review", "approved")))

	_, err := loadApprovalsWithHook(directory, func() {
		newer := approvalFixture(now.Add(time.Minute), "security_review", "rejected")
		writeJSONFile(t, directory+"/rejected.json", signApproval(t, private, newer))
	})
	if err == nil {
		t.Fatal("an approval added after the initial snapshot must fail closed")
	}
}

func approvalFixture(issued time.Time, control, decision string) SignedApproval {
	return SignedApproval{
		Schema: ApprovalArtifactSchema, Gate: "private_beta", Control: control, Decision: decision,
		Owner: "security-review", KeyID: "security-2026", EvidenceRef: "report://security/2026-08",
		EvidenceSHA256: strings.Repeat("a", 64), IssuedAt: issued.Format(time.RFC3339Nano),
		ExpiresAt: issued.Add(24 * time.Hour).Format(time.RFC3339Nano),
	}
}

func signApproval(t *testing.T, private ed25519.PrivateKey, value SignedApproval) SignedApproval {
	t.Helper()
	payload, err := CanonicalApprovalPayload(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
	return value
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}
