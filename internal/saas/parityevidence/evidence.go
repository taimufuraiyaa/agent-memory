// Package parityevidence normalizes content-free representative staging
// SQLite-to-hosted retrieval-parity evidence for CP5-A.
package parityevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"io"
	"math"
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
	InputSchemaV1         = "agent-memory-staging-retrieval-parity-input-v1"
	ReceiptSchemaV1       = "agent-memory-staging-retrieval-parity-receipt-v1"
	maximumInputBytes     = 128 << 10
	maximumEvaluationSpan = 24 * time.Hour
	maximumCollectionAge  = 24 * time.Hour
	maximumCaseCount      = 1_000_000
)

type CheckID string
type Outcome string

const (
	CheckRepresentativeDataset CheckID = "representative_dataset_approved"
	CheckTopKOverlap           CheckID = "top_k_overlap_threshold_met"
	CheckScoreDelta            CheckID = "normalized_score_delta_threshold_met"
	CheckExactTerm             CheckID = "exact_term_winner_equivalent"
	CheckFeedback              CheckID = "feedback_preference_equivalent"
	CheckDecay                 CheckID = "decay_behavior_equivalent"
	CheckSuppression           CheckID = "suppression_behavior_equivalent"
	CheckCitation              CheckID = "citation_resolution_equivalent"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{CheckRepresentativeDataset, CheckTopKOverlap, CheckScoreDelta, CheckExactTerm, CheckFeedback, CheckDecay, CheckSuppression, CheckCitation}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                              string    `json:"schema"`
	Classification                      string    `json:"classification"`
	Environment                         string    `json:"environment"`
	EvaluationID                        string    `json:"evaluation_id"`
	ThresholdVersion                    string    `json:"threshold_version"`
	DatasetVersion                      string    `json:"dataset_version"`
	InventoryID                         string    `json:"inventory_id"`
	InventoryReceiptSHA256              string    `json:"inventory_receipt_sha256"`
	PlanID                              string    `json:"plan_id"`
	PlanReceiptSHA256                   string    `json:"plan_receipt_sha256"`
	ChangeID                            string    `json:"change_id"`
	ChangeReceiptSHA256                 string    `json:"change_receipt_sha256"`
	ReleaseID                           string    `json:"release_id"`
	ReleaseReceiptSHA256                string    `json:"release_receipt_sha256"`
	ThresholdDecisionSHA256             string    `json:"threshold_decision_sha256"`
	ParityReportSHA256                  string    `json:"parity_report_sha256"`
	ThresholdApprovedAt                 time.Time `json:"threshold_approved_at"`
	EvaluationStartedAt                 time.Time `json:"evaluation_started_at"`
	EvaluationCompletedAt               time.Time `json:"evaluation_completed_at"`
	GeneratedAt                         time.Time `json:"generated_at"`
	CaseCount                           int       `json:"case_count"`
	MinimumTopKOverlap                  float64   `json:"minimum_top_k_overlap"`
	MaximumNormalizedScoreDelta         float64   `json:"maximum_normalized_score_delta"`
	ObservedTopKOverlap                 float64   `json:"observed_top_k_overlap"`
	ObservedMaximumNormalizedScoreDelta float64   `json:"observed_maximum_normalized_score_delta"`
	Ready                               bool      `json:"ready"`
	Checks                              []Check   `json:"checks"`
}

type Receipt struct {
	Schema                              string    `json:"schema"`
	Classification                      string    `json:"classification"`
	Environment                         string    `json:"environment"`
	EvaluationID                        string    `json:"evaluation_id"`
	ThresholdVersion                    string    `json:"threshold_version"`
	DatasetVersion                      string    `json:"dataset_version"`
	InventoryID                         string    `json:"inventory_id"`
	InventoryReceiptSHA256              string    `json:"inventory_receipt_sha256"`
	PlanID                              string    `json:"plan_id"`
	PlanReceiptSHA256                   string    `json:"plan_receipt_sha256"`
	ChangeID                            string    `json:"change_id"`
	ChangeReceiptSHA256                 string    `json:"change_receipt_sha256"`
	ReleaseID                           string    `json:"release_id"`
	ReleaseReceiptSHA256                string    `json:"release_receipt_sha256"`
	ThresholdDecisionSHA256             string    `json:"threshold_decision_sha256"`
	ParityReportSHA256                  string    `json:"parity_report_sha256"`
	InputSHA256                         string    `json:"input_sha256"`
	ThresholdApprovedAt                 time.Time `json:"threshold_approved_at"`
	EvaluationStartedAt                 time.Time `json:"evaluation_started_at"`
	EvaluationCompletedAt               time.Time `json:"evaluation_completed_at"`
	GeneratedAt                         time.Time `json:"generated_at"`
	CollectedAt                         time.Time `json:"collected_at"`
	CaseCount                           int       `json:"case_count"`
	MinimumTopKOverlap                  float64   `json:"minimum_top_k_overlap"`
	MaximumNormalizedScoreDelta         float64   `json:"maximum_normalized_score_delta"`
	ObservedTopKOverlap                 float64   `json:"observed_top_k_overlap"`
	ObservedMaximumNormalizedScoreDelta float64   `json:"observed_maximum_normalized_score_delta"`
	Ready                               bool      `json:"ready"`
	CheckCount                          int       `json:"check_count"`
	PassedCount                         int       `json:"passed_count"`
	FailedCount                         int       `json:"failed_count"`
	InconclusiveCount                   int       `json:"inconclusive_count"`
	MetricBreachCount                   int       `json:"metric_breach_count"`
	Checks                              []Check   `json:"checks"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

// LoadReadyReceipt reloads and revalidates a published ready CP5-A receipt by
// exact opened bytes for downstream acceptance gates.
func LoadReadyReceipt(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil {
		return Receipt{}, "", err
	}
	canonical := len(checks) == len(receipt.Checks)
	for index := range checks {
		canonical = canonical && checks[index] == receipt.Checks[index] && checks[index].Outcome == OutcomePassed
	}
	validIdentity := receipt.Schema == ReceiptSchemaV1 && receipt.Classification == "staging_external" && receipt.Environment == "staging" &&
		opaquePattern.MatchString(receipt.EvaluationID) && opaquePattern.MatchString(receipt.ThresholdVersion) && opaquePattern.MatchString(receipt.DatasetVersion) &&
		opaquePattern.MatchString(receipt.InventoryID) && opaquePattern.MatchString(receipt.PlanID) && opaquePattern.MatchString(receipt.ChangeID) && opaquePattern.MatchString(receipt.ReleaseID)
	validDigests := digestPattern.MatchString(receipt.InventoryReceiptSHA256) && digestPattern.MatchString(receipt.PlanReceiptSHA256) && digestPattern.MatchString(receipt.ChangeReceiptSHA256) && digestPattern.MatchString(receipt.ReleaseReceiptSHA256) && digestPattern.MatchString(receipt.ThresholdDecisionSHA256) && digestPattern.MatchString(receipt.ParityReportSHA256) && digestPattern.MatchString(receipt.InputSHA256) && digestPattern.MatchString(digest)
	validMetrics := receipt.CaseCount >= 1 && receipt.CaseCount <= maximumCaseCount && validMetric(receipt.MinimumTopKOverlap, false) && validMetric(receipt.MaximumNormalizedScoreDelta, true) && validMetric(receipt.ObservedTopKOverlap, true) && validMetric(receipt.ObservedMaximumNormalizedScoreDelta, true) && receipt.ObservedTopKOverlap >= receipt.MinimumTopKOverlap && receipt.ObservedMaximumNormalizedScoreDelta <= receipt.MaximumNormalizedScoreDelta && !contradictsMetric(checks, CheckTopKOverlap, true) && !contradictsMetric(checks, CheckScoreDelta, true)
	approved, started, completed, generated, collected := receipt.ThresholdApprovedAt.UTC(), receipt.EvaluationStartedAt.UTC(), receipt.EvaluationCompletedAt.UTC(), receipt.GeneratedAt.UTC(), receipt.CollectedAt.UTC()
	validTimeline := !approved.IsZero() && !started.IsZero() && !completed.IsZero() && !generated.IsZero() && !collected.IsZero() && !approved.After(started) && !completed.Before(started) && completed.Sub(started) <= maximumEvaluationSpan && !generated.Before(completed) && !collected.Before(generated)
	validSummary := receipt.Ready && receipt.CheckCount == len(requiredChecks) && receipt.PassedCount == len(requiredChecks) && passed == len(requiredChecks) && receipt.FailedCount == 0 && failed == 0 && receipt.InconclusiveCount == 0 && inconclusive == 0 && receipt.MetricBreachCount == 0
	if !canonical || !validIdentity || !validDigests || !validMetrics || !validTimeline || !validSummary {
		return Receipt{}, "", errors.New("retrieval parity receipt is invalid or unready")
	}
	receipt.ThresholdApprovedAt, receipt.EvaluationStartedAt, receipt.EvaluationCompletedAt = approved, started, completed
	receipt.GeneratedAt, receipt.CollectedAt, receipt.Checks = generated, collected, checks
	return receipt, digest, nil
}

func Collect(inventoryPath, planPath, changePath, releasePath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed platform inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed infrastructure plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed infrastructure change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load passed staging release: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging ||
		plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready ||
		change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready ||
		!digestPattern.MatchString(inventory.ReceiptSHA256) || !digestPattern.MatchString(plan.ReceiptSHA256) || !digestPattern.MatchString(change.ReceiptSHA256) {
		return Receipt{}, errors.New("retrieval parity platform chain is invalid or unready")
	}
	if !validPassedRelease(release, releaseDigest) {
		return Receipt{}, errors.New("retrieval parity staging release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" ||
		!opaquePattern.MatchString(input.EvaluationID) || !opaquePattern.MatchString(input.ThresholdVersion) || !opaquePattern.MatchString(input.DatasetVersion) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 ||
		input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest ||
		!digestPattern.MatchString(input.ThresholdDecisionSHA256) || !digestPattern.MatchString(input.ParityReportSHA256) || !digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("retrieval parity input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("retrieval parity collection time is invalid")
	}
	now = now.UTC()
	approved, started, completed, generated := input.ThresholdApprovedAt.UTC(), input.EvaluationStartedAt.UTC(), input.EvaluationCompletedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if approved.IsZero() || started.IsZero() || completed.IsZero() || generated.IsZero() || approved.After(started) || started.Before(earliest) || completed.Before(started) || completed.Sub(started) > maximumEvaluationSpan || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("retrieval parity timeline is invalid")
	}
	if !validMetric(input.MinimumTopKOverlap, false) || !validMetric(input.MaximumNormalizedScoreDelta, true) || !validMetric(input.ObservedTopKOverlap, true) || !validMetric(input.ObservedMaximumNormalizedScoreDelta, true) || input.CaseCount < 1 || input.CaseCount > maximumCaseCount {
		return Receipt{}, errors.New("retrieval parity metrics are invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	breaches := 0
	overlapMet := input.ObservedTopKOverlap >= input.MinimumTopKOverlap
	deltaMet := input.ObservedMaximumNormalizedScoreDelta <= input.MaximumNormalizedScoreDelta
	if !overlapMet {
		breaches++
	}
	if !deltaMet {
		breaches++
	}
	if contradictsMetric(checks, CheckTopKOverlap, overlapMet) || contradictsMetric(checks, CheckScoreDelta, deltaMet) {
		return Receipt{}, errors.New("retrieval parity metric outcome contradicts observation")
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("retrieval parity readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, EvaluationID: input.EvaluationID, ThresholdVersion: input.ThresholdVersion, DatasetVersion: input.DatasetVersion,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256, ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256,
		ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: releaseDigest, ThresholdDecisionSHA256: input.ThresholdDecisionSHA256, ParityReportSHA256: input.ParityReportSHA256, InputSHA256: inputDigest,
		ThresholdApprovedAt: approved, EvaluationStartedAt: started, EvaluationCompletedAt: completed, GeneratedAt: generated, CollectedAt: now, CaseCount: input.CaseCount,
		MinimumTopKOverlap: input.MinimumTopKOverlap, MaximumNormalizedScoreDelta: input.MaximumNormalizedScoreDelta, ObservedTopKOverlap: input.ObservedTopKOverlap, ObservedMaximumNormalizedScoreDelta: input.ObservedMaximumNormalizedScoreDelta,
		Ready: ready, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, MetricBreachCount: breaches, Checks: checks}, nil
}

func validPassedRelease(release platformrollback.ReleaseReceipt, digest string) bool {
	return release.Schema == "agent-memory-kubernetes-release-receipt-v1" && release.Environment == "staging" && release.Namespace == "agent-memory-staging" && opaquePattern.MatchString(release.ReleaseID) && release.Outcome == "passed" && release.Migration.Outcome == "complete" && release.Rollouts.Outcome == "healthy" && !release.Rollback.Attempted && !release.Rollback.Succeeded && !release.StartedAt.IsZero() && !release.CompletedAt.Before(release.StartedAt) && digestPattern.MatchString(digest)
}

func validMetric(value float64, zeroAllowed bool) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return false
	}
	return zeroAllowed || value > 0
}

func validateChecks(checks []Check) ([]Check, int, int, int, error) {
	if len(checks) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("retrieval parity checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(checks))
	passed, failed, inconclusive := 0, 0, 0
	for _, check := range checks {
		if !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("retrieval parity check evidence digest is invalid")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("retrieval parity check outcome is invalid")
		}
		if _, duplicate := byID[check.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("retrieval parity check is duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		check, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("retrieval parity required check is missing")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}

func contradictsMetric(checks []Check, id CheckID, met bool) bool {
	for _, check := range checks {
		if check.ID != id {
			continue
		}
		if check.Outcome == OutcomeInconclusive {
			return false
		}
		return (check.Outcome == OutcomePassed) != met
	}
	return true
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("retrieval parity input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("retrieval parity input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open retrieval parity input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("retrieval parity input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read retrieval parity input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("retrieval parity input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("retrieval parity input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("retrieval parity input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("retrieval parity input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("retrieval parity receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("retrieval parity receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect retrieval parity receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-parity-evidence-*")
}
