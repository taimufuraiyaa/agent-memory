package betaintegrityevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaoperationsevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestCollectValidatesCompleteProductionChainAndBothPrerequisiteReceipts(t *testing.T) {
	now := time.Date(2026, 8, 13, 18, 0, 0, 0, time.UTC)
	inventoryPath, planPath, changePath, inventory, plan, change := productionChain(t)
	releasePath := writeJSON(t, "release.json", productionReleaseMap())
	release, releaseDigest, err := platformrollback.LoadPassedReleaseForEnvironment(releasePath, "production")
	if err != nil {
		t.Fatal(err)
	}
	sloInput := productionSLOInput(now, inventory, plan, change, release, releaseDigest)
	sloInputPath := writeJSON(t, "slo-input.json", sloInput)
	sloReceipt, err := betasloevidence.Collect(inventoryPath, planPath, changePath, releasePath, sloInputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	sloReceiptPath := filepath.Join(t.TempDir(), "slo-receipt.json")
	if err := betasloevidence.Publish(sloReceiptPath, sloReceipt); err != nil {
		t.Fatal(err)
	}
	sloDigest := digestFile(t, sloReceiptPath)
	operationsInput := productionOperationsInput(now, inventory, plan, change, release, releaseDigest, sloReceipt, sloDigest)
	operationsInputPath := writeJSON(t, "operations-input.json", operationsInput)
	operationsReceipt, err := betaoperationsevidence.Collect(inventoryPath, planPath, changePath, releasePath, sloReceiptPath, operationsInputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	operationsReceiptPath := filepath.Join(t.TempDir(), "operations-receipt.json")
	if err := betaoperationsevidence.Publish(operationsReceiptPath, operationsReceipt); err != nil {
		t.Fatal(err)
	}
	operationsDigest := digestFile(t, operationsReceiptPath)
	_, _, _, _, _, _, input := validEvidence(now)
	input.InventoryID, input.InventoryReceiptSHA256 = inventory.InventoryID, inventory.ReceiptSHA256
	input.PlanID, input.PlanReceiptSHA256 = plan.PlanID, plan.ReceiptSHA256
	input.ChangeID, input.ChangeReceiptSHA256 = change.ChangeID, change.ReceiptSHA256
	input.ReleaseID, input.ReleaseReceiptSHA256 = release.ReleaseID, releaseDigest
	input.BetaSLOObservationID, input.BetaSLOReceiptSHA256 = sloReceipt.ObservationID, sloDigest
	input.BetaOperationsAssessmentID, input.BetaOperationsReceiptSHA256 = operationsReceipt.AssessmentID, operationsDigest
	input.WindowStart, input.WindowEnd = sloReceipt.WindowStart, sloReceipt.WindowEnd
	inputPath := writeJSON(t, "integrity-input.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, sloReceiptPath, operationsReceiptPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.BetaOperationsReceiptSHA256 != operationsDigest {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, sloReceiptPath, operationsReceiptPath, linked, now); err == nil {
		t.Fatal("symlink accepted")
	}
	var unknown map[string]any
	encoded, _ := json.Marshal(input)
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["event_id"] = "unsafe"
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, sloReceiptPath, operationsReceiptPath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func productionSLOInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) betasloevidence.Input {
	checks := make([]betasloevidence.Check, 0, len(betasloevidence.RequiredChecks()))
	for _, id := range betasloevidence.RequiredChecks() {
		checks = append(checks, betasloevidence.Check{ID: id, Outcome: betasloevidence.OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	values := map[betasloevidence.MetricID]int64{betasloevidence.MetricAPIAvailability: 999_500, betasloevidence.MetricSearchP95: 700_000, betasloevidence.MetricMemoryWriteP95: 250_000, betasloevidence.MetricStatusMetadataP95: 250_000, betasloevidence.MetricUploadAcceptanceP95: 1_500_000, betasloevidence.MetricNativeIndexingWithin60: 960_000}
	metrics := make([]betasloevidence.MetricObservation, 0, len(values))
	for _, id := range betasloevidence.RequiredMetrics() {
		metrics = append(metrics, betasloevidence.MetricObservation{ID: id, ObservedValue: values[id], ExpectedSampleCount: 288, ObservedSampleCount: 288, EvidenceSHA256: digest(string(id))})
	}
	return betasloevidence.Input{Schema: betasloevidence.InputSchemaV1, Classification: "production_external", Environment: "production", ObservationID: "beta-slo-2026-08", MetricSourceVersion: "prometheus-v1", QueryManifestVersion: "queries-v1", SLODefinitionVersion: "slo-v1", WindowDecisionVersion: "window-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, MetricExportSHA256: digest("metric-export"), QueryManifestSHA256: digest("query-manifest"), WindowDecisionSHA256: digest("window-decision"), SLODefinitionDecisionSHA256: digest("slo-decision"), ProductOperationsReviewSHA256: digest("slo-review"), WindowApprovedAt: now.Add(-57 * time.Hour), WindowStart: now.Add(-48 * time.Hour), WindowEnd: now.Add(-24 * time.Hour), EvaluatedAt: now.Add(-23 * time.Hour), GeneratedAt: now.Add(-time.Hour), MinimumWindowSeconds: 86_400, Ready: true, Metrics: metrics, Checks: checks}
}

func productionOperationsInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, slo betasloevidence.Receipt, sloDigest string) betaoperationsevidence.Input {
	domains := []betaoperationsevidence.DomainAggregate{
		{ID: betaoperationsevidence.DomainDeletion, DueCaseCount: 5, WithinTargetCount: 5, RequiredSampleCount: 2, SampledCaseCount: 2, MatchedSampleCount: 2, MaximumTargetSeconds: 3_600, MaximumObservedDurationSeconds: 1_800, EvidenceSHA256: digest("deletion")},
		{ID: betaoperationsevidence.DomainRightsNotice, DueCaseCount: 3, WithinTargetCount: 3, RequiredSampleCount: 2, SampledCaseCount: 2, MatchedSampleCount: 2, MaximumTargetSeconds: 172_800, MaximumObservedDurationSeconds: 7_200, EvidenceSHA256: digest("notice")},
		{ID: betaoperationsevidence.DomainAnomalyAlert, MaximumTargetSeconds: 3_600, EvidenceSHA256: digest("anomaly")},
		{ID: betaoperationsevidence.DomainSupportCase, DueCaseCount: 6, WithinTargetCount: 6, RequiredSampleCount: 2, SampledCaseCount: 2, MatchedSampleCount: 2, MaximumTargetSeconds: 86_400, MaximumObservedDurationSeconds: 3_600, EvidenceSHA256: digest("support")},
	}
	checks := make([]betaoperationsevidence.Check, 0, len(betaoperationsevidence.RequiredChecks()))
	for _, id := range betaoperationsevidence.RequiredChecks() {
		checks = append(checks, betaoperationsevidence.Check{ID: id, Outcome: betaoperationsevidence.OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	return betaoperationsevidence.Input{Schema: betaoperationsevidence.InputSchemaV1, Classification: "production_external", Environment: "production", AssessmentID: "beta-operations-2026-08", AggregatePolicyVersion: "aggregates-v1", SamplePolicyVersion: "samples-v1", TargetVersion: "targets-v1", SupportExportVersion: "support-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, BetaSLOObservationID: slo.ObservationID, BetaSLOReceiptSHA256: sloDigest, WindowStart: slo.WindowStart, WindowEnd: slo.WindowEnd, DeletionReceiptExportSHA256: digest("deletion-export"), NoticeCaseExportSHA256: digest("notice-export"), AnomalyCaseExportSHA256: digest("anomaly-export"), SupportCaseExportSHA256: digest("support-export"), SampleManifestSHA256: digest("samples"), TargetDecisionSHA256: digest("targets"), PrivacySecuritySupportReviewSHA256: digest("operations-review"), TargetApprovedAt: now.Add(-49 * time.Hour), AggregatedAt: now.Add(-23 * time.Hour), ReviewedAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-time.Hour), Ready: true, Domains: domains, Checks: checks}
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

func writeJSON(t *testing.T, name string, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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

func digestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := sha256.Sum256(contents)
	return fmt.Sprintf("%x", value)
}
