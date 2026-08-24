// Package platformchange validates sanitized infrastructure apply and drift receipts.
package platformchange

import (
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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
)

const (
	SchemaV1           = "agent-memory-self-managed-infrastructure-change-v1"
	maximumChangeBytes = 256 << 10
)

type ApplyOutcome string
type RollbackOutcome string
type ResourceInventoryOutcome string
type DriftOutcome string
type CapabilityOutcome string

const (
	ApplySucceeded ApplyOutcome = "succeeded"
	ApplyFailed    ApplyOutcome = "failed"

	RollbackNotRequired  RollbackOutcome = "not_required"
	RollbackNotAttempted RollbackOutcome = "not_attempted"
	RollbackSucceeded    RollbackOutcome = "succeeded"
	RollbackFailed       RollbackOutcome = "failed"

	ResourceInventoryCollected    ResourceInventoryOutcome = "collected"
	ResourceInventoryNotCollected ResourceInventoryOutcome = "not_collected"

	DriftClean       DriftOutcome = "clean"
	DriftDetected    DriftOutcome = "drift_detected"
	DriftCheckFailed DriftOutcome = "check_failed"
	DriftNotRun      DriftOutcome = "not_run"

	CapabilityUnchanged  CapabilityOutcome = "unchanged"
	CapabilityApplied    CapabilityOutcome = "applied"
	CapabilityFailed     CapabilityOutcome = "failed"
	CapabilityRolledBack CapabilityOutcome = "rolled_back"
)

var (
	opaqueIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Receipt struct {
	Schema                 string                        `json:"schema"`
	Environment            platforminventory.Environment `json:"environment"`
	ChangeID               string                        `json:"change_id"`
	InventoryID            string                        `json:"inventory_id"`
	InventoryReceiptSHA256 string                        `json:"inventory_receipt_sha256"`
	PlanID                 string                        `json:"plan_id"`
	PlanReceiptSHA256      string                        `json:"plan_receipt_sha256"`
	GeneratedAt            time.Time                     `json:"generated_at"`
	Apply                  Apply                         `json:"apply"`
	Rollback               Rollback                      `json:"rollback"`
	ResourceInventory      ResourceInventory             `json:"resource_inventory"`
	Drift                  Drift                         `json:"drift"`
	Capabilities           []CapabilityResult            `json:"capabilities"`
	ReceiptSHA256          string                        `json:"-"`
}

type Apply struct {
	Outcome         ApplyOutcome `json:"outcome"`
	CompletedAt     time.Time    `json:"completed_at"`
	RawOutputSHA256 string       `json:"raw_output_sha256"`
}

type Rollback struct {
	Outcome         RollbackOutcome `json:"outcome"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	RawOutputSHA256 string          `json:"raw_output_sha256,omitempty"`
}

type ResourceInventory struct {
	Outcome       ResourceInventoryOutcome `json:"outcome"`
	CollectedAt   *time.Time               `json:"collected_at,omitempty"`
	SHA256        string                   `json:"sha256,omitempty"`
	ResourceCount *int                     `json:"resource_count,omitempty"`
}

type Drift struct {
	Outcome         DriftOutcome `json:"outcome"`
	CheckedAt       *time.Time   `json:"checked_at,omitempty"`
	RawOutputSHA256 string       `json:"raw_output_sha256,omitempty"`
}

type CapabilityResult struct {
	ID      platformplan.CapabilityID `json:"id"`
	Outcome CapabilityOutcome         `json:"outcome"`
}

type Assessment struct {
	Ready                    bool
	ApplyOutcome             ApplyOutcome
	RollbackOutcome          RollbackOutcome
	ResourceInventoryOutcome ResourceInventoryOutcome
	DriftOutcome             DriftOutcome
	CapabilityCount          int
	ResourceCount            int
}

func Load(path string, inventory platforminventory.Inventory, plan platformplan.Plan) (Receipt, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, fmt.Errorf("load infrastructure change receipt: %w", err)
	}
	receipt.ReceiptSHA256 = digest
	if err := validate(receipt, inventory, plan); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func Assess(receipt Receipt) Assessment {
	resourceCount := 0
	if receipt.ResourceInventory.ResourceCount != nil {
		resourceCount = *receipt.ResourceInventory.ResourceCount
	}
	return Assessment{
		Ready:                    receipt.Apply.Outcome == ApplySucceeded && receipt.Rollback.Outcome == RollbackNotRequired && receipt.ResourceInventory.Outcome == ResourceInventoryCollected && receipt.Drift.Outcome == DriftClean,
		ApplyOutcome:             receipt.Apply.Outcome,
		RollbackOutcome:          receipt.Rollback.Outcome,
		ResourceInventoryOutcome: receipt.ResourceInventory.Outcome,
		DriftOutcome:             receipt.Drift.Outcome,
		CapabilityCount:          len(receipt.Capabilities),
		ResourceCount:            resourceCount,
	}
}

func validate(receipt Receipt, inventory platforminventory.Inventory, plan platformplan.Plan) error {
	if receipt.Schema != SchemaV1 || receipt.Environment != inventory.Environment || receipt.Environment != plan.Environment {
		return errors.New("infrastructure change environment is invalid")
	}
	if !opaqueIDPattern.MatchString(receipt.ChangeID) || receipt.InventoryID != inventory.InventoryID || receipt.PlanID != plan.PlanID {
		return errors.New("infrastructure change identity is invalid")
	}
	if receipt.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || receipt.PlanReceiptSHA256 != plan.ReceiptSHA256 || !digestPattern.MatchString(receipt.InventoryReceiptSHA256) || !digestPattern.MatchString(receipt.PlanReceiptSHA256) {
		return errors.New("infrastructure change receipt binding is invalid")
	}
	if !platformplan.Assess(plan).Ready {
		return errors.New("infrastructure change plan is not ready")
	}
	if receipt.GeneratedAt.IsZero() || receipt.Apply.CompletedAt.IsZero() || receipt.Apply.CompletedAt.Before(plan.GeneratedAt) || receipt.GeneratedAt.Before(receipt.Apply.CompletedAt) || !digestPattern.MatchString(receipt.Apply.RawOutputSHA256) {
		return errors.New("infrastructure change apply metadata is invalid")
	}
	if err := validateCapabilityResults(receipt, plan); err != nil {
		return err
	}
	switch receipt.Apply.Outcome {
	case ApplySucceeded:
		return validateSuccessfulChange(receipt)
	case ApplyFailed:
		return validateFailedChange(receipt)
	default:
		return errors.New("infrastructure change apply outcome is invalid")
	}
}

func validateSuccessfulChange(receipt Receipt) error {
	if !emptyRollback(receipt.Rollback, RollbackNotRequired) {
		return errors.New("successful infrastructure change rollback state is invalid")
	}
	if receipt.ResourceInventory.Outcome != ResourceInventoryCollected || receipt.ResourceInventory.CollectedAt == nil || receipt.ResourceInventory.CollectedAt.Before(receipt.Apply.CompletedAt) || receipt.GeneratedAt.Before(*receipt.ResourceInventory.CollectedAt) || !digestPattern.MatchString(receipt.ResourceInventory.SHA256) || receipt.ResourceInventory.ResourceCount == nil || *receipt.ResourceInventory.ResourceCount < 1 || *receipt.ResourceInventory.ResourceCount > 10_000_000 {
		return errors.New("successful infrastructure change resource inventory is invalid")
	}
	if receipt.Drift.Outcome != DriftClean && receipt.Drift.Outcome != DriftDetected && receipt.Drift.Outcome != DriftCheckFailed {
		return errors.New("successful infrastructure change drift outcome is invalid")
	}
	if receipt.Drift.CheckedAt == nil || receipt.Drift.CheckedAt.Before(*receipt.ResourceInventory.CollectedAt) || receipt.GeneratedAt.Before(*receipt.Drift.CheckedAt) || !digestPattern.MatchString(receipt.Drift.RawOutputSHA256) {
		return errors.New("successful infrastructure change drift metadata is invalid")
	}
	return nil
}

func validateFailedChange(receipt Receipt) error {
	if receipt.ResourceInventory.Outcome != ResourceInventoryNotCollected || receipt.ResourceInventory.CollectedAt != nil || receipt.ResourceInventory.SHA256 != "" || receipt.ResourceInventory.ResourceCount != nil {
		return errors.New("failed infrastructure change resource inventory is invalid")
	}
	if receipt.Drift.Outcome != DriftNotRun || receipt.Drift.CheckedAt != nil || receipt.Drift.RawOutputSHA256 != "" {
		return errors.New("failed infrastructure change drift state is invalid")
	}
	switch receipt.Rollback.Outcome {
	case RollbackNotAttempted:
		if receipt.Rollback.CompletedAt != nil || receipt.Rollback.RawOutputSHA256 != "" {
			return errors.New("failed infrastructure change rollback metadata is invalid")
		}
	case RollbackSucceeded, RollbackFailed:
		if receipt.Rollback.CompletedAt == nil || receipt.Rollback.CompletedAt.Before(receipt.Apply.CompletedAt) || receipt.GeneratedAt.Before(*receipt.Rollback.CompletedAt) || !digestPattern.MatchString(receipt.Rollback.RawOutputSHA256) {
			return errors.New("failed infrastructure change rollback metadata is invalid")
		}
	default:
		return errors.New("failed infrastructure change rollback outcome is invalid")
	}
	return nil
}

func validateCapabilityResults(receipt Receipt, plan platformplan.Plan) error {
	if len(receipt.Capabilities) != len(plan.Capabilities) {
		return errors.New("infrastructure change capability result set is incomplete")
	}
	results := make(map[platformplan.CapabilityID]CapabilityOutcome, len(receipt.Capabilities))
	for _, result := range receipt.Capabilities {
		if _, duplicate := results[result.ID]; duplicate {
			return errors.New("infrastructure change capability result is duplicated")
		}
		results[result.ID] = result.Outcome
	}
	for _, capability := range plan.Capabilities {
		actual, exists := results[capability.ID]
		if !exists {
			return errors.New("infrastructure change capability result is missing")
		}
		expected := expectedCapabilityOutcome(receipt.Apply.Outcome, receipt.Rollback.Outcome, capability.Action)
		if expected == "" || actual != expected {
			return errors.New("infrastructure change capability result is inconsistent")
		}
	}
	return nil
}

func expectedCapabilityOutcome(apply ApplyOutcome, rollback RollbackOutcome, action platformplan.Action) CapabilityOutcome {
	if action == platformplan.ActionNoChange {
		return CapabilityUnchanged
	}
	if action != platformplan.ActionCreate && action != platformplan.ActionUpdate {
		return ""
	}
	if apply == ApplySucceeded {
		return CapabilityApplied
	}
	if apply == ApplyFailed && rollback == RollbackSucceeded {
		return CapabilityRolledBack
	}
	if apply == ApplyFailed {
		return CapabilityFailed
	}
	return ""
}

func emptyRollback(rollback Rollback, outcome RollbackOutcome) bool {
	return rollback.Outcome == outcome && rollback.CompletedAt == nil && rollback.RawOutputSHA256 == ""
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("change receipt path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumChangeBytes {
		return "", errors.New("change receipt must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open change receipt")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("change receipt changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumChangeBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumChangeBytes {
		return "", errors.New("read change receipt")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("change receipt JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("change receipt contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("change receipt changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("change receipt changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
