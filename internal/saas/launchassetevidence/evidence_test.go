package launchassetevidence

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
	inputPath := writeJSON(t, "launch-assets.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.LiveAssetCount != 7 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, linked, now); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestBuildDerivesSevenLiveOwnedMonitoredAssets(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("release"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.AssetCount != 7 || receipt.LiveAssetCount != 7 || receipt.StaleAssetCount != 0 || receipt.PassedCount != 9 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildPreservesHonestProbeFailure(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Assets[0].SuccessfulProbeCount--
	setOutcome(input.Checks, CheckLiveProbeCoverage, OutcomeFailed)
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("release"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.LiveAssetCount != 6 || receipt.FailedCount != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteUnsafeAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *Input){
		"probe failure passed": func(_ *platformchange.Receipt, input *Input) { input.Assets[0].HTTPStatus = 503; input.Ready = false },
		"duplicate asset":      func(_ *platformchange.Receipt, input *Input) { input.Assets[1].ID = input.Assets[0].ID },
		"wrong owner":          func(_ *platformchange.Receipt, input *Input) { input.Assets[0].OwnerGroup = "security" },
		"unsafe version":       func(_ *platformchange.Receipt, input *Input) { input.ManifestVersion = "person@example.test" },
		"future observation": func(_ *platformchange.Receipt, input *Input) {
			input.Assets[0].ObservedAt = input.SnapshotAt.Add(time.Second)
		},
		"stale passed": func(_ *platformchange.Receipt, input *Input) {
			input.Assets[0].ObservedAt = input.SnapshotAt.Add(-901 * time.Second)
			input.Ready = false
		},
		"out of range status": func(_ *platformchange.Receipt, input *Input) {
			input.Assets[0].HTTPStatus = 700
			setOutcome(input.Checks, CheckLiveProbeCoverage, OutcomeFailed)
			input.Ready = false
		},
		"change binding": func(change *platformchange.Receipt, _ *Input) { change.PlanReceiptSHA256 = digest("wrong") },
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, input := validEvidence(now)
			mutate(&change, &input)
			if _, err := build(inventory, plan, change, release, digest("release"), input, digest("input"), now); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("receipt replaced")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Production, InventoryID: "production-inventory", ReceiptSHA256: digest("inventory")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Production, PlanID: "production-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("plan")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Production, ChangeID: "production-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-24 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("change")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "production", Namespace: "agent-memory-production", ReleaseID: "production-release", CompletedAt: now.Add(-time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	assets := make([]Asset, 0, len(RequiredAssets()))
	for _, id := range RequiredAssets() {
		assets = append(assets, Asset{ID: id, OwnerGroup: OwnerFor(id), PublicURLSHA256: digest(string(id) + "-url"), RenderedCopySHA256: digest(string(id) + "-copy"), MonitoringConfigSHA256: digest(string(id) + "-monitor"), RouteTestSHA256: digest(string(id) + "-route"), OwnerDecisionSHA256: digest(string(id) + "-owner"), ObservedAt: now.Add(-5 * time.Minute), HTTPStatus: 200, ProbeCount: 3, SuccessfulProbeCount: 3})
	}
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	input := Input{Schema: InputSchemaV1, Classification: "production_external", Environment: "production", ReviewID: "launch-assets-2026-08", ManifestVersion: "manifest-v1", ProbeVersion: "probe-v1", CopyReviewVersion: "copy-v1", MonitoringReviewVersion: "monitor-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), ManifestSHA256: digest("manifest"), AccountableReviewSHA256: digest("review"), SnapshotAt: now.Add(-4 * time.Minute), ReviewedAt: now.Add(-2 * time.Minute), GeneratedAt: now.Add(-time.Minute), Ready: true, Assets: assets, Checks: checks}
	return inventory, plan, change, release, input
}

func setOutcome(checks []Check, id CheckID, outcome Outcome) {
	for index := range checks {
		if checks[index].ID == id {
			checks[index].Outcome = outcome
		}
	}
}
func digest(seed string) string {
	value := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%x", value)
}

func productionChain(t *testing.T) (string, string, string, platforminventory.Inventory, platformplan.Plan, platformchange.Receipt) {
	t.Helper()
	root := repositoryRoot(t)
	var inventoryMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-platform-inventory.production.example.json"), &inventoryMap)
	inventoryMap["inventory_id"] = "production-inventory"
	inventoryPath := writeJSON(t, "inventory.json", inventoryMap)
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
	return map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "production", "namespace": "agent-memory-production", "kubernetes_context": "production-context", "release_id": "production-release", "started_at": "2026-08-10T04:30:00Z", "completed_at": "2026-08-10T05:00:00Z", "outcome": "passed",
		"images":    map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")},
		"migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"},
		"deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}},
		"rollback":    map[string]any{"attempted": false, "succeeded": false},
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func readJSON(t *testing.T, path string, destination any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, destination); err != nil {
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
