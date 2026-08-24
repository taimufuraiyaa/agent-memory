// Package backupexpiry validates content-free evidence that a real
// self-managed backup aged out after the complete installed retention window.
package backupexpiry

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
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retentioninventory"
)

const (
	InputSchemaV1           = "agent-memory-self-managed-backup-expiry-drill-v1"
	ReceiptSchemaV1         = "agent-memory-self-managed-backup-expiry-receipt-v1"
	maximumInputBytes       = 64 << 10
	maximumCollectionAge    = 24 * time.Hour
	maximumVerificationSpan = 6 * time.Hour
)

type CheckID string
type Outcome string

const (
	CheckBackupManifestVerified       CheckID = "backup_manifest_verified"
	CheckDeletedRecordPresent         CheckID = "deleted_record_present_in_backup"
	CheckDeletionReceiptVerified      CheckID = "deletion_receipt_verified"
	CheckExpiryScheduleVerified       CheckID = "expiry_schedule_verified"
	CheckBackupAbsentAfterDeadline    CheckID = "backup_absent_after_deadline"
	CheckRestoreUnavailable           CheckID = "restore_unavailable"
	CheckCryptographicMaterialExpired CheckID = "cryptographic_material_expired"

	OutcomePassed Outcome = "passed"
	OutcomeFailed Outcome = "failed"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{
		CheckBackupManifestVerified,
		CheckDeletedRecordPresent,
		CheckDeletionReceiptVerified,
		CheckExpiryScheduleVerified,
		CheckBackupAbsentAfterDeadline,
		CheckRestoreUnavailable,
		CheckCryptographicMaterialExpired,
	}
)

type Timeline struct {
	BackupCreatedAt         time.Time `json:"backup_created_at"`
	DeletionCompletedAt     time.Time `json:"deletion_completed_at"`
	VerificationStartedAt   time.Time `json:"verification_started_at"`
	VerificationCompletedAt time.Time `json:"verification_completed_at"`
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
	DrillID                         string    `json:"drill_id"`
	BackupID                        string    `json:"backup_id"`
	InventoryID                     string    `json:"inventory_id"`
	InventoryReceiptSHA256          string    `json:"inventory_receipt_sha256"`
	ChangeID                        string    `json:"change_id"`
	ChangeReceiptSHA256             string    `json:"change_receipt_sha256"`
	RetentionInventoryReceiptSHA256 string    `json:"retention_inventory_receipt_sha256"`
	PoliciesSHA256                  string    `json:"policies_sha256"`
	BackupPolicyVersion             string    `json:"backup_policy_version"`
	BackupRetentionSeconds          int64     `json:"backup_retention_seconds"`
	Ready                           bool      `json:"ready"`
	GeneratedAt                     time.Time `json:"generated_at"`
	Timeline                        Timeline  `json:"timeline"`
	Checks                          []Check   `json:"checks"`
}

type Receipt struct {
	Schema                          string    `json:"schema"`
	Classification                  string    `json:"classification"`
	Environment                     string    `json:"environment"`
	DrillID                         string    `json:"drill_id"`
	BackupID                        string    `json:"backup_id"`
	InventoryID                     string    `json:"inventory_id"`
	InventoryReceiptSHA256          string    `json:"inventory_receipt_sha256"`
	ChangeID                        string    `json:"change_id"`
	ChangeReceiptSHA256             string    `json:"change_receipt_sha256"`
	RetentionInventoryReceiptSHA256 string    `json:"retention_inventory_receipt_sha256"`
	PoliciesSHA256                  string    `json:"policies_sha256"`
	InputSHA256                     string    `json:"input_sha256"`
	BackupPolicyVersion             string    `json:"backup_policy_version"`
	BackupRetentionSeconds          int64     `json:"backup_retention_seconds"`
	ExpiryDeadlineAt                time.Time `json:"expiry_deadline_at"`
	ElapsedSinceDeletionSeconds     int64     `json:"elapsed_since_deletion_seconds"`
	Ready                           bool      `json:"ready"`
	GeneratedAt                     time.Time `json:"generated_at"`
	CollectedAt                     time.Time `json:"collected_at"`
	Timeline                        Timeline  `json:"timeline"`
	CheckCount                      int       `json:"check_count"`
	PassedCount                     int       `json:"passed_count"`
	FailedCount                     int       `json:"failed_count"`
	Checks                          []Check   `json:"checks"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(inventoryPath, planPath, changePath, retentionPath, drillPath string, now time.Time) (Receipt, error) {
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
	policies, err := retentioninventory.Load(retentionPath)
	if err != nil {
		return Receipt{}, err
	}
	var input Input
	inputDigest, err := decodeStrictRegular(drillPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return Build(inventory, change, policies, input, inputDigest, now)
}

func Build(inventory platforminventory.Inventory, change platformchange.Receipt, policies retentioninventory.Receipt, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Production ||
		change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment ||
		change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 ||
		!platformchange.Assess(change).Ready {
		return Receipt{}, errors.New("backup expiry platform chain is invalid or unready")
	}
	if policies.Schema != retentioninventory.ReceiptSchemaV1 || !policies.Ready || policies.Environment != string(inventory.Environment) ||
		policies.InventoryID != inventory.InventoryID || policies.InventoryReceiptSHA256 != inventory.ReceiptSHA256 ||
		policies.ChangeID != change.ChangeID || policies.ChangeReceiptSHA256 != change.ReceiptSHA256 ||
		!digestPattern.MatchString(policies.ReceiptSHA256) {
		return Receipt{}, errors.New("backup expiry retention inventory binding is invalid")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "self_managed_external" || input.Environment != string(platforminventory.Production) ||
		!opaquePattern.MatchString(input.DrillID) || !opaquePattern.MatchString(input.BackupID) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 ||
		input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 ||
		input.RetentionInventoryReceiptSHA256 != policies.ReceiptSHA256 || input.PoliciesSHA256 != policies.PoliciesSHA256 ||
		!digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("backup expiry drill identity or binding is invalid")
	}
	backupPolicy, ok := installedBackupPolicy(policies.Policies)
	if !ok || input.BackupPolicyVersion != backupPolicy.PolicyVersion || input.BackupRetentionSeconds != backupPolicy.DurationSeconds || backupPolicy.DurationSeconds <= 0 {
		return Receipt{}, errors.New("backup expiry installed policy is invalid or mismatched")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("backup expiry collection time is invalid")
	}
	now = now.UTC()
	timeline := normalizeTimeline(input.Timeline)
	generated := input.GeneratedAt.UTC()
	deadline := timeline.DeletionCompletedAt.Add(time.Duration(backupPolicy.DurationSeconds) * time.Second)
	if timeline.BackupCreatedAt.IsZero() || timeline.DeletionCompletedAt.IsZero() || timeline.VerificationStartedAt.IsZero() || timeline.VerificationCompletedAt.IsZero() || generated.IsZero() ||
		timeline.BackupCreatedAt.Before(policies.CollectedAt.UTC()) || timeline.BackupCreatedAt.Before(change.GeneratedAt.UTC()) ||
		backupPolicy.EffectiveAt.UTC().After(timeline.BackupCreatedAt) || !timeline.BackupCreatedAt.Before(timeline.DeletionCompletedAt) ||
		timeline.DeletionCompletedAt.Before(change.GeneratedAt.UTC()) || timeline.VerificationStartedAt.Before(deadline) ||
		timeline.VerificationCompletedAt.Before(timeline.VerificationStartedAt) || timeline.VerificationCompletedAt.Sub(timeline.VerificationStartedAt) > maximumVerificationSpan ||
		generated.Before(timeline.VerificationCompletedAt) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("backup expiry drill timeline is invalid")
	}
	checks, passed, failed, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	ready := failed == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("backup expiry readiness contradicts checks")
	}
	elapsed := int64(timeline.VerificationCompletedAt.Sub(timeline.DeletionCompletedAt) / time.Second)
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment,
		DrillID: input.DrillID, BackupID: input.BackupID,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256,
		ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256,
		RetentionInventoryReceiptSHA256: input.RetentionInventoryReceiptSHA256, PoliciesSHA256: input.PoliciesSHA256,
		InputSHA256: inputDigest, BackupPolicyVersion: backupPolicy.PolicyVersion,
		BackupRetentionSeconds: backupPolicy.DurationSeconds, ExpiryDeadlineAt: deadline,
		ElapsedSinceDeletionSeconds: elapsed, Ready: ready, GeneratedAt: generated, CollectedAt: now,
		Timeline: timeline, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, Checks: checks,
	}, nil
}

func installedBackupPolicy(policies []retentioninventory.Policy) (retentioninventory.Policy, bool) {
	for _, policy := range policies {
		if policy.DataClass == "backups" {
			return policy, true
		}
	}
	return retentioninventory.Policy{}, false
}

func validateChecks(checks []Check) ([]Check, int, int, error) {
	if len(checks) != len(requiredChecks) {
		return nil, 0, 0, errors.New("backup expiry checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(checks))
	passed := 0
	for _, check := range checks {
		if (check.Outcome != OutcomePassed && check.Outcome != OutcomeFailed) || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, errors.New("backup expiry check is invalid")
		}
		if _, duplicate := byID[check.ID]; duplicate {
			return nil, 0, 0, errors.New("backup expiry check is duplicated")
		}
		byID[check.ID] = check
		if check.Outcome == OutcomePassed {
			passed++
		}
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		check, exists := byID[id]
		if !exists {
			return nil, 0, 0, errors.New("backup expiry required check is missing")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, len(checks) - passed, nil
}

func normalizeTimeline(value Timeline) Timeline {
	value.BackupCreatedAt = value.BackupCreatedAt.UTC()
	value.DeletionCompletedAt = value.DeletionCompletedAt.UTC()
	value.VerificationStartedAt = value.VerificationStartedAt.UTC()
	value.VerificationCompletedAt = value.VerificationCompletedAt.UTC()
	return value
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("backup expiry drill path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("backup expiry drill must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open backup expiry drill")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("backup expiry drill changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read backup expiry drill")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("backup expiry drill JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("backup expiry drill contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("backup expiry drill changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("backup expiry drill changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("backup expiry receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("backup expiry receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect backup expiry receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-backup-expiry-*")
}
