// Package publicbetagateevidence normalizes shared-window production evidence
// for the CP11-B public-beta gate.
package publicbetagateevidence

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaintegrityevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaoperationsevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billingreconciliation"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

const (
	InputSchemaV1        = "agent-memory-production-public-beta-gate-input-v1"
	ReceiptSchemaV1      = "agent-memory-production-public-beta-gate-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumCount         = 100_000_000
	maximumMoneyMicroUSD = int64(1_000_000_000_000_000)
	maximumCollectionAge = 24 * time.Hour
)

type CheckID string
type Outcome string

const (
	CheckPrerequisitesReady CheckID = "prerequisite_receipts_ready"
	CheckSharedWindow       CheckID = "shared_window_complete"
	CheckBillingGate        CheckID = "billing_gate_passed"
	CheckAbuseExport        CheckID = "abuse_export_complete"
	CheckAbuseClassified    CheckID = "abuse_findings_classified"
	CheckNoBlockingAbuse    CheckID = "no_launch_blocking_abuse"
	CheckCostExport         CheckID = "cost_export_complete"
	CheckCostCeiling        CheckID = "cost_ceiling_passed"
	CheckDomainReview       CheckID = "domain_owner_review_complete"
	OutcomePassed           Outcome = "passed"
	OutcomeFailed           Outcome = "failed"
	OutcomeInconclusive     Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{CheckPrerequisitesReady, CheckSharedWindow, CheckBillingGate, CheckAbuseExport, CheckAbuseClassified, CheckNoBlockingAbuse, CheckCostExport, CheckCostCeiling, CheckDomainReview}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema              string `json:"schema"`
	Classification      string `json:"classification"`
	Environment         string `json:"environment"`
	GateReviewID        string `json:"gate_review_id"`
	AbusePolicyVersion  string `json:"abuse_policy_version"`
	CostPolicyVersion   string `json:"cost_policy_version"`
	ReviewPolicyVersion string `json:"review_policy_version"`

	InventoryID            string `json:"inventory_id"`
	InventoryReceiptSHA256 string `json:"inventory_receipt_sha256"`
	PlanID                 string `json:"plan_id"`
	PlanReceiptSHA256      string `json:"plan_receipt_sha256"`
	ChangeID               string `json:"change_id"`
	ChangeReceiptSHA256    string `json:"change_receipt_sha256"`
	ReleaseID              string `json:"release_id"`
	ReleaseReceiptSHA256   string `json:"release_receipt_sha256"`

	BillingReconciliationID     string `json:"billing_reconciliation_id"`
	BillingReceiptSHA256        string `json:"billing_receipt_sha256"`
	BetaSLOObservationID        string `json:"beta_slo_observation_id"`
	BetaSLOReceiptSHA256        string `json:"beta_slo_receipt_sha256"`
	BetaOperationsAssessmentID  string `json:"beta_operations_assessment_id"`
	BetaOperationsReceiptSHA256 string `json:"beta_operations_receipt_sha256"`
	BetaIntegrityReviewID       string `json:"beta_integrity_review_id"`
	BetaIntegrityReceiptSHA256  string `json:"beta_integrity_receipt_sha256"`

	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	AbuseExportSHA256       string `json:"abuse_export_sha256"`
	CostExportSHA256        string `json:"cost_export_sha256"`
	TargetDecisionSHA256    string `json:"target_decision_sha256"`
	DomainOwnerReviewSHA256 string `json:"domain_owner_review_sha256"`

	TargetApprovedAt time.Time `json:"target_approved_at"`
	SnapshotAt       time.Time `json:"snapshot_at"`
	ReviewedAt       time.Time `json:"reviewed_at"`
	GeneratedAt      time.Time `json:"generated_at"`

	SignupAttemptCount                  int     `json:"signup_attempt_count"`
	AbuseFindingCount                   int     `json:"abuse_finding_count"`
	ClosedAbuseFindingCount             int     `json:"closed_abuse_finding_count"`
	OpenNonblockingAbuseFindingCount    int     `json:"open_nonblocking_abuse_finding_count"`
	OpenLaunchBlockingAbuseFindingCount int     `json:"open_launch_blocking_abuse_finding_count"`
	UnclassifiedAbuseFindingCount       int     `json:"unclassified_abuse_finding_count"`
	ActiveTenantCount                   int     `json:"active_tenant_count"`
	ActualWindowCostMicroUSD            int64   `json:"actual_window_cost_microusd"`
	MaximumWindowCostMicroUSD           int64   `json:"maximum_window_cost_microusd"`
	MaximumCostPerActiveTenantMicroUSD  int64   `json:"maximum_cost_per_active_tenant_microusd"`
	Ready                               bool    `json:"ready"`
	Checks                              []Check `json:"checks"`
}

type Receipt struct {
	Input
	Schema                            string    `json:"schema"`
	InputSHA256                       string    `json:"input_sha256"`
	CollectedAt                       time.Time `json:"collected_at"`
	ActualCostPerActiveTenantMicroUSD int64     `json:"actual_cost_per_active_tenant_microusd"`
	AbuseClassificationComplete       bool      `json:"abuse_classification_complete"`
	CostWithinCeiling                 bool      `json:"cost_within_ceiling"`
	CheckCount                        int       `json:"check_count"`
	PassedCount                       int       `json:"passed_count"`
	FailedCount                       int       `json:"failed_count"`
	InconclusiveCount                 int       `json:"inconclusive_count"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func LoadReady(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "production_external" || receipt.Environment != "production" || !receipt.Ready || !receipt.AbuseClassificationComplete || !receipt.CostWithinCeiling || receipt.CheckCount != len(requiredChecks) || receipt.PassedCount != len(requiredChecks) || receipt.FailedCount != 0 || receipt.InconclusiveCount != 0 {
		return Receipt{}, "", errors.New("public beta gate receipt is not ready")
	}
	if !allOpaque(receipt.GateReviewID, receipt.AbusePolicyVersion, receipt.CostPolicyVersion, receipt.ReviewPolicyVersion, receipt.InventoryID, receipt.PlanID, receipt.ChangeID, receipt.ReleaseID, receipt.BillingReconciliationID, receipt.BetaSLOObservationID, receipt.BetaOperationsAssessmentID, receipt.BetaIntegrityReviewID) || !allDigests(receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.BillingReceiptSHA256, receipt.BetaSLOReceiptSHA256, receipt.BetaOperationsReceiptSHA256, receipt.BetaIntegrityReceiptSHA256, receipt.AbuseExportSHA256, receipt.CostExportSHA256, receipt.TargetDecisionSHA256, receipt.DomainOwnerReviewSHA256, receipt.InputSHA256, digest) {
		return Receipt{}, "", errors.New("public beta gate receipt identity or binding is invalid")
	}
	if err := validateAggregates(receipt.Input); err != nil {
		return Receipt{}, "", err
	}
	actualPerTenant := ceilingDivide(receipt.ActualWindowCostMicroUSD, int64(receipt.ActiveTenantCount))
	if receipt.UnclassifiedAbuseFindingCount != 0 || receipt.OpenLaunchBlockingAbuseFindingCount != 0 || receipt.ActualCostPerActiveTenantMicroUSD != actualPerTenant || receipt.ActualWindowCostMicroUSD > receipt.MaximumWindowCostMicroUSD || actualPerTenant > receipt.MaximumCostPerActiveTenantMicroUSD {
		return Receipt{}, "", errors.New("public beta gate receipt derivation is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil || passed != len(requiredChecks) || failed != 0 || inconclusive != 0 {
		return Receipt{}, "", errors.New("public beta gate receipt checks are invalid")
	}
	if !receipt.WindowEnd.After(receipt.WindowStart) || receipt.TargetApprovedAt.After(receipt.WindowStart) || receipt.SnapshotAt.Before(receipt.WindowEnd) || receipt.ReviewedAt.Before(receipt.SnapshotAt) || receipt.GeneratedAt.Before(receipt.ReviewedAt) || receipt.CollectedAt.Before(receipt.GeneratedAt) || receipt.CollectedAt.IsZero() {
		return Receipt{}, "", errors.New("public beta gate receipt timeline is invalid")
	}
	receipt.Checks = checks
	return receipt, digest, nil
}

func Collect(inventoryPath, planPath, changePath, releasePath, billingPath, sloPath, operationsPath, integrityPath, inputPath string, now time.Time) (Receipt, error) {
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
	billing, billingDigest, err := billingreconciliation.LoadReady(billingPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready billing receipt: %w", err)
	}
	slo, sloDigest, err := betasloevidence.LoadReady(sloPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready beta SLO receipt: %w", err)
	}
	operations, operationsDigest, err := betaoperationsevidence.LoadReady(operationsPath, slo, sloDigest)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready beta operations receipt: %w", err)
	}
	integrity, integrityDigest, err := betaintegrityevidence.LoadReady(integrityPath, slo, sloDigest, operations, operationsDigest)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready beta integrity receipt: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, billing, billingDigest, slo, sloDigest, operations, operationsDigest, integrity, integrityDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, billing billingreconciliation.Receipt, billingDigest string, slo betasloevidence.Receipt, sloDigest string, operations betaoperationsevidence.Receipt, operationsDigest string, integrity betaintegrityevidence.Receipt, integrityDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if err := validateChain(inventory, plan, change, release, releaseDigest, billing, billingDigest, slo, sloDigest, operations, operationsDigest, integrity, integrityDigest); err != nil {
		return Receipt{}, err
	}
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.GateReviewID, input.AbusePolicyVersion, input.CostPolicyVersion, input.ReviewPolicyVersion) {
		return Receipt{}, errors.New("public beta gate identity is invalid")
	}
	if input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || input.BillingReconciliationID != billing.ReconciliationID || input.BillingReceiptSHA256 != billingDigest || input.BetaSLOObservationID != slo.ObservationID || input.BetaSLOReceiptSHA256 != sloDigest || input.BetaOperationsAssessmentID != operations.AssessmentID || input.BetaOperationsReceiptSHA256 != operationsDigest || input.BetaIntegrityReviewID != integrity.ReviewID || input.BetaIntegrityReceiptSHA256 != integrityDigest {
		return Receipt{}, errors.New("public beta gate platform or prerequisite binding is invalid")
	}
	if !allDigests(input.AbuseExportSHA256, input.CostExportSHA256, input.TargetDecisionSHA256, input.DomainOwnerReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("public beta gate artifact binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("public beta gate collection time is invalid")
	}
	now = now.UTC()
	start, end := input.WindowStart.UTC(), input.WindowEnd.UTC()
	approved, snapshot, reviewed, generated := input.TargetApprovedAt.UTC(), input.SnapshotAt.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if start != slo.WindowStart.UTC() || end != slo.WindowEnd.UTC() || start != billing.PeriodStart.UTC() || end != billing.PeriodEnd.UTC() || approved.IsZero() || approved.After(start) || snapshot.Before(end) || snapshot.Sub(end) > maximumCollectionAge || reviewed.Before(snapshot) || reviewed.Sub(snapshot) > maximumCollectionAge || generated.Before(reviewed) || generated.Before(now.Add(-maximumCollectionAge)) || generated.After(now) {
		return Receipt{}, errors.New("public beta gate timeline or shared window is invalid")
	}
	if err := validateAggregates(input); err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	abuseClassified := input.UnclassifiedAbuseFindingCount == 0
	noBlockingAbuse := input.OpenLaunchBlockingAbuseFindingCount == 0
	actualPerTenant := ceilingDivide(input.ActualWindowCostMicroUSD, int64(input.ActiveTenantCount))
	costWithin := input.ActualWindowCostMicroUSD <= input.MaximumWindowCostMicroUSD && actualPerTenant <= input.MaximumCostPerActiveTenantMicroUSD
	for _, contradiction := range []struct {
		bad bool
		id  CheckID
	}{
		{!abuseClassified, CheckAbuseClassified}, {!noBlockingAbuse, CheckNoBlockingAbuse}, {!costWithin, CheckCostCeiling},
	} {
		if contradiction.bad && outcomeFor(checks, contradiction.id) != OutcomeFailed {
			return Receipt{}, errors.New("public beta gate check contradicts aggregates")
		}
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && abuseClassified && noBlockingAbuse && costWithin
	if input.Ready != ready {
		return Receipt{}, errors.New("public beta gate readiness contradicts evidence")
	}
	input.Schema = ReceiptSchemaV1
	input.WindowStart, input.WindowEnd, input.TargetApprovedAt = start, end, approved
	input.SnapshotAt, input.ReviewedAt, input.GeneratedAt, input.Checks = snapshot, reviewed, generated, checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, ActualCostPerActiveTenantMicroUSD: actualPerTenant, AbuseClassificationComplete: abuseClassified, CostWithinCeiling: costWithin, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func validateChain(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, billing billingreconciliation.Receipt, billingDigest string, slo betasloevidence.Receipt, sloDigest string, operations betaoperationsevidence.Receipt, operationsDigest string, integrity betaintegrityevidence.Receipt, integrityDigest string) error {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Production || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return errors.New("public beta gate production platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "production" || release.Namespace != "agent-memory-production" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return errors.New("public beta gate production release is invalid or unready")
	}
	if billing.Schema != billingreconciliation.ReceiptSchemaV1 || slo.Schema != betasloevidence.ReceiptSchemaV1 || operations.Schema != betaoperationsevidence.ReceiptSchemaV1 || integrity.Schema != betaintegrityevidence.ReceiptSchemaV1 || !allDigests(billingDigest, sloDigest, operationsDigest, integrityDigest) || !billing.Ready || !slo.Ready || !operations.Ready || !integrity.Ready {
		return errors.New("public beta gate prerequisite receipt is unready")
	}
	for _, binding := range []struct{ inventoryID, inventoryDigest, planID, planDigest, changeID, changeDigest, releaseID, releaseDigest string }{
		{billing.InventoryID, billing.InventoryReceiptSHA256, billing.PlanID, billing.PlanReceiptSHA256, billing.ChangeID, billing.ChangeReceiptSHA256, billing.ReleaseID, billing.ReleaseReceiptSHA256},
		{slo.InventoryID, slo.InventoryReceiptSHA256, slo.PlanID, slo.PlanReceiptSHA256, slo.ChangeID, slo.ChangeReceiptSHA256, slo.ReleaseID, slo.ReleaseReceiptSHA256},
		{operations.InventoryID, operations.InventoryReceiptSHA256, operations.PlanID, operations.PlanReceiptSHA256, operations.ChangeID, operations.ChangeReceiptSHA256, operations.ReleaseID, operations.ReleaseReceiptSHA256},
		{integrity.InventoryID, integrity.InventoryReceiptSHA256, integrity.PlanID, integrity.PlanReceiptSHA256, integrity.ChangeID, integrity.ChangeReceiptSHA256, integrity.ReleaseID, integrity.ReleaseReceiptSHA256},
	} {
		if binding.inventoryID != inventory.InventoryID || binding.inventoryDigest != inventory.ReceiptSHA256 || binding.planID != plan.PlanID || binding.planDigest != plan.ReceiptSHA256 || binding.changeID != change.ChangeID || binding.changeDigest != change.ReceiptSHA256 || binding.releaseID != release.ReleaseID || binding.releaseDigest != releaseDigest {
			return errors.New("public beta gate prerequisite receipt is misbound")
		}
	}
	return nil
}

func validateAggregates(input Input) error {
	counts := []int{input.SignupAttemptCount, input.AbuseFindingCount, input.ClosedAbuseFindingCount, input.OpenNonblockingAbuseFindingCount, input.OpenLaunchBlockingAbuseFindingCount, input.UnclassifiedAbuseFindingCount, input.ActiveTenantCount}
	for _, count := range counts {
		if count < 0 || count > maximumCount {
			return errors.New("public beta gate count is invalid")
		}
	}
	if input.SignupAttemptCount == 0 || input.ActiveTenantCount == 0 || int64(input.ClosedAbuseFindingCount)+int64(input.OpenNonblockingAbuseFindingCount)+int64(input.OpenLaunchBlockingAbuseFindingCount)+int64(input.UnclassifiedAbuseFindingCount) != int64(input.AbuseFindingCount) {
		return errors.New("public beta gate coverage or finding reconciliation is invalid")
	}
	if input.ActualWindowCostMicroUSD < 0 || input.ActualWindowCostMicroUSD > maximumMoneyMicroUSD || input.MaximumWindowCostMicroUSD <= 0 || input.MaximumWindowCostMicroUSD > maximumMoneyMicroUSD || input.MaximumCostPerActiveTenantMicroUSD <= 0 || input.MaximumCostPerActiveTenantMicroUSD > maximumMoneyMicroUSD {
		return errors.New("public beta gate cost is invalid")
	}
	return nil
}

func ceilingDivide(value, divisor int64) int64 {
	quotient := value / divisor
	if value%divisor != 0 {
		quotient++
	}
	return quotient
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("public beta gate checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(input))
	for _, check := range input {
		if _, duplicate := byID[check.ID]; duplicate || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("public beta gate check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("public beta gate check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("public beta gate check outcome is invalid")
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
		return "", errors.New("public beta gate input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open public beta gate input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("public beta gate input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return "", errors.New("read public beta gate input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("public beta gate input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("public beta gate input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("public beta gate input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("public beta gate input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("public beta gate receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("public beta gate receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect public beta gate receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-public-beta-gate-*")
}
