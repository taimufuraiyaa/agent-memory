package betasloevidence

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
	now := time.Date(2026, 8, 13, 18, 0, 0, 0, time.UTC)
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
	inputPath := writeJSON(t, "beta-slo.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) || len(receipt.MetricResults) != 6 {
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
	unknown["promql"] = "unsafe"
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestBuildDerivesFixedTargetsCoverageAndElapsedWindow(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("release"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || !receipt.CoverageComplete || receipt.MetricBreachCount != 0 || receipt.ObservationDurationSeconds != 86_400 || receipt.PassedCount != 6 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	wantTargets := map[MetricID]int64{
		MetricAPIAvailability:        999_000,
		MetricSearchP95:              800_000,
		MetricMemoryWriteP95:         300_000,
		MetricStatusMetadataP95:      300_000,
		MetricUploadAcceptanceP95:    2_000_000,
		MetricNativeIndexingWithin60: 950_000,
	}
	for _, result := range receipt.MetricResults {
		if result.TargetValue != wantTargets[result.ID] || !result.Passed || !result.CoverageComplete {
			t.Fatalf("unexpected metric result: %+v", result)
		}
	}
}

func TestBuildPreservesHonestCoverageAndTargetFailures(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Metrics[0].ObservedSampleCount--
	input.Metrics[1].ObservedValue = 800_001
	input.Checks[3].Outcome = OutcomeFailed
	input.Checks[4].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("release"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.CoverageComplete || receipt.CoverageShortfallCount != 1 || receipt.MetricBreachCount != 1 || receipt.FailedCount != 2 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteUnsafeStaleAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *Input){
		"coverage shortfall passed": func(_ *platformchange.Receipt, input *Input) {
			input.Metrics[0].ObservedSampleCount--
			input.Ready = false
		},
		"target breach passed": func(_ *platformchange.Receipt, input *Input) {
			input.Metrics[1].ObservedValue = 800_001
			input.Ready = false
		},
		"duplicate metric": func(_ *platformchange.Receipt, input *Input) { input.Metrics[1].ID = input.Metrics[0].ID },
		"missing check":    func(_ *platformchange.Receipt, input *Input) { input.Checks = input.Checks[:5]; input.Ready = false },
		"unsafe version":   func(_ *platformchange.Receipt, input *Input) { input.MetricSourceVersion = "person@example.test" },
		"short window": func(_ *platformchange.Receipt, input *Input) {
			input.WindowEnd = input.WindowStart.Add(23 * time.Hour)
			input.EvaluatedAt = input.WindowEnd
		},
		"pre-release window": func(_ *platformchange.Receipt, input *Input) { input.WindowStart = now.Add(-30 * time.Hour) },
		"late evaluation": func(_ *platformchange.Receipt, input *Input) {
			input.EvaluatedAt = input.WindowEnd.Add(25 * time.Hour)
			input.GeneratedAt = input.EvaluatedAt
		},
		"stale generation":  func(_ *platformchange.Receipt, input *Input) { input.GeneratedAt = now.Add(-25 * time.Hour) },
		"future generation": func(_ *platformchange.Receipt, input *Input) { input.GeneratedAt = now.Add(time.Second) },
		"change binding":    func(change *platformchange.Receipt, _ *Input) { change.PlanReceiptSHA256 = digest("wrong") },
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
		t.Fatal("receipt replaced")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	_ = os.WriteFile(target, []byte("{}"), 0o600)
	linked := filepath.Join(t.TempDir(), "linked.json")
	_ = os.Symlink(target, linked)
	if err := Publish(linked, Receipt{}); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestLoadReadyReloadsExactReceiptAndRejectsUnreadyOrTamperedReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("release"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "ready.json", receipt)
	loaded, receiptDigest, err := LoadReady(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Ready || receiptDigest != digestFile(t, path) || loaded.ObservationDurationSeconds != 86_400 {
		t.Fatalf("unexpected loaded receipt: %+v digest=%s", loaded, receiptDigest)
	}
	for name, mutate := range map[string]func(*Receipt){
		"unready":          func(value *Receipt) { value.Ready = false },
		"target changed":   func(value *Receipt) { value.MetricResults[0].TargetValue-- },
		"coverage changed": func(value *Receipt) { value.CoverageComplete = false },
		"window changed":   func(value *Receipt) { value.ObservationDurationSeconds-- },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := receipt
			candidate.Metrics = append([]MetricObservation(nil), receipt.Metrics...)
			candidate.MetricResults = append([]MetricResult(nil), receipt.MetricResults...)
			mutate(&candidate)
			if _, _, err := LoadReady(writeJSON(t, name+".json", candidate)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Production, InventoryID: "production-inventory", ReceiptSHA256: digest("inventory")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Production, PlanID: "production-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("plan")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Production, ChangeID: "production-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-72 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("change")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "production", Namespace: "agent-memory-production", ReleaseID: "production-release", StartedAt: now.Add(-60 * time.Hour), CompletedAt: now.Add(-58 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	values := map[MetricID]int64{MetricAPIAvailability: 999_500, MetricSearchP95: 700_000, MetricMemoryWriteP95: 250_000, MetricStatusMetadataP95: 250_000, MetricUploadAcceptanceP95: 1_500_000, MetricNativeIndexingWithin60: 960_000}
	metrics := make([]MetricObservation, 0, len(RequiredMetrics()))
	for _, id := range RequiredMetrics() {
		metrics = append(metrics, MetricObservation{ID: id, ObservedValue: values[id], ExpectedSampleCount: 288, ObservedSampleCount: 288, EvidenceSHA256: digest(string(id))})
	}
	input := Input{Schema: InputSchemaV1, Classification: "production_external", Environment: "production", ObservationID: "beta-slo-2026-08", MetricSourceVersion: "prometheus-v1", QueryManifestVersion: "queries-v1", SLODefinitionVersion: "slo-v1", WindowDecisionVersion: "window-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), MetricExportSHA256: digest("metric-export"), QueryManifestSHA256: digest("query-manifest"), WindowDecisionSHA256: digest("window-decision"), SLODefinitionDecisionSHA256: digest("slo-decision"), ProductOperationsReviewSHA256: digest("review"), WindowApprovedAt: now.Add(-57 * time.Hour), WindowStart: now.Add(-48 * time.Hour), WindowEnd: now.Add(-24 * time.Hour), EvaluatedAt: now.Add(-23 * time.Hour), GeneratedAt: now.Add(-time.Hour), MinimumWindowSeconds: 86_400, Ready: true, Metrics: metrics, Checks: checks}
	return inventory, plan, change, release, input
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
	changeMap["inventory_id"], changeMap["inventory_receipt_sha256"] = inventory.InventoryID, inventory.ReceiptSHA256
	changeMap["plan_id"], changeMap["plan_receipt_sha256"], changeMap["change_id"] = plan.PlanID, plan.ReceiptSHA256, "production-change"
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
		"images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"},
		"deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false},
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
