// Package operationalsafety normalizes content-free staging evidence for the
// rollback, managed-secret rotation, and human operator-access CP1-B drills.
package operationalsafety

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
	InputSchemaV1        = "agent-memory-staging-operational-safety-drills-v1"
	ReceiptSchemaV1      = "agent-memory-staging-operational-safety-receipt-v1"
	maximumEvidenceBytes = 128 << 10
	maximumDrillDuration = 4 * time.Hour
	maximumCollectionAge = 24 * time.Hour
)

type DrillID string
type CheckID string
type Outcome string

const (
	DrillManagedSecretRotation DrillID = "managed_secret_rotation"
	DrillHumanOperatorAccess   DrillID = "human_operator_access"

	CheckManagedReplacementCreated CheckID = "managed_replacement_created"
	CheckWorkloadRolloutCompleted  CheckID = "workload_rollout_completed"
	CheckOldValueRejected          CheckID = "old_value_rejected"
	CheckNewValueAccepted          CheckID = "new_value_accepted"
	CheckServiceRecovered          CheckID = "service_recovered"
	CheckHumanIdentityVerified     CheckID = "human_identity_verified"
	CheckIndependentApproval       CheckID = "independent_approval_verified"
	CheckLeastPrivilegeScope       CheckID = "least_privilege_scope_verified"
	CheckAccessExpiryEnforced      CheckID = "access_expiry_enforced"
	CheckAccessRevoked             CheckID = "access_revoked"
	CheckImmutableAuditRetained    CheckID = "immutable_audit_retained"
	CheckCustomerContentAbsent     CheckID = "customer_content_absent"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredDrills = []DrillID{DrillManagedSecretRotation, DrillHumanOperatorAccess}
	requiredChecks = map[DrillID][]CheckID{
		DrillManagedSecretRotation: {
			CheckManagedReplacementCreated, CheckWorkloadRolloutCompleted, CheckOldValueRejected,
			CheckNewValueAccepted, CheckServiceRecovered, CheckImmutableAuditRetained, CheckCustomerContentAbsent,
		},
		DrillHumanOperatorAccess: {
			CheckHumanIdentityVerified, CheckIndependentApproval, CheckLeastPrivilegeScope,
			CheckAccessExpiryEnforced, CheckAccessRevoked, CheckImmutableAuditRetained, CheckCustomerContentAbsent,
		},
	}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Drill struct {
	ID          DrillID   `json:"id"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Checks      []Check   `json:"checks"`
}

type Input struct {
	Schema                     string    `json:"schema"`
	Classification             string    `json:"classification"`
	Environment                string    `json:"environment"`
	BundleID                   string    `json:"bundle_id"`
	InventoryID                string    `json:"inventory_id"`
	InventoryReceiptSHA256     string    `json:"inventory_receipt_sha256"`
	PlanID                     string    `json:"plan_id"`
	PlanReceiptSHA256          string    `json:"plan_receipt_sha256"`
	ChangeID                   string    `json:"change_id"`
	ChangeReceiptSHA256        string    `json:"change_receipt_sha256"`
	BaselineReleaseID          string    `json:"baseline_release_id"`
	BaselineReceiptSHA256      string    `json:"baseline_receipt_sha256"`
	FailedAttemptReleaseID     string    `json:"failed_attempt_release_id"`
	FailedAttemptReceiptSHA256 string    `json:"failed_attempt_receipt_sha256"`
	RollbackReceiptSHA256      string    `json:"rollback_receipt_sha256"`
	Ready                      bool      `json:"ready"`
	GeneratedAt                time.Time `json:"generated_at"`
	Drills                     []Drill   `json:"drills"`
}

type Receipt struct {
	Schema                     string    `json:"schema"`
	Classification             string    `json:"classification"`
	Environment                string    `json:"environment"`
	BundleID                   string    `json:"bundle_id"`
	InventoryID                string    `json:"inventory_id"`
	InventoryReceiptSHA256     string    `json:"inventory_receipt_sha256"`
	PlanID                     string    `json:"plan_id"`
	PlanReceiptSHA256          string    `json:"plan_receipt_sha256"`
	ChangeID                   string    `json:"change_id"`
	ChangeReceiptSHA256        string    `json:"change_receipt_sha256"`
	BaselineReleaseID          string    `json:"baseline_release_id"`
	BaselineReceiptSHA256      string    `json:"baseline_receipt_sha256"`
	FailedAttemptReleaseID     string    `json:"failed_attempt_release_id"`
	FailedAttemptReceiptSHA256 string    `json:"failed_attempt_receipt_sha256"`
	RollbackReceiptSHA256      string    `json:"rollback_receipt_sha256"`
	InputSHA256                string    `json:"input_sha256"`
	Ready                      bool      `json:"ready"`
	GeneratedAt                time.Time `json:"generated_at"`
	CollectedAt                time.Time `json:"collected_at"`
	DrillCount                 int       `json:"drill_count"`
	CheckCount                 int       `json:"check_count"`
	PassedCount                int       `json:"passed_count"`
	FailedCount                int       `json:"failed_count"`
	InconclusiveCount          int       `json:"inconclusive_count"`
	Drills                     []Drill   `json:"drills"`
}

func RequiredChecks(drill DrillID) []CheckID {
	return append([]CheckID(nil), requiredChecks[drill]...)
}

func Collect(inventoryPath, planPath, changePath, baselinePath, attemptPath, rollbackPath, inputPath string, now time.Time) (Receipt, error) {
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
	pair, err := platformrollback.LoadPair(baselinePath, attemptPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load staging rollback release pair: %w", err)
	}
	rollback, rollbackDigest, err := platformrollback.LoadReceipt(rollbackPath)
	if err != nil {
		return Receipt{}, err
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, pair, rollback, rollbackDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, pair platformrollback.Pair, rollback platformrollback.Receipt, rollbackDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging ||
		plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID ||
		plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready ||
		change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID ||
		change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 ||
		!platformchange.Assess(change).Ready || !digestPattern.MatchString(inventory.ReceiptSHA256) || !digestPattern.MatchString(plan.ReceiptSHA256) || !digestPattern.MatchString(change.ReceiptSHA256) {
		return Receipt{}, errors.New("operational-safety platform chain is invalid or unready")
	}
	if !validReleasePair(pair) || !digestPattern.MatchString(rollbackDigest) || !rollback.Ready || rollback.Schema != platformrollback.ReceiptSchemaV1 ||
		rollback.Environment != "staging" || rollback.Namespace != "agent-memory-staging" || rollback.BaselineReleaseID != pair.Baseline.ReleaseID ||
		rollback.BaselineReceiptSHA256 != pair.BaselineReceiptSHA256 || rollback.FailedAttemptReleaseID != pair.Attempt.ReleaseID ||
		rollback.FailedAttemptReceiptSHA256 != pair.AttemptReceiptSHA256 || rollback.CollectedAt.Before(pair.Attempt.CompletedAt) || !rollbackAssessmentReady(rollback) {
		return Receipt{}, errors.New("operational-safety rollback evidence is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !opaquePattern.MatchString(input.BundleID) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID ||
		input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 ||
		input.BaselineReleaseID != pair.Baseline.ReleaseID || input.BaselineReceiptSHA256 != pair.BaselineReceiptSHA256 ||
		input.FailedAttemptReleaseID != pair.Attempt.ReleaseID || input.FailedAttemptReceiptSHA256 != pair.AttemptReceiptSHA256 ||
		input.RollbackReceiptSHA256 != rollbackDigest || !digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("operational-safety input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("operational-safety collection time is invalid")
	}
	now = now.UTC()
	generated := input.GeneratedAt.UTC()
	if generated.IsZero() || generated.Before(rollback.CollectedAt.UTC()) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("operational-safety collection timeline is invalid")
	}
	drills, passed, failed, inconclusive, err := validateDrills(input.Drills, pair.Baseline.CompletedAt.UTC(), generated)
	if err != nil {
		return Receipt{}, err
	}
	ready := passed == totalRequiredChecks() && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("operational-safety readiness contradicts checks")
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, BundleID: input.BundleID,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256,
		ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256, BaselineReleaseID: input.BaselineReleaseID,
		BaselineReceiptSHA256: input.BaselineReceiptSHA256, FailedAttemptReleaseID: input.FailedAttemptReleaseID,
		FailedAttemptReceiptSHA256: input.FailedAttemptReceiptSHA256, RollbackReceiptSHA256: rollbackDigest, InputSHA256: inputDigest,
		Ready: ready, GeneratedAt: generated, CollectedAt: now, DrillCount: len(drills), CheckCount: passed + failed + inconclusive,
		PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, Drills: drills,
	}, nil
}

func validReleasePair(pair platformrollback.Pair) bool {
	return pair.Baseline.Schema == "agent-memory-kubernetes-release-receipt-v1" && pair.Baseline.Environment == "staging" && pair.Baseline.Namespace == "agent-memory-staging" &&
		pair.Baseline.Outcome == "passed" && pair.Baseline.Migration.Outcome == "complete" && pair.Baseline.Rollouts.Outcome == "healthy" &&
		pair.Attempt.Schema == "agent-memory-kubernetes-release-receipt-v1" && pair.Attempt.Environment == "staging" && pair.Attempt.Namespace == "agent-memory-staging" &&
		pair.Attempt.KubernetesContext == pair.Baseline.KubernetesContext && pair.Attempt.Outcome == "failed" && pair.Attempt.Migration.Outcome == "complete" &&
		pair.Attempt.Rollouts.Outcome == "failed" && pair.Attempt.Rollback.Attempted && pair.Attempt.Rollback.Succeeded &&
		!pair.Attempt.StartedAt.Before(pair.Baseline.CompletedAt) && digestPattern.MatchString(pair.BaselineReceiptSHA256) && digestPattern.MatchString(pair.AttemptReceiptSHA256)
}

func rollbackAssessmentReady(receipt platformrollback.Receipt) bool {
	assessment := platformrollback.Assess(receipt)
	return assessment.Ready && assessment.DeploymentCount == 3 && assessment.RestoredCount == 3 && assessment.ImageMismatchCount == 0 && assessment.NotReadyCount == 0 && assessment.UnavailableCount == 0
}

func validateDrills(drills []Drill, earliest, generated time.Time) ([]Drill, int, int, int, error) {
	if len(drills) != len(requiredDrills) {
		return nil, 0, 0, 0, errors.New("operational-safety drills are incomplete")
	}
	byID := make(map[DrillID]Drill, len(drills))
	for _, drill := range drills {
		if _, duplicate := byID[drill.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("operational-safety drill is duplicated")
		}
		byID[drill.ID] = drill
	}
	ordered := make([]Drill, 0, len(requiredDrills))
	passed, failed, inconclusive := 0, 0, 0
	for _, drillID := range requiredDrills {
		drill, exists := byID[drillID]
		if !exists {
			return nil, 0, 0, 0, errors.New("operational-safety required drill is missing")
		}
		drill.StartedAt, drill.CompletedAt = drill.StartedAt.UTC(), drill.CompletedAt.UTC()
		if drill.StartedAt.IsZero() || drill.CompletedAt.IsZero() || drill.StartedAt.Before(earliest) || drill.CompletedAt.Before(drill.StartedAt) || drill.CompletedAt.Sub(drill.StartedAt) > maximumDrillDuration || drill.CompletedAt.After(generated) {
			return nil, 0, 0, 0, errors.New("operational-safety drill timeline is invalid")
		}
		checks, drillPassed, drillFailed, drillInconclusive, err := validateChecks(drillID, drill.Checks)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		drill.Checks = checks
		passed, failed, inconclusive = passed+drillPassed, failed+drillFailed, inconclusive+drillInconclusive
		ordered = append(ordered, drill)
	}
	return ordered, passed, failed, inconclusive, nil
}

func validateChecks(drillID DrillID, checks []Check) ([]Check, int, int, int, error) {
	required := requiredChecks[drillID]
	if len(checks) != len(required) {
		return nil, 0, 0, 0, errors.New("operational-safety drill checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(checks))
	passed, failed, inconclusive := 0, 0, 0
	for _, check := range checks {
		if !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("operational-safety check evidence digest is invalid")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("operational-safety check outcome is invalid")
		}
		if _, duplicate := byID[check.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("operational-safety check is duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(required))
	for _, checkID := range required {
		check, exists := byID[checkID]
		if !exists {
			return nil, 0, 0, 0, errors.New("operational-safety required check is missing")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}

func totalRequiredChecks() int {
	return len(requiredChecks[DrillManagedSecretRotation]) + len(requiredChecks[DrillHumanOperatorAccess])
}

func Load(path string) (Receipt, error) {
	var receipt Receipt
	if _, err := decodeStrictRegular(path, &receipt); err != nil {
		return Receipt{}, err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "staging_external" || receipt.Environment != "staging" || !opaquePattern.MatchString(receipt.BundleID) ||
		!opaquePattern.MatchString(receipt.InventoryID) || !opaquePattern.MatchString(receipt.PlanID) || !opaquePattern.MatchString(receipt.ChangeID) ||
		!opaquePattern.MatchString(receipt.BaselineReleaseID) || !opaquePattern.MatchString(receipt.FailedAttemptReleaseID) ||
		!digestPattern.MatchString(receipt.InventoryReceiptSHA256) || !digestPattern.MatchString(receipt.PlanReceiptSHA256) || !digestPattern.MatchString(receipt.ChangeReceiptSHA256) ||
		!digestPattern.MatchString(receipt.BaselineReceiptSHA256) || !digestPattern.MatchString(receipt.FailedAttemptReceiptSHA256) || !digestPattern.MatchString(receipt.RollbackReceiptSHA256) || !digestPattern.MatchString(receipt.InputSHA256) ||
		receipt.GeneratedAt.IsZero() || receipt.CollectedAt.IsZero() || receipt.GeneratedAt.After(receipt.CollectedAt) || receipt.DrillCount != len(requiredDrills) || receipt.CheckCount != totalRequiredChecks() {
		return Receipt{}, errors.New("operational-safety receipt identity is invalid")
	}
	drills, passed, failed, inconclusive, err := validateDrills(receipt.Drills, time.Time{}, receipt.GeneratedAt.UTC())
	if err != nil {
		return Receipt{}, err
	}
	ready := passed == totalRequiredChecks() && failed == 0 && inconclusive == 0
	if receipt.PassedCount != passed || receipt.FailedCount != failed || receipt.InconclusiveCount != inconclusive || receipt.Ready != ready {
		return Receipt{}, errors.New("operational-safety receipt aggregates are invalid")
	}
	receipt.Drills = drills
	return receipt, nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("operational-safety receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("operational-safety receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect operational-safety receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-operational-safety-*")
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("operational-safety evidence path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumEvidenceBytes {
		return "", errors.New("operational-safety evidence must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open operational-safety evidence")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("operational-safety evidence changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumEvidenceBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumEvidenceBytes {
		return "", errors.New("read operational-safety evidence")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("operational-safety evidence JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("operational-safety evidence contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("operational-safety evidence changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("operational-safety evidence changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
