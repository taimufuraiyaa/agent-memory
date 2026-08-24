// Package platformexposure validates production private-authority exposure receipts.
package platformexposure

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

const (
	SchemaV1            = "agent-memory-production-private-authority-exposure-v1"
	maximumReceiptBytes = 64 << 10
)

type TargetID string
type Outcome string

const (
	TargetPostgres          TargetID = "postgres"
	TargetObjectStorage     TargetID = "object_storage"
	TargetDurableQueue      TargetID = "durable_queue"
	TargetSecrets           TargetID = "secrets"
	TargetObservability     TargetID = "observability"
	TargetBackup            TargetID = "backup"
	TargetKubernetesControl TargetID = "kubernetes_control"

	OutcomeBlocked      Outcome = "blocked"
	OutcomeReachable    Outcome = "reachable"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	requiredTargets = []TargetID{
		TargetPostgres,
		TargetObjectStorage,
		TargetDurableQueue,
		TargetSecrets,
		TargetObservability,
		TargetBackup,
		TargetKubernetesControl,
	}
	opaqueIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	toolPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
)

type Receipt struct {
	Schema                 string                        `json:"schema"`
	Environment            platforminventory.Environment `json:"environment"`
	ExposureID             string                        `json:"exposure_id"`
	InventoryID            string                        `json:"inventory_id"`
	InventoryReceiptSHA256 string                        `json:"inventory_receipt_sha256"`
	ChangeID               string                        `json:"change_id"`
	ChangeReceiptSHA256    string                        `json:"change_receipt_sha256"`
	GeneratedAt            time.Time                     `json:"generated_at"`
	FirewallExportSHA256   string                        `json:"firewall_export_sha256"`
	Scan                   Scan                          `json:"scan"`
	Targets                []TargetResult                `json:"targets"`
	ReceiptSHA256          string                        `json:"-"`
}

type Scan struct {
	Scanner         Scanner   `json:"scanner"`
	ScannedAt       time.Time `json:"scanned_at"`
	RawOutputSHA256 string    `json:"raw_output_sha256"`
}

type Scanner struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type TargetResult struct {
	ID      TargetID `json:"id"`
	Outcome Outcome  `json:"outcome"`
}

type Assessment struct {
	Ready             bool
	TargetCount       int
	BlockedCount      int
	ReachableCount    int
	InconclusiveCount int
}

func Load(path string, inventory platforminventory.Inventory, change platformchange.Receipt) (Receipt, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production exposure receipt: %w", err)
	}
	receipt.ReceiptSHA256 = digest
	if err := validate(receipt, inventory, change); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func Assess(receipt Receipt) Assessment {
	assessment := Assessment{TargetCount: len(receipt.Targets)}
	for _, target := range receipt.Targets {
		switch target.Outcome {
		case OutcomeBlocked:
			assessment.BlockedCount++
		case OutcomeReachable:
			assessment.ReachableCount++
		case OutcomeInconclusive:
			assessment.InconclusiveCount++
		}
	}
	assessment.Ready = assessment.TargetCount == len(requiredTargets) && assessment.BlockedCount == len(requiredTargets)
	return assessment
}

func validate(receipt Receipt, inventory platforminventory.Inventory, change platformchange.Receipt) error {
	if receipt.Schema != SchemaV1 || receipt.Environment != platforminventory.Production || inventory.Environment != platforminventory.Production || change.Environment != platforminventory.Production {
		return errors.New("production exposure environment is invalid")
	}
	if !opaqueIDPattern.MatchString(receipt.ExposureID) || receipt.InventoryID != inventory.InventoryID || receipt.ChangeID != change.ChangeID {
		return errors.New("production exposure identity is invalid")
	}
	if receipt.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || receipt.ChangeReceiptSHA256 != change.ReceiptSHA256 || !digestPattern.MatchString(receipt.InventoryReceiptSHA256) || !digestPattern.MatchString(receipt.ChangeReceiptSHA256) {
		return errors.New("production exposure receipt binding is invalid")
	}
	if !platformchange.Assess(change).Ready {
		return errors.New("production exposure infrastructure change is not ready")
	}
	if receipt.GeneratedAt.IsZero() || receipt.Scan.ScannedAt.IsZero() || receipt.Scan.ScannedAt.Before(change.GeneratedAt) || receipt.GeneratedAt.Before(receipt.Scan.ScannedAt) {
		return errors.New("production exposure timestamps are invalid")
	}
	if !digestPattern.MatchString(receipt.FirewallExportSHA256) || !digestPattern.MatchString(receipt.Scan.RawOutputSHA256) || !toolPattern.MatchString(receipt.Scan.Scanner.Name) || !toolPattern.MatchString(receipt.Scan.Scanner.Version) {
		return errors.New("production exposure scan metadata is invalid")
	}
	return validateTargets(receipt.Targets)
}

func validateTargets(targets []TargetResult) error {
	if len(targets) != len(requiredTargets) {
		return errors.New("production exposure target set is incomplete")
	}
	allowed := make(map[TargetID]struct{}, len(requiredTargets))
	for _, target := range requiredTargets {
		allowed[target] = struct{}{}
	}
	seen := make(map[TargetID]struct{}, len(targets))
	for _, target := range targets {
		if _, exists := allowed[target.ID]; !exists {
			return errors.New("production exposure target is unknown")
		}
		if _, duplicate := seen[target.ID]; duplicate {
			return errors.New("production exposure target is duplicated")
		}
		seen[target.ID] = struct{}{}
		switch target.Outcome {
		case OutcomeBlocked, OutcomeReachable, OutcomeInconclusive:
		default:
			return errors.New("production exposure target outcome is invalid")
		}
	}
	return nil
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("exposure receipt path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumReceiptBytes {
		return "", errors.New("exposure receipt must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open exposure receipt")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("exposure receipt changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumReceiptBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumReceiptBytes {
		return "", errors.New("read exposure receipt")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("exposure receipt JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("exposure receipt contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("exposure receipt changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("exposure receipt changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
