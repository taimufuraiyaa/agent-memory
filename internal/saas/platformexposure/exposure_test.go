package platformexposure

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

func TestLoadAcceptsBlockedProductionReceiptAndHashesExactBytes(t *testing.T) {
	inventory, change := validBindings()
	contents := validReceiptJSON(inventory, change)
	path := writeReceipt(t, contents)

	receipt, err := Load(path, inventory, change)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(contents)))
	if receipt.ReceiptSHA256 != want {
		t.Fatalf("receipt digest=%q, want %q", receipt.ReceiptSHA256, want)
	}
	assessment := Assess(receipt)
	if !assessment.Ready || assessment.TargetCount != 7 || assessment.BlockedCount != 7 || assessment.ReachableCount != 0 || assessment.InconclusiveCount != 0 {
		t.Fatalf("unexpected assessment: %+v", assessment)
	}
}

func TestLoadTreatsReachableAndInconclusiveAsValidUnreadyEvidence(t *testing.T) {
	inventory, change := validBindings()
	value := validReceiptMap(inventory, change)
	results := value["targets"].([]map[string]string)
	results[0]["outcome"] = "reachable"
	results[1]["outcome"] = "inconclusive"

	receipt, err := Load(writeReceipt(t, encode(value)), inventory, change)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(receipt)
	if assessment.Ready || assessment.ReachableCount != 1 || assessment.InconclusiveCount != 1 || assessment.BlockedCount != 5 {
		t.Fatalf("unexpected assessment: %+v", assessment)
	}
}

func TestLoadRejectsUnsafeOrInvalidReceipts(t *testing.T) {
	inventory, change := validBindings()
	valid := validReceiptJSON(inventory, change)

	tests := map[string]string{
		"unknown field":       strings.TrimSuffix(valid, "}") + `,"reviewed":true}`,
		"staging":             strings.Replace(valid, `"environment":"production"`, `"environment":"staging"`, 1),
		"inventory binding":   strings.Replace(valid, inventory.ReceiptSHA256, strings.Repeat("a", 64), 1),
		"change binding":      strings.Replace(valid, change.ReceiptSHA256, strings.Repeat("b", 64), 1),
		"stale scan":          strings.Replace(valid, `"scanned_at":"2026-08-10T05:00:00Z"`, `"scanned_at":"2026-08-10T03:59:59Z"`, 1),
		"invalid digest":      strings.Replace(valid, strings.Repeat("d", 64), "not-a-digest", 1),
		"unknown target":      strings.Replace(valid, `"id":"postgres"`, `"id":"database"`, 1),
		"invalid outcome":     strings.Replace(valid, `"outcome":"blocked"`, `"outcome":"approved"`, 1),
		"missing target":      strings.Replace(valid, `,{"id":"kubernetes_control","outcome":"blocked"}`, "", 1),
		"duplicate target":    strings.Replace(valid, `"id":"kubernetes_control"`, `"id":"postgres"`, 1),
		"scanner with spaces": strings.Replace(valid, `"name":"nmap"`, `"name":"external scanner"`, 1),
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeReceipt(t, contents), inventory, change); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}

	symlink := filepath.Join(t.TempDir(), "receipt-link.json")
	if err := os.Symlink(writeReceipt(t, valid), symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(symlink, inventory, change); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestLoadRejectsUnreadyInfrastructureChange(t *testing.T) {
	inventory, change := validBindings()
	change.Drift.Outcome = platformchange.DriftDetected
	if _, err := Load(writeReceipt(t, validReceiptJSON(inventory, change)), inventory, change); err == nil {
		t.Fatal("expected unready change rejection")
	}
}

func validBindings() (platforminventory.Inventory, platformchange.Receipt) {
	inventory := platforminventory.Inventory{
		Schema:        platforminventory.SchemaV1,
		Environment:   platforminventory.Production,
		InventoryID:   "inventory-production",
		GeneratedAt:   time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		ReceiptSHA256: strings.Repeat("8", 64),
	}
	count := 42
	collected := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	checked := time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC)
	change := platformchange.Receipt{
		Schema:                 platformchange.SchemaV1,
		Environment:            platforminventory.Production,
		ChangeID:               "change-production",
		InventoryID:            inventory.InventoryID,
		InventoryReceiptSHA256: inventory.ReceiptSHA256,
		GeneratedAt:            time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC),
		Apply:                  platformchange.Apply{Outcome: platformchange.ApplySucceeded, CompletedAt: time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)},
		Rollback:               platformchange.Rollback{Outcome: platformchange.RollbackNotRequired},
		ResourceInventory:      platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected, CollectedAt: &collected, ResourceCount: &count},
		Drift:                  platformchange.Drift{Outcome: platformchange.DriftClean, CheckedAt: &checked},
		ReceiptSHA256:          strings.Repeat("9", 64),
	}
	return inventory, change
}

func validReceiptJSON(inventory platforminventory.Inventory, change platformchange.Receipt) string {
	return encode(validReceiptMap(inventory, change))
}

func validReceiptMap(inventory platforminventory.Inventory, change platformchange.Receipt) map[string]any {
	targets := make([]map[string]string, 0, len(requiredTargets))
	for _, target := range requiredTargets {
		targets = append(targets, map[string]string{"id": string(target), "outcome": "blocked"})
	}
	return map[string]any{
		"schema":                   SchemaV1,
		"environment":              "production",
		"exposure_id":              "exposure-20260810",
		"inventory_id":             inventory.InventoryID,
		"inventory_receipt_sha256": inventory.ReceiptSHA256,
		"change_id":                change.ChangeID,
		"change_receipt_sha256":    change.ReceiptSHA256,
		"generated_at":             "2026-08-10T06:00:00Z",
		"firewall_export_sha256":   strings.Repeat("d", 64),
		"scan": map[string]any{
			"scanner":           map[string]string{"name": "nmap", "version": "7.95"},
			"scanned_at":        "2026-08-10T05:00:00Z",
			"raw_output_sha256": strings.Repeat("e", 64),
		},
		"targets": targets,
	}
}

func encode(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func writeReceipt(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "exposure.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
