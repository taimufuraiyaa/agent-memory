package approvalexportevidence

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/alertevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/blockerevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/capacityevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/securityclosureevidence"
)

const (
	PrivateBetaManifestSchemaV1 = "agent-memory-private-beta-approval-export-manifest-v1"
	PrivateBetaInputSchemaV1    = "agent-memory-private-beta-approval-export-input-v1"
	PrivateBetaReceiptSchemaV1  = "agent-memory-private-beta-approval-export-receipt-v1"
)

const (
	PrivateBetaCheckPrerequisites     CheckID = "private_beta_prerequisite_receipts_ready"
	PrivateBetaCheckCommonRelease     CheckID = "private_beta_common_staging_release"
	PrivateBetaCheckEvidenceBundle    CheckID = "private_beta_evidence_bundle_derived"
	PrivateBetaCheckTrustBundle       CheckID = "private_beta_trust_bundle_valid"
	PrivateBetaCheckCompleteExport    CheckID = "private_beta_immutable_export_complete"
	PrivateBetaCheckRequiredApprovals CheckID = "private_beta_required_approvals_verified"
	PrivateBetaCheckEvidenceBinding   CheckID = "private_beta_reviewed_evidence_bound"
	PrivateBetaCheckCurrent           CheckID = "private_beta_approvals_current"
	PrivateBetaCheckDomainReview      CheckID = "private_beta_accountable_domain_review_complete"
)

var privateBetaRequiredControls = []string{"legal_review", "operations_review", "privacy_review", "product_review", "security_review"}
var privateBetaRequiredChecks = []CheckID{PrivateBetaCheckPrerequisites, PrivateBetaCheckCommonRelease, PrivateBetaCheckEvidenceBundle, PrivateBetaCheckTrustBundle, PrivateBetaCheckCompleteExport, PrivateBetaCheckRequiredApprovals, PrivateBetaCheckEvidenceBinding, PrivateBetaCheckCurrent, PrivateBetaCheckDomainReview}

type PrivateBetaInput struct {
	Schema                           string    `json:"schema"`
	Classification                   string    `json:"classification"`
	Environment                      string    `json:"environment"`
	ExportID                         string    `json:"export_id"`
	ReviewID                         string    `json:"review_id"`
	ReviewPolicyVersion              string    `json:"review_policy_version"`
	InventoryID                      string    `json:"inventory_id"`
	InventoryReceiptSHA256           string    `json:"inventory_receipt_sha256"`
	PlanID                           string    `json:"plan_id"`
	PlanReceiptSHA256                string    `json:"plan_receipt_sha256"`
	ChangeID                         string    `json:"change_id"`
	ChangeReceiptSHA256              string    `json:"change_receipt_sha256"`
	ReleaseID                        string    `json:"release_id"`
	ReleaseReceiptSHA256             string    `json:"release_receipt_sha256"`
	SecurityClosureReviewID          string    `json:"security_closure_review_id"`
	SecurityClosureReceiptSHA256     string    `json:"security_closure_receipt_sha256"`
	AlertBundleID                    string    `json:"alert_bundle_id"`
	AlertReceiptSHA256               string    `json:"alert_receipt_sha256"`
	BlockerReviewID                  string    `json:"blocker_review_id"`
	BlockerReceiptSHA256             string    `json:"blocker_receipt_sha256"`
	CapacityAssessmentID             string    `json:"capacity_assessment_id"`
	CapacityReceiptSHA256            string    `json:"capacity_receipt_sha256"`
	SupportingEvidenceManifestSHA256 string    `json:"supporting_evidence_manifest_sha256"`
	PrivateBetaEvidenceBundleSHA256  string    `json:"private_beta_evidence_bundle_sha256"`
	TrustBundleSHA256                string    `json:"trust_bundle_sha256"`
	ApprovalExportSHA256             string    `json:"approval_export_sha256"`
	DomainReviewSHA256               string    `json:"domain_review_sha256"`
	ExportedAt                       time.Time `json:"exported_at"`
	ReviewedAt                       time.Time `json:"reviewed_at"`
	GeneratedAt                      time.Time `json:"generated_at"`
	Ready                            bool      `json:"ready"`
	Checks                           []Check   `json:"checks"`
}

type PrivateBetaReceipt struct {
	PrivateBetaInput
	Schema                string          `json:"schema"`
	InputSHA256           string          `json:"input_sha256"`
	ManifestSHA256        string          `json:"manifest_sha256"`
	CollectedAt           time.Time       `json:"collected_at"`
	ApprovalArtifactCount int             `json:"approval_artifact_count"`
	RequiredControlCount  int             `json:"required_control_count"`
	VerifiedControlCount  int             `json:"verified_control_count"`
	MissingControlCount   int             `json:"missing_control_count"`
	RejectedControlCount  int             `json:"rejected_control_count"`
	ExpiredControlCount   int             `json:"expired_control_count"`
	MinimumExpirySeconds  int64           `json:"minimum_expiry_seconds"`
	ControlResults        []ControlResult `json:"control_results"`
	CheckCount            int             `json:"check_count"`
	PassedCount           int             `json:"passed_count"`
	FailedCount           int             `json:"failed_count"`
	InconclusiveCount     int             `json:"inconclusive_count"`
}

func PrivateBetaRequiredControls() []string {
	return append([]string(nil), privateBetaRequiredControls...)
}
func PrivateBetaRequiredChecks() []CheckID {
	return append([]CheckID(nil), privateBetaRequiredChecks...)
}

func CollectPrivateBeta(securityPath, alertPath, blockerPath, capacityPath, trustPath, approvalsDirectory, manifestPath, inputPath string, now time.Time) (PrivateBetaReceipt, error) {
	var security securityclosureevidence.Receipt
	securityDigest, err := decodeStrictRegular(securityPath, &security)
	if err != nil {
		return PrivateBetaReceipt{}, fmt.Errorf("load ready security-closure receipt: %w", err)
	}
	var alert alertevidence.Receipt
	alertDigest, err := decodeStrictRegular(alertPath, &alert)
	if err != nil {
		return PrivateBetaReceipt{}, fmt.Errorf("load ready alert-routing receipt: %w", err)
	}
	var blocker blockerevidence.Receipt
	blockerDigest, err := decodeStrictRegular(blockerPath, &blocker)
	if err != nil {
		return PrivateBetaReceipt{}, fmt.Errorf("load ready blocker-review receipt: %w", err)
	}
	var capacity capacityevidence.Receipt
	capacityDigest, err := decodeStrictRegular(capacityPath, &capacity)
	if err != nil {
		return PrivateBetaReceipt{}, fmt.Errorf("load ready capacity receipt: %w", err)
	}
	var bundle readiness.TrustBundle
	trustDigest, err := decodeStrictRegular(trustPath, &bundle)
	if err != nil {
		return PrivateBetaReceipt{}, fmt.Errorf("load private-beta trust bundle: %w", err)
	}
	if err := readiness.ValidateTrustBundle(bundle); err != nil {
		return PrivateBetaReceipt{}, err
	}
	var manifest ExportManifest
	manifestDigest, err := decodeStrictRegular(manifestPath, &manifest)
	if err != nil {
		return PrivateBetaReceipt{}, fmt.Errorf("load private-beta export manifest: %w", err)
	}
	exportDigest, artifactCount, approvals, err := loadVerifiedExportFor(approvalsDirectory, manifest, PrivateBetaManifestSchemaV1, "private_beta")
	if err != nil {
		return PrivateBetaReceipt{}, err
	}
	var input PrivateBetaInput
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return PrivateBetaReceipt{}, err
	}
	return buildPrivateBeta(security, securityDigest, alert, alertDigest, blocker, blockerDigest, capacity, capacityDigest, trustDigest, bundle, approvals, manifest, manifestDigest, exportDigest, artifactCount, input, inputDigest, now)
}

func PrivateBetaEvidenceBundleDigest(securityDigest, alertDigest, blockerDigest, capacityDigest, supportingDigest string) (string, error) {
	values := []struct{ name, value string }{{"security_closure", securityDigest}, {"alert_routing", alertDigest}, {"blocker_review", blockerDigest}, {"capacity_economics", capacityDigest}, {"supporting_evidence_manifest", supportingDigest}}
	for _, v := range values {
		if !digestPattern.MatchString(v.value) {
			return "", errors.New("private-beta evidence bundle digest input is invalid")
		}
	}
	hash := sha256.New()
	hash.Write([]byte("agent-memory-private-beta-evidence-bundle-v1\n"))
	for _, v := range values {
		fmt.Fprintf(hash, "%s:%s\n", v.name, v.value)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func buildPrivateBeta(security securityclosureevidence.Receipt, securityDigest string, alert alertevidence.Receipt, alertDigest string, blocker blockerevidence.Receipt, blockerDigest string, capacity capacityevidence.Receipt, capacityDigest, trustDigest string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, manifest ExportManifest, manifestDigest, exportDigest string, artifactCount int, input PrivateBetaInput, inputDigest string, now time.Time) (PrivateBetaReceipt, error) {
	if err := validatePrivateBetaPrerequisites(security, alert, blocker, capacity); err != nil {
		return PrivateBetaReceipt{}, err
	}
	if !samePrivateBetaRelease(security, alert, blocker, capacity) {
		return PrivateBetaReceipt{}, errors.New("private-beta approval prerequisites do not share one staging release")
	}
	if input.Schema != PrivateBetaInputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !allOpaque(input.ExportID, input.ReviewID, input.ReviewPolicyVersion, input.InventoryID, input.PlanID, input.ChangeID, input.ReleaseID, input.SecurityClosureReviewID, input.AlertBundleID, input.BlockerReviewID, input.CapacityAssessmentID) {
		return PrivateBetaReceipt{}, errors.New("private-beta approval identity is invalid")
	}
	if input.ExportID != manifest.ExportID || manifest.Schema != PrivateBetaManifestSchemaV1 || manifest.Gate != "private_beta" || input.ExportedAt.UTC() != manifest.ExportedAt.UTC() || input.InventoryID != security.InventoryID || input.InventoryReceiptSHA256 != security.InventoryReceiptSHA256 || input.PlanID != security.PlanID || input.PlanReceiptSHA256 != security.PlanReceiptSHA256 || input.ChangeID != security.ChangeID || input.ChangeReceiptSHA256 != security.ChangeReceiptSHA256 || input.ReleaseID != security.ReleaseID || input.ReleaseReceiptSHA256 != security.ReleaseReceiptSHA256 || input.SecurityClosureReviewID != security.ReviewID || input.SecurityClosureReceiptSHA256 != securityDigest || input.AlertBundleID != alert.BundleID || input.AlertReceiptSHA256 != alertDigest || input.BlockerReviewID != blocker.ReviewID || input.BlockerReceiptSHA256 != blockerDigest || input.CapacityAssessmentID != capacity.AssessmentID || input.CapacityReceiptSHA256 != capacityDigest || input.TrustBundleSHA256 != trustDigest || input.ApprovalExportSHA256 != exportDigest || !allDigests(securityDigest, alertDigest, blockerDigest, capacityDigest, input.SupportingEvidenceManifestSHA256, input.PrivateBetaEvidenceBundleSHA256, trustDigest, exportDigest, input.DomainReviewSHA256, manifestDigest, inputDigest) {
		return PrivateBetaReceipt{}, errors.New("private-beta approval prerequisite or artifact binding is invalid")
	}
	derivedBundle, err := PrivateBetaEvidenceBundleDigest(securityDigest, alertDigest, blockerDigest, capacityDigest, input.SupportingEvidenceManifestSHA256)
	if err != nil || derivedBundle != input.PrivateBetaEvidenceBundleSHA256 {
		return PrivateBetaReceipt{}, errors.New("private-beta evidence bundle binding is invalid")
	}
	if now.IsZero() {
		return PrivateBetaReceipt{}, errors.New("private-beta approval collection time is invalid")
	}
	now = now.UTC()
	exported, reviewed, generated := input.ExportedAt.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	latest := latestPrivateBetaCollection(security, alert, blocker, capacity)
	if exported.IsZero() || exported.Before(latest) || reviewed.Before(exported) || reviewed.Sub(exported) > maximumEvidenceAge || generated.Before(reviewed) || generated.Before(now.Add(-maximumEvidenceAge)) || generated.After(now) {
		return PrivateBetaReceipt{}, errors.New("private-beta approval timeline is invalid")
	}
	report, err := readiness.VerifyApprovals("private_beta", privateBetaRequiredControls, bundle, approvals, now)
	if err != nil {
		return PrivateBetaReceipt{}, err
	}
	for _, approval := range approvals {
		issued, err := time.Parse(time.RFC3339Nano, approval.IssuedAt)
		if err != nil || issued.After(exported) {
			return PrivateBetaReceipt{}, errors.New("private-beta approval was issued after export")
		}
	}
	for _, verified := range report.Verified {
		if verified.EvidenceSHA256 != derivedBundle {
			return PrivateBetaReceipt{}, errors.New("private-beta reviewed-evidence binding is invalid")
		}
	}
	complete := len(report.Missing) == 0 && len(report.Rejected) == 0 && len(report.Expired) == 0
	expected := map[CheckID]Outcome{PrivateBetaCheckPrerequisites: OutcomePassed, PrivateBetaCheckCommonRelease: OutcomePassed, PrivateBetaCheckEvidenceBundle: OutcomePassed, PrivateBetaCheckTrustBundle: OutcomePassed, PrivateBetaCheckCompleteExport: OutcomePassed, PrivateBetaCheckRequiredApprovals: outcome(complete), PrivateBetaCheckEvidenceBinding: conditionalOutcome(report.Ready), PrivateBetaCheckCurrent: outcome(len(report.Expired) == 0), PrivateBetaCheckDomainReview: outcomeForInput(input.Checks, PrivateBetaCheckDomainReview)}
	checks, passed, failed, inconclusive, err := validateChecksFor(input.Checks, expected, privateBetaRequiredChecks)
	if err != nil {
		return PrivateBetaReceipt{}, err
	}
	ready := report.Ready && passed == len(privateBetaRequiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return PrivateBetaReceipt{}, errors.New("private-beta approval readiness contradicts evidence")
	}
	controls, minimum := summarizePrivateBetaControls(report, now)
	input.Schema = PrivateBetaReceiptSchemaV1
	input.ExportedAt, input.ReviewedAt, input.GeneratedAt, input.Checks = exported, reviewed, generated, checks
	return PrivateBetaReceipt{PrivateBetaInput: input, Schema: PrivateBetaReceiptSchemaV1, InputSHA256: inputDigest, ManifestSHA256: manifestDigest, CollectedAt: now, ApprovalArtifactCount: artifactCount, RequiredControlCount: len(privateBetaRequiredControls), VerifiedControlCount: len(report.Verified), MissingControlCount: len(report.Missing), RejectedControlCount: len(report.Rejected), ExpiredControlCount: len(report.Expired), MinimumExpirySeconds: minimum, ControlResults: controls, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func validatePrivateBetaPrerequisites(security securityclosureevidence.Receipt, alert alertevidence.Receipt, blocker blockerevidence.Receipt, capacity capacityevidence.Receipt) error {
	if security.Schema != securityclosureevidence.ReceiptSchemaV1 || security.Classification != "staging_external" || security.Environment != "staging" || !security.Ready || !security.CoverageComplete || security.SourceCount != 4 || security.BlockingFindingCount != 0 || security.OpenBlockingFindingCount != 0 || security.RetestIncompleteCount != 0 || security.InconclusiveClassificationCount != 0 || security.CheckCount != 7 || security.PassedCount != 7 || security.FailedCount != 0 || security.InconclusiveCount != 0 {
		return errors.New("private-beta security-closure prerequisite is invalid or unready")
	}
	if alert.Schema != alertevidence.ReceiptSchemaV1 || alert.Classification != "staging_external" || alert.Environment != "staging" || !alert.Ready || alert.AlertCount != 7 || alert.TargetBreachCount != 0 || alert.PassedCount != 7 || alert.FailedCount != 0 || alert.InconclusiveCount != 0 {
		return errors.New("private-beta alert-routing prerequisite is invalid or unready")
	}
	if blocker.Schema != blockerevidence.ReceiptSchemaV1 || blocker.Classification != "staging_external" || blocker.Environment != "staging" || !blocker.Ready || blocker.BlockerCount != 0 || !blocker.ReviewCoverageComplete || blocker.CheckCount != 5 || blocker.PassedCount != 5 || blocker.FailedCount != 0 || blocker.InconclusiveCount != 0 {
		return errors.New("private-beta blocker-review prerequisite is invalid or unready")
	}
	if capacity.Schema != capacityevidence.ReceiptSchemaV1 || capacity.Classification != "staging_external" || capacity.Environment != "staging" || !capacity.Ready || capacity.MetricBreachCount != 0 || capacity.CheckCount != 8 || capacity.PassedCount != 8 || capacity.FailedCount != 0 || capacity.InconclusiveCount != 0 {
		return errors.New("private-beta capacity prerequisite is invalid or unready")
	}
	return nil
}

func samePrivateBetaRelease(s securityclosureevidence.Receipt, a alertevidence.Receipt, b blockerevidence.Receipt, c capacityevidence.Receipt) bool {
	return allDigests(s.InventoryReceiptSHA256, s.PlanReceiptSHA256, s.ChangeReceiptSHA256, s.ReleaseReceiptSHA256) && s.InventoryID == a.InventoryID && s.InventoryID == b.InventoryID && s.InventoryID == c.InventoryID && s.InventoryReceiptSHA256 == a.InventoryReceiptSHA256 && s.InventoryReceiptSHA256 == b.InventoryReceiptSHA256 && s.InventoryReceiptSHA256 == c.InventoryReceiptSHA256 && s.PlanID == a.PlanID && s.PlanID == b.PlanID && s.PlanID == c.PlanID && s.PlanReceiptSHA256 == a.PlanReceiptSHA256 && s.PlanReceiptSHA256 == b.PlanReceiptSHA256 && s.PlanReceiptSHA256 == c.PlanReceiptSHA256 && s.ChangeID == a.ChangeID && s.ChangeID == b.ChangeID && s.ChangeID == c.ChangeID && s.ChangeReceiptSHA256 == a.ChangeReceiptSHA256 && s.ChangeReceiptSHA256 == b.ChangeReceiptSHA256 && s.ChangeReceiptSHA256 == c.ChangeReceiptSHA256 && s.ReleaseID == a.ReleaseID && s.ReleaseID == b.ReleaseID && s.ReleaseID == c.ReleaseID && s.ReleaseReceiptSHA256 == a.ReleaseReceiptSHA256 && s.ReleaseReceiptSHA256 == b.ReleaseReceiptSHA256 && s.ReleaseReceiptSHA256 == c.ReleaseReceiptSHA256
}

func latestPrivateBetaCollection(s securityclosureevidence.Receipt, a alertevidence.Receipt, b blockerevidence.Receipt, c capacityevidence.Receipt) time.Time {
	latest := s.CollectedAt.UTC()
	for _, v := range []time.Time{a.CollectedAt, b.CollectedAt, c.CollectedAt} {
		if v.UTC().After(latest) {
			latest = v.UTC()
		}
	}
	return latest
}

func summarizePrivateBetaControls(report readiness.ApprovalReport, now time.Time) ([]ControlResult, int64) {
	missing, rejected, expired := stringSet(report.Missing), stringSet(report.Rejected), stringSet(report.Expired)
	results := make([]ControlResult, 0, len(privateBetaRequiredControls))
	minimum := int64(0)
	for _, control := range privateBetaRequiredControls {
		value := ControlResult{Control: control, Outcome: OutcomePassed}
		if missing[control] {
			value.Outcome = OutcomeInconclusive
		} else if rejected[control] || expired[control] {
			value.Outcome = OutcomeFailed
		}
		if verified, ok := report.Verified[control]; ok {
			expires, _ := time.Parse(time.RFC3339Nano, verified.ExpiresAt)
			remaining := int64(expires.Sub(now) / time.Second)
			if minimum == 0 || remaining < minimum {
				minimum = remaining
			}
		}
		results = append(results, value)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Control < results[j].Control })
	return results, minimum
}

func PublishPrivateBeta(path string, receipt PrivateBetaReceipt) error {
	return publishAny(path, receipt, "private-beta approval receipt", ".agent-memory-private-beta-approval-*")
}
