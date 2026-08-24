package blockerevidence

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
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestCollectValidatesCompleteOpenedFileChain(t *testing.T) {
	root := repositoryRoot(t)
	inventoryPath := filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json")
	planPath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json")
	changePath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.example.json")
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
	releasePath := writeJSON(t, "release.json", releaseMap())
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inputPath := writeJSON(t, "blockers.json", validInput(now, inventory, plan, change, release, releaseDigest))
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.OpenItemCount != 5 || receipt.ReviewedOpenItemCount != 5 || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.PassedCount != 5 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, linked, now); err == nil {
		t.Fatal("symlink accepted")
	}
	unknown := map[string]any{}
	encoded, _ := json.Marshal(validInput(now, inventory, plan, change, release, releaseDigest))
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["incident_ids"] = []string{"secret"}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestBuildPreservesHonestBlockersAndIncompleteReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.SeverityOneIncidentCount = 1
	input.UnresolvedLaunchBlockerCount = 2
	input.ReviewedOpenItemCount = 4
	input.Checks[2].Outcome = OutcomeFailed
	input.Checks[3].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.BlockerCount != 3 || receipt.ReviewCoverageComplete || receipt.FailedCount != 2 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteStaleUnsafeAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *Input){
		"contradictory readiness":       func(_ *platformchange.Receipt, in *Input) { in.SeverityOneIncidentCount = 1 },
		"partial review declared ready": func(_ *platformchange.Receipt, in *Input) { in.ReviewedOpenItemCount-- },
		"missing check":                 func(_ *platformchange.Receipt, in *Input) { in.Checks = in.Checks[:4]; in.Ready = false },
		"duplicate check":               func(_ *platformchange.Receipt, in *Input) { in.Checks[1].ID = in.Checks[0].ID },
		"unknown outcome":               func(_ *platformchange.Receipt, in *Input) { in.Checks[0].Outcome = "unknown"; in.Ready = false },
		"unsafe version":                func(_ *platformchange.Receipt, in *Input) { in.RegisterVersion = "register/person@example.test" },
		"review before snapshot":        func(_ *platformchange.Receipt, in *Input) { in.ReviewedAt = in.SnapshotAt.Add(-time.Second) },
		"pre-release snapshot":          func(_ *platformchange.Receipt, in *Input) { in.SnapshotAt = now.Add(-20 * time.Hour) },
		"stale":                         func(_ *platformchange.Receipt, in *Input) { in.GeneratedAt = now.Add(-25 * time.Hour) },
		"stale snapshot repackaged now": func(_ *platformchange.Receipt, in *Input) {
			in.SnapshotAt = now.Add(-25 * time.Hour)
			in.ReviewedAt = now.Add(-time.Hour)
		},
		"count overflow":      func(_ *platformchange.Receipt, in *Input) { in.OpenFindingCount = maximumCount + 1; in.Ready = false },
		"review exceeds open": func(_ *platformchange.Receipt, in *Input) { in.ReviewedOpenItemCount++; in.Ready = false },
		"change binding":      func(change *platformchange.Receipt, _ *Input) { change.PlanReceiptSHA256 = digest("9") },
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, input := validEvidence(now)
			mutate(&change, &input)
			if _, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now); err == nil {
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
		t.Fatal("existing destination replaced")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	if err := Publish(linked, Receipt{}); err == nil {
		t.Fatal("symlink destination replaced")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "staging-inventory", ReceiptSHA256: digest("a")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "staging-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "staging-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-12 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", ReleaseID: "staging-release", StartedAt: now.Add(-11 * time.Hour), CompletedAt: now.Add(-10 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	return inventory, plan, change, release, validInput(now, inventory, plan, change, release, digest("d"))
}

func validInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) Input {
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", ReviewID: "blocker-review-1", RegisterVersion: "register-v1", ClassificationPolicyVersion: "classification-v1", ReviewVersion: "review-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, FindingExportSHA256: digest("2"), IncidentExportSHA256: digest("3"), ClassificationPolicySHA256: digest("4"), ReviewDecisionSHA256: digest("5"), SnapshotAt: now.Add(-6 * time.Hour), ReviewedAt: now.Add(-5 * time.Hour), GeneratedAt: now.Add(-30 * time.Minute), OpenFindingCount: 3, OpenIncidentCount: 2, ReviewedOpenItemCount: 5, Ready: true, Checks: checks}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
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
func digest(character string) string { return strings.Repeat(character, 64) }
func releaseMap() map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest("a") }
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": "staging-release", "started_at": "2026-08-10T05:00:00Z", "completed_at": "2026-08-10T06:00:00Z", "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
}
