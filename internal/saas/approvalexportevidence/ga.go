package approvalexportevidence

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/gadrillevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/gascorecardevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

const (
	GAManifestSchemaV1             = "agent-memory-ga-approval-export-manifest-v1"
	GAInputSchemaV1                = "agent-memory-ga-approval-export-input-v1"
	GAReceiptSchemaV1              = "agent-memory-ga-approval-export-receipt-v1"
	GACheckPrerequisites   CheckID = "ga_prerequisite_receipts_ready"
	GACheckCommonRelease   CheckID = "ga_common_production_release"
	GACheckTrustBundle     CheckID = "ga_trust_bundle_valid"
	GACheckCompleteExport  CheckID = "ga_immutable_export_complete"
	GACheckSignatures      CheckID = "ga_authorized_signatures_valid"
	GACheckEvidenceBinding CheckID = "ga_reviewed_evidence_bound"
	GACheckCurrent         CheckID = "ga_approvals_current"
	GACheckDomainReview    CheckID = "ga_accountable_domain_review_complete"
)

var gaRequiredControls = []string{"product", "security", "privacy", "legal", "operations"}
var gaRequiredChecks = []CheckID{GACheckPrerequisites, GACheckCommonRelease, GACheckTrustBundle, GACheckCompleteExport, GACheckSignatures, GACheckEvidenceBinding, GACheckCurrent, GACheckDomainReview}

type GAInput struct {
	Schema                 string    `json:"schema"`
	Classification         string    `json:"classification"`
	Environment            string    `json:"environment"`
	ExportID               string    `json:"export_id"`
	ReviewID               string    `json:"review_id"`
	ReviewPolicyVersion    string    `json:"review_policy_version"`
	InventoryID            string    `json:"inventory_id"`
	InventoryReceiptSHA256 string    `json:"inventory_receipt_sha256"`
	PlanID                 string    `json:"plan_id"`
	PlanReceiptSHA256      string    `json:"plan_receipt_sha256"`
	ChangeID               string    `json:"change_id"`
	ChangeReceiptSHA256    string    `json:"change_receipt_sha256"`
	ReleaseID              string    `json:"release_id"`
	ReleaseReceiptSHA256   string    `json:"release_receipt_sha256"`
	ScorecardID            string    `json:"scorecard_id"`
	ScorecardReceiptSHA256 string    `json:"scorecard_receipt_sha256"`
	DrillReviewID          string    `json:"drill_review_id"`
	DrillReceiptSHA256     string    `json:"drill_receipt_sha256"`
	GAEvidenceBundleSHA256 string    `json:"ga_evidence_bundle_sha256"`
	TrustBundleSHA256      string    `json:"trust_bundle_sha256"`
	ApprovalExportSHA256   string    `json:"approval_export_sha256"`
	DomainReviewSHA256     string    `json:"domain_review_sha256"`
	ExportedAt             time.Time `json:"exported_at"`
	ReviewedAt             time.Time `json:"reviewed_at"`
	GeneratedAt            time.Time `json:"generated_at"`
	Ready                  bool      `json:"ready"`
	Checks                 []Check   `json:"checks"`
}

type GAReceipt struct {
	GAInput
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

func GARequiredControls() []string { return append([]string(nil), gaRequiredControls...) }
func GARequiredChecks() []CheckID  { return append([]CheckID(nil), gaRequiredChecks...) }
func GAEvidenceBundleDigest(scorecardDigest, drillDigest string) (string, error) {
	if !allDigests(scorecardDigest, drillDigest) {
		return "", errors.New("GA evidence receipt digest is invalid")
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(scorecardDigest+"\n"+drillDigest+"\n"))), nil
}

func CollectGA(scorecardPath, drillPath, trustPath, approvalsDirectory, manifestPath, inputPath string, now time.Time) (GAReceipt, error) {
	scorecard, scorecardDigest, err := gascorecardevidence.LoadReady(scorecardPath)
	if err != nil {
		return GAReceipt{}, fmt.Errorf("load ready GA scorecard: %w", err)
	}
	drills, drillDigest, err := gadrillevidence.LoadReady(drillPath)
	if err != nil {
		return GAReceipt{}, fmt.Errorf("load ready GA drills: %w", err)
	}
	var bundle readiness.TrustBundle
	trustDigest, err := decodeStrictRegular(trustPath, &bundle)
	if err != nil {
		return GAReceipt{}, fmt.Errorf("load GA trust bundle: %w", err)
	}
	if err := readiness.ValidateTrustBundle(bundle); err != nil {
		return GAReceipt{}, err
	}
	var manifest ExportManifest
	manifestDigest, err := decodeStrictRegular(manifestPath, &manifest)
	if err != nil {
		return GAReceipt{}, fmt.Errorf("load GA export manifest: %w", err)
	}
	exportDigest, artifactCount, approvals, err := loadVerifiedExportFor(approvalsDirectory, manifest, GAManifestSchemaV1, "ga")
	if err != nil {
		return GAReceipt{}, err
	}
	var input GAInput
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return GAReceipt{}, err
	}
	return buildGA(scorecard, scorecardDigest, drills, drillDigest, trustDigest, bundle, approvals, manifest, manifestDigest, exportDigest, artifactCount, input, inputDigest, now)
}

func buildGA(scorecard gascorecardevidence.Receipt, scorecardDigest string, drills gadrillevidence.Receipt, drillDigest, trustDigest string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, manifest ExportManifest, manifestDigest, exportDigest string, artifactCount int, input GAInput, inputDigest string, now time.Time) (GAReceipt, error) {
	if drills.ScorecardID != scorecard.ScorecardID || drills.ScorecardReceiptSHA256 != scorecardDigest || drills.InventoryID != scorecard.InventoryID || drills.InventoryReceiptSHA256 != scorecard.InventoryReceiptSHA256 || drills.PlanID != scorecard.PlanID || drills.PlanReceiptSHA256 != scorecard.PlanReceiptSHA256 || drills.ChangeID != scorecard.ChangeID || drills.ChangeReceiptSHA256 != scorecard.ChangeReceiptSHA256 || drills.ReleaseID != scorecard.ReleaseID || drills.ReleaseReceiptSHA256 != scorecard.ReleaseReceiptSHA256 {
		return GAReceipt{}, errors.New("GA approval prerequisites do not share one production evidence chain")
	}
	bundleDigest, err := GAEvidenceBundleDigest(scorecardDigest, drillDigest)
	if err != nil {
		return GAReceipt{}, err
	}
	if input.Schema != GAInputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.ExportID, input.ReviewID, input.ReviewPolicyVersion, input.InventoryID, input.PlanID, input.ChangeID, input.ReleaseID, input.ScorecardID, input.DrillReviewID) {
		return GAReceipt{}, errors.New("GA approval export identity is invalid")
	}
	if input.ExportID != manifest.ExportID || input.ExportedAt.UTC() != manifest.ExportedAt.UTC() || input.InventoryID != scorecard.InventoryID || input.InventoryReceiptSHA256 != scorecard.InventoryReceiptSHA256 || input.PlanID != scorecard.PlanID || input.PlanReceiptSHA256 != scorecard.PlanReceiptSHA256 || input.ChangeID != scorecard.ChangeID || input.ChangeReceiptSHA256 != scorecard.ChangeReceiptSHA256 || input.ReleaseID != scorecard.ReleaseID || input.ReleaseReceiptSHA256 != scorecard.ReleaseReceiptSHA256 || input.ScorecardID != scorecard.ScorecardID || input.ScorecardReceiptSHA256 != scorecardDigest || input.DrillReviewID != drills.ReviewID || input.DrillReceiptSHA256 != drillDigest || input.GAEvidenceBundleSHA256 != bundleDigest || input.TrustBundleSHA256 != trustDigest || input.ApprovalExportSHA256 != exportDigest || !allDigests(input.InventoryReceiptSHA256, input.PlanReceiptSHA256, input.ChangeReceiptSHA256, input.ReleaseReceiptSHA256, input.ScorecardReceiptSHA256, input.DrillReceiptSHA256, input.GAEvidenceBundleSHA256, input.TrustBundleSHA256, input.ApprovalExportSHA256, input.DomainReviewSHA256, manifestDigest, inputDigest) {
		return GAReceipt{}, errors.New("GA approval prerequisite or artifact binding is invalid")
	}
	if now.IsZero() {
		return GAReceipt{}, errors.New("GA approval collection time is invalid")
	}
	now = now.UTC()
	exported, reviewed, generated := input.ExportedAt.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if exported.IsZero() || exported.Before(scorecard.CollectedAt.UTC()) || exported.Before(drills.CollectedAt.UTC()) || reviewed.Before(exported) || reviewed.Sub(exported) > maximumEvidenceAge || generated.Before(reviewed) || generated.Before(now.Add(-maximumEvidenceAge)) || generated.After(now) {
		return GAReceipt{}, errors.New("GA approval export timeline is invalid")
	}
	report, err := readiness.VerifyApprovals("ga", gaRequiredControls, bundle, approvals, now)
	if err != nil {
		return GAReceipt{}, err
	}
	bindingsReady := report.Ready
	if bindingsReady {
		for _, verified := range report.Verified {
			if verified.EvidenceSHA256 != bundleDigest {
				return GAReceipt{}, errors.New("GA approval reviewed-evidence binding is invalid")
			}
		}
	}
	expected := map[CheckID]Outcome{GACheckPrerequisites: OutcomePassed, GACheckCommonRelease: OutcomePassed, GACheckTrustBundle: OutcomePassed, GACheckCompleteExport: OutcomePassed, GACheckSignatures: outcome(len(report.Missing) == 0), GACheckEvidenceBinding: conditionalOutcome(bindingsReady), GACheckCurrent: outcome(len(report.Expired) == 0), GACheckDomainReview: outcomeForInput(input.Checks, GACheckDomainReview)}
	checks, passed, failed, inconclusive, err := validateChecksFor(input.Checks, expected, gaRequiredChecks)
	if err != nil {
		return GAReceipt{}, err
	}
	ready := report.Ready && bindingsReady && passed == len(gaRequiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return GAReceipt{}, errors.New("GA approval readiness contradicts evidence")
	}
	controls, minimum := summarizeGAControls(report, now)
	input.Schema, input.ExportedAt, input.ReviewedAt, input.GeneratedAt, input.Checks = GAReceiptSchemaV1, exported, reviewed, generated, checks
	return GAReceipt{GAInput: input, Schema: GAReceiptSchemaV1, InputSHA256: inputDigest, ManifestSHA256: manifestDigest, CollectedAt: now, ApprovalArtifactCount: artifactCount, RequiredControlCount: len(gaRequiredControls), VerifiedControlCount: len(report.Verified), MissingControlCount: len(report.Missing), RejectedControlCount: len(report.Rejected), ExpiredControlCount: len(report.Expired), MinimumExpirySeconds: minimum, ControlResults: controls, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func summarizeGAControls(report readiness.ApprovalReport, now time.Time) ([]ControlResult, int64) {
	missing, rejected, expired := stringSet(report.Missing), stringSet(report.Rejected), stringSet(report.Expired)
	results := make([]ControlResult, 0, len(gaRequiredControls))
	minimum := int64(0)
	for _, control := range gaRequiredControls {
		result := ControlResult{Control: control, Outcome: OutcomePassed}
		switch {
		case missing[control]:
			result.Outcome = OutcomeInconclusive
		case rejected[control] || expired[control]:
			result.Outcome = OutcomeFailed
		}
		if verified, ok := report.Verified[control]; ok {
			expires, _ := time.Parse(time.RFC3339Nano, verified.ExpiresAt)
			remaining := int64(expires.Sub(now) / time.Second)
			if minimum == 0 || remaining < minimum {
				minimum = remaining
			}
		}
		results = append(results, result)
	}
	return results, minimum
}

func PublishGA(path string, receipt GAReceipt) error {
	return publishAny(path, receipt, "GA approval export receipt", ".agent-memory-ga-approval-export-*")
}
