// Package alphaevidence normalizes content-free evidence from a real internal
// alpha cohort without accepting account, source, support-case, or customer data.
package alphaevidence

import (
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"

	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/stagingjourney"
)

const (
	InputSchemaV1   = "agent-memory-staging-internal-alpha-input-v1"
	ReceiptSchemaV1 = "agent-memory-staging-internal-alpha-receipt-v1"

	minimumAlphaWindow = 28 * 24 * time.Hour
	maximumAlphaWindow = 93 * 24 * time.Hour
	maximumInputAge    = 24 * time.Hour
	maximumAccounts    = 100
	maximumSources     = 1_000_000
	maximumSourceBytes = int64(1 << 40)
	maximumTarget      = 30 * 24 * 60 * 60
	maximumInputBytes  = 256 << 10
)

type FormatID string
type StageID string
type CheckID string
type Outcome string

const (
	FormatPDF      FormatID = "pdf"
	FormatEPUB     FormatID = "epub"
	FormatMarkdown FormatID = "markdown"
	FormatText     FormatID = "text"

	StageInvitation      StageID = "invitation_acceptance"
	StageSignup          StageID = "signup"
	StageConsent         StageID = "rights_consent"
	StageUpload          StageID = "source_upload"
	StageIndexing        StageID = "indexing"
	StageSearch          StageID = "search_query"
	StageReview          StageID = "memory_review"
	StageExport          StageID = "export"
	StageConsentRenewal  StageID = "monthly_consent_renewal"
	StageSourceDeletion  StageID = "source_deletion"
	StageAccountDeletion StageID = "account_deletion"

	CheckCohortApproval    CheckID = "cohort_approval_complete"
	CheckSourceCaps        CheckID = "source_caps_respected"
	CheckNonSensitive      CheckID = "non_sensitive_sources_verified"
	CheckAllFormats        CheckID = "all_formats_processed"
	CheckLifecycle         CheckID = "lifecycle_complete"
	CheckSupport           CheckID = "support_process_exercised"
	CheckDeletion          CheckID = "deletion_reconciled"
	CheckTraceAudit        CheckID = "immutable_trace_audit_complete"
	CheckAccountableReview CheckID = "product_qa_operations_review_complete"

	OutcomePassed Outcome = "passed"
	OutcomeFailed Outcome = "failed"
)

var (
	opaquePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	requiredFormats = []FormatID{FormatPDF, FormatEPUB, FormatMarkdown, FormatText}
	requiredStages  = []StageID{StageInvitation, StageSignup, StageConsent, StageUpload, StageIndexing, StageSearch, StageReview, StageExport, StageConsentRenewal, StageSourceDeletion, StageAccountDeletion}
	requiredChecks  = []CheckID{CheckCohortApproval, CheckSourceCaps, CheckNonSensitive, CheckAllFormats, CheckLifecycle, CheckSupport, CheckDeletion, CheckTraceAudit, CheckAccountableReview}
)

type Format struct {
	ID                FormatID `json:"id"`
	SourceCount       int      `json:"source_count"`
	SourceBytes       int64    `json:"source_bytes"`
	IndexedCount      int      `json:"indexed_count"`
	NonSensitiveCount int      `json:"non_sensitive_count"`
	DeletedCount      int      `json:"deleted_count"`
	EvidenceSHA256    string   `json:"evidence_sha256"`
}

type Stage struct {
	ID             StageID   `json:"id"`
	Outcome        Outcome   `json:"outcome"`
	CompletedAt    time.Time `json:"completed_at"`
	EvidenceSHA256 string    `json:"evidence_sha256"`
}

type Support struct {
	CaseCount                    int    `json:"case_count"`
	ResolvedCaseCount            int    `json:"resolved_case_count"`
	OpenCaseCount                int    `json:"open_case_count"`
	OverdueCaseCount             int    `json:"overdue_case_count"`
	SampledCaseCount             int    `json:"sampled_case_count"`
	MatchedSampleCount           int    `json:"matched_sample_count"`
	AcknowledgementTargetSeconds int    `json:"acknowledgement_target_seconds"`
	ResolutionTargetSeconds      int    `json:"resolution_target_seconds"`
	MaxAcknowledgementSeconds    int    `json:"max_acknowledgement_seconds"`
	MaxResolutionSeconds         int    `json:"max_resolution_seconds"`
	EvidenceSHA256               string `json:"evidence_sha256"`
}

type Deletion struct {
	AccountRequestedCount int    `json:"account_requested_count"`
	AccountDeletedCount   int    `json:"account_deleted_count"`
	AccountPendingCount   int    `json:"account_pending_count"`
	SourceRequestedCount  int    `json:"source_requested_count"`
	SourceDeletedCount    int    `json:"source_deleted_count"`
	SourcePendingCount    int    `json:"source_pending_count"`
	EvidenceSHA256        string `json:"evidence_sha256"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                          string    `json:"schema"`
	Classification                  string    `json:"classification"`
	Environment                     string    `json:"environment"`
	CohortID                        string    `json:"cohort_id"`
	ReviewVersion                   string    `json:"review_version"`
	InventoryID                     string    `json:"inventory_id"`
	InventoryReceiptSHA256          string    `json:"inventory_receipt_sha256"`
	PlanID                          string    `json:"plan_id"`
	PlanReceiptSHA256               string    `json:"plan_receipt_sha256"`
	ChangeID                        string    `json:"change_id"`
	ChangeReceiptSHA256             string    `json:"change_receipt_sha256"`
	ReleaseID                       string    `json:"release_id"`
	ReleaseReceiptSHA256            string    `json:"release_receipt_sha256"`
	JourneyReceiptSHA256            string    `json:"journey_receipt_sha256"`
	CohortDecisionSHA256            string    `json:"cohort_decision_sha256"`
	SourcePolicySHA256              string    `json:"source_policy_sha256"`
	SupportPolicySHA256             string    `json:"support_policy_sha256"`
	DeletionManifestSHA256          string    `json:"deletion_manifest_sha256"`
	TraceAuditManifestSHA256        string    `json:"trace_audit_manifest_sha256"`
	ProductQAOperationsReviewSHA256 string    `json:"product_qa_operations_review_sha256"`
	StartedAt                       time.Time `json:"started_at"`
	CompletedAt                     time.Time `json:"completed_at"`
	GeneratedAt                     time.Time `json:"generated_at"`
	Ready                           bool      `json:"ready"`
	AccountCount                    int       `json:"account_count"`
	ApprovedSourceCountCap          int       `json:"approved_source_count_cap"`
	ApprovedSourceBytesCap          int64     `json:"approved_source_bytes_cap"`
	SourceCount                     int       `json:"source_count"`
	SourceBytes                     int64     `json:"source_bytes"`
	Formats                         []Format  `json:"formats"`
	Stages                          []Stage   `json:"stages"`
	Support                         Support   `json:"support"`
	Deletion                        Deletion  `json:"deletion"`
	Checks                          []Check   `json:"checks"`
}

type Receipt struct {
	Input
	Schema            string    `json:"schema"`
	InputSHA256       string    `json:"input_sha256"`
	CollectedAt       time.Time `json:"collected_at"`
	AlphaDays         int       `json:"alpha_days"`
	FormatCount       int       `json:"format_count"`
	StageCount        int       `json:"stage_count"`
	SupportCaseCount  int       `json:"support_case_count"`
	TargetBreachCount int       `json:"target_breach_count"`
	CheckCount        int       `json:"check_count"`
	PassedCount       int       `json:"passed_count"`
	FailedCount       int       `json:"failed_count"`
}

func RequiredStages() []StageID { return append([]StageID(nil), requiredStages...) }
func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(inventoryPath, planPath, changePath, releasePath, journeyPath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load internal-alpha inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load internal-alpha plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load internal-alpha change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load internal-alpha release: %w", err)
	}
	var journey stagingjourney.Receipt
	journeyDigest, err := decodeStrictRegular(journeyPath, &journey)
	if err != nil {
		return Receipt{}, fmt.Errorf("load internal-alpha journey: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, fmt.Errorf("load internal-alpha input: %w", err)
	}
	return build(inventory, plan, change, release, releaseDigest, journey, journeyDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, journey stagingjourney.Receipt, journeyDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "staging" || release.Namespace != "agent-memory-staging" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded {
		return Receipt{}, errors.New("internal-alpha platform or release chain is invalid")
	}
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if err := validateJourneyReceipt(journey, release, releaseDigest, earliest); err != nil {
		return Receipt{}, errors.New("internal-alpha prerequisite journey is invalid")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !allOpaque(input.CohortID, input.ReviewVersion, input.InventoryID, input.PlanID, input.ChangeID, input.ReleaseID) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || input.JourneyReceiptSHA256 != journeyDigest || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256, releaseDigest, journeyDigest, input.CohortDecisionSHA256, input.SourcePolicySHA256, input.SupportPolicySHA256, input.DeletionManifestSHA256, input.TraceAuditManifestSHA256, input.ProductQAOperationsReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("internal-alpha input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("internal-alpha collection time is invalid")
	}
	now = now.UTC()
	started, completed, generated := input.StartedAt.UTC(), input.CompletedAt.UTC(), input.GeneratedAt.UTC()
	window := completed.Sub(started)
	if started.IsZero() || completed.IsZero() || generated.IsZero() || started.Before(earliest) || started.Before(journey.CollectedAt.UTC()) || window < minimumAlphaWindow || window > maximumAlphaWindow || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumInputAge)) {
		return Receipt{}, errors.New("internal-alpha timeline is invalid")
	}
	if input.AccountCount < 1 || input.AccountCount > maximumAccounts || input.ApprovedSourceCountCap < 1 || input.ApprovedSourceCountCap > maximumSources || input.ApprovedSourceBytesCap < 1 || input.ApprovedSourceBytesCap > maximumSourceBytes || input.SourceCount < 1 || input.SourceCount > maximumSources || input.SourceBytes < 1 || input.SourceBytes > maximumSourceBytes {
		return Receipt{}, errors.New("internal-alpha cohort aggregate is invalid")
	}
	formats, formatOutcomes, err := validateFormats(input.Formats, input.SourceCount, input.SourceBytes, input.ApprovedSourceCountCap, input.ApprovedSourceBytesCap)
	if err != nil {
		return Receipt{}, err
	}
	stages, lifecycleOutcome, err := validateStages(input.Stages, started, completed)
	if err != nil {
		return Receipt{}, err
	}
	supportOutcome, breaches, err := validateSupport(input.Support)
	if err != nil {
		return Receipt{}, err
	}
	deletionOutcome, err := validateDeletion(input.Deletion, input.AccountCount, input.SourceCount)
	if err != nil {
		return Receipt{}, err
	}
	formatDeleted := 0
	for _, format := range formats {
		formatDeleted += format.DeletedCount
	}
	if formatDeleted != input.Deletion.SourceDeletedCount {
		return Receipt{}, errors.New("internal-alpha format and deletion totals do not reconcile")
	}
	checks, passed, failed, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	derived := map[CheckID]Outcome{
		CheckSourceCaps: formatOutcomes[0], CheckNonSensitive: formatOutcomes[1], CheckAllFormats: formatOutcomes[2],
		CheckLifecycle: lifecycleOutcome, CheckSupport: supportOutcome, CheckDeletion: deletionOutcome,
	}
	for id, outcome := range derived {
		if outcomeFor(checks, id) != outcome {
			return Receipt{}, errors.New("internal-alpha check contradicts aggregate evidence")
		}
	}
	bindings := map[CheckID]string{CheckCohortApproval: input.CohortDecisionSHA256, CheckSourceCaps: input.SourcePolicySHA256, CheckSupport: input.SupportPolicySHA256, CheckDeletion: input.DeletionManifestSHA256, CheckTraceAudit: input.TraceAuditManifestSHA256, CheckAccountableReview: input.ProductQAOperationsReviewSHA256}
	for id, digest := range bindings {
		if evidenceFor(checks, id) != digest {
			return Receipt{}, errors.New("internal-alpha check artifact binding is invalid")
		}
	}
	if input.Support.EvidenceSHA256 != input.SupportPolicySHA256 || input.Deletion.EvidenceSHA256 != input.DeletionManifestSHA256 {
		return Receipt{}, errors.New("internal-alpha domain artifact binding is invalid")
	}
	ready := passed == len(requiredChecks) && failed == 0 && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("internal-alpha readiness contradicts evidence")
	}
	result := Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, AlphaDays: int(window / (24 * time.Hour)), FormatCount: len(formats), StageCount: len(stages), SupportCaseCount: input.Support.CaseCount, TargetBreachCount: breaches, CheckCount: passed + failed, PassedCount: passed, FailedCount: failed}
	result.StartedAt, result.CompletedAt, result.GeneratedAt = started, completed, generated
	result.Formats, result.Stages, result.Checks = formats, stages, checks
	return result, nil
}

func validateFormats(values []Format, sourceCount int, sourceBytes int64, countCap int, bytesCap int64) ([]Format, [3]Outcome, error) {
	if len(values) != len(requiredFormats) {
		return nil, [3]Outcome{}, errors.New("internal-alpha format coverage is incomplete")
	}
	by := make(map[FormatID]Format, len(values))
	var totalCount, indexed, nonSensitive, deleted int
	var totalBytes int64
	for _, value := range values {
		if _, exists := by[value.ID]; exists || value.SourceCount < 1 || value.SourceBytes < 1 || value.IndexedCount < 0 || value.IndexedCount > value.SourceCount || value.NonSensitiveCount < 0 || value.NonSensitiveCount > value.SourceCount || value.DeletedCount < 0 || value.DeletedCount > value.SourceCount || !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, [3]Outcome{}, errors.New("internal-alpha format evidence is invalid")
		}
		by[value.ID] = value
		totalCount += value.SourceCount
		totalBytes += value.SourceBytes
		indexed += value.IndexedCount
		nonSensitive += value.NonSensitiveCount
		deleted += value.DeletedCount
	}
	ordered := make([]Format, 0, len(requiredFormats))
	for _, id := range requiredFormats {
		value, exists := by[id]
		if !exists {
			return nil, [3]Outcome{}, errors.New("internal-alpha required format is missing")
		}
		ordered = append(ordered, value)
	}
	if totalCount != sourceCount || totalBytes != sourceBytes {
		return nil, [3]Outcome{}, errors.New("internal-alpha format totals do not reconcile")
	}
	return ordered, [3]Outcome{booleanOutcome(sourceCount <= countCap && sourceBytes <= bytesCap), booleanOutcome(nonSensitive == sourceCount), booleanOutcome(indexed == sourceCount && deleted == sourceCount)}, nil
}

func validateStages(values []Stage, started, completed time.Time) ([]Stage, Outcome, error) {
	if len(values) != len(requiredStages) {
		return nil, "", errors.New("internal-alpha lifecycle coverage is incomplete")
	}
	by := make(map[StageID]Stage, len(values))
	allPassed := true
	previous := started
	ordered := make([]Stage, 0, len(values))
	for _, value := range values {
		if _, exists := by[value.ID]; exists {
			return nil, "", errors.New("internal-alpha lifecycle stage is duplicated")
		}
		by[value.ID] = value
	}
	for _, id := range requiredStages {
		value, exists := by[id]
		if !exists || (value.Outcome != OutcomePassed && value.Outcome != OutcomeFailed) || value.CompletedAt.IsZero() || value.CompletedAt.UTC().Before(previous) || value.CompletedAt.UTC().After(completed) || !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, "", errors.New("internal-alpha lifecycle stage is invalid")
		}
		value.CompletedAt = value.CompletedAt.UTC()
		previous = value.CompletedAt
		allPassed = allPassed && value.Outcome == OutcomePassed
		ordered = append(ordered, value)
	}
	if ordered[8].CompletedAt.Sub(ordered[2].CompletedAt) < minimumAlphaWindow {
		return nil, "", errors.New("internal-alpha consent renewal interval is too short")
	}
	return ordered, booleanOutcome(allPassed), nil
}

func validateSupport(value Support) (Outcome, int, error) {
	if value.CaseCount < 1 || value.ResolvedCaseCount < 0 || value.OpenCaseCount < 0 || value.ResolvedCaseCount+value.OpenCaseCount != value.CaseCount || value.OverdueCaseCount < 0 || value.OverdueCaseCount > value.OpenCaseCount || value.SampledCaseCount < 1 || value.SampledCaseCount > value.CaseCount || value.MatchedSampleCount < 0 || value.MatchedSampleCount > value.SampledCaseCount || value.AcknowledgementTargetSeconds < 1 || value.AcknowledgementTargetSeconds > maximumTarget || value.ResolutionTargetSeconds < 1 || value.ResolutionTargetSeconds > maximumTarget || value.MaxAcknowledgementSeconds < 0 || value.MaxAcknowledgementSeconds > maximumTarget || value.MaxResolutionSeconds < 0 || value.MaxResolutionSeconds > maximumTarget || !digestPattern.MatchString(value.EvidenceSHA256) {
		return "", 0, errors.New("internal-alpha support evidence is invalid")
	}
	breaches := 0
	if value.MaxAcknowledgementSeconds > value.AcknowledgementTargetSeconds {
		breaches++
	}
	if value.MaxResolutionSeconds > value.ResolutionTargetSeconds {
		breaches++
	}
	ready := value.ResolvedCaseCount == value.CaseCount && value.OpenCaseCount == 0 && value.OverdueCaseCount == 0 && value.MatchedSampleCount == value.SampledCaseCount && breaches == 0
	return booleanOutcome(ready), breaches, nil
}

func validateDeletion(value Deletion, accounts, sources int) (Outcome, error) {
	if value.AccountRequestedCount != accounts || value.AccountDeletedCount < 0 || value.AccountPendingCount < 0 || value.AccountDeletedCount+value.AccountPendingCount != value.AccountRequestedCount || value.SourceRequestedCount != sources || value.SourceDeletedCount < 0 || value.SourcePendingCount < 0 || value.SourceDeletedCount+value.SourcePendingCount != value.SourceRequestedCount || !digestPattern.MatchString(value.EvidenceSHA256) {
		return "", errors.New("internal-alpha deletion evidence is invalid")
	}
	return booleanOutcome(value.AccountDeletedCount == accounts && value.AccountPendingCount == 0 && value.SourceDeletedCount == sources && value.SourcePendingCount == 0), nil
}

func validateChecks(values []Check) ([]Check, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, errors.New("internal-alpha checks are incomplete")
	}
	by := make(map[CheckID]Check, len(values))
	passed, failed := 0, 0
	for _, value := range values {
		if _, exists := by[value.ID]; exists || (value.Outcome != OutcomePassed && value.Outcome != OutcomeFailed) || !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, 0, errors.New("internal-alpha check is invalid")
		}
		by[value.ID] = value
	}
	ordered := make([]Check, 0, len(values))
	for _, id := range requiredChecks {
		value, exists := by[id]
		if !exists {
			return nil, 0, 0, errors.New("internal-alpha required check is missing")
		}
		ordered = append(ordered, value)
		if value.Outcome == OutcomePassed {
			passed++
		} else {
			failed++
		}
	}
	return ordered, passed, failed, nil
}

func validateJourneyReceipt(receipt stagingjourney.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, earliest time.Time) error {
	if receipt.Schema != stagingjourney.ReceiptSchemaV1 || !receipt.Ready || receipt.Environment != "staging" || receipt.ReleaseID != release.ReleaseID || receipt.ReleaseReceiptSHA256 != releaseDigest || !digestPattern.MatchString(receipt.ReleaseReceiptSHA256) || receipt.CollectedAt.IsZero() || receipt.CollectedAt.UTC().Before(earliest) || len(receipt.Journeys) != 2 {
		return errors.New("journey receipt identity is invalid")
	}
	requiredJourneyChecks := []stagingjourney.CheckID{stagingjourney.CheckAuthenticated, stagingjourney.CheckMemoryWriteAudited, stagingjourney.CheckMemorySearchAudit, stagingjourney.CheckExportReadyAudited, stagingjourney.CheckClientCleanup}
	kinds := map[stagingjourney.ClientKind]bool{}
	traces := map[string]bool{}
	inputs := map[string]bool{}
	requests := map[string]bool{}
	tracePattern := regexp.MustCompile(`^[a-f0-9]{32}$`)
	for _, journey := range receipt.Journeys {
		if (journey.ClientKind != stagingjourney.HumanWeb && journey.ClientKind != stagingjourney.ScopedAgent) || kinds[journey.ClientKind] || traces[journey.TraceID] || inputs[journey.InputSHA256] || !digestPattern.MatchString(journey.InputSHA256) || !tracePattern.MatchString(journey.TraceID) || journey.StartedAt.IsZero() || journey.CompletedAt.IsZero() || journey.StartedAt.UTC().Before(earliest) || journey.CompletedAt.UTC().Before(journey.StartedAt.UTC()) || journey.CompletedAt.Sub(journey.StartedAt) > 30*time.Minute || journey.CompletedAt.UTC().After(receipt.CollectedAt.UTC()) || journey.CompletedAt.UTC().Before(receipt.CollectedAt.UTC().Add(-24*time.Hour)) || len(journey.Checks) != len(requiredJourneyChecks) {
			return errors.New("journey receipt client is invalid")
		}
		kinds[journey.ClientKind], traces[journey.TraceID], inputs[journey.InputSHA256] = true, true, true
		checks := map[stagingjourney.CheckID]bool{}
		for _, check := range journey.Checks {
			parsed, err := uuid.Parse(check.RequestID)
			if err != nil || parsed.String() != check.RequestID || parsed.Version() != 4 || check.Outcome != stagingjourney.OutcomePassed || checks[check.ID] || requests[check.RequestID] {
				return errors.New("journey receipt check is invalid")
			}
			checks[check.ID], requests[check.RequestID] = true, true
		}
		for _, id := range requiredJourneyChecks {
			if !checks[id] {
				return errors.New("journey receipt check is missing")
			}
		}
	}
	if !kinds[stagingjourney.HumanWeb] || !kinds[stagingjourney.ScopedAgent] {
		return errors.New("journey receipt client coverage is incomplete")
	}
	return nil
}

func decodeStrictRegular(path string, target any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("internal-alpha input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("internal-alpha input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open internal-alpha input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("internal-alpha input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read internal-alpha input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return "", errors.New("internal-alpha JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("internal-alpha input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("internal-alpha input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("internal-alpha input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("internal-alpha receipt path is required")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-internal-alpha-*")
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
func booleanOutcome(value bool) Outcome {
	if value {
		return OutcomePassed
	}
	return OutcomeFailed
}
func outcomeFor(values []Check, id CheckID) Outcome {
	for _, value := range values {
		if value.ID == id {
			return value.Outcome
		}
	}
	return ""
}
func evidenceFor(values []Check, id CheckID) string {
	for _, value := range values {
		if value.ID == id {
			return value.EvidenceSHA256
		}
	}
	return ""
}
