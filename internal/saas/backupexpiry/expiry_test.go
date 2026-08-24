package backupexpiry

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retentioninventory"
)

func TestBuildDerivesInstalledPolicyDeadlineAndCanonicalChecks(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change, policies := validBindings(now)
	input := validInput(now, inventory, change, policies, true)
	receipt, err := Build(inventory, change, policies, input, strings.Repeat("9", 64), now)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	wantDeadline := input.Timeline.DeletionCompletedAt.Add(30 * 24 * time.Hour)
	if receipt.Schema != ReceiptSchemaV1 || !receipt.Ready || receipt.ExpiryDeadlineAt != wantDeadline || receipt.BackupRetentionSeconds != int64((30*24*time.Hour)/time.Second) || receipt.CheckCount != len(RequiredChecks()) || receipt.PassedCount != len(RequiredChecks()) {
		t.Fatalf("receipt=%+v", receipt)
	}
	for index, id := range RequiredChecks() {
		if receipt.Checks[index].ID != id {
			t.Fatalf("check[%d]=%q want %q", index, receipt.Checks[index].ID, id)
		}
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"tenant_id", "record_id", "object_path", "backup_path", "credential", "endpoint", "database", "customer_content", "raw_output"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("receipt leaked forbidden token %q", forbidden)
		}
	}
}

func TestBuildPreservesHonestPostDeadlineFailureAsUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change, policies := validBindings(now)
	input := validInput(now, inventory, change, policies, false)
	receipt, err := Build(inventory, change, policies, input, strings.Repeat("9", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.PassedCount != 6 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestBuildRejectsEarlyStaleMisboundAndContradictoryDrills(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change, policies := validBindings(now)
	base := validInput(now, inventory, change, policies, true)
	tests := map[string]func(*Input){
		"early verification": func(input *Input) {
			input.Timeline.VerificationStartedAt = input.Timeline.DeletionCompletedAt.Add(29 * 24 * time.Hour)
		},
		"stale collection": func(input *Input) { input.GeneratedAt = now.Add(-25 * time.Hour) },
		"policy duration":  func(input *Input) { input.BackupRetentionSeconds-- },
		"policy version":   func(input *Input) { input.BackupPolicyVersion = "retention-shortened" },
		"retention digest": func(input *Input) { input.RetentionInventoryReceiptSHA256 = strings.Repeat("8", 64) },
		"platform binding": func(input *Input) { input.ChangeID = "different-change" },
		"duplicate check":  func(input *Input) { input.Checks[1] = input.Checks[0] },
		"readiness":        func(input *Input) { input.Ready = false },
		"future":           func(input *Input) { input.GeneratedAt = now.Add(time.Second) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Checks = append([]Check(nil), base.Checks...)
			mutate(&input)
			if _, err := Build(inventory, change, policies, input, strings.Repeat("9", 64), now); err == nil {
				t.Fatal("Build() accepted invalid drill")
			}
		})
	}
}

func TestPublishIsCreateOnlyMode0600(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change, policies := validBindings(now)
	receipt, err := Build(inventory, change, policies, validInput(now, inventory, change, policies, true), strings.Repeat("9", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, receipt); err == nil {
		t.Fatal("Publish() replaced an existing receipt")
	}
}

func TestCollectLoadsExactProductionChainRetentionReceiptAndDrillBytes(t *testing.T) {
	root := repositoryRoot(t)
	inventoryPath := filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.production.example.json")
	planPath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.production.example.json")
	changePath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.production.example.json")
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		t.Fatal(err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	policyValues := make([]retention.Policy, 0, len(retention.DataClasses))
	for _, dataClass := range retention.DataClasses {
		duration := 24 * time.Hour
		if dataClass == "backups" {
			duration = 30 * 24 * time.Hour
		}
		policyValues = append(policyValues, retention.Policy{DataClass: dataClass, Purpose: "operate service", Version: "retention-v1", Owner: "privacy", Trigger: "record_created", Duration: duration, DeletionMethod: "hard_delete", HoldBehavior: "scoped_hold", MigrationPlan: "forward-only", CustomerImpact: "access ends", EffectiveAt: change.GeneratedAt.Add(-time.Hour)})
	}
	policyReceipt, err := retentioninventory.Build(inventory, change, policyValues, change.GeneratedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	retentionPath := filepath.Join(t.TempDir(), "retention.json")
	if err := retentioninventory.Publish(retentionPath, policyReceipt); err != nil {
		t.Fatal(err)
	}
	policyReceipt, err = retentioninventory.Load(retentionPath)
	if err != nil {
		t.Fatal(err)
	}
	input := validInput(now, inventory, change, policyReceipt, true)
	drillBytes, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	drillPath := filepath.Join(t.TempDir(), "drill.json")
	if err := os.WriteFile(drillPath, drillBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := Collect(inventoryPath, planPath, changePath, retentionPath, drillPath, now)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if !receipt.Ready || receipt.InputSHA256 != digestBytes(drillBytes) || receipt.RetentionInventoryReceiptSHA256 != policyReceipt.ReceiptSHA256 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func validBindings(now time.Time) (platforminventory.Inventory, platformchange.Receipt, retentioninventory.Receipt) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Production, InventoryID: "inventory-production", ReceiptSHA256: strings.Repeat("a", 64)}
	count := 42
	generated := now.Add(-33 * 24 * time.Hour)
	collected, checked := generated.Add(-2*time.Minute), generated.Add(-time.Minute)
	change := platformchange.Receipt{
		Schema: platformchange.SchemaV1, Environment: inventory.Environment, ChangeID: "change-production",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256,
		ReceiptSHA256: strings.Repeat("b", 64), GeneratedAt: generated,
		Apply:             platformchange.Apply{Outcome: platformchange.ApplySucceeded, CompletedAt: generated.Add(-3 * time.Minute), RawOutputSHA256: strings.Repeat("c", 64)},
		Rollback:          platformchange.Rollback{Outcome: platformchange.RollbackNotRequired},
		ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected, CollectedAt: &collected, SHA256: strings.Repeat("d", 64), ResourceCount: &count},
		Drift:             platformchange.Drift{Outcome: platformchange.DriftClean, CheckedAt: &checked, RawOutputSHA256: strings.Repeat("e", 64)},
	}
	policyValues := make([]retention.Policy, 0, len(retention.DataClasses))
	for _, dataClass := range retention.DataClasses {
		duration := 24 * time.Hour
		if dataClass == "backups" {
			duration = 30 * 24 * time.Hour
		}
		policyValues = append(policyValues, retention.Policy{DataClass: dataClass, Purpose: "operate service", Version: "retention-v1", Owner: "privacy", Trigger: "record_created", Duration: duration, DeletionMethod: "hard_delete", HoldBehavior: "scoped_hold", MigrationPlan: "forward-only", CustomerImpact: "access ends", EffectiveAt: generated.Add(-time.Hour)})
	}
	policies, err := retentioninventory.Build(inventory, change, policyValues, now.Add(-32*24*time.Hour))
	if err != nil {
		panic(err)
	}
	policies.ReceiptSHA256 = strings.Repeat("f", 64)
	return inventory, change, policies
}

func validInput(now time.Time, inventory platforminventory.Inventory, change platformchange.Receipt, policies retentioninventory.Receipt, ready bool) Input {
	deletion := now.Add(-30*24*time.Hour - 3*time.Hour)
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	if !ready {
		checks[len(checks)-1].Outcome = OutcomeFailed
	}
	return Input{
		Schema: InputSchemaV1, Classification: "self_managed_external", Environment: "production",
		DrillID: "backup-expiry-20260810", BackupID: "backup-20260709",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256,
		ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256,
		RetentionInventoryReceiptSHA256: policies.ReceiptSHA256, PoliciesSHA256: policies.PoliciesSHA256,
		BackupPolicyVersion: "retention-v1", BackupRetentionSeconds: int64((30 * 24 * time.Hour) / time.Second),
		Ready: ready, GeneratedAt: now.Add(-time.Hour),
		Timeline: Timeline{BackupCreatedAt: now.Add(-31 * 24 * time.Hour), DeletionCompletedAt: deletion, VerificationStartedAt: now.Add(-2 * time.Hour), VerificationCompletedAt: now.Add(-90 * time.Minute)},
		Checks:   checks,
	}
}

func digest(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }

func digestBytes(value []byte) string { return fmt.Sprintf("%x", sha256.Sum256(value)) }

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}
