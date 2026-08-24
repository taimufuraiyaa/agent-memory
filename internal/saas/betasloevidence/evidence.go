// Package betasloevidence normalizes content-free production beta SLO
// observation evidence for P11.3-A.
package betasloevidence

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
	InputSchemaV1   = "agent-memory-production-beta-slo-input-v1"
	ReceiptSchemaV1 = "agent-memory-production-beta-slo-receipt-v1"

	maximumInputBytes      = 128 << 10
	minimumWindow          = 24 * time.Hour
	maximumWindow          = 31 * 24 * time.Hour
	maximumEvaluationDelay = 24 * time.Hour
	maximumCollectionAge   = 24 * time.Hour
	maximumSampleCount     = 1_000_000_000
	maximumLatencyMicros   = int64(86_400_000_000)
	partsPerMillion        = int64(1_000_000)
)

type MetricID string
type CheckID string
type Outcome string

const (
	MetricAPIAvailability        MetricID = "api_availability_ppm"
	MetricSearchP95              MetricID = "search_p95_microseconds"
	MetricMemoryWriteP95         MetricID = "memory_write_p95_microseconds"
	MetricStatusMetadataP95      MetricID = "status_metadata_p95_microseconds"
	MetricUploadAcceptanceP95    MetricID = "upload_acceptance_p95_microseconds"
	MetricNativeIndexingWithin60 MetricID = "native_text_indexing_within_60s_ppm"

	CheckWindowApproved       CheckID = "observation_window_approved"
	CheckMetricExportComplete CheckID = "immutable_metric_export_complete"
	CheckQueryManifest        CheckID = "query_manifest_reviewed"
	CheckMetricCoverage       CheckID = "metric_sample_coverage_complete"
	CheckTargetsMet           CheckID = "provisional_slo_targets_met"
	CheckProductOperations    CheckID = "product_operations_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

type metricDefinition struct {
	Comparator string
	Target     int64
	Maximum    int64
}

var (
	digestPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredMetrics   = []MetricID{MetricAPIAvailability, MetricSearchP95, MetricMemoryWriteP95, MetricStatusMetadataP95, MetricUploadAcceptanceP95, MetricNativeIndexingWithin60}
	requiredChecks    = []CheckID{CheckWindowApproved, CheckMetricExportComplete, CheckQueryManifest, CheckMetricCoverage, CheckTargetsMet, CheckProductOperations}
	metricDefinitions = map[MetricID]metricDefinition{
		MetricAPIAvailability:        {Comparator: "gte", Target: 999_000, Maximum: partsPerMillion},
		MetricSearchP95:              {Comparator: "lte", Target: 800_000, Maximum: maximumLatencyMicros},
		MetricMemoryWriteP95:         {Comparator: "lte", Target: 300_000, Maximum: maximumLatencyMicros},
		MetricStatusMetadataP95:      {Comparator: "lte", Target: 300_000, Maximum: maximumLatencyMicros},
		MetricUploadAcceptanceP95:    {Comparator: "lte", Target: 2_000_000, Maximum: maximumLatencyMicros},
		MetricNativeIndexingWithin60: {Comparator: "gte", Target: 950_000, Maximum: partsPerMillion},
	}
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
	ObservationID         string `json:"observation_id"`
	MetricSourceVersion   string `json:"metric_source_version"`
	QueryManifestVersion  string `json:"query_manifest_version"`
	SLODefinitionVersion  string `json:"slo_definition_version"`
	WindowDecisionVersion string `json:"window_decision_version"`

	InventoryID            string `json:"inventory_id"`
	InventoryReceiptSHA256 string `json:"inventory_receipt_sha256"`
	PlanID                 string `json:"plan_id"`
	PlanReceiptSHA256      string `json:"plan_receipt_sha256"`
	ChangeID               string `json:"change_id"`
	ChangeReceiptSHA256    string `json:"change_receipt_sha256"`
	ReleaseID              string `json:"release_id"`
	ReleaseReceiptSHA256   string `json:"release_receipt_sha256"`

	MetricExportSHA256            string `json:"metric_export_sha256"`
	QueryManifestSHA256           string `json:"query_manifest_sha256"`
	WindowDecisionSHA256          string `json:"window_decision_sha256"`
	SLODefinitionDecisionSHA256   string `json:"slo_definition_decision_sha256"`
	ProductOperationsReviewSHA256 string `json:"product_operations_review_sha256"`

	WindowApprovedAt     time.Time           `json:"window_approved_at"`
	WindowStart          time.Time           `json:"window_start"`
	WindowEnd            time.Time           `json:"window_end"`
	EvaluatedAt          time.Time           `json:"evaluated_at"`
	GeneratedAt          time.Time           `json:"generated_at"`
	MinimumWindowSeconds int64               `json:"minimum_window_seconds"`
	Ready                bool                `json:"ready"`
	Metrics              []MetricObservation `json:"metrics"`
	Checks               []Check             `json:"checks"`
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
	MetricResults              []MetricResult `json:"metric_results"`
	CheckCount                 int            `json:"check_count"`
	PassedCount                int            `json:"passed_count"`
	FailedCount                int            `json:"failed_count"`
	InconclusiveCount          int            `json:"inconclusive_count"`
}

func RequiredMetrics() []MetricID { return append([]MetricID(nil), requiredMetrics...) }
func RequiredChecks() []CheckID   { return append([]CheckID(nil), requiredChecks...) }

// LoadReady strictly reloads a normalized ready receipt and returns the exact
// file digest for downstream same-window evidence binding.
func LoadReady(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "production_external" || receipt.Environment != "production" || !receipt.Ready || !receipt.CoverageComplete || receipt.CoverageShortfallCount != 0 || receipt.MetricBreachCount != 0 || receipt.CheckCount != len(requiredChecks) || receipt.PassedCount != len(requiredChecks) || receipt.FailedCount != 0 || receipt.InconclusiveCount != 0 {
		return Receipt{}, "", errors.New("beta SLO receipt is not ready")
	}
	if !allOpaque(receipt.ObservationID, receipt.MetricSourceVersion, receipt.QueryManifestVersion, receipt.SLODefinitionVersion, receipt.WindowDecisionVersion, receipt.InventoryID, receipt.PlanID, receipt.ChangeID, receipt.ReleaseID) || !allDigests(receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.MetricExportSHA256, receipt.QueryManifestSHA256, receipt.WindowDecisionSHA256, receipt.SLODefinitionDecisionSHA256, receipt.ProductOperationsReviewSHA256, receipt.InputSHA256, digest) {
		return Receipt{}, "", errors.New("beta SLO receipt identity or binding is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil || passed != len(requiredChecks) || failed != 0 || inconclusive != 0 {
		return Receipt{}, "", errors.New("beta SLO receipt checks are invalid")
	}
	results, coverageShortfalls, metricBreaches, err := evaluateMetrics(receipt.Metrics)
	if err != nil || coverageShortfalls != 0 || metricBreaches != 0 || len(receipt.MetricResults) != len(results) {
		return Receipt{}, "", errors.New("beta SLO receipt metrics are invalid")
	}
	for index := range results {
		if receipt.MetricResults[index] != results[index] {
			return Receipt{}, "", errors.New("beta SLO receipt metric derivation is invalid")
		}
	}
	start, end := receipt.WindowStart.UTC(), receipt.WindowEnd.UTC()
	approved, evaluated := receipt.WindowApprovedAt.UTC(), receipt.EvaluatedAt.UTC()
	generated, collected := receipt.GeneratedAt.UTC(), receipt.CollectedAt.UTC()
	minimumSeconds := int64(minimumWindow / time.Second)
	maximumSeconds := int64(maximumWindow / time.Second)
	if receipt.MinimumWindowSeconds < minimumSeconds || receipt.MinimumWindowSeconds > maximumSeconds || !end.After(start) || int64(end.Sub(start)/time.Second) != receipt.ObservationDurationSeconds || receipt.ObservationDurationSeconds < receipt.MinimumWindowSeconds || receipt.ObservationDurationSeconds > maximumSeconds || approved.IsZero() || approved.After(start) || evaluated.Before(end) || evaluated.Sub(end) > maximumEvaluationDelay || generated.Before(evaluated) || collected.Before(generated) || collected.IsZero() {
		return Receipt{}, "", errors.New("beta SLO receipt timeline is invalid")
	}
	receipt.WindowApprovedAt, receipt.WindowStart, receipt.WindowEnd = approved, start, end
	receipt.EvaluatedAt, receipt.GeneratedAt, receipt.CollectedAt = evaluated, generated, collected
	receipt.Checks = checks
	return receipt, digest, nil
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
	if err := validateIdentity(input, inventory, plan, change, release, releaseDigest, inputDigest); err != nil {
		return Receipt{}, err
	}
	if now.IsZero() {
		return Receipt{}, errors.New("beta SLO collection time is invalid")
	}
	now = now.UTC()
	approved, start, end := input.WindowApprovedAt.UTC(), input.WindowStart.UTC(), input.WindowEnd.UTC()
	evaluated, generated := input.EvaluatedAt.UTC(), input.GeneratedAt.UTC()
	duration := end.Sub(start)
	minimum := time.Duration(input.MinimumWindowSeconds) * time.Second
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if approved.IsZero() || approved.After(start) || start.Before(earliest) || duration < minimum || duration > maximumWindow || minimum < minimumWindow || minimum > maximumWindow || evaluated.Before(end) || evaluated.Sub(end) > maximumEvaluationDelay || generated.Before(evaluated) || generated.Before(now.Add(-maximumCollectionAge)) || generated.After(now) {
		return Receipt{}, errors.New("beta SLO observation timeline is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	results, coverageShortfalls, metricBreaches, err := evaluateMetrics(input.Metrics)
	if err != nil {
		return Receipt{}, err
	}
	if coverageShortfalls > 0 && outcomeFor(checks, CheckMetricCoverage) != OutcomeFailed {
		return Receipt{}, errors.New("beta SLO coverage outcome contradicts observations")
	}
	if metricBreaches > 0 && outcomeFor(checks, CheckTargetsMet) != OutcomeFailed {
		return Receipt{}, errors.New("beta SLO target outcome contradicts observations")
	}
	coverageComplete := coverageShortfalls == 0
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && coverageComplete && metricBreaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("beta SLO readiness contradicts evidence")
	}
	input.Schema = ReceiptSchemaV1
	input.WindowApprovedAt, input.WindowStart, input.WindowEnd = approved, start, end
	input.EvaluatedAt, input.GeneratedAt, input.Metrics, input.Checks = evaluated, generated, orderedObservations(results), checks
	return Receipt{
		Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now,
		ObservationDurationSeconds: int64(duration / time.Second), CoverageComplete: coverageComplete,
		CoverageShortfallCount: coverageShortfalls, MetricBreachCount: metricBreaches, MetricResults: results,
		CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive,
	}, nil
}

func validatePlatformChain(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) error {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Production || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return errors.New("beta SLO production platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "production" || release.Namespace != "agent-memory-production" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return errors.New("beta SLO production release is invalid or unready")
	}
	return nil
}

func validateIdentity(input Input, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest, inputDigest string) error {
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.ObservationID, input.MetricSourceVersion, input.QueryManifestVersion, input.SLODefinitionVersion, input.WindowDecisionVersion) {
		return errors.New("beta SLO identity is invalid")
	}
	if input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest {
		return errors.New("beta SLO platform binding is invalid")
	}
	if !allDigests(input.MetricExportSHA256, input.QueryManifestSHA256, input.WindowDecisionSHA256, input.SLODefinitionDecisionSHA256, input.ProductOperationsReviewSHA256, inputDigest) {
		return errors.New("beta SLO artifact binding is invalid")
	}
	return nil
}

func evaluateMetrics(input []MetricObservation) ([]MetricResult, int, int, error) {
	if len(input) != len(requiredMetrics) {
		return nil, 0, 0, errors.New("beta SLO metrics are incomplete")
	}
	byID := make(map[MetricID]MetricObservation, len(input))
	for _, metric := range input {
		definition, known := metricDefinitions[metric.ID]
		if _, duplicate := byID[metric.ID]; !known || duplicate || metric.ObservedValue < 0 || metric.ObservedValue > definition.Maximum || metric.ExpectedSampleCount <= 0 || metric.ExpectedSampleCount > maximumSampleCount || metric.ObservedSampleCount <= 0 || metric.ObservedSampleCount > metric.ExpectedSampleCount || !digestPattern.MatchString(metric.EvidenceSHA256) {
			return nil, 0, 0, errors.New("beta SLO metric is invalid or duplicated")
		}
		byID[metric.ID] = metric
	}
	results := make([]MetricResult, 0, len(requiredMetrics))
	coverageShortfalls, metricBreaches := 0, 0
	for _, id := range requiredMetrics {
		metric, ok := byID[id]
		if !ok {
			return nil, 0, 0, errors.New("beta SLO metric is missing")
		}
		definition := metricDefinitions[id]
		coverageComplete := metric.ObservedSampleCount == metric.ExpectedSampleCount
		passed := compare(metric.ObservedValue, definition.Target, definition.Comparator)
		if !coverageComplete {
			coverageShortfalls++
		}
		if !passed {
			metricBreaches++
		}
		results = append(results, MetricResult{MetricObservation: metric, Comparator: definition.Comparator, TargetValue: definition.Target, CoverageComplete: coverageComplete, Passed: passed})
	}
	return results, coverageShortfalls, metricBreaches, nil
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("beta SLO checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(input))
	for _, check := range input {
		if _, duplicate := byID[check.ID]; duplicate || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("beta SLO check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("beta SLO check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("beta SLO check outcome is invalid")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}

func orderedObservations(results []MetricResult) []MetricObservation {
	observations := make([]MetricObservation, 0, len(results))
	for _, result := range results {
		observations = append(observations, result.MetricObservation)
	}
	return observations
}

func compare(value, target int64, comparator string) bool {
	if comparator == "gte" {
		return value >= target
	}
	return value <= target
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
		return "", errors.New("beta SLO input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open beta SLO input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("beta SLO input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read beta SLO input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("beta SLO input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("beta SLO input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("beta SLO input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("beta SLO input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("beta SLO receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("beta SLO receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect beta SLO receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-beta-slo-*")
}
