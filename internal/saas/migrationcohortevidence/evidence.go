// Package migrationcohortevidence normalizes content-free representative
// internal migration-cohort evidence for CP9-A.
package migrationcohortevidence

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
	InputSchemaV1        = "agent-memory-staging-migration-cohort-input-v1"
	ReceiptSchemaV1      = "agent-memory-staging-migration-cohort-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumCohortSpan    = 14 * 24 * time.Hour
	maximumConsentAge    = 31 * 24 * time.Hour
	maximumCollectionAge = 24 * time.Hour
	maximumAggregate     = 10_000_000
)

type CheckID string
type Outcome string
type Format string
type SizeBucket string

const (
	CheckConsentAuthorized      CheckID = "cohort_consent_authorized"
	CheckCohortRepresentative   CheckID = "cohort_representative"
	CheckBundleIntegrity        CheckID = "bundle_integrity_verified"
	CheckImportCompleted        CheckID = "import_completed"
	CheckReconciliationComplete CheckID = "reconciliation_complete"
	CheckNoUnexplainedLoss      CheckID = "no_unexplained_loss"
	CheckNoDuplicatePublication CheckID = "no_duplicate_publication"
	CheckFailuresReviewed       CheckID = "failures_reviewed"
	CheckProductQAReview        CheckID = "product_qa_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"

	FormatPDF      Format = "pdf"
	FormatEPUB     Format = "epub"
	FormatMarkdown Format = "markdown"
	FormatText     Format = "text"

	SizeSmall  SizeBucket = "small"
	SizeMedium SizeBucket = "medium"
	SizeLarge  SizeBucket = "large"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{
		CheckConsentAuthorized, CheckCohortRepresentative, CheckBundleIntegrity,
		CheckImportCompleted, CheckReconciliationComplete, CheckNoUnexplainedLoss,
		CheckNoDuplicatePublication, CheckFailuresReviewed, CheckProductQAReview,
	}
	requiredFormats = []Format{FormatPDF, FormatEPUB, FormatMarkdown, FormatText}
	requiredSizes   = []SizeBucket{SizeSmall, SizeMedium, SizeLarge}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type FormatCoverage struct {
	Format      Format `json:"format"`
	SourceCount int    `json:"source_count"`
}

type SizeCoverage struct {
	Bucket      SizeBucket `json:"bucket"`
	SourceCount int        `json:"source_count"`
}

type Input struct {
	Schema                    string           `json:"schema"`
	Classification            string           `json:"classification"`
	Environment               string           `json:"environment"`
	CohortID                  string           `json:"cohort_id"`
	DatasetVersion            string           `json:"dataset_version"`
	ConsentVersion            string           `json:"consent_version"`
	ImporterVersion           string           `json:"importer_version"`
	InventoryID               string           `json:"inventory_id"`
	InventoryReceiptSHA256    string           `json:"inventory_receipt_sha256"`
	PlanID                    string           `json:"plan_id"`
	PlanReceiptSHA256         string           `json:"plan_receipt_sha256"`
	ChangeID                  string           `json:"change_id"`
	ChangeReceiptSHA256       string           `json:"change_receipt_sha256"`
	ReleaseID                 string           `json:"release_id"`
	ReleaseReceiptSHA256      string           `json:"release_receipt_sha256"`
	CohortDecisionSHA256      string           `json:"cohort_decision_sha256"`
	CohortReportSHA256        string           `json:"cohort_report_sha256"`
	ConsentApprovedAt         time.Time        `json:"consent_approved_at"`
	StartedAt                 time.Time        `json:"started_at"`
	CompletedAt               time.Time        `json:"completed_at"`
	GeneratedAt               time.Time        `json:"generated_at"`
	AccountCount              int              `json:"account_count"`
	LibraryCount              int              `json:"library_count"`
	SourceCount               int              `json:"source_count"`
	MemoryCount               int              `json:"memory_count"`
	NoteCount                 int              `json:"note_count"`
	ExpectedItemCount         int              `json:"expected_item_count"`
	ImportedItemCount         int              `json:"imported_item_count"`
	MergedItemCount           int              `json:"merged_item_count"`
	SkippedItemCount          int              `json:"skipped_item_count"`
	FailedItemCount           int              `json:"failed_item_count"`
	UnexplainedLossCount      int              `json:"unexplained_loss_count"`
	DuplicatePublicationCount int              `json:"duplicate_publication_count"`
	Formats                   []FormatCoverage `json:"formats"`
	SizeBuckets               []SizeCoverage   `json:"size_buckets"`
	Ready                     bool             `json:"ready"`
	Checks                    []Check          `json:"checks"`
}

type Receipt struct {
	Schema                    string           `json:"schema"`
	Classification            string           `json:"classification"`
	Environment               string           `json:"environment"`
	CohortID                  string           `json:"cohort_id"`
	DatasetVersion            string           `json:"dataset_version"`
	ConsentVersion            string           `json:"consent_version"`
	ImporterVersion           string           `json:"importer_version"`
	InventoryID               string           `json:"inventory_id"`
	InventoryReceiptSHA256    string           `json:"inventory_receipt_sha256"`
	PlanID                    string           `json:"plan_id"`
	PlanReceiptSHA256         string           `json:"plan_receipt_sha256"`
	ChangeID                  string           `json:"change_id"`
	ChangeReceiptSHA256       string           `json:"change_receipt_sha256"`
	ReleaseID                 string           `json:"release_id"`
	ReleaseReceiptSHA256      string           `json:"release_receipt_sha256"`
	CohortDecisionSHA256      string           `json:"cohort_decision_sha256"`
	CohortReportSHA256        string           `json:"cohort_report_sha256"`
	InputSHA256               string           `json:"input_sha256"`
	ConsentApprovedAt         time.Time        `json:"consent_approved_at"`
	StartedAt                 time.Time        `json:"started_at"`
	CompletedAt               time.Time        `json:"completed_at"`
	GeneratedAt               time.Time        `json:"generated_at"`
	CollectedAt               time.Time        `json:"collected_at"`
	AccountCount              int              `json:"account_count"`
	LibraryCount              int              `json:"library_count"`
	SourceCount               int              `json:"source_count"`
	MemoryCount               int              `json:"memory_count"`
	NoteCount                 int              `json:"note_count"`
	ExpectedItemCount         int              `json:"expected_item_count"`
	ImportedItemCount         int              `json:"imported_item_count"`
	MergedItemCount           int              `json:"merged_item_count"`
	SkippedItemCount          int              `json:"skipped_item_count"`
	FailedItemCount           int              `json:"failed_item_count"`
	UnexplainedLossCount      int              `json:"unexplained_loss_count"`
	DuplicatePublicationCount int              `json:"duplicate_publication_count"`
	FormatCoverageComplete    bool             `json:"format_coverage_complete"`
	SizeCoverageComplete      bool             `json:"size_coverage_complete"`
	ReconciliationComplete    bool             `json:"reconciliation_complete"`
	Ready                     bool             `json:"ready"`
	CheckCount                int              `json:"check_count"`
	PassedCount               int              `json:"passed_count"`
	FailedCount               int              `json:"failed_count"`
	InconclusiveCount         int              `json:"inconclusive_count"`
	Formats                   []FormatCoverage `json:"formats"`
	SizeBuckets               []SizeCoverage   `json:"size_buckets"`
	Checks                    []Check          `json:"checks"`
}

type Evaluation struct {
	Ready                     bool
	FormatCoverageComplete    bool
	SizeCoverageComplete      bool
	ReconciliationComplete    bool
	FailedItemCount           int
	UnexplainedLossCount      int
	DuplicatePublicationCount int
	CheckCount                int
	PassedCount               int
	FailedCount               int
	InconclusiveCount         int
	Formats                   []FormatCoverage
	SizeBuckets               []SizeCoverage
	Checks                    []Check
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

// LoadReadyReceipt reloads and revalidates a published ready CP9-A receipt by
// exact opened bytes for downstream acceptance gates.
func LoadReadyReceipt(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	validIdentity := receipt.Schema == ReceiptSchemaV1 && receipt.Classification == "staging_external" && receipt.Environment == "staging" &&
		opaquePattern.MatchString(receipt.CohortID) && opaquePattern.MatchString(receipt.DatasetVersion) && opaquePattern.MatchString(receipt.ConsentVersion) && opaquePattern.MatchString(receipt.ImporterVersion) &&
		opaquePattern.MatchString(receipt.InventoryID) && opaquePattern.MatchString(receipt.PlanID) && opaquePattern.MatchString(receipt.ChangeID) && opaquePattern.MatchString(receipt.ReleaseID)
	validDigests := true
	for _, value := range []string{receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.CohortDecisionSHA256, receipt.CohortReportSHA256, receipt.InputSHA256, digest} {
		validDigests = validDigests && digestPattern.MatchString(value)
	}
	approved, started, completed, generated, collected := receipt.ConsentApprovedAt.UTC(), receipt.StartedAt.UTC(), receipt.CompletedAt.UTC(), receipt.GeneratedAt.UTC(), receipt.CollectedAt.UTC()
	validTimeline := !approved.IsZero() && !started.IsZero() && !completed.IsZero() && !generated.IsZero() && !collected.IsZero() && !approved.After(started) && started.Sub(approved) <= maximumConsentAge && !completed.Before(started) && completed.Sub(started) <= maximumCohortSpan && !generated.Before(completed) && !collected.Before(generated)
	input := Input{AccountCount: receipt.AccountCount, LibraryCount: receipt.LibraryCount, SourceCount: receipt.SourceCount, MemoryCount: receipt.MemoryCount, NoteCount: receipt.NoteCount,
		ExpectedItemCount: receipt.ExpectedItemCount, ImportedItemCount: receipt.ImportedItemCount, MergedItemCount: receipt.MergedItemCount, SkippedItemCount: receipt.SkippedItemCount,
		FailedItemCount: receipt.FailedItemCount, UnexplainedLossCount: receipt.UnexplainedLossCount, DuplicatePublicationCount: receipt.DuplicatePublicationCount,
		Formats: receipt.Formats, SizeBuckets: receipt.SizeBuckets, Ready: receipt.Ready, Checks: receipt.Checks}
	evaluation, evaluationErr := Evaluate(input)
	canonical := evaluationErr == nil && len(evaluation.Checks) == len(receipt.Checks)
	if canonical {
		for index := range evaluation.Checks {
			canonical = canonical && evaluation.Checks[index] == receipt.Checks[index]
		}
	}
	validDerived := evaluationErr == nil && receipt.Ready && evaluation.Ready && receipt.FormatCoverageComplete == evaluation.FormatCoverageComplete && receipt.SizeCoverageComplete == evaluation.SizeCoverageComplete && receipt.ReconciliationComplete == evaluation.ReconciliationComplete && receipt.CheckCount == evaluation.CheckCount && receipt.PassedCount == evaluation.PassedCount && receipt.FailedCount == evaluation.FailedCount && receipt.InconclusiveCount == evaluation.InconclusiveCount
	if !validIdentity || !validDigests || !validTimeline || !canonical || !validDerived {
		return Receipt{}, "", errors.New("migration cohort receipt is invalid or unready")
	}
	receipt.ConsentApprovedAt, receipt.StartedAt, receipt.CompletedAt = approved, started, completed
	receipt.GeneratedAt, receipt.CollectedAt = generated, collected
	receipt.Formats, receipt.SizeBuckets, receipt.Checks = evaluation.Formats, evaluation.SizeBuckets, evaluation.Checks
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
		return Receipt{}, errors.New("migration cohort platform chain is invalid or unready")
	}
	if !validPassedRelease(release, releaseDigest) {
		return Receipt{}, errors.New("migration cohort staging release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" ||
		!opaquePattern.MatchString(input.CohortID) || !opaquePattern.MatchString(input.DatasetVersion) || !opaquePattern.MatchString(input.ConsentVersion) || !opaquePattern.MatchString(input.ImporterVersion) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 ||
		input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest ||
		!digestPattern.MatchString(input.CohortDecisionSHA256) || !digestPattern.MatchString(input.CohortReportSHA256) || !digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("migration cohort input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("migration cohort collection time is invalid")
	}
	now = now.UTC()
	approved, started, completed, generated := input.ConsentApprovedAt.UTC(), input.StartedAt.UTC(), input.CompletedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if approved.IsZero() || started.IsZero() || completed.IsZero() || generated.IsZero() || approved.After(started) || started.Sub(approved) > maximumConsentAge || started.Before(earliest) || completed.Before(started) || completed.Sub(started) > maximumCohortSpan || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("migration cohort timeline is invalid")
	}
	for _, count := range []int{input.AccountCount, input.LibraryCount, input.SourceCount, input.MemoryCount, input.NoteCount, input.ExpectedItemCount, input.ImportedItemCount, input.MergedItemCount, input.SkippedItemCount, input.FailedItemCount, input.UnexplainedLossCount, input.DuplicatePublicationCount} {
		if count > maximumAggregate {
			return Receipt{}, errors.New("migration cohort aggregate exceeds limit")
		}
	}
	evaluation, err := Evaluate(input)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment,
		CohortID: input.CohortID, DatasetVersion: input.DatasetVersion, ConsentVersion: input.ConsentVersion, ImporterVersion: input.ImporterVersion,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256,
		ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256, ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: releaseDigest,
		CohortDecisionSHA256: input.CohortDecisionSHA256, CohortReportSHA256: input.CohortReportSHA256, InputSHA256: inputDigest,
		ConsentApprovedAt: approved, StartedAt: started, CompletedAt: completed, GeneratedAt: generated, CollectedAt: now,
		AccountCount: input.AccountCount, LibraryCount: input.LibraryCount, SourceCount: input.SourceCount, MemoryCount: input.MemoryCount, NoteCount: input.NoteCount,
		ExpectedItemCount: input.ExpectedItemCount, ImportedItemCount: input.ImportedItemCount, MergedItemCount: input.MergedItemCount, SkippedItemCount: input.SkippedItemCount,
		FailedItemCount: evaluation.FailedItemCount, UnexplainedLossCount: evaluation.UnexplainedLossCount, DuplicatePublicationCount: evaluation.DuplicatePublicationCount,
		FormatCoverageComplete: evaluation.FormatCoverageComplete, SizeCoverageComplete: evaluation.SizeCoverageComplete, ReconciliationComplete: evaluation.ReconciliationComplete,
		Ready: evaluation.Ready, CheckCount: evaluation.CheckCount, PassedCount: evaluation.PassedCount, FailedCount: evaluation.FailedCount, InconclusiveCount: evaluation.InconclusiveCount,
		Formats: evaluation.Formats, SizeBuckets: evaluation.SizeBuckets, Checks: evaluation.Checks}, nil
}

func validPassedRelease(release platformrollback.ReleaseReceipt, digest string) bool {
	return release.Schema == "agent-memory-kubernetes-release-receipt-v1" && release.Environment == "staging" && release.Namespace == "agent-memory-staging" && opaquePattern.MatchString(release.ReleaseID) && release.Outcome == "passed" && release.Migration.Outcome == "complete" && release.Rollouts.Outcome == "healthy" && !release.Rollback.Attempted && !release.Rollback.Succeeded && !release.StartedAt.IsZero() && !release.CompletedAt.Before(release.StartedAt) && digestPattern.MatchString(digest)
}

func Evaluate(input Input) (Evaluation, error) {
	if input.AccountCount < 1 || input.LibraryCount < 1 || input.SourceCount < 1 ||
		input.MemoryCount < 0 || input.NoteCount < 0 || input.ExpectedItemCount < 1 ||
		input.ImportedItemCount < 0 || input.MergedItemCount < 0 || input.SkippedItemCount < 0 ||
		input.FailedItemCount < 0 || input.UnexplainedLossCount < 0 || input.DuplicatePublicationCount < 0 {
		return Evaluation{}, errors.New("migration cohort aggregate counts are invalid")
	}
	if input.ExpectedItemCount != input.SourceCount+input.MemoryCount+input.NoteCount ||
		input.ExpectedItemCount != input.ImportedItemCount+input.MergedItemCount+input.SkippedItemCount+input.FailedItemCount {
		return Evaluation{}, errors.New("migration cohort item reconciliation is invalid")
	}
	formats, formatComplete, err := validateFormats(input.Formats, input.SourceCount)
	if err != nil {
		return Evaluation{}, err
	}
	sizes, sizeComplete, err := validateSizes(input.SizeBuckets, input.SourceCount)
	if err != nil {
		return Evaluation{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Evaluation{}, err
	}
	reconciliation := true
	observations := map[CheckID]bool{
		CheckCohortRepresentative:   formatComplete && sizeComplete,
		CheckImportCompleted:        input.FailedItemCount == 0,
		CheckReconciliationComplete: reconciliation,
		CheckNoUnexplainedLoss:      input.UnexplainedLossCount == 0,
		CheckNoDuplicatePublication: input.DuplicatePublicationCount == 0,
	}
	for id, met := range observations {
		if (checks[id].Outcome == OutcomePassed) != met {
			return Evaluation{}, errors.New("migration cohort check contradicts aggregate observation")
		}
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 &&
		formatComplete && sizeComplete && input.FailedItemCount == 0 &&
		input.UnexplainedLossCount == 0 && input.DuplicatePublicationCount == 0
	if input.Ready != ready {
		return Evaluation{}, errors.New("migration cohort readiness contradicts evidence")
	}
	return Evaluation{Ready: ready, FormatCoverageComplete: formatComplete,
		SizeCoverageComplete: sizeComplete, ReconciliationComplete: reconciliation,
		FailedItemCount: input.FailedItemCount, UnexplainedLossCount: input.UnexplainedLossCount,
		DuplicatePublicationCount: input.DuplicatePublicationCount, CheckCount: len(checks),
		PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive,
		Formats: formats, SizeBuckets: sizes, Checks: orderedChecks(checks)}, nil
}

func validateFormats(values []FormatCoverage, total int) ([]FormatCoverage, bool, error) {
	if len(values) != len(requiredFormats) {
		return nil, false, errors.New("migration cohort format coverage is incomplete")
	}
	byID := make(map[Format]FormatCoverage, len(values))
	sum := 0
	for _, value := range values {
		if value.SourceCount < 1 {
			return nil, false, errors.New("migration cohort format coverage is invalid")
		}
		if _, exists := byID[value.Format]; exists {
			return nil, false, errors.New("migration cohort format coverage is duplicated")
		}
		byID[value.Format] = value
		sum += value.SourceCount
	}
	ordered := make([]FormatCoverage, 0, len(requiredFormats))
	for _, id := range requiredFormats {
		value, exists := byID[id]
		if !exists {
			return nil, false, errors.New("migration cohort required format is missing")
		}
		ordered = append(ordered, value)
	}
	if sum != total {
		return nil, false, errors.New("migration cohort format total is invalid")
	}
	return ordered, true, nil
}

func validateSizes(values []SizeCoverage, total int) ([]SizeCoverage, bool, error) {
	if len(values) != len(requiredSizes) {
		return nil, false, errors.New("migration cohort size coverage is incomplete")
	}
	byID := make(map[SizeBucket]SizeCoverage, len(values))
	sum := 0
	for _, value := range values {
		if value.SourceCount < 1 {
			return nil, false, errors.New("migration cohort size coverage is invalid")
		}
		if _, exists := byID[value.Bucket]; exists {
			return nil, false, errors.New("migration cohort size coverage is duplicated")
		}
		byID[value.Bucket] = value
		sum += value.SourceCount
	}
	ordered := make([]SizeCoverage, 0, len(requiredSizes))
	for _, id := range requiredSizes {
		value, exists := byID[id]
		if !exists {
			return nil, false, errors.New("migration cohort required size bucket is missing")
		}
		ordered = append(ordered, value)
	}
	if sum != total {
		return nil, false, errors.New("migration cohort size total is invalid")
	}
	return ordered, true, nil
}

func validateChecks(values []Check) (map[CheckID]Check, int, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("migration cohort checks are incomplete")
	}
	checks := make(map[CheckID]Check, len(values))
	passed, failed, inconclusive := 0, 0, 0
	for _, value := range values {
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("migration cohort check digest is invalid")
		}
		if _, exists := checks[value.ID]; exists {
			return nil, 0, 0, 0, errors.New("migration cohort check is duplicated")
		}
		switch value.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("migration cohort check outcome is invalid")
		}
		checks[value.ID] = value
	}
	for _, id := range requiredChecks {
		if _, exists := checks[id]; !exists {
			return nil, 0, 0, 0, errors.New("migration cohort required check is missing")
		}
	}
	return checks, passed, failed, inconclusive, nil
}

func orderedChecks(values map[CheckID]Check) []Check {
	result := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		result = append(result, values[id])
	}
	return result
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("migration cohort input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("migration cohort input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open migration cohort input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("migration cohort input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read migration cohort input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("migration cohort input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("migration cohort input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("migration cohort input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("migration cohort input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("migration cohort receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("migration cohort receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect migration cohort receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-migration-cohort-*")
}
