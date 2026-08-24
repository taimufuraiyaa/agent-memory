// Package gascorecardevidence normalizes retention-aware production GA
// scorecard evidence for P12.2-A.
package gascorecardevidence

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

const (
	InputSchemaV1        = "agent-memory-production-ga-scorecard-input-v1"
	ReceiptSchemaV1      = "agent-memory-production-ga-scorecard-receipt-v1"
	partsPerMillion      = int64(1_000_000)
	maximumInputBytes    = 256 << 10
	minimumWindow        = 28 * 24 * time.Hour
	maximumWindow        = 93 * 24 * time.Hour
	maximumEvaluationAge = 24 * time.Hour
	maximumCollectionAge = 24 * time.Hour
)

type MetricID string
type CheckID string
type Outcome string

const (
	MetricAPIAvailability       MetricID = "api_availability_ppm"
	MetricSearchP95             MetricID = "search_p95_microseconds"
	MetricMemoryWriteP95        MetricID = "memory_write_p95_microseconds"
	MetricCriticalFindings      MetricID = "critical_high_exploitable_findings"
	MetricTenantIsolation       MetricID = "tenant_isolation_passed"
	MetricDeletionCompliance    MetricID = "deletion_within_target_ppm"
	MetricAuditIntegrity        MetricID = "audit_integrity_verified_ppm"
	MetricBillingReconciliation MetricID = "billing_reconciled_ppm"
	MetricRestoreRPO            MetricID = "restore_rpo_minutes"
	MetricRestoreRTO            MetricID = "restore_rto_minutes"
	MetricCostPerActiveTenant   MetricID = "cost_per_active_tenant_microusd"
	MetricSupportResponse       MetricID = "support_response_within_target_ppm"
	MetricRetentionCompliance   MetricID = "retention_deletions_within_target_ppm"

	CheckWindowApproved  CheckID = "ga_window_approved"
	CheckScorecardExport CheckID = "immutable_scorecard_export_complete"
	CheckQueryManifest   CheckID = "query_manifest_reviewed"
	CheckMetricCoverage  CheckID = "metric_sample_coverage_complete"
	CheckTargetsMet      CheckID = "ga_targets_met"
	CheckRetention       CheckID = "retention_coverage_and_target_met"
	CheckDomainReview    CheckID = "product_domain_owner_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

type metricDefinition struct {
	Comparator string
	Target     int64
	Maximum    int64
	Dynamic    bool
}

var (
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredMetrics = []MetricID{
		MetricAPIAvailability, MetricSearchP95, MetricMemoryWriteP95,
		MetricCriticalFindings, MetricTenantIsolation, MetricDeletionCompliance,
		MetricAuditIntegrity, MetricBillingReconciliation, MetricRestoreRPO,
		MetricRestoreRTO, MetricCostPerActiveTenant, MetricSupportResponse,
		MetricRetentionCompliance,
	}
	metricDefinitions = map[MetricID]metricDefinition{
		MetricAPIAvailability:       {Comparator: "gte", Target: 999_000, Maximum: partsPerMillion},
		MetricSearchP95:             {Comparator: "lte", Target: 800_000, Maximum: 86_400_000_000},
		MetricMemoryWriteP95:        {Comparator: "lte", Target: 300_000, Maximum: 86_400_000_000},
		MetricCriticalFindings:      {Comparator: "lte", Target: 0, Maximum: 100_000_000},
		MetricTenantIsolation:       {Comparator: "gte", Target: 1, Maximum: 1},
		MetricDeletionCompliance:    {Comparator: "gte", Target: partsPerMillion, Maximum: partsPerMillion},
		MetricAuditIntegrity:        {Comparator: "gte", Target: partsPerMillion, Maximum: partsPerMillion},
		MetricBillingReconciliation: {Comparator: "gte", Target: partsPerMillion, Maximum: partsPerMillion},
		MetricRestoreRPO:            {Comparator: "lte", Target: 5, Maximum: 1_000_000},
		MetricRestoreRTO:            {Comparator: "lte", Target: 60, Maximum: 1_000_000},
		MetricCostPerActiveTenant:   {Comparator: "lte", Maximum: 1_000_000_000_000_000, Dynamic: true},
		MetricSupportResponse:       {Comparator: "gte", Target: partsPerMillion, Maximum: partsPerMillion},
		MetricRetentionCompliance:   {Comparator: "gte", Target: partsPerMillion, Maximum: partsPerMillion},
	}
	requiredChecks = []CheckID{CheckWindowApproved, CheckScorecardExport, CheckQueryManifest, CheckMetricCoverage, CheckTargetsMet, CheckRetention, CheckDomainReview}
)

type MetricObservation struct {
	ID                  MetricID `json:"id"`
	ObservedValue       int64    `json:"observed_value"`
	ExpectedSampleCount int      `json:"expected_sample_count"`
	ObservedSampleCount int      `json:"observed_sample_count"`
	EvidenceSHA256      string   `json:"evidence_sha256"`
}

type MetricResult struct {
	MetricObservation
	Comparator       string `json:"comparator"`
	TargetValue      int64  `json:"target_value"`
	CoverageComplete bool   `json:"coverage_complete"`
	Passed           bool   `json:"passed"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                string `json:"schema"`
	Classification        string `json:"classification"`
	Environment           string `json:"environment"`
	ScorecardID           string `json:"scorecard_id"`
	MetricSourceVersion   string `json:"metric_source_version"`
	QueryManifestVersion  string `json:"query_manifest_version"`
	TargetVersion         string `json:"target_version"`
	WindowDecisionVersion string `json:"window_decision_version"`

	InventoryID            string `json:"inventory_id"`
	InventoryReceiptSHA256 string `json:"inventory_receipt_sha256"`
	PlanID                 string `json:"plan_id"`
	PlanReceiptSHA256      string `json:"plan_receipt_sha256"`
	ChangeID               string `json:"change_id"`
	ChangeReceiptSHA256    string `json:"change_receipt_sha256"`
	ReleaseID              string `json:"release_id"`
	ReleaseReceiptSHA256   string `json:"release_receipt_sha256"`

	ScorecardExportSHA256     string `json:"scorecard_export_sha256"`
	QueryManifestSHA256       string `json:"query_manifest_sha256"`
	WindowDecisionSHA256      string `json:"window_decision_sha256"`
	TargetDecisionSHA256      string `json:"target_decision_sha256"`
	ProductDomainReviewSHA256 string `json:"product_domain_review_sha256"`

	WindowApprovedAt                    time.Time           `json:"window_approved_at"`
	WindowStart                         time.Time           `json:"window_start"`
	WindowEnd                           time.Time           `json:"window_end"`
	EvaluatedAt                         time.Time           `json:"evaluated_at"`
	GeneratedAt                         time.Time           `json:"generated_at"`
	ApprovedCostPerActiveTenantMicroUSD int64               `json:"approved_cost_per_active_tenant_microusd"`
	Ready                               bool                `json:"ready"`
	Metrics                             []MetricObservation `json:"metrics"`
	Checks                              []Check             `json:"checks"`
}

type Receipt struct {
	Input
	Schema                     string         `json:"schema"`
	InputSHA256                string         `json:"input_sha256"`
	CollectedAt                time.Time      `json:"collected_at"`
	ObservationDurationSeconds int64          `json:"observation_duration_seconds"`
	CoverageComplete           bool           `json:"coverage_complete"`
	CoverageShortfallCount     int            `json:"coverage_shortfall_count"`
	MetricBreachCount          int            `json:"metric_breach_count"`
	RetentionPassed            bool           `json:"retention_passed"`
	MetricResults              []MetricResult `json:"metric_results"`
	CheckCount                 int            `json:"check_count"`
	PassedCount                int            `json:"passed_count"`
	FailedCount                int            `json:"failed_count"`
	InconclusiveCount          int            `json:"inconclusive_count"`
}

func RequiredMetrics() []MetricID { return append([]MetricID(nil), requiredMetrics...) }
func RequiredChecks() []CheckID   { return append([]CheckID(nil), requiredChecks...) }

func evaluateMetrics(input []MetricObservation, approvedCostTarget int64) ([]MetricResult, int, int, bool, error) {
	if len(input) != len(requiredMetrics) || approvedCostTarget <= 0 || approvedCostTarget > metricDefinitions[MetricCostPerActiveTenant].Maximum {
		return nil, 0, 0, false, errors.New("GA scorecard metrics or cost target are incomplete")
	}
	byID := make(map[MetricID]MetricObservation, len(input))
	for _, metric := range input {
		definition, known := metricDefinitions[metric.ID]
		if !known || !digestPattern.MatchString(metric.EvidenceSHA256) || metric.ObservedValue < 0 || metric.ObservedValue > definition.Maximum || metric.ExpectedSampleCount <= 0 || metric.ExpectedSampleCount > 1_000_000_000 || metric.ObservedSampleCount <= 0 || metric.ObservedSampleCount > metric.ExpectedSampleCount {
			return nil, 0, 0, false, errors.New("GA scorecard metric is invalid")
		}
		if _, duplicate := byID[metric.ID]; duplicate {
			return nil, 0, 0, false, errors.New("GA scorecard metric is duplicated")
		}
		byID[metric.ID] = metric
	}
	results := make([]MetricResult, 0, len(requiredMetrics))
	coverageShortfalls, breaches := 0, 0
	retentionPassed := false
	for _, id := range requiredMetrics {
		metric, ok := byID[id]
		if !ok {
			return nil, 0, 0, false, errors.New("GA scorecard metric is missing")
		}
		definition := metricDefinitions[id]
		target := definition.Target
		if definition.Dynamic {
			target = approvedCostTarget
		}
		coverageComplete := metric.ObservedSampleCount == metric.ExpectedSampleCount
		passed := metric.ObservedValue >= target
		if definition.Comparator == "lte" {
			passed = metric.ObservedValue <= target
		}
		if !coverageComplete {
			coverageShortfalls++
		}
		if !passed {
			breaches++
		}
		if id == MetricRetentionCompliance {
			retentionPassed = coverageComplete && passed
		}
		results = append(results, MetricResult{MetricObservation: metric, Comparator: definition.Comparator, TargetValue: target, CoverageComplete: coverageComplete, Passed: passed})
	}
	return results, coverageShortfalls, breaches, retentionPassed, nil
}

func Collect(inventoryPath, planPath, changePath, releasePath, inputPath string, now time.Time) (Receipt, error) {
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
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if err := validatePlatformChain(inventory, plan, change, release, releaseDigest); err != nil {
		return Receipt{}, err
	}
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.ScorecardID, input.MetricSourceVersion, input.QueryManifestVersion, input.TargetVersion, input.WindowDecisionVersion, input.InventoryID, input.PlanID, input.ChangeID, input.ReleaseID) {
		return Receipt{}, errors.New("GA scorecard identity is invalid")
	}
	if input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !allDigests(input.InventoryReceiptSHA256, input.PlanReceiptSHA256, input.ChangeReceiptSHA256, input.ReleaseReceiptSHA256, input.ScorecardExportSHA256, input.QueryManifestSHA256, input.WindowDecisionSHA256, input.TargetDecisionSHA256, input.ProductDomainReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("GA scorecard platform or artifact binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("GA scorecard collection time is invalid")
	}
	now = now.UTC()
	approved, start, end := input.WindowApprovedAt.UTC(), input.WindowStart.UTC(), input.WindowEnd.UTC()
	evaluated, generated := input.EvaluatedAt.UTC(), input.GeneratedAt.UTC()
	duration := end.Sub(start)
	if approved.IsZero() || approved.After(start) || start.Before(release.CompletedAt.UTC()) || duration < minimumWindow || duration > maximumWindow || evaluated.Before(end) || evaluated.Sub(end) > maximumEvaluationAge || generated.Before(evaluated) || generated.Before(now.Add(-maximumCollectionAge)) || generated.After(now) {
		return Receipt{}, errors.New("GA scorecard observation timeline is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	results, shortfalls, breaches, retentionPassed, err := evaluateMetrics(input.Metrics, input.ApprovedCostPerActiveTenantMicroUSD)
	if err != nil {
		return Receipt{}, err
	}
	for _, derived := range []struct {
		id      CheckID
		failure bool
	}{
		{CheckMetricCoverage, shortfalls > 0},
		{CheckTargetsMet, breaches > 0},
		{CheckRetention, !retentionPassed},
	} {
		expected := OutcomePassed
		if derived.failure {
			expected = OutcomeFailed
		}
		if outcomeFor(checks, derived.id) != expected {
			return Receipt{}, errors.New("GA scorecard check contradicts derived evidence")
		}
	}
	ready := shortfalls == 0 && breaches == 0 && retentionPassed && passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("GA scorecard readiness contradicts evidence")
	}
	input.Schema, input.WindowApprovedAt, input.WindowStart, input.WindowEnd = ReceiptSchemaV1, approved, start, end
	input.EvaluatedAt, input.GeneratedAt, input.Metrics, input.Checks = evaluated, generated, orderedObservations(results), checks
	return Receipt{
		Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now,
		ObservationDurationSeconds: int64(duration / time.Second), CoverageComplete: shortfalls == 0,
		CoverageShortfallCount: shortfalls, MetricBreachCount: breaches, RetentionPassed: retentionPassed,
		MetricResults: results, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive,
	}, nil
}

func LoadReady(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "production_external" || receipt.Environment != "production" || !receipt.Ready || !receipt.CoverageComplete || !receipt.RetentionPassed || receipt.CoverageShortfallCount != 0 || receipt.MetricBreachCount != 0 || receipt.CheckCount != len(requiredChecks) || receipt.PassedCount != len(requiredChecks) || receipt.FailedCount != 0 || receipt.InconclusiveCount != 0 {
		return Receipt{}, "", errors.New("GA scorecard receipt is not ready")
	}
	if !allOpaque(receipt.ScorecardID, receipt.MetricSourceVersion, receipt.QueryManifestVersion, receipt.TargetVersion, receipt.WindowDecisionVersion, receipt.InventoryID, receipt.PlanID, receipt.ChangeID, receipt.ReleaseID) || !allDigests(receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.ScorecardExportSHA256, receipt.QueryManifestSHA256, receipt.WindowDecisionSHA256, receipt.TargetDecisionSHA256, receipt.ProductDomainReviewSHA256, receipt.InputSHA256, digest) {
		return Receipt{}, "", errors.New("GA scorecard receipt identity or binding is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil || passed != len(requiredChecks) || failed != 0 || inconclusive != 0 {
		return Receipt{}, "", errors.New("GA scorecard receipt checks are invalid")
	}
	results, shortfalls, breaches, retentionPassed, err := evaluateMetrics(receipt.Metrics, receipt.ApprovedCostPerActiveTenantMicroUSD)
	if err != nil || shortfalls != 0 || breaches != 0 || !retentionPassed || len(results) != len(receipt.MetricResults) {
		return Receipt{}, "", errors.New("GA scorecard receipt metrics are invalid")
	}
	for index := range results {
		if results[index] != receipt.MetricResults[index] {
			return Receipt{}, "", errors.New("GA scorecard receipt derivation is invalid")
		}
	}
	start, end := receipt.WindowStart.UTC(), receipt.WindowEnd.UTC()
	if !end.After(start) || int64(end.Sub(start)/time.Second) != receipt.ObservationDurationSeconds || end.Sub(start) < minimumWindow || end.Sub(start) > maximumWindow || receipt.WindowApprovedAt.After(start) || receipt.EvaluatedAt.Before(end) || receipt.EvaluatedAt.Sub(end) > maximumEvaluationAge || receipt.GeneratedAt.Before(receipt.EvaluatedAt) || receipt.CollectedAt.Before(receipt.GeneratedAt) || receipt.CollectedAt.IsZero() {
		return Receipt{}, "", errors.New("GA scorecard receipt timeline is invalid")
	}
	receipt.MetricResults, receipt.Checks = results, checks
	return receipt, digest, nil
}

func validatePlatformChain(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) error {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Production || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return errors.New("GA scorecard production platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "production" || release.Namespace != "agent-memory-production" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return errors.New("GA scorecard production release is invalid or unready")
	}
	return nil
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("GA scorecard checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(input))
	for _, check := range input {
		if _, duplicate := byID[check.ID]; duplicate || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("GA scorecard check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("GA scorecard check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("GA scorecard check outcome is invalid")
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

func orderedObservations(results []MetricResult) []MetricObservation {
	observations := make([]MetricObservation, 0, len(results))
	for _, result := range results {
		observations = append(observations, result.MetricObservation)
	}
	return observations
}

func allOpaque(values ...string) bool {
	for _, value := range values {
		if !opaquePattern.MatchString(value) || strings.Contains(value, "@") {
			return false
		}
	}
	return true
}

func allDigests(values ...string) bool {
	for _, value := range values {
		if !digestPattern.MatchString(value) {
			return false
		}
	}
	return true
}

func decodeStrictRegular(path string, destination any) (string, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("GA scorecard input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open GA scorecard input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("GA scorecard input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return "", errors.New("read GA scorecard input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("GA scorecard input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("GA scorecard input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("GA scorecard input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("GA scorecard input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("GA scorecard receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("GA scorecard receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect GA scorecard receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-ga-scorecard-*")
}
