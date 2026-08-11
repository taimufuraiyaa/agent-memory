// Package securityclosureevidence normalizes independent high/critical finding
// closure and retest evidence for P10.2-B.
package securityclosureevidence

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

type SourceID string
type Outcome string
type Severity string
type Exploitability string
type FindingState string
type RetestOutcome string
type CheckID string

const (
	SourceApplicationPenetration SourceID = "application_penetration"
	SourceTenantIsolation        SourceID = "tenant_isolation"
	SourceDependencySupplyChain  SourceID = "dependency_supply_chain"
	SourceContainerImage         SourceID = "container_image"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"

	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"

	ExploitabilityExploitable    Exploitability = "exploitable"
	ExploitabilityNotExploitable Exploitability = "not_exploitable"
	ExploitabilityInconclusive   Exploitability = "inconclusive"

	FindingOpen   FindingState = "open"
	FindingClosed FindingState = "closed"

	RetestPassed       RetestOutcome = "passed"
	RetestFailed       RetestOutcome = "failed"
	RetestInconclusive RetestOutcome = "inconclusive"
	RetestNotRequired  RetestOutcome = "not_required"

	CheckSourceExports      CheckID = "assessment_source_exports_complete"
	CheckAssessmentCoverage CheckID = "assessment_scope_coverage_complete"
	CheckClassification     CheckID = "severity_and_exploitability_classified"
	CheckRegisterReconciled CheckID = "finding_register_reconciled"
	CheckRemediationClosure CheckID = "critical_high_remediation_closed"
	CheckIndependentRetests CheckID = "critical_high_independent_retests_passed"
	CheckSecurityReview     CheckID = "security_release_review_complete"
)

const (
	InputSchemaV1        = "agent-memory-staging-security-closure-input-v1"
	ReceiptSchemaV1      = "agent-memory-staging-security-closure-receipt-v1"
	maximumCount         = 100_000_000
	maximumInputBytes    = 1 << 20
	maximumCollectionAge = 24 * time.Hour
)

var (
	requiredSources = []SourceID{SourceApplicationPenetration, SourceTenantIsolation, SourceDependencySupplyChain, SourceContainerImage}
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks  = []CheckID{CheckSourceExports, CheckAssessmentCoverage, CheckClassification, CheckRegisterReconciled, CheckRemediationClosure, CheckIndependentRetests, CheckSecurityReview}
)

type AssessmentSource struct {
	ID                  SourceID `json:"id"`
	Outcome             Outcome  `json:"outcome"`
	ExpectedTargetCount int      `json:"expected_target_count"`
	ObservedTargetCount int      `json:"observed_target_count"`
	EvidenceSHA256      string   `json:"evidence_sha256"`
}

type Finding struct {
	FingerprintSHA256 string         `json:"fingerprint_sha256"`
	Severity          Severity       `json:"severity"`
	Exploitability    Exploitability `json:"exploitability"`
	State             FindingState   `json:"state"`
	RetestOutcome     RetestOutcome  `json:"retest_outcome"`
	EvidenceSHA256    string         `json:"evidence_sha256"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                     string             `json:"schema"`
	Classification             string             `json:"classification"`
	Environment                string             `json:"environment"`
	ReviewID                   string             `json:"review_id"`
	RegisterVersion            string             `json:"register_version"`
	ScopeVersion               string             `json:"scope_version"`
	InventoryID                string             `json:"inventory_id"`
	InventoryReceiptSHA256     string             `json:"inventory_receipt_sha256"`
	PlanID                     string             `json:"plan_id"`
	PlanReceiptSHA256          string             `json:"plan_receipt_sha256"`
	ChangeID                   string             `json:"change_id"`
	ChangeReceiptSHA256        string             `json:"change_receipt_sha256"`
	ReleaseID                  string             `json:"release_id"`
	ReleaseReceiptSHA256       string             `json:"release_receipt_sha256"`
	SourceManifestSHA256       string             `json:"source_manifest_sha256"`
	FindingRegisterSHA256      string             `json:"finding_register_sha256"`
	ClassificationPolicySHA256 string             `json:"classification_policy_sha256"`
	RetestReportSHA256         string             `json:"retest_report_sha256"`
	SecurityReviewSHA256       string             `json:"security_review_sha256"`
	SnapshotAt                 time.Time          `json:"snapshot_at"`
	ReviewedAt                 time.Time          `json:"reviewed_at"`
	GeneratedAt                time.Time          `json:"generated_at"`
	Ready                      bool               `json:"ready"`
	Sources                    []AssessmentSource `json:"sources"`
	Findings                   []Finding          `json:"findings"`
	Checks                     []Check            `json:"checks"`
}

type Receipt struct {
	Input
	Schema                          string    `json:"schema"`
	InputSHA256                     string    `json:"input_sha256"`
	CollectedAt                     time.Time `json:"collected_at"`
	CoverageComplete                bool      `json:"coverage_complete"`
	ExpectedTargetCount             int       `json:"expected_target_count"`
	ObservedTargetCount             int       `json:"observed_target_count"`
	FindingCount                    int       `json:"finding_count"`
	BlockingFindingCount            int       `json:"blocking_finding_count"`
	OpenBlockingFindingCount        int       `json:"open_blocking_finding_count"`
	RetestIncompleteCount           int       `json:"retest_incomplete_count"`
	InconclusiveClassificationCount int       `json:"inconclusive_classification_count"`
	SourceCount                     int       `json:"source_count"`
	CheckCount                      int       `json:"check_count"`
	PassedCount                     int       `json:"passed_count"`
	FailedCount                     int       `json:"failed_count"`
	InconclusiveCount               int       `json:"inconclusive_count"`
}

type Evaluation struct {
	CoverageComplete                bool
	FindingCount                    int
	BlockingFindingCount            int
	OpenBlockingFindingCount        int
	RetestIncompleteCount           int
	InconclusiveClassificationCount int
	Ready                           bool
}

func RequiredSources() []SourceID { return append([]SourceID(nil), requiredSources...) }
func RequiredChecks() []CheckID   { return append([]CheckID(nil), requiredChecks...) }

func evaluate(sources []AssessmentSource, findings []Finding) (Evaluation, error) {
	if len(sources) != len(requiredSources) || len(findings) > 10_000 {
		return Evaluation{}, errors.New("security closure source or finding set is incomplete")
	}
	bySource := make(map[SourceID]AssessmentSource, len(sources))
	for _, source := range sources {
		if _, duplicate := bySource[source.ID]; duplicate || !digestPattern.MatchString(source.EvidenceSHA256) || source.ExpectedTargetCount <= 0 || source.ExpectedTargetCount > maximumCount || source.ObservedTargetCount <= 0 || source.ObservedTargetCount > source.ExpectedTargetCount {
			return Evaluation{}, errors.New("security closure assessment source is invalid or duplicated")
		}
		complete := source.ExpectedTargetCount == source.ObservedTargetCount
		if source.Outcome == OutcomePassed && !complete || source.Outcome != OutcomePassed && source.Outcome != OutcomeFailed && source.Outcome != OutcomeInconclusive {
			return Evaluation{}, errors.New("security closure assessment outcome contradicts coverage")
		}
		bySource[source.ID] = source
	}
	coverageComplete := true
	for _, id := range requiredSources {
		source, ok := bySource[id]
		if !ok {
			return Evaluation{}, errors.New("security closure assessment source is missing")
		}
		if source.ExpectedTargetCount != source.ObservedTargetCount || source.Outcome != OutcomePassed {
			coverageComplete = false
		}
	}

	fingerprints := make(map[string]struct{}, len(findings))
	result := Evaluation{CoverageComplete: coverageComplete, FindingCount: len(findings)}
	for _, finding := range findings {
		if !digestPattern.MatchString(finding.FingerprintSHA256) || !digestPattern.MatchString(finding.EvidenceSHA256) {
			return Evaluation{}, errors.New("security closure finding digest is invalid")
		}
		if _, duplicate := fingerprints[finding.FingerprintSHA256]; duplicate {
			return Evaluation{}, errors.New("security closure finding fingerprint is duplicated")
		}
		fingerprints[finding.FingerprintSHA256] = struct{}{}
		if !validFindingEnums(finding) {
			return Evaluation{}, errors.New("security closure finding classification is invalid")
		}
		if finding.Exploitability == ExploitabilityInconclusive {
			result.InconclusiveClassificationCount++
		}
		blocking := (finding.Severity == SeverityCritical || finding.Severity == SeverityHigh) && finding.Exploitability != ExploitabilityNotExploitable
		if finding.State == FindingOpen && finding.RetestOutcome == RetestPassed || blocking && finding.RetestOutcome == RetestNotRequired {
			return Evaluation{}, errors.New("security closure finding lifecycle is impossible")
		}
		if blocking {
			result.BlockingFindingCount++
			if finding.State != FindingClosed {
				result.OpenBlockingFindingCount++
			}
			if finding.RetestOutcome != RetestPassed {
				result.RetestIncompleteCount++
			}
		}
	}
	result.Ready = result.CoverageComplete && result.InconclusiveClassificationCount == 0 && result.OpenBlockingFindingCount == 0 && result.RetestIncompleteCount == 0
	return result, nil
}

func validFindingEnums(finding Finding) bool {
	severity := finding.Severity == SeverityCritical || finding.Severity == SeverityHigh || finding.Severity == SeverityMedium || finding.Severity == SeverityLow
	exploitability := finding.Exploitability == ExploitabilityExploitable || finding.Exploitability == ExploitabilityNotExploitable || finding.Exploitability == ExploitabilityInconclusive
	state := finding.State == FindingOpen || finding.State == FindingClosed
	retest := finding.RetestOutcome == RetestPassed || finding.RetestOutcome == RetestFailed || finding.RetestOutcome == RetestInconclusive || finding.RetestOutcome == RetestNotRequired
	return severity && exploitability && state && retest
}

func Collect(inventoryPath, planPath, changePath, releasePath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load platform inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load infrastructure plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load infrastructure change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load passed release: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return Receipt{}, errors.New("security closure platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "staging" || release.Namespace != "agent-memory-staging" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("security closure release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !allOpaque(input.ReviewID, input.RegisterVersion, input.ScopeVersion, input.InventoryID, input.PlanID, input.ChangeID, input.ReleaseID) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !allDigests(input.InventoryReceiptSHA256, input.PlanReceiptSHA256, input.ChangeReceiptSHA256, input.ReleaseReceiptSHA256, input.SourceManifestSHA256, input.FindingRegisterSHA256, input.ClassificationPolicySHA256, input.RetestReportSHA256, input.SecurityReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("security closure identity or artifact binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("security closure collection time is invalid")
	}
	now = now.UTC()
	snapshot, reviewed, generated := input.SnapshotAt.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if snapshot.IsZero() || snapshot.Before(earliest) || snapshot.Before(now.Add(-maximumCollectionAge)) || reviewed.Before(snapshot) || reviewed.Sub(snapshot) > maximumCollectionAge || generated.Before(reviewed) || generated.Before(now.Add(-maximumCollectionAge)) || generated.After(now) {
		return Receipt{}, errors.New("security closure timeline is invalid")
	}
	evaluation, err := evaluate(input.Sources, input.Findings)
	if err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	securityReview := outcomeFor(checks, CheckSecurityReview)
	if !evaluation.Ready && securityReview == OutcomePassed {
		return Receipt{}, errors.New("security closure review contradicts unresolved technical evidence")
	}
	expected := map[CheckID]Outcome{CheckSourceExports: OutcomePassed, CheckClassification: outcome(evaluation.InconclusiveClassificationCount == 0), CheckRegisterReconciled: OutcomePassed, CheckAssessmentCoverage: outcome(evaluation.CoverageComplete), CheckRemediationClosure: outcome(evaluation.OpenBlockingFindingCount == 0), CheckIndependentRetests: outcome(evaluation.RetestIncompleteCount == 0), CheckSecurityReview: securityReview}
	for _, check := range checks {
		if check.Outcome != expected[check.ID] {
			return Receipt{}, errors.New("security closure check contradicts derived evidence")
		}
	}
	ready := evaluation.Ready && passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("security closure readiness contradicts evidence")
	}
	expectedTargets, observedTargets := 0, 0
	for _, source := range input.Sources {
		if expectedTargets > maximumCount-source.ExpectedTargetCount || observedTargets > maximumCount-source.ObservedTargetCount {
			return Receipt{}, errors.New("security closure target count overflows")
		}
		expectedTargets += source.ExpectedTargetCount
		observedTargets += source.ObservedTargetCount
	}
	input.Schema, input.SnapshotAt, input.ReviewedAt, input.GeneratedAt, input.Checks = ReceiptSchemaV1, snapshot, reviewed, generated, checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, CoverageComplete: evaluation.CoverageComplete, ExpectedTargetCount: expectedTargets, ObservedTargetCount: observedTargets, FindingCount: evaluation.FindingCount, BlockingFindingCount: evaluation.BlockingFindingCount, OpenBlockingFindingCount: evaluation.OpenBlockingFindingCount, RetestIncompleteCount: evaluation.RetestIncompleteCount, InconclusiveClassificationCount: evaluation.InconclusiveClassificationCount, SourceCount: len(input.Sources), CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("security closure checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(input))
	for _, check := range input {
		if _, duplicate := byID[check.ID]; duplicate || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("security closure check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("security closure check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("security closure check outcome is invalid")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}

func outcome(value bool) Outcome {
	if value {
		return OutcomePassed
	}
	return OutcomeFailed
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
		return "", errors.New("security closure input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open security closure input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("security closure input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return "", errors.New("read security closure input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("security closure input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("security closure input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("security closure input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("security closure input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("security closure receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("security closure receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect security closure receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-security-closure-*")
}
