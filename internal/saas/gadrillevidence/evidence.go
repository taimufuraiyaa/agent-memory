// Package gadrillevidence normalizes repeated production GA drill evidence for P12.2-B.
package gadrillevidence

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/gascorecardevidence"
)

const (
	InputSchemaV1   = "agent-memory-production-ga-drill-input-v1"
	ReceiptSchemaV1 = "agent-memory-production-ga-drill-receipt-v1"
	maximumBytes    = 256 << 10
	maximumDrills   = 64
	minimumPerType  = 2
	maximumDuration = 24 * time.Hour
	maximumAge      = 24 * time.Hour
)

type Scenario string
type Outcome string
type CheckID string

const (
	ScenarioRestore     Scenario = "restore"
	ScenarioDeletion    Scenario = "deletion"
	ScenarioCredential  Scenario = "credential"
	ScenarioNotice      Scenario = "notice"
	OutcomePassed       Outcome  = "passed"
	OutcomeFailed       Outcome  = "failed"
	OutcomeInconclusive Outcome  = "inconclusive"
	CheckManifest       CheckID  = "drill_manifest_complete"
	CheckWindow         CheckID  = "all_drills_inside_ga_window"
	CheckRepetition     CheckID  = "minimum_repetitions_complete"
	CheckDates          CheckID  = "distinct_drill_dates_complete"
	CheckUnique         CheckID  = "drill_evidence_unique"
	CheckOutcomes       CheckID  = "all_drill_outcomes_passed"
	CheckReview         CheckID  = "operations_security_privacy_review_complete"
)

var (
	requiredScenarios = []Scenario{ScenarioRestore, ScenarioDeletion, ScenarioCredential, ScenarioNotice}
	requiredChecks    = []CheckID{CheckManifest, CheckWindow, CheckRepetition, CheckDates, CheckUnique, CheckOutcomes, CheckReview}
	digestPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

type Drill struct {
	Scenario       Scenario  `json:"scenario"`
	DrillID        string    `json:"drill_id"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
	Outcome        Outcome   `json:"outcome"`
	EvidenceSHA256 string    `json:"evidence_sha256"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type ScenarioResult struct {
	Scenario          Scenario `json:"scenario"`
	DrillCount        int      `json:"drill_count"`
	DistinctDateCount int      `json:"distinct_date_count"`
	PassedCount       int      `json:"passed_count"`
	FailedCount       int      `json:"failed_count"`
	InconclusiveCount int      `json:"inconclusive_count"`
	Ready             bool     `json:"ready"`
}

type Input struct {
	Schema                  string    `json:"schema"`
	Classification          string    `json:"classification"`
	Environment             string    `json:"environment"`
	ReviewID                string    `json:"review_id"`
	PolicyVersion           string    `json:"policy_version"`
	ScorecardID             string    `json:"scorecard_id"`
	ScorecardReceiptSHA256  string    `json:"scorecard_receipt_sha256"`
	InventoryID             string    `json:"inventory_id"`
	InventoryReceiptSHA256  string    `json:"inventory_receipt_sha256"`
	PlanID                  string    `json:"plan_id"`
	PlanReceiptSHA256       string    `json:"plan_receipt_sha256"`
	ChangeID                string    `json:"change_id"`
	ChangeReceiptSHA256     string    `json:"change_receipt_sha256"`
	ReleaseID               string    `json:"release_id"`
	ReleaseReceiptSHA256    string    `json:"release_receipt_sha256"`
	DrillManifestSHA256     string    `json:"drill_manifest_sha256"`
	RepetitionPolicySHA256  string    `json:"repetition_policy_sha256"`
	AccountableReviewSHA256 string    `json:"accountable_review_sha256"`
	GeneratedAt             time.Time `json:"generated_at"`
	Ready                   bool      `json:"ready"`
	Drills                  []Drill   `json:"drills"`
	Checks                  []Check   `json:"checks"`
}

type Receipt struct {
	Input
	Schema                 string           `json:"schema"`
	InputSHA256            string           `json:"input_sha256"`
	CollectedAt            time.Time        `json:"collected_at"`
	WindowStart            time.Time        `json:"window_start"`
	WindowEnd              time.Time        `json:"window_end"`
	DrillCount             int              `json:"drill_count"`
	ScenarioCount          int              `json:"scenario_count"`
	PassedDrillCount       int              `json:"passed_drill_count"`
	FailedDrillCount       int              `json:"failed_drill_count"`
	InconclusiveDrillCount int              `json:"inconclusive_drill_count"`
	ScenarioResults        []ScenarioResult `json:"scenario_results"`
	CheckCount             int              `json:"check_count"`
	PassedCheckCount       int              `json:"passed_check_count"`
	FailedCheckCount       int              `json:"failed_check_count"`
	InconclusiveCheckCount int              `json:"inconclusive_check_count"`
}

func RequiredScenarios() []Scenario { return append([]Scenario(nil), requiredScenarios...) }
func RequiredChecks() []CheckID     { return append([]CheckID(nil), requiredChecks...) }

func evaluate(drills []Drill, start, end time.Time) ([]ScenarioResult, int, int, int, error) {
	if len(drills) < len(requiredScenarios)*minimumPerType || len(drills) > maximumDrills || start.IsZero() || !end.After(start) {
		return nil, 0, 0, 0, errors.New("GA drill manifest is incomplete")
	}
	byScenario := make(map[Scenario][]Drill, len(requiredScenarios))
	ids := map[string]struct{}{}
	digests := map[string]struct{}{}
	known := map[Scenario]bool{ScenarioRestore: true, ScenarioDeletion: true, ScenarioCredential: true, ScenarioNotice: true}
	passed, failed, inconclusive := 0, 0, 0
	for _, drill := range drills {
		started, completed := drill.StartedAt.UTC(), drill.CompletedAt.UTC()
		if !known[drill.Scenario] || !opaquePattern.MatchString(drill.DrillID) || strings.Contains(drill.DrillID, "@") || !digestPattern.MatchString(drill.EvidenceSHA256) || started.Before(start) || completed.After(end) || !completed.After(started) || completed.Sub(started) > maximumDuration {
			return nil, 0, 0, 0, errors.New("GA drill is invalid or outside the scorecard window")
		}
		if _, ok := ids[drill.DrillID]; ok {
			return nil, 0, 0, 0, errors.New("GA drill ID is duplicated")
		}
		ids[drill.DrillID] = struct{}{}
		if _, ok := digests[drill.EvidenceSHA256]; ok {
			return nil, 0, 0, 0, errors.New("GA drill evidence is replayed")
		}
		digests[drill.EvidenceSHA256] = struct{}{}
		switch drill.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("GA drill outcome is invalid")
		}
		drill.StartedAt, drill.CompletedAt = started, completed
		byScenario[drill.Scenario] = append(byScenario[drill.Scenario], drill)
	}
	results := make([]ScenarioResult, 0, len(requiredScenarios))
	for _, scenario := range requiredScenarios {
		values := byScenario[scenario]
		if len(values) < minimumPerType {
			return nil, 0, 0, 0, errors.New("GA drill repetitions are incomplete")
		}
		dates := map[string]struct{}{}
		result := ScenarioResult{Scenario: scenario, DrillCount: len(values)}
		for _, drill := range values {
			dates[drill.CompletedAt.Format("2006-01-02")] = struct{}{}
			switch drill.Outcome {
			case OutcomePassed:
				result.PassedCount++
			case OutcomeFailed:
				result.FailedCount++
			case OutcomeInconclusive:
				result.InconclusiveCount++
			}
		}
		result.DistinctDateCount = len(dates)
		if len(dates) < minimumPerType {
			return nil, 0, 0, 0, errors.New("GA drill dates are not repeated")
		}
		result.Ready = result.PassedCount == result.DrillCount
		results = append(results, result)
	}
	return results, passed, failed, inconclusive, nil
}

func Collect(scorecardPath, inputPath string, now time.Time) (Receipt, error) {
	scorecard, scorecardDigest, err := gascorecardevidence.LoadReady(scorecardPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready GA scorecard: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.ReviewID, input.PolicyVersion, input.ScorecardID, input.InventoryID, input.PlanID, input.ChangeID, input.ReleaseID) {
		return Receipt{}, errors.New("GA drill review identity is invalid")
	}
	if input.ScorecardID != scorecard.ScorecardID || input.ScorecardReceiptSHA256 != scorecardDigest || input.InventoryID != scorecard.InventoryID || input.InventoryReceiptSHA256 != scorecard.InventoryReceiptSHA256 || input.PlanID != scorecard.PlanID || input.PlanReceiptSHA256 != scorecard.PlanReceiptSHA256 || input.ChangeID != scorecard.ChangeID || input.ChangeReceiptSHA256 != scorecard.ChangeReceiptSHA256 || input.ReleaseID != scorecard.ReleaseID || input.ReleaseReceiptSHA256 != scorecard.ReleaseReceiptSHA256 || !allDigests(input.ScorecardReceiptSHA256, input.InventoryReceiptSHA256, input.PlanReceiptSHA256, input.ChangeReceiptSHA256, input.ReleaseReceiptSHA256, input.DrillManifestSHA256, input.RepetitionPolicySHA256, input.AccountableReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("GA drill scorecard or artifact binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("GA drill collection time is invalid")
	}
	now = now.UTC()
	generated := input.GeneratedAt.UTC()
	if generated.Before(scorecard.WindowEnd) || generated.Before(now.Add(-maximumAge)) || generated.After(now) {
		return Receipt{}, errors.New("GA drill review timeline is invalid")
	}
	results, drillPassed, drillFailed, drillInconclusive, err := evaluate(input.Drills, scorecard.WindowStart.UTC(), scorecard.WindowEnd.UTC())
	if err != nil {
		return Receipt{}, err
	}
	checks, checkPassed, checkFailed, checkInconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	expectedOutcome := OutcomePassed
	if drillFailed > 0 {
		expectedOutcome = OutcomeFailed
	} else if drillInconclusive > 0 {
		expectedOutcome = OutcomeInconclusive
	}
	if outcomeFor(checks, CheckOutcomes) != expectedOutcome {
		return Receipt{}, errors.New("GA drill outcome check contradicts evidence")
	}
	for _, id := range []CheckID{CheckManifest, CheckWindow, CheckRepetition, CheckDates, CheckUnique} {
		if outcomeFor(checks, id) != OutcomePassed {
			return Receipt{}, errors.New("GA drill structural check contradicts accepted evidence")
		}
	}
	ready := drillFailed == 0 && drillInconclusive == 0 && checkPassed == len(requiredChecks) && checkFailed == 0 && checkInconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("GA drill readiness contradicts evidence")
	}
	input.Schema = ReceiptSchemaV1
	input.GeneratedAt = generated
	input.Checks = checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, WindowStart: scorecard.WindowStart.UTC(), WindowEnd: scorecard.WindowEnd.UTC(), DrillCount: len(input.Drills), ScenarioCount: len(results), PassedDrillCount: drillPassed, FailedDrillCount: drillFailed, InconclusiveDrillCount: drillInconclusive, ScenarioResults: results, CheckCount: len(checks), PassedCheckCount: checkPassed, FailedCheckCount: checkFailed, InconclusiveCheckCount: checkInconclusive}, nil
}

func LoadReady(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "production_external" || receipt.Environment != "production" || !receipt.Ready || receipt.ScenarioCount != len(requiredScenarios) || receipt.CheckCount != len(requiredChecks) || receipt.FailedDrillCount != 0 || receipt.InconclusiveDrillCount != 0 || receipt.PassedDrillCount != receipt.DrillCount || receipt.PassedCheckCount != len(requiredChecks) || receipt.FailedCheckCount != 0 || receipt.InconclusiveCheckCount != 0 {
		return Receipt{}, "", errors.New("GA drill receipt is not ready")
	}
	if !allOpaque(receipt.ReviewID, receipt.PolicyVersion, receipt.ScorecardID, receipt.InventoryID, receipt.PlanID, receipt.ChangeID, receipt.ReleaseID) || !allDigests(receipt.ScorecardReceiptSHA256, receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.DrillManifestSHA256, receipt.RepetitionPolicySHA256, receipt.AccountableReviewSHA256, receipt.InputSHA256, digest) {
		return Receipt{}, "", errors.New("GA drill receipt identity or binding is invalid")
	}
	results, passed, failed, inconclusive, err := evaluate(receipt.Drills, receipt.WindowStart.UTC(), receipt.WindowEnd.UTC())
	if err != nil || passed != receipt.PassedDrillCount || failed != 0 || inconclusive != 0 || len(results) != len(receipt.ScenarioResults) {
		return Receipt{}, "", errors.New("GA drill receipt derivation is invalid")
	}
	for index := range results {
		if results[index] != receipt.ScenarioResults[index] {
			return Receipt{}, "", errors.New("GA drill receipt scenario result is invalid")
		}
	}
	checks, checkPassed, checkFailed, checkInconclusive, err := validateChecks(receipt.Checks)
	if err != nil || checkPassed != len(requiredChecks) || checkFailed != 0 || checkInconclusive != 0 {
		return Receipt{}, "", errors.New("GA drill receipt checks are invalid")
	}
	if receipt.GeneratedAt.After(receipt.CollectedAt) || receipt.CollectedAt.IsZero() {
		return Receipt{}, "", errors.New("GA drill receipt timeline is invalid")
	}
	receipt.Checks, receipt.ScenarioResults = checks, results
	return receipt, digest, nil
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("GA drill checks are incomplete")
	}
	byID := map[CheckID]Check{}
	for _, check := range input {
		if !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("GA drill check is invalid")
		}
		if _, ok := byID[check.ID]; ok {
			return nil, 0, 0, 0, errors.New("GA drill check is duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("GA drill check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("GA drill check outcome is invalid")
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
func allOpaque(values ...string) bool {
	for _, v := range values {
		if !opaquePattern.MatchString(v) || strings.Contains(v, "@") {
			return false
		}
	}
	return true
}
func allDigests(values ...string) bool {
	for _, v := range values {
		if !digestPattern.MatchString(v) {
			return false
		}
	}
	return true
}

func decodeStrictRegular(path string, destination any) (string, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumBytes {
		return "", errors.New("GA drill input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open GA drill input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("GA drill input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return "", errors.New("read GA drill input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("GA drill input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("GA drill input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("GA drill input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("GA drill input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("GA drill receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("GA drill receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect GA drill receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-ga-drills-*")
}
