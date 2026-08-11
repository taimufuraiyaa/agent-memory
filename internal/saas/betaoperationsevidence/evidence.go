// Package betaoperationsevidence normalizes content-free production beta
// deletion, notice, anomaly, and support operations evidence for P11.3-B.
package betaoperationsevidence

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

const (
	InputSchemaV1   = "agent-memory-production-beta-operations-input-v1"
	ReceiptSchemaV1 = "agent-memory-production-beta-operations-receipt-v1"

	maximumInputBytes       = 128 << 10
	maximumCollectionAge    = 24 * time.Hour
	maximumAggregationDelay = 24 * time.Hour
	maximumCount            = 100_000_000
	maximumTargetSeconds    = int64(31 * 24 * 60 * 60)
	maximumObservedSeconds  = int64(365 * 24 * 60 * 60)
)

type DomainID string
type CheckID string
type Outcome string

const (
	DomainDeletion     DomainID = "deletion"
	DomainRightsNotice DomainID = "rights_notice"
	DomainAnomalyAlert DomainID = "anomaly_alert"
	DomainSupportCase  DomainID = "support_case"

	CheckSharedWindowExports CheckID = "shared_window_exports_complete"
	CheckTargetSamplePolicy  CheckID = "target_sample_policy_approved"
	CheckAggregateReconcile  CheckID = "aggregate_reconciliation_complete"
	CheckSampleCoverage      CheckID = "case_sample_coverage_complete"
	CheckDeletionTarget      CheckID = "deletion_operations_within_target"
	CheckNoticeTarget        CheckID = "notice_operations_within_target"
	CheckAnomalyTarget       CheckID = "anomaly_operations_within_target"
	CheckSupportTarget       CheckID = "support_operations_within_target"
	CheckAccountableReview   CheckID = "privacy_security_support_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredDomains = []DomainID{DomainDeletion, DomainRightsNotice, DomainAnomalyAlert, DomainSupportCase}
	requiredChecks  = []CheckID{CheckSharedWindowExports, CheckTargetSamplePolicy, CheckAggregateReconcile, CheckSampleCoverage, CheckDeletionTarget, CheckNoticeTarget, CheckAnomalyTarget, CheckSupportTarget, CheckAccountableReview}
)

type DomainAggregate struct {
	ID                             DomainID `json:"id"`
	DueCaseCount                   int      `json:"due_case_count"`
	WithinTargetCount              int      `json:"within_target_count"`
	LateCaseCount                  int      `json:"late_case_count"`
	OverdueOpenCount               int      `json:"overdue_open_count"`
	RequiredSampleCount            int      `json:"required_sample_count"`
	SampledCaseCount               int      `json:"sampled_case_count"`
	MatchedSampleCount             int      `json:"matched_sample_count"`
	MaximumTargetSeconds           int64    `json:"maximum_target_seconds"`
	MaximumObservedDurationSeconds int64    `json:"maximum_observed_duration_seconds"`
	EvidenceSHA256                 string   `json:"evidence_sha256"`
}

type DomainResult struct {
	DomainAggregate
	Reconciled             bool `json:"reconciled"`
	SampleCoverageComplete bool `json:"sample_coverage_complete"`
	TargetMet              bool `json:"target_met"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                 string `json:"schema"`
	Classification         string `json:"classification"`
	Environment            string `json:"environment"`
	AssessmentID           string `json:"assessment_id"`
	AggregatePolicyVersion string `json:"aggregate_policy_version"`
	SamplePolicyVersion    string `json:"sample_policy_version"`
	TargetVersion          string `json:"target_version"`
	SupportExportVersion   string `json:"support_export_version"`

	InventoryID            string `json:"inventory_id"`
	InventoryReceiptSHA256 string `json:"inventory_receipt_sha256"`
	PlanID                 string `json:"plan_id"`
	PlanReceiptSHA256      string `json:"plan_receipt_sha256"`
	ChangeID               string `json:"change_id"`
	ChangeReceiptSHA256    string `json:"change_receipt_sha256"`
	ReleaseID              string `json:"release_id"`
	ReleaseReceiptSHA256   string `json:"release_receipt_sha256"`
	BetaSLOObservationID   string `json:"beta_slo_observation_id"`
	BetaSLOReceiptSHA256   string `json:"beta_slo_receipt_sha256"`

	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	DeletionReceiptExportSHA256        string `json:"deletion_receipt_export_sha256"`
	NoticeCaseExportSHA256             string `json:"notice_case_export_sha256"`
	AnomalyCaseExportSHA256            string `json:"anomaly_case_export_sha256"`
	SupportCaseExportSHA256            string `json:"support_case_export_sha256"`
	SampleManifestSHA256               string `json:"sample_manifest_sha256"`
	TargetDecisionSHA256               string `json:"target_decision_sha256"`
	PrivacySecuritySupportReviewSHA256 string `json:"privacy_security_support_review_sha256"`

	TargetApprovedAt time.Time         `json:"target_approved_at"`
	AggregatedAt     time.Time         `json:"aggregated_at"`
	ReviewedAt       time.Time         `json:"reviewed_at"`
	GeneratedAt      time.Time         `json:"generated_at"`
	Ready            bool              `json:"ready"`
	Domains          []DomainAggregate `json:"domains"`
	Checks           []Check           `json:"checks"`
}

type Receipt struct {
	Input
	Schema                 string         `json:"schema"`
	InputSHA256            string         `json:"input_sha256"`
	CollectedAt            time.Time      `json:"collected_at"`
	DueCaseCount           int            `json:"due_case_count"`
	WithinTargetCount      int            `json:"within_target_count"`
	LateCaseCount          int            `json:"late_case_count"`
	OverdueOpenCount       int            `json:"overdue_open_count"`
	SampleCoverageComplete bool           `json:"sample_coverage_complete"`
	SampleShortfallCount   int            `json:"sample_shortfall_count"`
	TargetBreachCount      int            `json:"target_breach_count"`
	DomainResults          []DomainResult `json:"domain_results"`
	CheckCount             int            `json:"check_count"`
	PassedCount            int            `json:"passed_count"`
	FailedCount            int            `json:"failed_count"`
	InconclusiveCount      int            `json:"inconclusive_count"`
}

func RequiredDomains() []DomainID { return append([]DomainID(nil), requiredDomains...) }
func RequiredChecks() []CheckID   { return append([]CheckID(nil), requiredChecks...) }

// LoadReady strictly reloads a normalized ready receipt against its exact
// ready beta SLO prerequisite and returns the receipt's file digest for
// downstream same-window evidence binding.
func LoadReady(path string, betaSLO betasloevidence.Receipt, betaSLODigest string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "production_external" || receipt.Environment != "production" || !receipt.Ready || !receipt.SampleCoverageComplete || receipt.SampleShortfallCount != 0 || receipt.TargetBreachCount != 0 || receipt.CheckCount != len(requiredChecks) || receipt.PassedCount != len(requiredChecks) || receipt.FailedCount != 0 || receipt.InconclusiveCount != 0 {
		return Receipt{}, "", errors.New("beta operations receipt is not ready")
	}
	if !allOpaque(receipt.AssessmentID, receipt.AggregatePolicyVersion, receipt.SamplePolicyVersion, receipt.TargetVersion, receipt.SupportExportVersion, receipt.InventoryID, receipt.PlanID, receipt.ChangeID, receipt.ReleaseID, receipt.BetaSLOObservationID) || !allDigests(receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.BetaSLOReceiptSHA256, receipt.DeletionReceiptExportSHA256, receipt.NoticeCaseExportSHA256, receipt.AnomalyCaseExportSHA256, receipt.SupportCaseExportSHA256, receipt.SampleManifestSHA256, receipt.TargetDecisionSHA256, receipt.PrivacySecuritySupportReviewSHA256, receipt.InputSHA256, digest) {
		return Receipt{}, "", errors.New("beta operations receipt identity or binding is invalid")
	}
	if betaSLO.Schema != betasloevidence.ReceiptSchemaV1 || !betaSLO.Ready || !digestPattern.MatchString(betaSLODigest) || receipt.BetaSLOObservationID != betaSLO.ObservationID || receipt.BetaSLOReceiptSHA256 != betaSLODigest || receipt.InventoryID != betaSLO.InventoryID || receipt.InventoryReceiptSHA256 != betaSLO.InventoryReceiptSHA256 || receipt.PlanID != betaSLO.PlanID || receipt.PlanReceiptSHA256 != betaSLO.PlanReceiptSHA256 || receipt.ChangeID != betaSLO.ChangeID || receipt.ChangeReceiptSHA256 != betaSLO.ChangeReceiptSHA256 || receipt.ReleaseID != betaSLO.ReleaseID || receipt.ReleaseReceiptSHA256 != betaSLO.ReleaseReceiptSHA256 || !receipt.WindowStart.Equal(betaSLO.WindowStart) || !receipt.WindowEnd.Equal(betaSLO.WindowEnd) {
		return Receipt{}, "", errors.New("beta operations receipt prerequisite binding is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil || passed != len(requiredChecks) || failed != 0 || inconclusive != 0 {
		return Receipt{}, "", errors.New("beta operations receipt checks are invalid")
	}
	results, totals, sampleShortfalls, targetBreaches, err := evaluateDomains(receipt.Domains)
	if err != nil || sampleShortfalls != 0 || targetBreaches != 0 || len(receipt.DomainResults) != len(results) {
		return Receipt{}, "", errors.New("beta operations receipt domains are invalid")
	}
	for index := range results {
		if receipt.DomainResults[index] != results[index] {
			return Receipt{}, "", errors.New("beta operations receipt domain derivation is invalid")
		}
	}
	if receipt.DueCaseCount != totals.due || receipt.WithinTargetCount != totals.within || receipt.LateCaseCount != totals.late || receipt.OverdueOpenCount != totals.overdue {
		return Receipt{}, "", errors.New("beta operations receipt totals are invalid")
	}
	start, end := receipt.WindowStart.UTC(), receipt.WindowEnd.UTC()
	approved, aggregated := receipt.TargetApprovedAt.UTC(), receipt.AggregatedAt.UTC()
	reviewed, generated, collected := receipt.ReviewedAt.UTC(), receipt.GeneratedAt.UTC(), receipt.CollectedAt.UTC()
	if !end.After(start) || approved.IsZero() || approved.After(start) || aggregated.Before(end) || aggregated.Sub(end) > maximumAggregationDelay || reviewed.Before(aggregated) || reviewed.Sub(aggregated) > maximumCollectionAge || generated.Before(reviewed) || collected.Before(generated) || collected.IsZero() {
		return Receipt{}, "", errors.New("beta operations receipt timeline is invalid")
	}
	receipt.WindowStart, receipt.WindowEnd, receipt.TargetApprovedAt = start, end, approved
	receipt.AggregatedAt, receipt.ReviewedAt, receipt.GeneratedAt, receipt.CollectedAt = aggregated, reviewed, generated, collected
	receipt.Domains, receipt.DomainResults, receipt.Checks = orderedAggregates(results), results, checks
	return receipt, digest, nil
}

func Collect(inventoryPath, planPath, changePath, releasePath, betaSLOPath, inputPath string, now time.Time) (Receipt, error) {
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
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, betaSLO, betaSLODigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, betaSLO betasloevidence.Receipt, betaSLODigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if err := validateChain(inventory, plan, change, release, releaseDigest, betaSLO, betaSLODigest); err != nil {
		return Receipt{}, err
	}
	if err := validateIdentity(input, inventory, plan, change, release, releaseDigest, betaSLO, betaSLODigest, inputDigest); err != nil {
		return Receipt{}, err
	}
	if now.IsZero() {
		return Receipt{}, errors.New("beta operations collection time is invalid")
	}
	now = now.UTC()
	start, end := input.WindowStart.UTC(), input.WindowEnd.UTC()
	approved, aggregated := input.TargetApprovedAt.UTC(), input.AggregatedAt.UTC()
	reviewed, generated := input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if start != betaSLO.WindowStart.UTC() || end != betaSLO.WindowEnd.UTC() || approved.IsZero() || approved.After(start) || aggregated.Before(end) || aggregated.Sub(end) > maximumAggregationDelay || reviewed.Before(aggregated) || reviewed.Sub(aggregated) > maximumCollectionAge || generated.Before(reviewed) || generated.Before(now.Add(-maximumCollectionAge)) || generated.After(now) {
		return Receipt{}, errors.New("beta operations timeline or shared window is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	results, totals, sampleShortfalls, targetBreaches, err := evaluateDomains(input.Domains)
	if err != nil {
		return Receipt{}, err
	}
	if sampleShortfalls > 0 && outcomeFor(checks, CheckSampleCoverage) != OutcomeFailed {
		return Receipt{}, errors.New("beta operations sample outcome contradicts aggregates")
	}
	for _, result := range results {
		if !result.TargetMet && outcomeFor(checks, targetCheck(result.ID)) != OutcomeFailed {
			return Receipt{}, errors.New("beta operations target outcome contradicts aggregates")
		}
	}
	sampleCoverageComplete := sampleShortfalls == 0
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && sampleCoverageComplete && targetBreaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("beta operations readiness contradicts evidence")
	}
	input.Schema = ReceiptSchemaV1
	input.WindowStart, input.WindowEnd, input.TargetApprovedAt = start, end, approved
	input.AggregatedAt, input.ReviewedAt, input.GeneratedAt = aggregated, reviewed, generated
	input.Domains, input.Checks = orderedAggregates(results), checks
	return Receipt{
		Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now,
		DueCaseCount: totals.due, WithinTargetCount: totals.within, LateCaseCount: totals.late, OverdueOpenCount: totals.overdue,
		SampleCoverageComplete: sampleCoverageComplete, SampleShortfallCount: sampleShortfalls, TargetBreachCount: targetBreaches,
		DomainResults: results, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive,
	}, nil
}

func validateChain(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, betaSLO betasloevidence.Receipt, betaSLODigest string) error {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Production || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return errors.New("beta operations production platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "production" || release.Namespace != "agent-memory-production" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return errors.New("beta operations production release is invalid or unready")
	}
	if betaSLO.Schema != betasloevidence.ReceiptSchemaV1 || betaSLO.Classification != "production_external" || betaSLO.Environment != "production" || !betaSLO.Ready || !digestPattern.MatchString(betaSLODigest) || betaSLO.InventoryID != inventory.InventoryID || betaSLO.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || betaSLO.PlanID != plan.PlanID || betaSLO.PlanReceiptSHA256 != plan.ReceiptSHA256 || betaSLO.ChangeID != change.ChangeID || betaSLO.ChangeReceiptSHA256 != change.ReceiptSHA256 || betaSLO.ReleaseID != release.ReleaseID || betaSLO.ReleaseReceiptSHA256 != releaseDigest {
		return errors.New("beta operations beta SLO receipt is invalid or misbound")
	}
	return nil
}

func validateIdentity(input Input, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, betaSLO betasloevidence.Receipt, betaSLODigest, inputDigest string) error {
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.AssessmentID, input.AggregatePolicyVersion, input.SamplePolicyVersion, input.TargetVersion, input.SupportExportVersion) {
		return errors.New("beta operations identity is invalid")
	}
	if input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || input.BetaSLOObservationID != betaSLO.ObservationID || input.BetaSLOReceiptSHA256 != betaSLODigest {
		return errors.New("beta operations platform or SLO binding is invalid")
	}
	if !allDigests(input.DeletionReceiptExportSHA256, input.NoticeCaseExportSHA256, input.AnomalyCaseExportSHA256, input.SupportCaseExportSHA256, input.SampleManifestSHA256, input.TargetDecisionSHA256, input.PrivacySecuritySupportReviewSHA256, inputDigest) {
		return errors.New("beta operations artifact binding is invalid")
	}
	return nil
}

type aggregateTotals struct{ due, within, late, overdue int }

func evaluateDomains(input []DomainAggregate) ([]DomainResult, aggregateTotals, int, int, error) {
	if len(input) != len(requiredDomains) {
		return nil, aggregateTotals{}, 0, 0, errors.New("beta operations domains are incomplete")
	}
	byID := make(map[DomainID]DomainAggregate, len(input))
	for _, domain := range input {
		if _, duplicate := byID[domain.ID]; duplicate || !knownDomain(domain.ID) || !validDomain(domain) {
			return nil, aggregateTotals{}, 0, 0, errors.New("beta operations domain is invalid or duplicated")
		}
		byID[domain.ID] = domain
	}
	results := make([]DomainResult, 0, len(requiredDomains))
	var totals aggregateTotals
	sampleShortfalls, targetBreaches := 0, 0
	for _, id := range requiredDomains {
		domain, ok := byID[id]
		if !ok {
			return nil, aggregateTotals{}, 0, 0, errors.New("beta operations domain is missing")
		}
		sampleComplete := domain.SampledCaseCount >= domain.RequiredSampleCount && domain.MatchedSampleCount == domain.SampledCaseCount
		targetMet := domain.LateCaseCount == 0 && domain.OverdueOpenCount == 0 && domain.MaximumObservedDurationSeconds <= domain.MaximumTargetSeconds
		if !sampleComplete {
			sampleShortfalls++
		}
		if !targetMet {
			targetBreaches++
		}
		totals.due += domain.DueCaseCount
		totals.within += domain.WithinTargetCount
		totals.late += domain.LateCaseCount
		totals.overdue += domain.OverdueOpenCount
		results = append(results, DomainResult{DomainAggregate: domain, Reconciled: true, SampleCoverageComplete: sampleComplete, TargetMet: targetMet})
	}
	return results, totals, sampleShortfalls, targetBreaches, nil
}

func validDomain(domain DomainAggregate) bool {
	counts := []int{domain.DueCaseCount, domain.WithinTargetCount, domain.LateCaseCount, domain.OverdueOpenCount, domain.RequiredSampleCount, domain.SampledCaseCount, domain.MatchedSampleCount}
	for _, count := range counts {
		if count < 0 || count > maximumCount {
			return false
		}
	}
	if domain.MaximumTargetSeconds <= 0 || domain.MaximumTargetSeconds > maximumTargetSeconds || domain.MaximumObservedDurationSeconds < 0 || domain.MaximumObservedDurationSeconds > maximumObservedSeconds || !digestPattern.MatchString(domain.EvidenceSHA256) || domain.WithinTargetCount+domain.LateCaseCount+domain.OverdueOpenCount != domain.DueCaseCount || domain.SampledCaseCount > domain.DueCaseCount || domain.MatchedSampleCount > domain.SampledCaseCount {
		return false
	}
	if domain.DueCaseCount == 0 {
		return domain.WithinTargetCount == 0 && domain.LateCaseCount == 0 && domain.OverdueOpenCount == 0 && domain.RequiredSampleCount == 0 && domain.SampledCaseCount == 0 && domain.MatchedSampleCount == 0 && domain.MaximumObservedDurationSeconds == 0
	}
	if domain.RequiredSampleCount <= 0 || domain.RequiredSampleCount > domain.DueCaseCount {
		return false
	}
	breached := domain.LateCaseCount > 0 || domain.OverdueOpenCount > 0
	if breached != (domain.MaximumObservedDurationSeconds > domain.MaximumTargetSeconds) {
		return false
	}
	return true
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("beta operations checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(input))
	for _, check := range input {
		if _, duplicate := byID[check.ID]; duplicate || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("beta operations check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("beta operations check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("beta operations check outcome is invalid")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}

func targetCheck(id DomainID) CheckID {
	switch id {
	case DomainDeletion:
		return CheckDeletionTarget
	case DomainRightsNotice:
		return CheckNoticeTarget
	case DomainAnomalyAlert:
		return CheckAnomalyTarget
	default:
		return CheckSupportTarget
	}
}

func knownDomain(id DomainID) bool {
	for _, required := range requiredDomains {
		if id == required {
			return true
		}
	}
	return false
}

func outcomeFor(checks []Check, id CheckID) Outcome {
	for _, check := range checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}

func orderedAggregates(results []DomainResult) []DomainAggregate {
	aggregates := make([]DomainAggregate, 0, len(results))
	for _, result := range results {
		aggregates = append(aggregates, result.DomainAggregate)
	}
	return aggregates
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
		return "", errors.New("beta operations input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open beta operations input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("beta operations input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read beta operations input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("beta operations input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("beta operations input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("beta operations input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("beta operations input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("beta operations receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("beta operations receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect beta operations receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-beta-operations-*")
}
