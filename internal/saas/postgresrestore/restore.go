// Package postgresrestore validates content-free evidence from a self-managed
// PostgreSQL backup and restore drill. It does not connect to or restore a
// database itself.
package postgresrestore

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
)

const (
	DrillSchemaV1   = "agent-memory-self-managed-postgres-restore-drill-v1"
	ReceiptSchemaV1 = "agent-memory-self-managed-postgres-restore-receipt-v1"
	RPOSecondsLimit = int64(300)
	RTOSecondsLimit = int64(3600)

	maximumDrillBytes = 64 << 10
	maximumDrillAge   = 24 * time.Hour
	maximumDrillSpan  = 24 * time.Hour
)

type CheckID string
type Outcome string

const (
	CheckBackupIntegrity          CheckID = "backup_integrity"
	CheckRestoreCompleted         CheckID = "restore_completed"
	CheckSchemaMigrationsMatch    CheckID = "schema_migrations_match"
	CheckTenantCountsMatch        CheckID = "tenant_counts_match"
	CheckAuthoritativeRowsMatch   CheckID = "authoritative_rows_match"
	CheckOutboxReconciled         CheckID = "outbox_reconciled"
	CheckAuditChainVerified       CheckID = "audit_chain_verified"
	CheckDeletionTombstonesReplay CheckID = "deletion_tombstones_replayed"
	CheckDeletedDataAbsent        CheckID = "deleted_data_absent"
	CheckRestoreTargetDisposed    CheckID = "restore_target_disposed"

	OutcomePassed Outcome = "passed"
	OutcomeFailed Outcome = "failed"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{
		CheckBackupIntegrity,
		CheckRestoreCompleted,
		CheckSchemaMigrationsMatch,
		CheckTenantCountsMatch,
		CheckAuthoritativeRowsMatch,
		CheckOutboxReconciled,
		CheckAuditChainVerified,
		CheckDeletionTombstonesReplay,
		CheckDeletedDataAbsent,
		CheckRestoreTargetDisposed,
	}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Backup struct {
	ID                       string    `json:"backup_id"`
	CreatedAt                time.Time `json:"created_at"`
	VerifiedAt               time.Time `json:"verified_at"`
	ManifestSHA256           string    `json:"manifest_sha256"`
	VerificationOutputSHA256 string    `json:"verification_output_sha256"`
}

type Timeline struct {
	ImpairmentStartedAt       time.Time `json:"impairment_started_at"`
	RecoveryPointAt           time.Time `json:"recovery_point_at"`
	RestoreStartedAt          time.Time `json:"restore_started_at"`
	RestoreCompletedAt        time.Time `json:"restore_completed_at"`
	ReconciliationCompletedAt time.Time `json:"reconciliation_completed_at"`
	ServiceReadyAt            time.Time `json:"service_ready_at"`
	RestoreTargetDisposedAt   time.Time `json:"restore_target_disposed_at"`
}

type Drill struct {
	Schema                 string    `json:"schema"`
	Classification         string    `json:"classification"`
	Environment            string    `json:"environment"`
	DrillID                string    `json:"drill_id"`
	InventoryID            string    `json:"inventory_id"`
	InventoryReceiptSHA256 string    `json:"inventory_receipt_sha256"`
	ChangeID               string    `json:"change_id"`
	ChangeReceiptSHA256    string    `json:"change_receipt_sha256"`
	Ready                  bool      `json:"ready"`
	GeneratedAt            time.Time `json:"generated_at"`
	Backup                 Backup    `json:"backup"`
	Timeline               Timeline  `json:"timeline"`
	Checks                 []Check   `json:"checks"`
}

type RecoveryTargets struct {
	RPOSeconds int64 `json:"rpo_seconds"`
	RTOSeconds int64 `json:"rto_seconds"`
}

type Receipt struct {
	Schema                 string          `json:"schema"`
	Ready                  bool            `json:"ready"`
	Environment            string          `json:"environment"`
	DrillID                string          `json:"drill_id"`
	InventoryID            string          `json:"inventory_id"`
	InventoryReceiptSHA256 string          `json:"inventory_receipt_sha256"`
	ChangeID               string          `json:"change_id"`
	ChangeReceiptSHA256    string          `json:"change_receipt_sha256"`
	InputSHA256            string          `json:"input_sha256"`
	CollectedAt            time.Time       `json:"collected_at"`
	Backup                 Backup          `json:"backup"`
	Timeline               Timeline        `json:"timeline"`
	Targets                RecoveryTargets `json:"targets"`
	Measured               RecoveryTargets `json:"measured"`
	Checks                 []Check         `json:"checks"`
}

type Assessment struct {
	Ready       bool
	RPOSeconds  int64
	RTOSeconds  int64
	CheckCount  int
	PassedCount int
	FailedCount int
}

func Collect(inventoryPath, planPath, changePath, drillPath string, now time.Time) (Receipt, error) {
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
	if !platformchange.Assess(change).Ready {
		return Receipt{}, errors.New("self-managed infrastructure change is not ready")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("restore evidence collection time is invalid")
	}
	now = now.UTC()
	var drill Drill
	digest, err := decodeStrictRegular(drillPath, &drill)
	if err != nil {
		return Receipt{}, err
	}
	rpo, rto, ordered, err := validateDrill(drill, inventory, change, now)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Ready: drill.Ready, Environment: drill.Environment,
		DrillID: drill.DrillID, InventoryID: drill.InventoryID,
		InventoryReceiptSHA256: drill.InventoryReceiptSHA256, ChangeID: drill.ChangeID,
		ChangeReceiptSHA256: drill.ChangeReceiptSHA256, InputSHA256: digest, CollectedAt: now,
		Backup: normalizeBackup(drill.Backup), Timeline: normalizeTimeline(drill.Timeline),
		Targets:  RecoveryTargets{RPOSeconds: RPOSecondsLimit, RTOSeconds: RTOSecondsLimit},
		Measured: RecoveryTargets{RPOSeconds: rpo, RTOSeconds: rto}, Checks: ordered,
	}, nil
}

func validateDrill(drill Drill, inventory platforminventory.Inventory, change platformchange.Receipt, now time.Time) (int64, int64, []Check, error) {
	if drill.Schema != DrillSchemaV1 || drill.Classification != "self_managed_external" ||
		drill.Environment != string(inventory.Environment) || drill.InventoryID != inventory.InventoryID ||
		drill.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || drill.ChangeID != change.ChangeID ||
		drill.ChangeReceiptSHA256 != change.ReceiptSHA256 || !opaquePattern.MatchString(drill.DrillID) ||
		!digestPattern.MatchString(drill.InventoryReceiptSHA256) || !digestPattern.MatchString(drill.ChangeReceiptSHA256) {
		return 0, 0, nil, errors.New("restore drill identity or platform binding is invalid")
	}
	backup := drill.Backup
	if !opaquePattern.MatchString(backup.ID) || !digestPattern.MatchString(backup.ManifestSHA256) || !digestPattern.MatchString(backup.VerificationOutputSHA256) {
		return 0, 0, nil, errors.New("restore drill backup evidence is invalid")
	}
	bCreated, bVerified := backup.CreatedAt.UTC(), backup.VerifiedAt.UTC()
	t := normalizeTimeline(drill.Timeline)
	generated := drill.GeneratedAt.UTC()
	if bCreated.IsZero() || bVerified.IsZero() || t.ImpairmentStartedAt.IsZero() || t.RecoveryPointAt.IsZero() ||
		t.RestoreStartedAt.IsZero() || t.RestoreCompletedAt.IsZero() || t.ReconciliationCompletedAt.IsZero() ||
		t.ServiceReadyAt.IsZero() || t.RestoreTargetDisposedAt.IsZero() || generated.IsZero() ||
		bCreated.Before(change.GeneratedAt.UTC()) || bVerified.Before(bCreated) || bVerified.After(t.ImpairmentStartedAt) ||
		t.RecoveryPointAt.Before(bCreated) || t.RecoveryPointAt.After(t.ImpairmentStartedAt) ||
		t.RestoreStartedAt.Before(t.ImpairmentStartedAt) || t.RestoreCompletedAt.Before(t.RestoreStartedAt) ||
		t.ReconciliationCompletedAt.Before(t.RestoreCompletedAt) || t.ServiceReadyAt.Before(t.ReconciliationCompletedAt) ||
		t.RestoreTargetDisposedAt.Before(t.ServiceReadyAt) || generated.Before(t.RestoreTargetDisposedAt) ||
		generated.After(now) || generated.Before(now.Add(-maximumDrillAge)) || generated.Sub(bCreated) > maximumDrillSpan {
		return 0, 0, nil, errors.New("restore drill timeline is invalid")
	}
	rpo := durationSecondsCeil(t.ImpairmentStartedAt.Sub(t.RecoveryPointAt))
	rto := durationSecondsCeil(t.ServiceReadyAt.Sub(t.ImpairmentStartedAt))
	ordered, allPassed, err := validateChecks(drill.Checks)
	if err != nil {
		return 0, 0, nil, err
	}
	ready := allPassed && rpo <= RPOSecondsLimit && rto <= RTOSecondsLimit
	if drill.Ready != ready {
		return 0, 0, nil, errors.New("restore drill readiness contradicts checks or recovery targets")
	}
	return rpo, rto, ordered, nil
}

func durationSecondsCeil(value time.Duration) int64 {
	seconds := int64(value / time.Second)
	if value%time.Second != 0 {
		seconds++
	}
	return seconds
}

func validateChecks(checks []Check) ([]Check, bool, error) {
	if len(checks) != len(requiredChecks) {
		return nil, false, errors.New("restore drill checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(checks))
	allPassed := true
	for _, check := range checks {
		if (check.Outcome != OutcomePassed && check.Outcome != OutcomeFailed) || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, false, errors.New("restore drill check is invalid")
		}
		if _, exists := byID[check.ID]; exists {
			return nil, false, errors.New("restore drill check is duplicated")
		}
		byID[check.ID] = check
		allPassed = allPassed && check.Outcome == OutcomePassed
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		check, exists := byID[id]
		if !exists {
			return nil, false, errors.New("restore drill required check is missing")
		}
		ordered = append(ordered, check)
	}
	return ordered, allPassed, nil
}

func normalizeBackup(value Backup) Backup {
	value.CreatedAt = value.CreatedAt.UTC()
	value.VerifiedAt = value.VerifiedAt.UTC()
	return value
}

func normalizeTimeline(value Timeline) Timeline {
	value.ImpairmentStartedAt = value.ImpairmentStartedAt.UTC()
	value.RecoveryPointAt = value.RecoveryPointAt.UTC()
	value.RestoreStartedAt = value.RestoreStartedAt.UTC()
	value.RestoreCompletedAt = value.RestoreCompletedAt.UTC()
	value.ReconciliationCompletedAt = value.ReconciliationCompletedAt.UTC()
	value.ServiceReadyAt = value.ServiceReadyAt.UTC()
	value.RestoreTargetDisposedAt = value.RestoreTargetDisposedAt.UTC()
	return value
}

func Assess(receipt Receipt) Assessment {
	assessment := Assessment{Ready: receipt.Ready, RPOSeconds: receipt.Measured.RPOSeconds, RTOSeconds: receipt.Measured.RTOSeconds, CheckCount: len(receipt.Checks)}
	for _, check := range receipt.Checks {
		if check.Outcome == OutcomePassed {
			assessment.PassedCount++
		} else {
			assessment.FailedCount++
		}
	}
	return assessment
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("restore drill path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumDrillBytes {
		return "", errors.New("restore drill must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open restore drill")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("restore drill changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumDrillBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumDrillBytes {
		return "", errors.New("read restore drill")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("restore drill JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("restore drill contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("restore drill changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("restore drill changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("restore receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("restore receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect restore receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-postgres-restore-*")
}
