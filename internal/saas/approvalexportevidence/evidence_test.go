package approvalexportevidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/gadrillevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/gascorecardevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchassetevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/publicbetagateevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

func TestCollectVerifiesCompleteCurrentEvidenceBoundExport(t *testing.T) {
	fixture := newFixture(t, false, false)
	receipt, err := Collect(fixture.launch, fixture.gate, fixture.trust, fixture.approvals, fixture.manifest, fixture.input, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.VerifiedControlCount != len(requiredControls) || receipt.ApprovalArtifactCount != len(requiredControls) || receipt.ApprovalExportSHA256 == "" || receipt.MinimumExpirySeconds <= 0 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	destination := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(destination, receipt); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(destination); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode: %v %v", info, err)
	}
	if err := Publish(destination, receipt); err == nil {
		t.Fatal("create-only publication overwrote a receipt")
	}

	writeJSON(t, filepath.Join(fixture.approvals, "unexpected.json"), map[string]string{"unexpected": "artifact"})
	if _, err := Collect(fixture.launch, fixture.gate, fixture.trust, fixture.approvals, fixture.manifest, fixture.input, fixture.now); err == nil {
		t.Fatal("extra approval artifact was accepted")
	}
}

func TestCollectRejectsReviewedEvidenceSubstitutionAndSymlink(t *testing.T) {
	fixture := newFixture(t, true, false)
	if _, err := Collect(fixture.launch, fixture.gate, fixture.trust, fixture.approvals, fixture.manifest, fixture.input, fixture.now); err == nil {
		t.Fatal("approval bound to substituted evidence was accepted")
	}

	fixture = newFixture(t, false, false)
	linked := filepath.Join(t.TempDir(), "approvals")
	if err := os.Symlink(fixture.approvals, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(fixture.launch, fixture.gate, fixture.trust, linked, fixture.manifest, fixture.input, fixture.now); err == nil {
		t.Fatal("symlink approval directory was accepted")
	}
}

func TestCollectPreservesExpiredApprovalAsValidUnready(t *testing.T) {
	fixture := newFixture(t, false, true)
	receipt, err := Collect(fixture.launch, fixture.gate, fixture.trust, fixture.approvals, fixture.manifest, fixture.input, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.ExpiredControlCount != 1 || receipt.FailedCount != 1 || receipt.InconclusiveCount != 1 {
		t.Fatalf("unexpected unready receipt: %+v", receipt)
	}
}

func TestLoadVerifiedExportRejectsMembershipChangeAfterSnapshot(t *testing.T) {
	directory := t.TempDir()
	approval := readiness.SignedApproval{
		Schema: readiness.ApprovalArtifactSchema,
		Gate:   "public_beta", Control: "status_page", Decision: "approved",
		Owner: "release-authority", KeyID: "release-2026",
		EvidenceRef: "dossier://public-beta/status_page", EvidenceSHA256: strings.Repeat("a", 64),
		IssuedAt:  time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		ExpiresAt: time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		Signature: "signature",
	}
	path := writeJSON(t, filepath.Join(directory, "status-page.json"), approval)
	digest, err := digestRegular(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := ExportManifest{
		Schema: ManifestSchemaV1, ExportID: "export-stable-snapshot", Gate: "public_beta",
		ExportedAt: time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC),
		Files:      []ManifestFile{{Name: "status-page.json", SHA256: digest}},
	}

	_, _, _, err = loadVerifiedExportForWithHook(directory, manifest, ManifestSchemaV1, "public_beta", func() {
		writeJSON(t, filepath.Join(directory, "newer-rejection.json"), approval)
	})
	if err == nil {
		t.Fatal("an approval added after the export snapshot must fail closed")
	}
}

func TestBuildGAVerifiesFiveCurrentEvidenceBundleApprovals(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	common := strings.Repeat("a", 64)
	scorecardDigest := strings.Repeat("b", 64)
	drillDigest := strings.Repeat("c", 64)
	trustDigest := strings.Repeat("d", 64)
	manifestDigest := strings.Repeat("e", 64)
	exportDigest := strings.Repeat("f", 64)
	inputDigest := strings.Repeat("1", 64)
	scorecard := gascorecardevidence.Receipt{Input: gascorecardevidence.Input{ScorecardID: "scorecard", InventoryID: "inventory", InventoryReceiptSHA256: common, PlanID: "plan", PlanReceiptSHA256: common, ChangeID: "change", ChangeReceiptSHA256: common, ReleaseID: "release", ReleaseReceiptSHA256: common}, CollectedAt: now.Add(-4 * time.Hour)}
	drills := gadrillevidence.Receipt{Input: gadrillevidence.Input{ReviewID: "drill-review", ScorecardID: "scorecard", ScorecardReceiptSHA256: scorecardDigest, InventoryID: "inventory", InventoryReceiptSHA256: common, PlanID: "plan", PlanReceiptSHA256: common, ChangeID: "change", ChangeReceiptSHA256: common, ReleaseID: "release", ReleaseReceiptSHA256: common}, CollectedAt: now.Add(-3 * time.Hour)}
	bundleDigest, err := GAEvidenceBundleDigest(scorecardDigest, drillDigest)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{{KeyID: "ga-2026", Owner: "ga-authority", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"ga"}, Controls: gaRequiredControls}}}
	approvals := make([]readiness.SignedApproval, 0, len(gaRequiredControls))
	for _, control := range gaRequiredControls {
		approval := readiness.SignedApproval{Schema: readiness.ApprovalArtifactSchema, Gate: "ga", Control: control, Decision: "approved", Owner: "ga-authority", KeyID: "ga-2026", EvidenceRef: "dossier://ga/" + control, EvidenceSHA256: bundleDigest, IssuedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)}
		payload, err := readiness.CanonicalApprovalPayload(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
		approvals = append(approvals, approval)
	}
	exported := now.Add(-2 * time.Hour)
	manifest := ExportManifest{Schema: GAManifestSchemaV1, ExportID: "ga-export", Gate: "ga", ExportedAt: exported, Files: []ManifestFile{{Name: "product.json", SHA256: common}}}
	checks := make([]Check, 0, len(gaRequiredChecks))
	for _, id := range gaRequiredChecks {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: common})
	}
	input := GAInput{Schema: GAInputSchemaV1, Classification: "production_external", Environment: "production", ExportID: manifest.ExportID, ReviewID: "ga-review", ReviewPolicyVersion: "ga-policy", InventoryID: "inventory", InventoryReceiptSHA256: common, PlanID: "plan", PlanReceiptSHA256: common, ChangeID: "change", ChangeReceiptSHA256: common, ReleaseID: "release", ReleaseReceiptSHA256: common, ScorecardID: "scorecard", ScorecardReceiptSHA256: scorecardDigest, DrillReviewID: "drill-review", DrillReceiptSHA256: drillDigest, GAEvidenceBundleSHA256: bundleDigest, TrustBundleSHA256: trustDigest, ApprovalExportSHA256: exportDigest, DomainReviewSHA256: common, ExportedAt: exported, ReviewedAt: now.Add(-90 * time.Minute), GeneratedAt: now.Add(-time.Hour), Ready: true, Checks: checks}
	receipt, err := buildGA(scorecard, scorecardDigest, drills, drillDigest, trustDigest, bundle, approvals, manifest, manifestDigest, exportDigest, len(approvals), input, inputDigest, now)
	if err != nil || !receipt.Ready || receipt.VerifiedControlCount != 5 || receipt.MinimumExpirySeconds <= 0 {
		t.Fatalf("GA approval receipt=%+v err=%v", receipt, err)
	}
	destination := filepath.Join(t.TempDir(), "ga-receipt.json")
	if err := PublishGA(destination, receipt); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(destination); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info, err)
	}
	approvals[0].EvidenceSHA256 = common
	if _, err := buildGA(scorecard, scorecardDigest, drills, drillDigest, trustDigest, bundle, approvals, manifest, manifestDigest, exportDigest, len(approvals), input, inputDigest, now); err == nil {
		t.Fatal("GA evidence substitution accepted")
	}
}

type fixturePaths struct {
	now                                             time.Time
	launch, gate, trust, approvals, manifest, input string
}

func newFixture(t *testing.T, substituteEvidence, expired bool) fixturePaths {
	t.Helper()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	digest := strings.Repeat("a", 64)
	launch := validLaunchReceipt(now, digest)
	gate := validGateReceipt(now, digest)
	launchPath := writeJSON(t, filepath.Join(t.TempDir(), "launch.json"), launch)
	gatePath := writeJSON(t, filepath.Join(t.TempDir(), "gate.json"), gate)
	launchDigest, _ := digestRegular(launchPath)
	gateDigest, _ := digestRegular(gatePath)

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle := readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{{KeyID: "release-2026", Owner: "release-authority", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"public_beta"}, Controls: requiredControls}}}
	trustPath := writeJSON(t, filepath.Join(t.TempDir(), "trust.json"), bundle)
	trustDigest, _ := digestRegular(trustPath)

	approvalsDirectory := t.TempDir()
	files := make([]ManifestFile, 0, len(requiredControls))
	for _, control := range requiredControls {
		evidenceDigest := launchDigest
		if control == "beta_readiness" {
			evidenceDigest = gateDigest
		}
		if substituteEvidence && control == "status_page" {
			evidenceDigest = strings.Repeat("b", 64)
		}
		expiresAt := now.Add(7 * 24 * time.Hour)
		if expired && control == "status_page" {
			expiresAt = now.Add(-time.Minute)
		}
		approval := readiness.SignedApproval{Schema: readiness.ApprovalArtifactSchema, Gate: "public_beta", Control: control, Decision: "approved", Owner: "release-authority", KeyID: "release-2026", EvidenceRef: "dossier://public-beta/" + control, EvidenceSHA256: evidenceDigest, IssuedAt: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano)}
		payload, err := readiness.CanonicalApprovalPayload(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
		name := control + ".json"
		path := writeJSON(t, filepath.Join(approvalsDirectory, name), approval)
		fileDigest, _ := digestRegular(path)
		files = append(files, ManifestFile{Name: name, SHA256: fileDigest})
	}
	exportedAt := now.Add(-30 * time.Minute)
	manifest := ExportManifest{Schema: ManifestSchemaV1, ExportID: "export-2026-08", Gate: "public_beta", ExportedAt: exportedAt, Files: files}
	manifestPath := writeJSON(t, filepath.Join(t.TempDir(), "manifest.json"), manifest)
	exportDigest, _, err := verifyExport(approvalsDirectory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	checks := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		checkOutcome := OutcomePassed
		if expired && id == CheckEvidenceBinding {
			checkOutcome = OutcomeInconclusive
		}
		if expired && id == CheckCurrent {
			checkOutcome = OutcomeFailed
		}
		checks = append(checks, Check{ID: id, Outcome: checkOutcome, EvidenceSHA256: digest})
	}
	input := Input{Schema: InputSchemaV1, Classification: "production_external", Environment: "production", ExportID: manifest.ExportID, ReviewID: "review-2026-08", ReviewPolicyVersion: "policy-v1", InventoryID: launch.InventoryID, InventoryReceiptSHA256: launch.InventoryReceiptSHA256, PlanID: launch.PlanID, PlanReceiptSHA256: launch.PlanReceiptSHA256, ChangeID: launch.ChangeID, ChangeReceiptSHA256: launch.ChangeReceiptSHA256, ReleaseID: launch.ReleaseID, ReleaseReceiptSHA256: launch.ReleaseReceiptSHA256, LaunchAssetReviewID: launch.ReviewID, LaunchAssetSHA256: launchDigest, BetaGateReviewID: gate.GateReviewID, BetaGateSHA256: gateDigest, TrustBundleSHA256: trustDigest, ApprovalExportSHA256: exportDigest, ReleaseReviewSHA256: digest, ExportedAt: exportedAt, ReviewedAt: now.Add(-20 * time.Minute), GeneratedAt: now.Add(-10 * time.Minute), Ready: !expired, Checks: checks}
	inputPath := writeJSON(t, filepath.Join(t.TempDir(), "input.json"), input)
	return fixturePaths{now: now, launch: launchPath, gate: gatePath, trust: trustPath, approvals: approvalsDirectory, manifest: manifestPath, input: inputPath}
}

func validLaunchReceipt(now time.Time, digest string) launchassetevidence.Receipt {
	snapshot := now.Add(-4 * time.Hour)
	assets := make([]launchassetevidence.Asset, 0, len(launchassetevidence.RequiredAssets()))
	results := make([]launchassetevidence.AssetResult, 0, len(launchassetevidence.RequiredAssets()))
	for _, id := range launchassetevidence.RequiredAssets() {
		asset := launchassetevidence.Asset{ID: id, OwnerGroup: launchassetevidence.OwnerFor(id), PublicURLSHA256: digest, RenderedCopySHA256: digest, MonitoringConfigSHA256: digest, RouteTestSHA256: digest, OwnerDecisionSHA256: digest, ObservedAt: snapshot.Add(-5 * time.Minute), HTTPStatus: 200, ProbeCount: 1, SuccessfulProbeCount: 1}
		assets = append(assets, asset)
		results = append(results, launchassetevidence.AssetResult{Asset: asset, ProbeAgeSeconds: 300, Live: true})
	}
	checks := make([]launchassetevidence.Check, 0, len(launchassetevidence.RequiredChecks()))
	for _, id := range launchassetevidence.RequiredChecks() {
		checks = append(checks, launchassetevidence.Check{ID: id, Outcome: launchassetevidence.OutcomePassed, EvidenceSHA256: digest})
	}
	input := launchassetevidence.Input{Schema: launchassetevidence.ReceiptSchemaV1, Classification: "production_external", Environment: "production", ReviewID: "launch-review", ManifestVersion: "manifest-v1", ProbeVersion: "probe-v1", CopyReviewVersion: "copy-v1", MonitoringReviewVersion: "monitor-v1", InventoryID: "inventory", InventoryReceiptSHA256: digest, PlanID: "plan", PlanReceiptSHA256: digest, ChangeID: "change", ChangeReceiptSHA256: digest, ReleaseID: "release", ReleaseReceiptSHA256: digest, ManifestSHA256: digest, AccountableReviewSHA256: digest, SnapshotAt: snapshot, ReviewedAt: now.Add(-3 * time.Hour), GeneratedAt: now.Add(-2 * time.Hour), Ready: true, Assets: assets, Checks: checks}
	return launchassetevidence.Receipt{Input: input, Schema: launchassetevidence.ReceiptSchemaV1, InputSHA256: digest, CollectedAt: now.Add(-time.Hour), AssetCount: len(assets), LiveAssetCount: len(assets), AssetResults: results, CheckCount: len(checks), PassedCount: len(checks)}
}

func validGateReceipt(now time.Time, digest string) publicbetagateevidence.Receipt {
	checks := make([]publicbetagateevidence.Check, 0, len(publicbetagateevidence.RequiredChecks()))
	for _, id := range publicbetagateevidence.RequiredChecks() {
		checks = append(checks, publicbetagateevidence.Check{ID: id, Outcome: publicbetagateevidence.OutcomePassed, EvidenceSHA256: digest})
	}
	start, end := now.Add(-48*time.Hour), now.Add(-24*time.Hour)
	input := publicbetagateevidence.Input{Schema: publicbetagateevidence.ReceiptSchemaV1, Classification: "production_external", Environment: "production", GateReviewID: "gate-review", AbusePolicyVersion: "abuse-v1", CostPolicyVersion: "cost-v1", ReviewPolicyVersion: "review-v1", InventoryID: "inventory", InventoryReceiptSHA256: digest, PlanID: "plan", PlanReceiptSHA256: digest, ChangeID: "change", ChangeReceiptSHA256: digest, ReleaseID: "release", ReleaseReceiptSHA256: digest, BillingReconciliationID: "billing", BillingReceiptSHA256: digest, BetaSLOObservationID: "slo", BetaSLOReceiptSHA256: digest, BetaOperationsAssessmentID: "operations", BetaOperationsReceiptSHA256: digest, BetaIntegrityReviewID: "integrity", BetaIntegrityReceiptSHA256: digest, WindowStart: start, WindowEnd: end, AbuseExportSHA256: digest, CostExportSHA256: digest, TargetDecisionSHA256: digest, DomainOwnerReviewSHA256: digest, TargetApprovedAt: start.Add(-time.Hour), SnapshotAt: end.Add(time.Hour), ReviewedAt: now.Add(-3 * time.Hour), GeneratedAt: now.Add(-2 * time.Hour), SignupAttemptCount: 1, ActiveTenantCount: 1, ActualWindowCostMicroUSD: 100, MaximumWindowCostMicroUSD: 200, MaximumCostPerActiveTenantMicroUSD: 200, Ready: true, Checks: checks}
	return publicbetagateevidence.Receipt{Input: input, Schema: publicbetagateevidence.ReceiptSchemaV1, InputSHA256: digest, CollectedAt: now.Add(-time.Hour), ActualCostPerActiveTenantMicroUSD: 100, AbuseClassificationComplete: true, CostWithinCeiling: true, CheckCount: len(checks), PassedCount: len(checks)}
}

func writeJSON(t *testing.T, path string, value any) string {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
