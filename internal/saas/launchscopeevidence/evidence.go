// Package launchscopeevidence normalizes content-free P0.1 launch-scope and
// legal-position review evidence without interpreting law or approving launch.
package launchscopeevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const (
	InputSchemaV1        = "agent-memory-launch-scope-input-v1"
	ReceiptSchemaV1      = "agent-memory-launch-scope-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumCollectionAge = 24 * time.Hour
	maximumAggregate     = 1_000_000
)

type LegalPositionID string
type CheckID string
type Outcome string

const (
	LegalRightsAttestation LegalPositionID = "rights_attestation"
	LegalSourceRetention   LegalPositionID = "source_retention"
	LegalRightsNotice      LegalPositionID = "rights_notice"
	LegalDeletion          LegalPositionID = "deletion"
	LegalBackup            LegalPositionID = "backup"
	LegalAuditRetention    LegalPositionID = "audit_retention"

	CheckLaunchScopeApproved      CheckID = "launch_scope_approved"
	CheckJurisdictionMemoComplete CheckID = "jurisdiction_memo_complete"
	CheckMinimumAgeRecorded       CheckID = "minimum_age_recorded"
	CheckSupportCoverageRecorded  CheckID = "support_language_coverage_recorded"
	CheckNoticeRoutingRecorded    CheckID = "notice_jurisdiction_routing_recorded"
	CheckLegalPositionsReviewed   CheckID = "legal_positions_reviewed"
	CheckRiskRegisterReconciled   CheckID = "risk_register_reconciled"
	CheckAccountableReview        CheckID = "product_counsel_privacy_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern          = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredLegalPositions = []LegalPositionID{
		LegalRightsAttestation, LegalSourceRetention, LegalRightsNotice,
		LegalDeletion, LegalBackup, LegalAuditRetention,
	}
	requiredChecks = []CheckID{
		CheckLaunchScopeApproved, CheckJurisdictionMemoComplete,
		CheckMinimumAgeRecorded, CheckSupportCoverageRecorded,
		CheckNoticeRoutingRecorded, CheckLegalPositionsReviewed,
		CheckRiskRegisterReconciled, CheckAccountableReview,
	}
)

type LegalPosition struct {
	ID                   LegalPositionID `json:"id"`
	PolicyCopySHA256     string          `json:"policy_copy_sha256"`
	ReviewEvidenceSHA256 string          `json:"review_evidence_sha256"`
	Outcome              Outcome         `json:"outcome"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                    string          `json:"schema"`
	Classification            string          `json:"classification"`
	Environment               string          `json:"environment"`
	ScopeDecisionID           string          `json:"scope_decision_id"`
	ScopeDecisionVersion      string          `json:"scope_decision_version"`
	JurisdictionPolicyVersion string          `json:"jurisdiction_policy_version"`
	LegalReviewVersion        string          `json:"legal_review_version"`
	RiskRegisterVersion       string          `json:"risk_register_version"`
	DecisionRegisterSHA256    string          `json:"decision_register_sha256"`
	LaunchScopeDecisionSHA256 string          `json:"launch_scope_decision_sha256"`
	JurisdictionMemoSHA256    string          `json:"jurisdiction_memo_sha256"`
	PolicyManifestSHA256      string          `json:"policy_manifest_sha256"`
	LegalReviewSHA256         string          `json:"legal_review_sha256"`
	RiskRegisterSHA256        string          `json:"risk_register_sha256"`
	ScopeApprovedAt           time.Time       `json:"scope_approved_at"`
	LegalReviewCompletedAt    time.Time       `json:"legal_review_completed_at"`
	GeneratedAt               time.Time       `json:"generated_at"`
	LaunchCountryCount        int             `json:"launch_country_count"`
	MinimumAgeYears           int             `json:"minimum_age_years"`
	SupportLanguageCount      int             `json:"support_language_count"`
	NoticeJurisdictionCount   int             `json:"notice_jurisdiction_count"`
	BlockingRiskCount         int             `json:"blocking_risk_count"`
	UnownedRiskCount          int             `json:"unowned_risk_count"`
	DeferredRiskCount         int             `json:"deferred_risk_count"`
	Ready                     bool            `json:"ready"`
	LegalPositions            []LegalPosition `json:"legal_positions"`
	Checks                    []Check         `json:"checks"`
}

type Receipt struct {
	Schema                    string          `json:"schema"`
	Classification            string          `json:"classification"`
	Environment               string          `json:"environment"`
	ScopeDecisionID           string          `json:"scope_decision_id"`
	ScopeDecisionVersion      string          `json:"scope_decision_version"`
	JurisdictionPolicyVersion string          `json:"jurisdiction_policy_version"`
	LegalReviewVersion        string          `json:"legal_review_version"`
	RiskRegisterVersion       string          `json:"risk_register_version"`
	DecisionRegisterSHA256    string          `json:"decision_register_sha256"`
	LaunchScopeDecisionSHA256 string          `json:"launch_scope_decision_sha256"`
	JurisdictionMemoSHA256    string          `json:"jurisdiction_memo_sha256"`
	PolicyManifestSHA256      string          `json:"policy_manifest_sha256"`
	LegalReviewSHA256         string          `json:"legal_review_sha256"`
	RiskRegisterSHA256        string          `json:"risk_register_sha256"`
	InputSHA256               string          `json:"input_sha256"`
	ScopeApprovedAt           time.Time       `json:"scope_approved_at"`
	LegalReviewCompletedAt    time.Time       `json:"legal_review_completed_at"`
	GeneratedAt               time.Time       `json:"generated_at"`
	CollectedAt               time.Time       `json:"collected_at"`
	LaunchCountryCount        int             `json:"launch_country_count"`
	MinimumAgeYears           int             `json:"minimum_age_years"`
	SupportLanguageCount      int             `json:"support_language_count"`
	NoticeJurisdictionCount   int             `json:"notice_jurisdiction_count"`
	BlockingRiskCount         int             `json:"blocking_risk_count"`
	UnownedRiskCount          int             `json:"unowned_risk_count"`
	DeferredRiskCount         int             `json:"deferred_risk_count"`
	Ready                     bool            `json:"ready"`
	LegalPositionCount        int             `json:"legal_position_count"`
	LegalPassedCount          int             `json:"legal_passed_count"`
	LegalFailedCount          int             `json:"legal_failed_count"`
	LegalInconclusiveCount    int             `json:"legal_inconclusive_count"`
	CheckCount                int             `json:"check_count"`
	PassedCount               int             `json:"passed_count"`
	FailedCount               int             `json:"failed_count"`
	InconclusiveCount         int             `json:"inconclusive_count"`
	LegalPositions            []LegalPosition `json:"legal_positions"`
	Checks                    []Check         `json:"checks"`
}

func RequiredLegalPositions() []LegalPositionID {
	return append([]LegalPositionID(nil), requiredLegalPositions...)
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(inputPath string, now time.Time) (Receipt, error) {
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(input, inputDigest, now)
}

// LoadReady strictly reloads a published receipt, re-derives every normalized
// field, and returns the SHA-256 of the exact receipt bytes. It intentionally
// accepts only ready receipts because downstream gates must fail closed.
func LoadReady(path string) (Receipt, string, error) {
	var receipt Receipt
	receiptDigest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	input := Input{
		Schema: InputSchemaV1, Classification: receipt.Classification, Environment: receipt.Environment,
		ScopeDecisionID: receipt.ScopeDecisionID, ScopeDecisionVersion: receipt.ScopeDecisionVersion,
		JurisdictionPolicyVersion: receipt.JurisdictionPolicyVersion, LegalReviewVersion: receipt.LegalReviewVersion,
		RiskRegisterVersion: receipt.RiskRegisterVersion, DecisionRegisterSHA256: receipt.DecisionRegisterSHA256,
		LaunchScopeDecisionSHA256: receipt.LaunchScopeDecisionSHA256, JurisdictionMemoSHA256: receipt.JurisdictionMemoSHA256,
		PolicyManifestSHA256: receipt.PolicyManifestSHA256, LegalReviewSHA256: receipt.LegalReviewSHA256,
		RiskRegisterSHA256: receipt.RiskRegisterSHA256, ScopeApprovedAt: receipt.ScopeApprovedAt,
		LegalReviewCompletedAt: receipt.LegalReviewCompletedAt, GeneratedAt: receipt.GeneratedAt,
		LaunchCountryCount: receipt.LaunchCountryCount, MinimumAgeYears: receipt.MinimumAgeYears,
		SupportLanguageCount: receipt.SupportLanguageCount, NoticeJurisdictionCount: receipt.NoticeJurisdictionCount,
		BlockingRiskCount: receipt.BlockingRiskCount, UnownedRiskCount: receipt.UnownedRiskCount,
		DeferredRiskCount: receipt.DeferredRiskCount, Ready: receipt.Ready,
		LegalPositions: receipt.LegalPositions, Checks: receipt.Checks,
	}
	rebuilt, err := build(input, receipt.InputSHA256, receipt.CollectedAt)
	if err != nil || !receipt.Ready || !reflect.DeepEqual(receipt, rebuilt) {
		return Receipt{}, "", errors.New("launch-scope receipt is not a valid ready receipt")
	}
	return receipt, receiptDigest, nil
}

func build(input Input, inputDigest string, now time.Time) (Receipt, error) {
	if input.Schema != InputSchemaV1 || input.Classification != "external_business" || input.Environment != "external" ||
		!allOpaque(input.ScopeDecisionID, input.ScopeDecisionVersion, input.JurisdictionPolicyVersion, input.LegalReviewVersion, input.RiskRegisterVersion) ||
		!allDigests(input.DecisionRegisterSHA256, input.LaunchScopeDecisionSHA256, input.JurisdictionMemoSHA256, input.PolicyManifestSHA256, input.LegalReviewSHA256, input.RiskRegisterSHA256, inputDigest) {
		return Receipt{}, errors.New("launch-scope input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("launch-scope collection time is invalid")
	}
	now = now.UTC()
	approved, reviewed, generated := input.ScopeApprovedAt.UTC(), input.LegalReviewCompletedAt.UTC(), input.GeneratedAt.UTC()
	if approved.IsZero() || reviewed.IsZero() || generated.IsZero() || approved.After(generated) || reviewed.After(generated) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("launch-scope evidence timeline is invalid")
	}
	if input.LaunchCountryCount < 1 || input.SupportLanguageCount < 1 || input.NoticeJurisdictionCount < 1 || input.MinimumAgeYears < 1 || input.MinimumAgeYears > 120 ||
		input.LaunchCountryCount > maximumAggregate || input.SupportLanguageCount > maximumAggregate || input.NoticeJurisdictionCount > maximumAggregate ||
		input.BlockingRiskCount < 0 || input.UnownedRiskCount < 0 || input.DeferredRiskCount < 0 || input.BlockingRiskCount > maximumAggregate || input.UnownedRiskCount > maximumAggregate || input.DeferredRiskCount > maximumAggregate {
		return Receipt{}, errors.New("launch-scope aggregate is invalid")
	}
	positions, legalPassed, legalFailed, legalInconclusive, err := validateLegalPositions(input.LegalPositions)
	if err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	legalOutcome := aggregateOutcome(legalPassed, legalFailed, legalInconclusive, len(requiredLegalPositions))
	if outcomeFor(checks, CheckMinimumAgeRecorded) != OutcomePassed || outcomeFor(checks, CheckSupportCoverageRecorded) != OutcomePassed || outcomeFor(checks, CheckNoticeRoutingRecorded) != OutcomePassed ||
		outcomeFor(checks, CheckLegalPositionsReviewed) != legalOutcome || outcomeFor(checks, CheckRiskRegisterReconciled) != riskOutcome(input.BlockingRiskCount, input.UnownedRiskCount) {
		return Receipt{}, errors.New("launch-scope check contradicts aggregate evidence")
	}
	ready := legalPassed == len(requiredLegalPositions) && legalFailed == 0 && legalInconclusive == 0 && passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && input.BlockingRiskCount == 0 && input.UnownedRiskCount == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("launch-scope readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment,
		ScopeDecisionID: input.ScopeDecisionID, ScopeDecisionVersion: input.ScopeDecisionVersion, JurisdictionPolicyVersion: input.JurisdictionPolicyVersion, LegalReviewVersion: input.LegalReviewVersion, RiskRegisterVersion: input.RiskRegisterVersion,
		DecisionRegisterSHA256: input.DecisionRegisterSHA256, LaunchScopeDecisionSHA256: input.LaunchScopeDecisionSHA256, JurisdictionMemoSHA256: input.JurisdictionMemoSHA256, PolicyManifestSHA256: input.PolicyManifestSHA256, LegalReviewSHA256: input.LegalReviewSHA256, RiskRegisterSHA256: input.RiskRegisterSHA256, InputSHA256: inputDigest,
		ScopeApprovedAt: approved, LegalReviewCompletedAt: reviewed, GeneratedAt: generated, CollectedAt: now,
		LaunchCountryCount: input.LaunchCountryCount, MinimumAgeYears: input.MinimumAgeYears, SupportLanguageCount: input.SupportLanguageCount, NoticeJurisdictionCount: input.NoticeJurisdictionCount,
		BlockingRiskCount: input.BlockingRiskCount, UnownedRiskCount: input.UnownedRiskCount, DeferredRiskCount: input.DeferredRiskCount,
		Ready: ready, LegalPositionCount: len(positions), LegalPassedCount: legalPassed, LegalFailedCount: legalFailed, LegalInconclusiveCount: legalInconclusive,
		CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, LegalPositions: positions, Checks: checks}, nil
}

func validateLegalPositions(values []LegalPosition) ([]LegalPosition, int, int, int, error) {
	if len(values) != len(requiredLegalPositions) {
		return nil, 0, 0, 0, errors.New("launch-scope legal positions are incomplete")
	}
	byID := make(map[LegalPositionID]LegalPosition, len(values))
	passed, failed, inconclusive := 0, 0, 0
	for _, value := range values {
		if !allDigests(value.PolicyCopySHA256, value.ReviewEvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("launch-scope legal-position digest is invalid")
		}
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("launch-scope legal position is duplicated")
		}
		switch value.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("launch-scope legal-position outcome is invalid")
		}
		byID[value.ID] = value
	}
	ordered := make([]LegalPosition, 0, len(requiredLegalPositions))
	for _, id := range requiredLegalPositions {
		value, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("launch-scope required legal position is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, passed, failed, inconclusive, nil
}

func validateChecks(values []Check) ([]Check, int, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("launch-scope checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(values))
	passed, failed, inconclusive := 0, 0, 0
	for _, value := range values {
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("launch-scope check digest is invalid")
		}
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("launch-scope check is duplicated")
		}
		switch value.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("launch-scope check outcome is invalid")
		}
		byID[value.ID] = value
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		value, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("launch-scope required check is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, passed, failed, inconclusive, nil
}

func aggregateOutcome(passed, failed, inconclusive, total int) Outcome {
	if failed > 0 {
		return OutcomeFailed
	}
	if inconclusive > 0 || passed != total {
		return OutcomeInconclusive
	}
	return OutcomePassed
}

func riskOutcome(blocking, unowned int) Outcome {
	if blocking > 0 || unowned > 0 {
		return OutcomeFailed
	}
	return OutcomePassed
}

func outcomeFor(values []Check, id CheckID) Outcome {
	for _, value := range values {
		if value.ID == id {
			return value.Outcome
		}
	}
	return ""
}

func allOpaque(values ...string) bool {
	for _, value := range values {
		if !opaquePattern.MatchString(value) {
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
	if strings.TrimSpace(path) == "" {
		return "", errors.New("launch-scope input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("launch-scope input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open launch-scope input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("launch-scope input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read launch-scope input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("launch-scope input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("launch-scope input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("launch-scope input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("launch-scope input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("launch-scope receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("launch-scope receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect launch-scope receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-launch-scope-*")
}
