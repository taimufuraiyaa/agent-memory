package supportevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestCollectValidatesCompleteProductionFileChain(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventoryPath, planPath, changePath, inventory, plan, change := productionChain(t)
	releasePath := writeJSON(t, "release.json", productionReleaseMap())
	release, releaseDigest, err := platformrollback.LoadPassedReleaseForEnvironment(releasePath, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, input := validEvidence(now)
	input.InventoryID, input.InventoryReceiptSHA256 = inventory.InventoryID, inventory.ReceiptSHA256
	input.PlanID, input.PlanReceiptSHA256 = plan.PlanID, plan.ReceiptSHA256
	input.ChangeID, input.ChangeReceiptSHA256 = change.ChangeID, change.ReceiptSHA256
	input.ReleaseID, input.ReleaseReceiptSHA256 = release.ReleaseID, releaseDigest
	inputPath := writeJSON(t, "support.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) || len(receipt.DrillResults) != 2 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, linked, now); err == nil {
		t.Fatal("symlink accepted")
	}
	var unknown map[string]any
	encoded, _ := json.Marshal(input)
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["staff_names"] = []string{"secret"}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestDecodeRejectsValidateThenOpenPathReplacement(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	_, _, _, _, original := validEvidence(now)
	replacement := original
	replacement.ReviewID = "replacement-review"
	path := writeJSON(t, "support.json", original)
	replacementPath := writeJSON(t, "replacement.json", replacement)

	var decoded Input
	_, err := decodeStrictRegularWithHook(path, &decoded, func() {
		if renameErr := os.Rename(replacementPath, path); renameErr != nil {
			t.Fatalf("replace validated input: %v", renameErr)
		}
	})
	if err == nil {
		t.Fatalf("validate-then-open replacement was accepted: %+v", decoded)
	}
}

func TestBuildDerivesReadyCoverageAndDrillDurations(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inv, plan, change, release, input := validEvidence(now)
	receipt, err := build(inv, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || !receipt.CoverageComplete || receipt.PassedCount != 6 || receipt.TargetBreachCount != 0 || receipt.DrillResults[0].DeliverySeconds != 60 || receipt.DrillResults[0].AcknowledgementSeconds != 180 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildPreservesHonestCoverageAndTargetFailures(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inv, plan, change, release, input := validEvidence(now)
	input.PrimaryCoveredMinutes--
	input.Checks[2].Outcome = OutcomeFailed
	input.Drills[0].MaximumAcknowledgementSeconds = 100
	input.Drills[0].Outcome = OutcomeFailed
	input.Checks[3].Outcome = OutcomeFailed
	input.Checks[5].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inv, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.CoverageComplete || receipt.TargetBreachCount != 1 || receipt.FailedCount != 3 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteUnsafeStaleAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *Input){
		"coverage passed": func(_ *platformchange.Receipt, in *Input) { in.PrimaryCoveredMinutes--; in.Ready = false },
		"target breach passed": func(_ *platformchange.Receipt, in *Input) {
			in.Drills[0].MaximumAcknowledgementSeconds = 100
			in.Ready = false
		},
		"missing check":   func(_ *platformchange.Receipt, in *Input) { in.Checks = in.Checks[:5]; in.Ready = false },
		"duplicate drill": func(_ *platformchange.Receipt, in *Input) { in.Drills[1].ID = in.Drills[0].ID },
		"unsafe version":  func(_ *platformchange.Receipt, in *Input) { in.CoverageRosterVersion = "person@example.test" },
		"bad causal time": func(_ *platformchange.Receipt, in *Input) {
			in.Drills[0].AcknowledgedAt = in.Drills[0].EscalatedAt.Add(-time.Second)
		},
		"stale review":      func(_ *platformchange.Receipt, in *Input) { in.ReviewedAt = now.Add(-25 * time.Hour) },
		"future generation": func(_ *platformchange.Receipt, in *Input) { in.GeneratedAt = now.Add(time.Second) },
		"change binding":    func(change *platformchange.Receipt, _ *Input) { change.PlanReceiptSHA256 = digest("9") },
	} {
		t.Run(name, func(t *testing.T) {
			inv, plan, change, release, input := validEvidence(now)
			mutate(&change, &input)
			if _, err := build(inv, plan, change, release, digest("d"), input, digest("e"), now); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPublishCreatesPrivateReceiptOnceAndRejectsSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("replaced")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	_ = os.WriteFile(target, []byte("{}"), 0o600)
	linked := filepath.Join(t.TempDir(), "linked.json")
	_ = os.Symlink(target, linked)
	if err := Publish(linked, Receipt{}); err == nil {
		t.Fatal("symlink accepted")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inv := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Production, InventoryID: "production-inventory", ReceiptSHA256: digest("a")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Production, PlanID: "production-plan", InventoryID: inv.InventoryID, InventoryReceiptSHA256: inv.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Production, ChangeID: "production-change", InventoryID: inv.InventoryID, InventoryReceiptSHA256: inv.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-12 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "production", Namespace: "agent-memory-production", ReleaseID: "production-release", StartedAt: now.Add(-11 * time.Hour), CompletedAt: now.Add(-10 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	checks := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
	}
	drills := make([]Drill, 0, 2)
	for i, id := range requiredDrills {
		s := now.Add(time.Duration(-6+i) * time.Hour)
		drills = append(drills, Drill{ID: id, OwnerSlotVersion: fmt.Sprintf("slot-v%d", i+1), SubmittedAt: s, DeliveredAt: s.Add(time.Minute), EscalatedAt: s.Add(2 * time.Minute), AcknowledgedAt: s.Add(3 * time.Minute), ResolvedAt: s.Add(5 * time.Minute), MaximumDeliverySeconds: 120, MaximumAcknowledgementSeconds: 300, Outcome: OutcomePassed, EvidenceSHA256: digest(fmt.Sprint(i + 2))})
	}
	input := Input{Schema: InputSchemaV1, Classification: "production_external", Environment: "production", ReviewID: "support-review-1", ChannelInventoryVersion: "channels-v1", CoverageRosterVersion: "coverage-v1", ResponsePolicyVersion: "policy-v1", TargetVersion: "targets-v1", InventoryID: inv.InventoryID, InventoryReceiptSHA256: inv.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("d"), ChannelInventorySHA256: digest("4"), CoverageRosterSHA256: digest("5"), ResponsePolicySHA256: digest("6"), TargetDecisionSHA256: digest("7"), EscalationTestReportSHA256: digest("8"), TargetApprovedAt: now.Add(-9 * time.Hour), PeriodStart: now.Add(-8 * time.Hour), PeriodEnd: now.Add(-2 * time.Hour), ReviewedAt: now.Add(-time.Hour), GeneratedAt: now.Add(-30 * time.Minute), RequiredCoverageMinutes: 360, PrimaryCoveredMinutes: 360, BackupCoveredMinutes: 360, PrimarySlotCount: 2, BackupSlotCount: 2, Ready: true, Drills: drills, Checks: checks}
	return inv, plan, change, release, input
}
func digest(seed string) string { sum := sha256.Sum256([]byte(seed)); return fmt.Sprintf("%x", sum) }

func productionChain(t *testing.T) (string, string, string, platforminventory.Inventory, platformplan.Plan, platformchange.Receipt) {
	t.Helper()
	root := repositoryRoot(t)
	var invMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-platform-inventory.production.example.json"), &invMap)
	invMap["inventory_id"] = "production-inventory"
	inventoryPath := writeJSON(t, "inventory.json", invMap)
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	var planMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-infrastructure-plan.production.example.json"), &planMap)
	planMap["inventory_id"], planMap["inventory_receipt_sha256"], planMap["plan_id"] = inventory.InventoryID, inventory.ReceiptSHA256, "production-plan"
	planPath := writeJSON(t, "plan.json", planMap)
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		t.Fatal(err)
	}
	var changeMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-infrastructure-change.production.example.json"), &changeMap)
	changeMap["inventory_id"], changeMap["inventory_receipt_sha256"], changeMap["plan_id"], changeMap["plan_receipt_sha256"], changeMap["change_id"] = inventory.InventoryID, inventory.ReceiptSHA256, plan.PlanID, plan.ReceiptSHA256, "production-change"
	changePath := writeJSON(t, "change.json", changeMap)
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	return inventoryPath, planPath, changePath, inventory, plan, change
}
func productionReleaseMap() map[string]any {
	image := func(name string) string {
		return "registry.example/agent-memory-" + name + "@sha256:" + digest("image")
	}
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "production", "namespace": "agent-memory-production", "kubernetes_context": "production-context", "release_id": "production-release", "started_at": "2026-08-10T04:30:00Z", "completed_at": "2026-08-10T05:00:00Z", "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
}
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
func readJSON(t *testing.T, path string, dest any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, dest); err != nil {
		t.Fatal(err)
	}
}
func writeJSON(t *testing.T, name string, value any) string {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
func digestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}
