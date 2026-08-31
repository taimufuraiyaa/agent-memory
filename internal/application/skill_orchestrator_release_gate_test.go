package application

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillOrchestratorReleaseGateAcceptsCompleteSignedRollout(t *testing.T) {
	fixture := newSkillReleaseFixture(t)
	report, err := EvaluateSkillOrchestratorReleaseGate(fixture.config, fixture.evidence, fixture.approval, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.Blockers) != 0 || report.ApprovalDigest == "" || report.EvidenceDigest == "" {
		t.Fatalf("report = %+v", report)
	}
}

func TestSkillOrchestratorReleaseGateFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*skillReleaseFixture)
		blocker string
	}{
		{"rollout_order", func(f *skillReleaseFixture) { f.evidence.Rollout[1].Mode = core.SkillOrchestratorManual }, "rollout_sequence_invalid"},
		{"unsigned_configuration", func(f *skillReleaseFixture) { f.evidence.Rollout[4].ConfigurationSignatureValid = false }, "rollout_sequence_invalid"},
		{"single_drill", func(f *skillReleaseFixture) { f.evidence.Drills = f.evidence.Drills[:4] }, "staging_drills_incomplete"},
		{"active_skill_changed", func(f *skillReleaseFixture) { f.evidence.Drills[0].ActiveSkillDigestAfter = digestFor("other-skill") }, "staging_drills_incomplete"},
		{"audit_lost", func(f *skillReleaseFixture) { f.evidence.Drills[0].AuditRecordsAfter = 1 }, "staging_drills_incomplete"},
		{"rollback_slow", func(f *skillReleaseFixture) { f.evidence.Drills[1].RollbackMillis = 1001 }, "staging_drills_incomplete"},
		{"alert_unrouted", func(f *skillReleaseFixture) { f.evidence.Drills[2].AlertsRouted = false }, "staging_drills_incomplete"},
		{"binding_mismatch", func(f *skillReleaseFixture) {
			f.approval.BuildDigest = digestFor("other-build")
			signSkillReleaseApproval(t, f)
		}, "product_approval_binding_mismatch"},
		{"self_approval", func(f *skillReleaseFixture) {
			f.approval.ApproverID = f.config.ReleaseSignerID
			signSkillReleaseApproval(t, f)
		}, "separation_of_duty_invalid"},
		{"tampered_signature", func(f *skillReleaseFixture) {
			f.approval.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}, "product_approval_signature_invalid"},
		{"expired", func(f *skillReleaseFixture) { f.now = f.approval.ExpiresAt }, "product_approval_stale"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSkillReleaseFixture(t)
			test.mutate(fixture)
			report, err := EvaluateSkillOrchestratorReleaseGate(fixture.config, fixture.evidence, fixture.approval, fixture.now)
			if err != nil {
				t.Fatal(err)
			}
			if report.Ready || !containsString(report.Blockers, test.blocker) {
				t.Fatalf("report = %+v, want blocker %q", report, test.blocker)
			}
		})
	}
}

func TestSkillOrchestratorReleaseEvidenceRejectsContentBearingFields(t *testing.T) {
	fixture := newSkillReleaseFixture(t)
	fixture.evidence.Drills[0].RunbookID = strings.Repeat("x", 257)
	if _, err := EvaluateSkillOrchestratorReleaseGate(fixture.config, fixture.evidence, fixture.approval, fixture.now); err == nil {
		t.Fatal("unbounded evidence was accepted")
	}
}

func TestSignSkillProductionReleaseEvidenceAndProductApproval(t *testing.T) {
	fixture := newSkillReleaseFixture(t)
	fixture.evidence.Signature = ""
	signedEvidence, err := SignSkillProductionReleaseEvidence(fixture.evidence, fixture.releasePrivate)
	if err != nil || signedEvidence.Signature == "" {
		t.Fatalf("signed evidence=%+v err=%v", signedEvidence, err)
	}
	fixture.approval.Signature = ""
	signedApproval, err := SignSkillProductApproval(fixture.approval, fixture.approvalPrivate)
	if err != nil || signedApproval.Signature == "" {
		t.Fatalf("signed approval=%+v err=%v", signedApproval, err)
	}
	fixture.evidence, fixture.approval = signedEvidence, signedApproval
	report, err := EvaluateSkillOrchestratorReleaseGate(fixture.config, fixture.evidence, fixture.approval, fixture.now)
	if err != nil || !report.Ready {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestSkillOrchestratorConfigurationReceiptVerifiesExactSignedConfiguration(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	configuration := newSkillConfigurationFixture(t, core.SkillOrchestratorShadow).change.Configuration
	receipt, err := SignSkillOrchestratorConfigurationReceipt(SkillOrchestratorConfigurationReceipt{
		Schema: SkillOrchestratorConfigurationReceiptSchemaV1, ReceiptID: "configuration-shadow-v1",
		ReleaseID: "release-33", BuildDigest: digestFor("build"), MigrationDigest: digestFor("migration"),
		Configuration: configuration, SignerID: "release-signer", SignedAt: configuration.CreatedAt.Add(time.Minute), SigningKeyID: "release-key",
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySkillOrchestratorConfigurationReceipt(receipt, map[string]ed25519.PublicKey{"release-key": publicKey}); err != nil {
		t.Fatal(err)
	}
	tampered := receipt
	tampered.Configuration.Mode = core.SkillOrchestratorManual
	if err := VerifySkillOrchestratorConfigurationReceipt(tampered, map[string]ed25519.PublicKey{"release-key": publicKey}); err == nil {
		t.Fatal("tampered staged configuration receipt was accepted")
	}
}

type skillReleaseFixture struct {
	now             time.Time
	config          SkillReleaseGateConfig
	evidence        SkillProductionReleaseEvidence
	approval        SkillProductApproval
	releasePrivate  ed25519.PrivateKey
	approvalPrivate ed25519.PrivateKey
}

func newSkillReleaseFixture(t *testing.T) *skillReleaseFixture {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	approvalPublic, approvalPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	releasePublic, releasePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	modes := []core.SkillOrchestratorMode{
		core.SkillOrchestratorDisabled, core.SkillOrchestratorShadow, core.SkillOrchestratorManual,
		core.SkillOrchestratorCanary, core.SkillOrchestratorAutomaticLowRisk,
	}
	rollout := make([]SkillRolloutObservation, 0, len(modes))
	for index, mode := range modes {
		rollout = append(rollout, SkillRolloutObservation{
			Sequence: index + 1, Mode: mode, ConfigurationDigest: digestFor(string(mode)),
			ConfigurationSignatureValid: true, Passed: true,
		})
	}
	drills := make([]SkillOperationalDrill, 0, 8)
	for iteration := 1; iteration <= 2; iteration++ {
		for _, operation := range RequiredSkillReleaseDrillOperations() {
			drills = append(drills, SkillOperationalDrill{
				Iteration: iteration, Operation: operation, Passed: true,
				ActiveSkillDigestBefore: digestFor("active-skill"), ActiveSkillDigestAfter: digestFor("active-skill"),
				AuditRecordsBefore: 10, AuditRecordsAfter: 12, RollbackMillis: 500,
				AlertsRouted: true, RunbookID: "skill-revision-lifecycle", RunbookDigest: digestFor("runbook"),
			})
		}
	}
	evidence := SkillProductionReleaseEvidence{
		Schema: SkillProductionReleaseEvidenceSchemaV1, ReleaseID: "release-33",
		BuildDigest: digestFor("build"), MigrationDigest: digestFor("migration"), PolicyDigest: digestFor("policy"),
		Rollout: rollout, Drills: drills, RollbackSLOMillis: 1000,
		StandaloneReportDigest: digestFor("standalone"), HostedReportDigest: digestFor("hosted"),
		ChaosCertificateDigest: digestFor("chaos"), SecurityReportDigest: digestFor("security"),
		CapacityReportDigest: digestFor("capacity"), MigrationReportDigest: digestFor("migration-report"),
		AlertRoutingDigest: digestFor("alerts"), GeneratedAt: now.Add(-time.Hour),
		SigningKeyID: "release-key",
	}
	fixture := &skillReleaseFixture{
		now: now, evidence: evidence, releasePrivate: releasePrivate, approvalPrivate: approvalPrivate,
		config: SkillReleaseGateConfig{
			ReleaseID: evidence.ReleaseID, BuildDigest: evidence.BuildDigest, MigrationDigest: evidence.MigrationDigest,
			PolicyDigest: evidence.PolicyDigest, ReleaseSignerID: "release-signer",
			TrustedReleaseKeys: map[string]ed25519.PublicKey{"release-key": releasePublic},
			TrustedProductKeys: map[string]ed25519.PublicKey{"product-key": approvalPublic}, MaximumApprovalAge: 30 * 24 * time.Hour,
		},
		approval: SkillProductApproval{
			Schema: SkillProductApprovalSchemaV1, ApprovalID: "product-approval-33", ReleaseID: evidence.ReleaseID,
			BuildDigest: evidence.BuildDigest, MigrationDigest: evidence.MigrationDigest, PolicyDigest: evidence.PolicyDigest,
			ApproverID: "accountable-product-owner", ApproverRole: "accountable_product",
			RiskClassesApproved: true, ThresholdsApproved: true, CanaryPolicyApproved: true,
			RetryDeadLetterApproved: true, BudgetsApproved: true, RetentionApproved: true,
			SLOsApproved: true, AutomaticLowRiskApproved: true, ApprovedAt: now.Add(-30 * time.Minute),
			ExpiresAt: now.Add(7 * 24 * time.Hour), SigningKeyID: "product-key",
		},
	}
	signSkillReleaseEvidence(t, fixture)
	signSkillReleaseApproval(t, fixture)
	return fixture
}

func signSkillReleaseEvidence(t *testing.T, fixture *skillReleaseFixture) {
	t.Helper()
	payload, err := json.Marshal(skillProductionReleaseEvidenceUnsigned(fixture.evidence))
	if err != nil {
		t.Fatal(err)
	}
	fixture.evidence.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.releasePrivate, payload))
}

func signSkillReleaseApproval(t *testing.T, fixture *skillReleaseFixture) {
	t.Helper()
	payload, err := json.Marshal(skillProductApprovalUnsigned(fixture.approval))
	if err != nil {
		t.Fatal(err)
	}
	fixture.approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.approvalPrivate, payload))
}

func digestFor(value string) string {
	return "sha256:" + strings.Repeat(string("0123456789abcdef"[len(value)%16]), 64)
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
