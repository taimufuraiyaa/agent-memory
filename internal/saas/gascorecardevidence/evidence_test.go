package gascorecardevidence

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
)

func TestEvaluateMetricsCoversEveryGADomainAndDerivesTargets(t *testing.T) {
	metrics := passingMetrics()
	results, shortfalls, breaches, retentionPassed, err := evaluateMetrics(metrics, 4_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 13 || shortfalls != 0 || breaches != 0 || !retentionPassed {
		t.Fatalf("results=%d shortfalls=%d breaches=%d retention=%v", len(results), shortfalls, breaches, retentionPassed)
	}
	if results[0].ID != MetricAPIAvailability || results[len(results)-1].ID != MetricRetentionCompliance {
		t.Fatalf("metrics are not canonical: %+v", results)
	}

	for index := range metrics {
		if metrics[index].ID == MetricRetentionCompliance {
			metrics[index].ObservedValue = 999_999
		}
	}
	_, _, breaches, retentionPassed, err = evaluateMetrics(metrics, 4_000_000)
	if err != nil || breaches != 1 || retentionPassed {
		t.Fatalf("retention breach was not derived: breaches=%d retention=%v err=%v", breaches, retentionPassed, err)
	}
}

func TestEvaluateMetricsRejectsMissingDuplicateAndUnsafeEvidence(t *testing.T) {
	metrics := passingMetrics()
	for name, mutate := range map[string]func([]MetricObservation) []MetricObservation{
		"missing":   func(values []MetricObservation) []MetricObservation { return values[:len(values)-1] },
		"duplicate": func(values []MetricObservation) []MetricObservation { values[1].ID = values[0].ID; return values },
		"unsafe digest": func(values []MetricObservation) []MetricObservation {
			values[0].EvidenceSHA256 = "unsafe"
			return values
		},
		"partial coverage": func(values []MetricObservation) []MetricObservation { values[0].ObservedSampleCount = 9; return values },
	} {
		t.Run(name, func(t *testing.T) {
			values := append([]MetricObservation(nil), metrics...)
			values = mutate(values)
			_, shortfalls, _, _, err := evaluateMetrics(values, 4_000_000)
			if name == "partial coverage" {
				if err != nil || shortfalls != 1 {
					t.Fatalf("expected valid shortfall, got %d %v", shortfalls, err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid metric set accepted")
			}
		})
	}
}

func passingMetrics() []MetricObservation {
	digest := strings.Repeat("a", 64)
	values := map[MetricID]int64{
		MetricAPIAvailability: 999_500, MetricSearchP95: 700_000,
		MetricMemoryWriteP95: 250_000, MetricCriticalFindings: 0,
		MetricTenantIsolation: 1, MetricDeletionCompliance: 1_000_000,
		MetricAuditIntegrity: 1_000_000, MetricBillingReconciliation: 1_000_000,
		MetricRestoreRPO: 4, MetricRestoreRTO: 50,
		MetricCostPerActiveTenant: 3_000_000, MetricSupportResponse: 1_000_000,
		MetricRetentionCompliance: 1_000_000,
	}
	metrics := make([]MetricObservation, 0, len(requiredMetrics))
	for _, id := range requiredMetrics {
		metrics = append(metrics, MetricObservation{ID: id, ObservedValue: values[id], ExpectedSampleCount: 10, ObservedSampleCount: 10, EvidenceSHA256: digest})
	}
	return metrics
}

func TestCollectValidatesProductionChainWindowAndContentFreeInput(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	inventoryPath, planPath, changePath, releasePath, inventory, plan, change := productionFiles(t)
	releaseDigest := fileDigest(t, releasePath)
	digest := strings.Repeat("a", 64)
	checks := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest})
	}
	input := Input{
		Schema: InputSchemaV1, Classification: "production_external", Environment: "production",
		ScorecardID: "ga-scorecard-2026", MetricSourceVersion: "metrics-v1", QueryManifestVersion: "queries-v1", TargetVersion: "targets-v1", WindowDecisionVersion: "window-v1",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256,
		PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256,
		ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256,
		ReleaseID: "production-release", ReleaseReceiptSHA256: releaseDigest,
		ScorecardExportSHA256: digest, QueryManifestSHA256: digest, WindowDecisionSHA256: digest, TargetDecisionSHA256: digest, ProductDomainReviewSHA256: digest,
		WindowApprovedAt: time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
		WindowStart:      time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), WindowEnd: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC),
		EvaluatedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), GeneratedAt: now.Add(-time.Hour),
		ApprovedCostPerActiveTenantMicroUSD: 4_000_000, Ready: true, Metrics: passingMetrics(), Checks: checks,
	}
	inputPath := writeJSON(t, "ga-input.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || !receipt.CoverageComplete || !receipt.RetentionPassed || receipt.MetricBreachCount != 0 || receipt.ObservationDurationSeconds != int64(90*24*time.Hour/time.Second) {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(receiptPath, receipt); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(receiptPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode=%v err=%v", info, err)
	}
	loaded, _, err := LoadReady(receiptPath)
	if err != nil || loaded.ScorecardID != input.ScorecardID {
		t.Fatalf("load ready receipt: id=%q err=%v", loaded.ScorecardID, err)
	}
	if err := Publish(receiptPath, receipt); err == nil {
		t.Fatal("receipt overwrite accepted")
	}

	breached := input
	breached.Ready = false
	breached.Metrics = append([]MetricObservation(nil), input.Metrics...)
	for index := range breached.Metrics {
		if breached.Metrics[index].ID == MetricRetentionCompliance {
			breached.Metrics[index].ObservedValue = 999_999
		}
	}
	breached.Checks = append([]Check(nil), input.Checks...)
	for index := range breached.Checks {
		if breached.Checks[index].ID == CheckTargetsMet || breached.Checks[index].ID == CheckRetention {
			breached.Checks[index].Outcome = OutcomeFailed
		}
	}
	unready, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, "breached.json", breached), now)
	if err != nil || unready.Ready || unready.RetentionPassed || unready.MetricBreachCount != 1 {
		t.Fatalf("valid unready receipt not derived: %+v err=%v", unready, err)
	}

	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, linked, now); err == nil {
		t.Fatal("symlink input accepted")
	}
	var unknown map[string]any
	contents, _ := json.Marshal(input)
	if err := json.Unmarshal(contents, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["tenant_id"] = "unsafe"
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("content-bearing unknown field accepted")
	}
}

func productionFiles(t *testing.T) (string, string, string, string, platforminventory.Inventory, platformplan.Plan, platformchange.Receipt) {
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
	imageDigest := strings.Repeat("a", 64)
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + imageDigest }
	releasePath := writeJSON(t, "release.json", map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "production", "namespace": "agent-memory-production", "kubernetes_context": "production-context", "release_id": "production-release", "started_at": "2026-08-10T04:30:00Z", "completed_at": "2026-08-10T05:00:00Z", "outcome": "passed",
		"images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"},
		"deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false},
	})
	return inventoryPath, planPath, changePath, releasePath, inventory, plan, change
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

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}
