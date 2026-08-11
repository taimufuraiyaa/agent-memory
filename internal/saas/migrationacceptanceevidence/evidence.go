// Package migrationacceptanceevidence normalizes content-free migration
// parity and rollback tabletop acceptance evidence for CP9-B.
package migrationacceptanceevidence

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/migrationcohortevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/parityevidence"
)

const (
	InputSchemaV1        = "agent-memory-staging-migration-acceptance-input-v1"
	ReceiptSchemaV1      = "agent-memory-staging-migration-acceptance-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumTabletopSpan  = 4 * time.Hour
	maximumCollectionAge = 24 * time.Hour
)

type CheckID string
type Outcome string

const (
	CheckOriginalLocalCopyPreserved CheckID = "original_local_copy_preserved"
	CheckHostedProfileDisabled      CheckID = "hosted_profile_disabled"
	CheckCredentialRevocation       CheckID = "credential_revocation_rehearsed"
	CheckImportReportReconciled     CheckID = "import_report_reconciled"
	CheckHostedDeletionPathReviewed CheckID = "hosted_deletion_path_reviewed"
	CheckExplicitLocalContinuity    CheckID = "explicit_local_continuity_confirmed"
	CheckFreshRemigrationBundle     CheckID = "remigration_requires_fresh_bundle"
	CheckCrossFunctionalReview      CheckID = "product_engineering_operations_review_complete"
	OutcomePassed                   Outcome = "passed"
	OutcomeFailed                   Outcome = "failed"
	OutcomeInconclusive             Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{
		CheckOriginalLocalCopyPreserved, CheckHostedProfileDisabled,
		CheckCredentialRevocation, CheckImportReportReconciled,
		CheckHostedDeletionPathReviewed, CheckExplicitLocalContinuity,
		CheckFreshRemigrationBundle, CheckCrossFunctionalReview,
	}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                   string    `json:"schema"`
	Classification           string    `json:"classification"`
	Environment              string    `json:"environment"`
	AcceptanceID             string    `json:"acceptance_id"`
	RollbackPlanVersion      string    `json:"rollback_plan_version"`
	CohortID                 string    `json:"cohort_id"`
	CohortReceiptSHA256      string    `json:"cohort_receipt_sha256"`
	ParityEvaluationID       string    `json:"parity_evaluation_id"`
	ParityReceiptSHA256      string    `json:"parity_receipt_sha256"`
	InventoryID              string    `json:"inventory_id"`
	InventoryReceiptSHA256   string    `json:"inventory_receipt_sha256"`
	PlanID                   string    `json:"plan_id"`
	PlanReceiptSHA256        string    `json:"plan_receipt_sha256"`
	ChangeID                 string    `json:"change_id"`
	ChangeReceiptSHA256      string    `json:"change_receipt_sha256"`
	ReleaseID                string    `json:"release_id"`
	ReleaseReceiptSHA256     string    `json:"release_receipt_sha256"`
	DatasetVersion           string    `json:"dataset_version"`
	RollbackPlanSHA256       string    `json:"rollback_plan_sha256"`
	TabletopReportSHA256     string    `json:"tabletop_report_sha256"`
	AcceptanceDecisionSHA256 string    `json:"acceptance_decision_sha256"`
	StartedAt                time.Time `json:"started_at"`
	CompletedAt              time.Time `json:"completed_at"`
	GeneratedAt              time.Time `json:"generated_at"`
	Ready                    bool      `json:"ready"`
	Checks                   []Check   `json:"checks"`
}

type Receipt struct {
	Schema                   string    `json:"schema"`
	Classification           string    `json:"classification"`
	Environment              string    `json:"environment"`
	AcceptanceID             string    `json:"acceptance_id"`
	RollbackPlanVersion      string    `json:"rollback_plan_version"`
	CohortID                 string    `json:"cohort_id"`
	CohortReceiptSHA256      string    `json:"cohort_receipt_sha256"`
	ParityEvaluationID       string    `json:"parity_evaluation_id"`
	ParityReceiptSHA256      string    `json:"parity_receipt_sha256"`
	EvidenceBundleSHA256     string    `json:"evidence_bundle_sha256"`
	InventoryID              string    `json:"inventory_id"`
	InventoryReceiptSHA256   string    `json:"inventory_receipt_sha256"`
	PlanID                   string    `json:"plan_id"`
	PlanReceiptSHA256        string    `json:"plan_receipt_sha256"`
	ChangeID                 string    `json:"change_id"`
	ChangeReceiptSHA256      string    `json:"change_receipt_sha256"`
	ReleaseID                string    `json:"release_id"`
	ReleaseReceiptSHA256     string    `json:"release_receipt_sha256"`
	DatasetVersion           string    `json:"dataset_version"`
	RollbackPlanSHA256       string    `json:"rollback_plan_sha256"`
	TabletopReportSHA256     string    `json:"tabletop_report_sha256"`
	AcceptanceDecisionSHA256 string    `json:"acceptance_decision_sha256"`
	InputSHA256              string    `json:"input_sha256"`
	StartedAt                time.Time `json:"started_at"`
	CompletedAt              time.Time `json:"completed_at"`
	GeneratedAt              time.Time `json:"generated_at"`
	CollectedAt              time.Time `json:"collected_at"`
	Ready                    bool      `json:"ready"`
	CheckCount               int       `json:"check_count"`
	PassedCount              int       `json:"passed_count"`
	FailedCount              int       `json:"failed_count"`
	InconclusiveCount        int       `json:"inconclusive_count"`
	Checks                   []Check   `json:"checks"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(cohortPath, parityPath, inputPath string, now time.Time) (Receipt, error) {
	cohort, cohortDigest, err := migrationcohortevidence.LoadReadyReceipt(cohortPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready migration cohort receipt: %w", err)
	}
	parity, parityDigest, err := parityevidence.LoadReadyReceipt(parityPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready retrieval parity receipt: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(cohort, cohortDigest, parity, parityDigest, input, inputDigest, now)
}

func build(cohort migrationcohortevidence.Receipt, cohortDigest string, parity parityevidence.Receipt, parityDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if !samePlatform(cohort, parity) || cohort.DatasetVersion != parity.DatasetVersion {
		return Receipt{}, errors.New("migration acceptance prerequisites do not share one platform, release, and dataset")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" ||
		!opaquePattern.MatchString(input.AcceptanceID) || !opaquePattern.MatchString(input.RollbackPlanVersion) ||
		input.CohortID != cohort.CohortID || input.CohortReceiptSHA256 != cohortDigest ||
		input.ParityEvaluationID != parity.EvaluationID || input.ParityReceiptSHA256 != parityDigest ||
		input.InventoryID != cohort.InventoryID || input.InventoryReceiptSHA256 != cohort.InventoryReceiptSHA256 ||
		input.PlanID != cohort.PlanID || input.PlanReceiptSHA256 != cohort.PlanReceiptSHA256 ||
		input.ChangeID != cohort.ChangeID || input.ChangeReceiptSHA256 != cohort.ChangeReceiptSHA256 ||
		input.ReleaseID != cohort.ReleaseID || input.ReleaseReceiptSHA256 != cohort.ReleaseReceiptSHA256 ||
		input.DatasetVersion != cohort.DatasetVersion || !allDigests(input.RollbackPlanSHA256, input.TabletopReportSHA256, input.AcceptanceDecisionSHA256, inputDigest) {
		return Receipt{}, errors.New("migration acceptance identity or prerequisite binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("migration acceptance collection time is invalid")
	}
	now = now.UTC()
	started, completed, generated := input.StartedAt.UTC(), input.CompletedAt.UTC(), input.GeneratedAt.UTC()
	earliest := cohort.CollectedAt.UTC()
	if parity.CollectedAt.UTC().After(earliest) {
		earliest = parity.CollectedAt.UTC()
	}
	if started.IsZero() || completed.IsZero() || generated.IsZero() || started.Before(earliest) || completed.Before(started) || completed.Sub(started) > maximumTabletopSpan || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("migration acceptance timeline is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("migration acceptance readiness contradicts check outcomes")
	}
	bundle := sha256.Sum256([]byte(cohortDigest + ":" + parityDigest))
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment,
		AcceptanceID: input.AcceptanceID, RollbackPlanVersion: input.RollbackPlanVersion,
		CohortID: input.CohortID, CohortReceiptSHA256: cohortDigest, ParityEvaluationID: input.ParityEvaluationID, ParityReceiptSHA256: parityDigest, EvidenceBundleSHA256: fmt.Sprintf("%x", bundle),
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256,
		ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256, ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: input.ReleaseReceiptSHA256, DatasetVersion: input.DatasetVersion,
		RollbackPlanSHA256: input.RollbackPlanSHA256, TabletopReportSHA256: input.TabletopReportSHA256, AcceptanceDecisionSHA256: input.AcceptanceDecisionSHA256, InputSHA256: inputDigest,
		StartedAt: started, CompletedAt: completed, GeneratedAt: generated, CollectedAt: now,
		Ready: ready, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, Checks: checks}, nil
}

func samePlatform(cohort migrationcohortevidence.Receipt, parity parityevidence.Receipt) bool {
	return cohort.InventoryID == parity.InventoryID && cohort.InventoryReceiptSHA256 == parity.InventoryReceiptSHA256 &&
		cohort.PlanID == parity.PlanID && cohort.PlanReceiptSHA256 == parity.PlanReceiptSHA256 &&
		cohort.ChangeID == parity.ChangeID && cohort.ChangeReceiptSHA256 == parity.ChangeReceiptSHA256 &&
		cohort.ReleaseID == parity.ReleaseID && cohort.ReleaseReceiptSHA256 == parity.ReleaseReceiptSHA256
}

func allDigests(values ...string) bool {
	for _, value := range values {
		if !digestPattern.MatchString(value) {
			return false
		}
	}
	return true
}

func validateChecks(values []Check) ([]Check, int, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("migration acceptance checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(values))
	passed, failed, inconclusive := 0, 0, 0
	for _, value := range values {
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("migration acceptance check digest is invalid")
		}
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("migration acceptance check is duplicated")
		}
		switch value.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("migration acceptance check outcome is invalid")
		}
		byID[value.ID] = value
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		value, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("migration acceptance required check is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, passed, failed, inconclusive, nil
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("migration acceptance input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("migration acceptance input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open migration acceptance input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("migration acceptance input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read migration acceptance input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("migration acceptance input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("migration acceptance input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("migration acceptance input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("migration acceptance input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("migration acceptance receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("migration acceptance receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect migration acceptance receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-migration-acceptance-*")
}
