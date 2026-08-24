// Package betaintegrityevidence normalizes content-free production beta
// isolation and audit-integrity review evidence for P11.3-C.
package betaintegrityevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaoperationsevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

const (
	InputSchemaV1   = "agent-memory-production-beta-integrity-input-v1"
	ReceiptSchemaV1 = "agent-memory-production-beta-integrity-receipt-v1"

	maximumInputBytes    = 128 << 10
	maximumCount         = 100_000_000
	maximumCollectionAge = 24 * time.Hour
	maximumSnapshotDelay = 24 * time.Hour
	maximumReviewDelay   = 24 * time.Hour
)

type CheckID string
type Outcome string

const (
	CheckSharedWindowExports    CheckID = "shared_window_exports_complete"
	CheckAuditChain             CheckID = "audit_chain_verification_complete"
	CheckArchiveReconcile       CheckID = "audit_archive_reconciliation_complete"
	CheckIsolationClassify      CheckID = "isolation_signals_classified"
	CheckAuditIntegrityClassify CheckID = "audit_integrity_signals_classified"
	CheckAnomalyClosed          CheckID = "anomaly_report_closed"
	CheckNoUnexplainedIsolation CheckID = "no_unexplained_isolation_signal"
	CheckNoUnexplainedAudit     CheckID = "no_unexplained_audit_integrity_signal"
	CheckSecurityResidualReview CheckID = "security_residual_risk_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{CheckSharedWindowExports, CheckAuditChain, CheckArchiveReconcile, CheckIsolationClassify, CheckAuditIntegrityClassify, CheckAnomalyClosed, CheckNoUnexplainedIsolation, CheckNoUnexplainedAudit, CheckSecurityResidualReview}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                      string `json:"schema"`
	Classification              string `json:"classification"`
	Environment                 string `json:"environment"`
	ReviewID                    string `json:"review_id"`
	AnomalyEngineVersion        string `json:"anomaly_engine_version"`
	AuditChainVerifierVersion   string `json:"audit_chain_verifier_version"`
	ArchiveReconcilerVersion    string `json:"archive_reconciler_version"`
	SignalClassificationVersion string `json:"signal_classification_version"`
	ResidualRiskPolicyVersion   string `json:"residual_risk_policy_version"`

	InventoryID                 string `json:"inventory_id"`
	InventoryReceiptSHA256      string `json:"inventory_receipt_sha256"`
	PlanID                      string `json:"plan_id"`
	PlanReceiptSHA256           string `json:"plan_receipt_sha256"`
	ChangeID                    string `json:"change_id"`
	ChangeReceiptSHA256         string `json:"change_receipt_sha256"`
	ReleaseID                   string `json:"release_id"`
	ReleaseReceiptSHA256        string `json:"release_receipt_sha256"`
	BetaSLOObservationID        string `json:"beta_slo_observation_id"`
	BetaSLOReceiptSHA256        string `json:"beta_slo_receipt_sha256"`
	BetaOperationsAssessmentID  string `json:"beta_operations_assessment_id"`
	BetaOperationsReceiptSHA256 string `json:"beta_operations_receipt_sha256"`

	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	AuditDatabaseChainReportSHA256   string `json:"audit_database_chain_report_sha256"`
	AuditArchiveReconciliationSHA256 string `json:"audit_archive_reconciliation_sha256"`
	IsolationSignalExportSHA256      string `json:"isolation_signal_export_sha256"`
	AuditIntegritySignalExportSHA256 string `json:"audit_integrity_signal_export_sha256"`
	AnomalyReportSHA256              string `json:"anomaly_report_sha256"`
	ResidualRiskDecisionSHA256       string `json:"residual_risk_decision_sha256"`
	SecurityReviewSHA256             string `json:"security_review_sha256"`

	RiskPolicyApprovedAt time.Time `json:"risk_policy_approved_at"`
	SnapshotAt           time.Time `json:"snapshot_at"`
	ReviewedAt           time.Time `json:"reviewed_at"`
	GeneratedAt          time.Time `json:"generated_at"`

	AuditEventCount                       int     `json:"audit_event_count"`
	ChainVerifiedEventCount               int     `json:"chain_verified_event_count"`
	ChainBreakCount                       int     `json:"chain_break_count"`
	ArchiveExpectedCount                  int     `json:"archive_expected_count"`
	ArchiveVerifiedCount                  int     `json:"archive_verified_count"`
	ArchiveMissingCount                   int     `json:"archive_missing_count"`
	ArchiveChecksumMismatchCount          int     `json:"archive_checksum_mismatch_count"`
	IsolationSignalCount                  int     `json:"isolation_signal_count"`
	IsolationExplainedSignalCount         int     `json:"isolation_explained_signal_count"`
	IsolationUnexplainedSignalCount       int     `json:"isolation_unexplained_signal_count"`
	IsolationUnclassifiedSignalCount      int     `json:"isolation_unclassified_signal_count"`
	AuditIntegritySignalCount             int     `json:"audit_integrity_signal_count"`
	AuditIntegrityExplainedSignalCount    int     `json:"audit_integrity_explained_signal_count"`
	AuditIntegrityUnexplainedSignalCount  int     `json:"audit_integrity_unexplained_signal_count"`
	AuditIntegrityUnclassifiedSignalCount int     `json:"audit_integrity_unclassified_signal_count"`
	AnomalyFindingCount                   int     `json:"anomaly_finding_count"`
	ClosedAnomalyFindingCount             int     `json:"closed_anomaly_finding_count"`
	OpenAnomalyFindingCount               int     `json:"open_anomaly_finding_count"`
	Ready                                 bool    `json:"ready"`
	Checks                                []Check `json:"checks"`
}

type Receipt struct {
	Input
	Schema                               string    `json:"schema"`
	InputSHA256                          string    `json:"input_sha256"`
	CollectedAt                          time.Time `json:"collected_at"`
	ChainCoverageComplete                bool      `json:"chain_coverage_complete"`
	ArchiveReconciliationComplete        bool      `json:"archive_reconciliation_complete"`
	IsolationClassificationComplete      bool      `json:"isolation_classification_complete"`
	AuditIntegrityClassificationComplete bool      `json:"audit_integrity_classification_complete"`
	FindingClosureComplete               bool      `json:"finding_closure_complete"`
	IntegrityBreachCount                 int       `json:"integrity_breach_count"`
	UnexplainedSignalCount               int       `json:"unexplained_signal_count"`
	UnclassifiedSignalCount              int       `json:"unclassified_signal_count"`
	OpenFindingCount                     int       `json:"open_finding_count"`
	CheckCount                           int       `json:"check_count"`
	PassedCount                          int       `json:"passed_count"`
	FailedCount                          int       `json:"failed_count"`
	InconclusiveCount                    int       `json:"inconclusive_count"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

// LoadReady strictly reloads a normalized ready receipt against its exact
// ready beta SLO and operations prerequisites.
func LoadReady(path string, betaSLO betasloevidence.Receipt, betaSLODigest string, betaOperations betaoperationsevidence.Receipt, betaOperationsDigest string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "production_external" || receipt.Environment != "production" || !receipt.Ready || !receipt.ChainCoverageComplete || !receipt.ArchiveReconciliationComplete || !receipt.IsolationClassificationComplete || !receipt.AuditIntegrityClassificationComplete || !receipt.FindingClosureComplete || receipt.IntegrityBreachCount != 0 || receipt.UnexplainedSignalCount != 0 || receipt.UnclassifiedSignalCount != 0 || receipt.OpenFindingCount != 0 || receipt.CheckCount != len(requiredChecks) || receipt.PassedCount != len(requiredChecks) || receipt.FailedCount != 0 || receipt.InconclusiveCount != 0 {
		return Receipt{}, "", errors.New("beta integrity receipt is not ready")
	}
	if !allOpaque(receipt.ReviewID, receipt.AnomalyEngineVersion, receipt.AuditChainVerifierVersion, receipt.ArchiveReconcilerVersion, receipt.SignalClassificationVersion, receipt.ResidualRiskPolicyVersion, receipt.InventoryID, receipt.PlanID, receipt.ChangeID, receipt.ReleaseID, receipt.BetaSLOObservationID, receipt.BetaOperationsAssessmentID) || !allDigests(receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.BetaSLOReceiptSHA256, receipt.BetaOperationsReceiptSHA256, receipt.AuditDatabaseChainReportSHA256, receipt.AuditArchiveReconciliationSHA256, receipt.IsolationSignalExportSHA256, receipt.AuditIntegritySignalExportSHA256, receipt.AnomalyReportSHA256, receipt.ResidualRiskDecisionSHA256, receipt.SecurityReviewSHA256, receipt.InputSHA256, digest) {
		return Receipt{}, "", errors.New("beta integrity receipt identity or binding is invalid")
	}
	if betaSLO.Schema != betasloevidence.ReceiptSchemaV1 || !betaSLO.Ready || betaOperations.Schema != betaoperationsevidence.ReceiptSchemaV1 || !betaOperations.Ready || !digestPattern.MatchString(betaSLODigest) || !digestPattern.MatchString(betaOperationsDigest) || receipt.InventoryID != betaSLO.InventoryID || receipt.InventoryReceiptSHA256 != betaSLO.InventoryReceiptSHA256 || receipt.PlanID != betaSLO.PlanID || receipt.PlanReceiptSHA256 != betaSLO.PlanReceiptSHA256 || receipt.ChangeID != betaSLO.ChangeID || receipt.ChangeReceiptSHA256 != betaSLO.ChangeReceiptSHA256 || receipt.ReleaseID != betaSLO.ReleaseID || receipt.ReleaseReceiptSHA256 != betaSLO.ReleaseReceiptSHA256 || receipt.BetaSLOObservationID != betaSLO.ObservationID || receipt.BetaSLOReceiptSHA256 != betaSLODigest || receipt.BetaOperationsAssessmentID != betaOperations.AssessmentID || receipt.BetaOperationsReceiptSHA256 != betaOperationsDigest || !receipt.WindowStart.Equal(betaSLO.WindowStart) || !receipt.WindowEnd.Equal(betaSLO.WindowEnd) || !receipt.WindowStart.Equal(betaOperations.WindowStart) || !receipt.WindowEnd.Equal(betaOperations.WindowEnd) {
		return Receipt{}, "", errors.New("beta integrity receipt prerequisite binding is invalid")
	}
	if err := validateCounts(receipt.Input); err != nil {
		return Receipt{}, "", err
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil || passed != len(requiredChecks) || failed != 0 || inconclusive != 0 {
		return Receipt{}, "", errors.New("beta integrity receipt checks are invalid")
	}
	approved, start, end := receipt.RiskPolicyApprovedAt.UTC(), receipt.WindowStart.UTC(), receipt.WindowEnd.UTC()
	snapshot, reviewed, generated, collected := receipt.SnapshotAt.UTC(), receipt.ReviewedAt.UTC(), receipt.GeneratedAt.UTC(), receipt.CollectedAt.UTC()
	if approved.IsZero() || approved.After(start) || !end.After(start) || snapshot.Before(end) || snapshot.Sub(end) > maximumSnapshotDelay || reviewed.Before(snapshot) || reviewed.Sub(snapshot) > maximumReviewDelay || generated.Before(reviewed) || collected.Before(generated) || collected.IsZero() {
		return Receipt{}, "", errors.New("beta integrity receipt timeline is invalid")
	}
	receipt.RiskPolicyApprovedAt, receipt.WindowStart, receipt.WindowEnd = approved, start, end
	receipt.SnapshotAt, receipt.ReviewedAt, receipt.GeneratedAt, receipt.CollectedAt, receipt.Checks = snapshot, reviewed, generated, collected, checks
	return receipt, digest, nil
}

func Collect(inventoryPath, planPath, changePath, releasePath, betaSLOPath, betaOperationsPath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedReleaseForEnvironment(releasePath, "production")
	if err != nil {
		return Receipt{}, fmt.Errorf("load production release: %w", err)
	}
	betaSLO, betaSLODigest, err := betasloevidence.LoadReady(betaSLOPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready beta SLO receipt: %w", err)
	}
	betaOperations, betaOperationsDigest, err := betaoperationsevidence.LoadReady(betaOperationsPath, betaSLO, betaSLODigest)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready beta operations receipt: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, betaSLO, betaSLODigest, betaOperations, betaOperationsDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, betaSLO betasloevidence.Receipt, betaSLODigest string, betaOperations betaoperationsevidence.Receipt, betaOperationsDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if err := validateChain(inventory, plan, change, release, releaseDigest, betaSLO, betaSLODigest, betaOperations, betaOperationsDigest); err != nil {
		return Receipt{}, err
	}
	if err := validateIdentity(input, inventory, plan, change, release, releaseDigest, betaSLO, betaSLODigest, betaOperations, betaOperationsDigest, inputDigest); err != nil {
		return Receipt{}, err
	}
	if now.IsZero() {
		return Receipt{}, errors.New("beta integrity collection time is invalid")
	}
	now = now.UTC()
	approved, start, end := input.RiskPolicyApprovedAt.UTC(), input.WindowStart.UTC(), input.WindowEnd.UTC()
	snapshot, reviewed, generated := input.SnapshotAt.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if start != betaSLO.WindowStart.UTC() || end != betaSLO.WindowEnd.UTC() || start != betaOperations.WindowStart.UTC() || end != betaOperations.WindowEnd.UTC() || approved.IsZero() || approved.After(start) || snapshot.Before(end) || snapshot.Sub(end) > maximumSnapshotDelay || reviewed.Before(snapshot) || reviewed.Sub(snapshot) > maximumReviewDelay || generated.Before(reviewed) || generated.Before(now.Add(-maximumCollectionAge)) || generated.After(now) {
		return Receipt{}, errors.New("beta integrity timeline or shared window is invalid")
	}
	if err := validateCounts(input); err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	chainComplete := input.ChainVerifiedEventCount == input.AuditEventCount && input.ChainBreakCount == 0
	archiveComplete := input.ArchiveMissingCount == 0 && input.ArchiveChecksumMismatchCount == 0
	isolationClassified := input.IsolationUnclassifiedSignalCount == 0
	auditClassified := input.AuditIntegrityUnclassifiedSignalCount == 0
	findingsClosed := input.OpenAnomalyFindingCount == 0
	integrityBreaches := input.ChainBreakCount + input.ArchiveMissingCount + input.ArchiveChecksumMismatchCount
	unexplained := input.IsolationUnexplainedSignalCount + input.AuditIntegrityUnexplainedSignalCount
	unclassified := input.IsolationUnclassifiedSignalCount + input.AuditIntegrityUnclassifiedSignalCount
	contradictions := []struct {
		bad bool
		id  CheckID
	}{
		{!chainComplete, CheckAuditChain}, {!archiveComplete, CheckArchiveReconcile}, {!isolationClassified, CheckIsolationClassify}, {!auditClassified, CheckAuditIntegrityClassify}, {!findingsClosed, CheckAnomalyClosed},
		{input.IsolationUnexplainedSignalCount > 0, CheckNoUnexplainedIsolation}, {input.AuditIntegrityUnexplainedSignalCount > 0, CheckNoUnexplainedAudit},
	}
	for _, contradiction := range contradictions {
		if contradiction.bad && outcomeFor(checks, contradiction.id) != OutcomeFailed {
			return Receipt{}, errors.New("beta integrity check contradicts aggregate evidence")
		}
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && chainComplete && archiveComplete && isolationClassified && auditClassified && findingsClosed && unexplained == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("beta integrity readiness contradicts evidence")
	}
	input.Schema = ReceiptSchemaV1
	input.RiskPolicyApprovedAt, input.WindowStart, input.WindowEnd = approved, start, end
	input.SnapshotAt, input.ReviewedAt, input.GeneratedAt, input.Checks = snapshot, reviewed, generated, checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, ChainCoverageComplete: chainComplete, ArchiveReconciliationComplete: archiveComplete, IsolationClassificationComplete: isolationClassified, AuditIntegrityClassificationComplete: auditClassified, FindingClosureComplete: findingsClosed, IntegrityBreachCount: integrityBreaches, UnexplainedSignalCount: unexplained, UnclassifiedSignalCount: unclassified, OpenFindingCount: input.OpenAnomalyFindingCount, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func validateChain(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, betaSLO betasloevidence.Receipt, betaSLODigest string, betaOperations betaoperationsevidence.Receipt, betaOperationsDigest string) error {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Production || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return errors.New("beta integrity production platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "production" || release.Namespace != "agent-memory-production" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return errors.New("beta integrity production release is invalid or unready")
	}
	if betaSLO.Schema != betasloevidence.ReceiptSchemaV1 || !betaSLO.Ready || !digestPattern.MatchString(betaSLODigest) || betaSLO.InventoryID != inventory.InventoryID || betaSLO.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || betaSLO.PlanID != plan.PlanID || betaSLO.PlanReceiptSHA256 != plan.ReceiptSHA256 || betaSLO.ChangeID != change.ChangeID || betaSLO.ChangeReceiptSHA256 != change.ReceiptSHA256 || betaSLO.ReleaseID != release.ReleaseID || betaSLO.ReleaseReceiptSHA256 != releaseDigest {
		return errors.New("beta integrity beta SLO receipt is invalid or misbound")
	}
	if betaOperations.Schema != betaoperationsevidence.ReceiptSchemaV1 || !betaOperations.Ready || !digestPattern.MatchString(betaOperationsDigest) || betaOperations.InventoryID != inventory.InventoryID || betaOperations.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || betaOperations.PlanID != plan.PlanID || betaOperations.PlanReceiptSHA256 != plan.ReceiptSHA256 || betaOperations.ChangeID != change.ChangeID || betaOperations.ChangeReceiptSHA256 != change.ReceiptSHA256 || betaOperations.ReleaseID != release.ReleaseID || betaOperations.ReleaseReceiptSHA256 != releaseDigest || betaOperations.BetaSLOObservationID != betaSLO.ObservationID || betaOperations.BetaSLOReceiptSHA256 != betaSLODigest || !betaOperations.WindowStart.Equal(betaSLO.WindowStart) || !betaOperations.WindowEnd.Equal(betaSLO.WindowEnd) {
		return errors.New("beta integrity beta operations receipt is invalid or misbound")
	}
	return nil
}

func validateIdentity(input Input, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, betaSLO betasloevidence.Receipt, betaSLODigest string, betaOperations betaoperationsevidence.Receipt, betaOperationsDigest, inputDigest string) error {
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.ReviewID, input.AnomalyEngineVersion, input.AuditChainVerifierVersion, input.ArchiveReconcilerVersion, input.SignalClassificationVersion, input.ResidualRiskPolicyVersion) {
		return errors.New("beta integrity identity is invalid")
	}
	if input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || input.BetaSLOObservationID != betaSLO.ObservationID || input.BetaSLOReceiptSHA256 != betaSLODigest || input.BetaOperationsAssessmentID != betaOperations.AssessmentID || input.BetaOperationsReceiptSHA256 != betaOperationsDigest {
		return errors.New("beta integrity platform or prerequisite binding is invalid")
	}
	if !allDigests(input.AuditDatabaseChainReportSHA256, input.AuditArchiveReconciliationSHA256, input.IsolationSignalExportSHA256, input.AuditIntegritySignalExportSHA256, input.AnomalyReportSHA256, input.ResidualRiskDecisionSHA256, input.SecurityReviewSHA256, inputDigest) {
		return errors.New("beta integrity artifact binding is invalid")
	}
	return nil
}

func validateCounts(input Input) error {
	counts := []int{input.AuditEventCount, input.ChainVerifiedEventCount, input.ChainBreakCount, input.ArchiveExpectedCount, input.ArchiveVerifiedCount, input.ArchiveMissingCount, input.ArchiveChecksumMismatchCount, input.IsolationSignalCount, input.IsolationExplainedSignalCount, input.IsolationUnexplainedSignalCount, input.IsolationUnclassifiedSignalCount, input.AuditIntegritySignalCount, input.AuditIntegrityExplainedSignalCount, input.AuditIntegrityUnexplainedSignalCount, input.AuditIntegrityUnclassifiedSignalCount, input.AnomalyFindingCount, input.ClosedAnomalyFindingCount, input.OpenAnomalyFindingCount}
	for _, count := range counts {
		if count < 0 || count > maximumCount {
			return errors.New("beta integrity count is invalid")
		}
	}
	if input.AuditEventCount == 0 || input.ChainVerifiedEventCount > input.AuditEventCount || input.ChainBreakCount > input.ChainVerifiedEventCount || input.ArchiveExpectedCount != input.AuditEventCount || int64(input.ArchiveVerifiedCount)+int64(input.ArchiveMissingCount)+int64(input.ArchiveChecksumMismatchCount) != int64(input.ArchiveExpectedCount) || int64(input.IsolationExplainedSignalCount)+int64(input.IsolationUnexplainedSignalCount)+int64(input.IsolationUnclassifiedSignalCount) != int64(input.IsolationSignalCount) || int64(input.AuditIntegrityExplainedSignalCount)+int64(input.AuditIntegrityUnexplainedSignalCount)+int64(input.AuditIntegrityUnclassifiedSignalCount) != int64(input.AuditIntegritySignalCount) || int64(input.ClosedAnomalyFindingCount)+int64(input.OpenAnomalyFindingCount) != int64(input.AnomalyFindingCount) {
		return errors.New("beta integrity aggregate count is contradictory")
	}
	integrityBreaches := int64(input.ChainBreakCount) + int64(input.ArchiveMissingCount) + int64(input.ArchiveChecksumMismatchCount)
	if integrityBreaches > maximumCount || int64(input.AuditIntegritySignalCount) < integrityBreaches || int64(input.IsolationUnexplainedSignalCount)+int64(input.AuditIntegrityUnexplainedSignalCount) > maximumCount || int64(input.IsolationUnclassifiedSignalCount)+int64(input.AuditIntegrityUnclassifiedSignalCount) > maximumCount {
		return errors.New("beta integrity derived count is invalid")
	}
	return nil
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("beta integrity checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(input))
	for _, check := range input {
		if _, duplicate := byID[check.ID]; duplicate || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("beta integrity check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("beta integrity check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("beta integrity check outcome is invalid")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}

func outcomeFor(checks []Check, id CheckID) Outcome {
	for _, check := range checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}
func allDigests(values ...string) bool {
	for _, value := range values {
		if !digestPattern.MatchString(value) {
			return false
		}
	}
	return true
}
func allOpaque(values ...string) bool {
	for _, value := range values {
		if !opaquePattern.MatchString(value) || strings.Contains(value, "@") {
			return false
		}
	}
	return true
}

func decodeStrictRegular(path string, destination any) (string, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("beta integrity input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open beta integrity input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("beta integrity input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read beta integrity input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("beta integrity input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("beta integrity input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("beta integrity input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("beta integrity input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("beta integrity receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("beta integrity receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect beta integrity receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-beta-integrity-*")
}
