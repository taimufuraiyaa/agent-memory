package approvalexportevidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/alertevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/blockerevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/capacityevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/securityclosureevidence"
)

func TestBuildPrivateBetaVerifiesFiveCurrentCommonBundleApprovals(t *testing.T) {
	fixture := privateBetaFixture(t, false, "")
	receipt, err := buildPrivateBeta(fixture.security, fixture.securityDigest, fixture.alert, fixture.alertDigest, fixture.blocker, fixture.blockerDigest, fixture.capacity, fixture.capacityDigest, fixture.trustDigest, fixture.bundle, fixture.approvals, fixture.manifest, fixture.manifestDigest, fixture.exportDigest, len(fixture.approvals), fixture.input, fixture.inputDigest, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.RequiredControlCount != 5 || receipt.VerifiedControlCount != 5 || receipt.ApprovalArtifactCount != 5 || receipt.CheckCount != 9 || receipt.PassedCount != 9 || receipt.MinimumExpirySeconds <= 0 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestBuildPrivateBetaRejectsReceiptAndReviewedEvidenceSubstitution(t *testing.T) {
	fixture := privateBetaFixture(t, false, "")
	fixture.input.SecurityClosureReceiptSHA256 = digestPB("9")
	if _, err := buildPrivateBeta(fixture.security, fixture.securityDigest, fixture.alert, fixture.alertDigest, fixture.blocker, fixture.blockerDigest, fixture.capacity, fixture.capacityDigest, fixture.trustDigest, fixture.bundle, fixture.approvals, fixture.manifest, fixture.manifestDigest, fixture.exportDigest, len(fixture.approvals), fixture.input, fixture.inputDigest, fixture.now); err == nil {
		t.Fatal("substituted prerequisite accepted")
	}

	fixture = privateBetaFixture(t, true, "")
	if _, err := buildPrivateBeta(fixture.security, fixture.securityDigest, fixture.alert, fixture.alertDigest, fixture.blocker, fixture.blockerDigest, fixture.capacity, fixture.capacityDigest, fixture.trustDigest, fixture.bundle, fixture.approvals, fixture.manifest, fixture.manifestDigest, fixture.exportDigest, len(fixture.approvals), fixture.input, fixture.inputDigest, fixture.now); err == nil {
		t.Fatal("substituted reviewed evidence accepted")
	}
}

func TestBuildPrivateBetaPreservesExpiredApprovalAsValidUnready(t *testing.T) {
	fixture := privateBetaFixture(t, false, "expired")
	receipt, err := buildPrivateBeta(fixture.security, fixture.securityDigest, fixture.alert, fixture.alertDigest, fixture.blocker, fixture.blockerDigest, fixture.capacity, fixture.capacityDigest, fixture.trustDigest, fixture.bundle, fixture.approvals, fixture.manifest, fixture.manifestDigest, fixture.exportDigest, len(fixture.approvals), fixture.input, fixture.inputDigest, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.ExpiredControlCount != 1 || receipt.FailedCount != 2 || receipt.InconclusiveCount != 1 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestCollectPrivateBetaBindsExactFilesAndCompleteExport(t *testing.T) {
	fixture := privateBetaFixture(t, false, "")
	securityPath := writeJSON(t, t.TempDir()+"/security.json", fixture.security)
	fixture.securityDigest = fileDigestPB(t, securityPath)
	alertPath := writeJSON(t, t.TempDir()+"/alert.json", fixture.alert)
	fixture.alertDigest = fileDigestPB(t, alertPath)
	blockerPath := writeJSON(t, t.TempDir()+"/blocker.json", fixture.blocker)
	fixture.blockerDigest = fileDigestPB(t, blockerPath)
	capacityPath := writeJSON(t, t.TempDir()+"/capacity.json", fixture.capacity)
	fixture.capacityDigest = fileDigestPB(t, capacityPath)
	trustPath := writeJSON(t, t.TempDir()+"/trust.json", fixture.bundle)
	fixture.trustDigest = fileDigestPB(t, trustPath)
	bundleDigest, err := PrivateBetaEvidenceBundleDigest(fixture.securityDigest, fixture.alertDigest, fixture.blockerDigest, fixture.capacityDigest, fixture.input.SupportingEvidenceManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	approvalsDirectory := t.TempDir()
	files := make([]ManifestFile, 0, 5)
	for index, approval := range fixture.approvals {
		approval.EvidenceSHA256 = bundleDigest
		payload, err := readiness.CanonicalApprovalPayload(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey, payload))
		name := "approval-" + string(rune('a'+index)) + ".json"
		path := writeJSON(t, approvalsDirectory+"/"+name, approval)
		files = append(files, ManifestFile{Name: name, SHA256: fileDigestPB(t, path)})
	}
	fixture.manifest.Files = files
	manifestPath := writeJSON(t, t.TempDir()+"/manifest.json", fixture.manifest)
	exportDigest, _, err := verifyExportFor(approvalsDirectory, fixture.manifest, PrivateBetaManifestSchemaV1, "private_beta")
	if err != nil {
		t.Fatal(err)
	}
	fixture.input.SecurityClosureReceiptSHA256 = fixture.securityDigest
	fixture.input.AlertReceiptSHA256 = fixture.alertDigest
	fixture.input.BlockerReceiptSHA256 = fixture.blockerDigest
	fixture.input.CapacityReceiptSHA256 = fixture.capacityDigest
	fixture.input.PrivateBetaEvidenceBundleSHA256 = bundleDigest
	fixture.input.TrustBundleSHA256 = fixture.trustDigest
	fixture.input.ApprovalExportSHA256 = exportDigest
	inputPath := writeJSON(t, t.TempDir()+"/input.json", fixture.input)
	receipt, err := CollectPrivateBeta(securityPath, alertPath, blockerPath, capacityPath, trustPath, approvalsDirectory, manifestPath, inputPath, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.ApprovalArtifactCount != 5 {
		t.Fatalf("receipt=%+v", receipt)
	}
	writeJSON(t, approvalsDirectory+"/extra.json", map[string]string{"extra": "artifact"})
	if _, err := CollectPrivateBeta(securityPath, alertPath, blockerPath, capacityPath, trustPath, approvalsDirectory, manifestPath, inputPath, fixture.now); err == nil {
		t.Fatal("undeclared export artifact accepted")
	}
}

type privateBetaTestFixture struct {
	now                                                        time.Time
	security                                                   securityclosureevidence.Receipt
	alert                                                      alertevidence.Receipt
	blocker                                                    blockerevidence.Receipt
	capacity                                                   capacityevidence.Receipt
	securityDigest, alertDigest, blockerDigest, capacityDigest string
	trustDigest, manifestDigest, exportDigest, inputDigest     string
	bundle                                                     readiness.TrustBundle
	privateKey                                                 ed25519.PrivateKey
	approvals                                                  []readiness.SignedApproval
	manifest                                                   ExportManifest
	input                                                      PrivateBetaInput
}

func privateBetaFixture(t *testing.T, substitute bool, state string) privateBetaTestFixture {
	t.Helper()
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	common := digestPB("a")
	security := securityclosureevidence.Receipt{Input: securityclosureevidence.Input{Schema: securityclosureevidence.ReceiptSchemaV1, Classification: "staging_external", Environment: "staging", ReviewID: "security-review", InventoryID: "inventory", InventoryReceiptSHA256: common, PlanID: "plan", PlanReceiptSHA256: common, ChangeID: "change", ChangeReceiptSHA256: common, ReleaseID: "release", ReleaseReceiptSHA256: common, Ready: true}, Schema: securityclosureevidence.ReceiptSchemaV1, CollectedAt: now.Add(-5 * time.Hour), CoverageComplete: true, SourceCount: 4, CheckCount: 7, PassedCount: 7}
	alert := alertevidence.Receipt{Schema: alertevidence.ReceiptSchemaV1, Classification: "staging_external", Environment: "staging", BundleID: "alert-bundle", InventoryID: "inventory", InventoryReceiptSHA256: common, PlanID: "plan", PlanReceiptSHA256: common, ChangeID: "change", ChangeReceiptSHA256: common, ReleaseID: "release", ReleaseReceiptSHA256: common, Ready: true, AlertCount: 7, PassedCount: 7, CollectedAt: now.Add(-4 * time.Hour)}
	blocker := blockerevidence.Receipt{Input: blockerevidence.Input{Schema: blockerevidence.ReceiptSchemaV1, Classification: "staging_external", Environment: "staging", ReviewID: "blocker-review", InventoryID: "inventory", InventoryReceiptSHA256: common, PlanID: "plan", PlanReceiptSHA256: common, ChangeID: "change", ChangeReceiptSHA256: common, ReleaseID: "release", ReleaseReceiptSHA256: common, Ready: true}, Schema: blockerevidence.ReceiptSchemaV1, CollectedAt: now.Add(-3 * time.Hour), ReviewCoverageComplete: true, CheckCount: 5, PassedCount: 5}
	capacity := capacityevidence.Receipt{Input: capacityevidence.Input{Schema: capacityevidence.ReceiptSchemaV1, Classification: "staging_external", Environment: "staging", AssessmentID: "capacity-assessment", InventoryID: "inventory", InventoryReceiptSHA256: common, PlanID: "plan", PlanReceiptSHA256: common, ChangeID: "change", ChangeReceiptSHA256: common, ReleaseID: "release", ReleaseReceiptSHA256: common, FixedMonthlyCostMicroUSD: 100, VariableMonthlyCostPerTenantMicroUSD: 10, BetaAccountCap: 10, EstimatedWorstCaseMonthlyCostMicroUSD: 200, ApprovedMonthlyCostCeilingMicroUSD: 300, Ready: true}, CollectedAt: now.Add(-2 * time.Hour), CheckCount: 8, PassedCount: 8}
	securityDigest, alertDigest, blockerDigest, capacityDigest := digestPB("b"), digestPB("c"), digestPB("d"), digestPB("e")
	support := digestPB("f")
	bundleDigest, err := PrivateBetaEvidenceBundleDigest(securityDigest, alertDigest, blockerDigest, capacityDigest, support)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{{KeyID: "private-beta-2026", Owner: "private-beta-authority", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"private_beta"}, Controls: privateBetaRequiredControls}}}
	approvals := make([]readiness.SignedApproval, 0, 5)
	for _, control := range privateBetaRequiredControls {
		evidence := bundleDigest
		if substitute && control == "security_review" {
			evidence = common
		}
		expires := now.Add(7 * 24 * time.Hour)
		if state == "expired" && control == "privacy_review" {
			expires = now.Add(-time.Minute)
		}
		approval := readiness.SignedApproval{Schema: readiness.ApprovalArtifactSchema, Gate: "private_beta", Control: control, Decision: "approved", Owner: "private-beta-authority", KeyID: "private-beta-2026", EvidenceRef: "dossier://private-beta/" + control, EvidenceSHA256: evidence, IssuedAt: now.Add(-90 * time.Minute).Format(time.RFC3339Nano), ExpiresAt: expires.Format(time.RFC3339Nano)}
		payload, err := readiness.CanonicalApprovalPayload(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
		approvals = append(approvals, approval)
	}
	exported := now.Add(-time.Hour)
	manifest := ExportManifest{Schema: PrivateBetaManifestSchemaV1, ExportID: "private-beta-export", Gate: "private_beta", ExportedAt: exported, Files: []ManifestFile{{Name: "approval.json", SHA256: common}}}
	checks := make([]Check, 0, 9)
	for _, id := range privateBetaRequiredChecks {
		outcome := OutcomePassed
		if state == "expired" && id == PrivateBetaCheckRequiredApprovals {
			outcome = OutcomeFailed
		}
		if state == "expired" && id == PrivateBetaCheckEvidenceBinding {
			outcome = OutcomeInconclusive
		}
		if state == "expired" && id == PrivateBetaCheckCurrent {
			outcome = OutcomeFailed
		}
		checks = append(checks, Check{ID: id, Outcome: outcome, EvidenceSHA256: common})
	}
	input := PrivateBetaInput{Schema: PrivateBetaInputSchemaV1, Classification: "staging_external", Environment: "staging", ExportID: manifest.ExportID, ReviewID: "private-beta-review", ReviewPolicyVersion: "policy-v1", InventoryID: "inventory", InventoryReceiptSHA256: common, PlanID: "plan", PlanReceiptSHA256: common, ChangeID: "change", ChangeReceiptSHA256: common, ReleaseID: "release", ReleaseReceiptSHA256: common, SecurityClosureReviewID: "security-review", SecurityClosureReceiptSHA256: securityDigest, AlertBundleID: "alert-bundle", AlertReceiptSHA256: alertDigest, BlockerReviewID: "blocker-review", BlockerReceiptSHA256: blockerDigest, CapacityAssessmentID: "capacity-assessment", CapacityReceiptSHA256: capacityDigest, SupportingEvidenceManifestSHA256: support, PrivateBetaEvidenceBundleSHA256: bundleDigest, TrustBundleSHA256: digestPB("1"), ApprovalExportSHA256: digestPB("2"), DomainReviewSHA256: digestPB("3"), ExportedAt: exported, ReviewedAt: now.Add(-45 * time.Minute), GeneratedAt: now.Add(-30 * time.Minute), Ready: state == "", Checks: checks}
	return privateBetaTestFixture{now: now, security: security, alert: alert, blocker: blocker, capacity: capacity, securityDigest: securityDigest, alertDigest: alertDigest, blockerDigest: blockerDigest, capacityDigest: capacityDigest, trustDigest: digestPB("1"), manifestDigest: digestPB("4"), exportDigest: digestPB("2"), inputDigest: digestPB("5"), bundle: trust, privateKey: private, approvals: approvals, manifest: manifest, input: input}
}

func digestPB(value string) string { return strings.Repeat(value, 64) }
func fileDigestPB(t *testing.T, path string) string {
	t.Helper()
	value, err := digestRegular(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
