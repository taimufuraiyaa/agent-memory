package capacityevidence

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
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrievalload"
)

func TestCollectValidatesCompletePlatformReleaseAndLoadChain(t *testing.T) {
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
	loadInputPath := writeJSON(t, "load-input.json", validLoadInput(now, inventory, plan, change, release, releaseDigest))
	loadReceipt, err := retrievalload.Collect(inventoryPath, planPath, changePath, releasePath, loadInputPath, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	loadPath := filepath.Join(t.TempDir(), "load-receipt.json")
	if err := retrievalload.Publish(loadPath, loadReceipt); err != nil {
		t.Fatal(err)
	}
	inputPath := writeJSON(t, "capacity.json", validInput(now, inventory, plan, change, release, releaseDigest, loadReceipt, digestFile(t, loadPath)))
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, loadPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.CheckCount != 8 || receipt.PassedCount != 8 || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.RetrievalLoadReceiptSHA256 != digestFile(t, loadPath) {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	assertReceiptSchemaSurface(t, root, receipt)
}

func TestBuildDerivesWorstCaseCostAndCanonicalizesChecks(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, load, input := validEvidence(now)
	input.Checks[0], input.Checks[7] = input.Checks[7], input.Checks[0]
	receipt, err := build(inventory, plan, change, release, digest("d"), load, digest("e"), input, digest("f"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Checks[0].ID != RequiredChecks()[0] || receipt.EstimatedWorstCaseMonthlyCostMicroUSD != 600000000 || receipt.BetaAccountCap != 100 || receipt.SupportedConcurrentTenants != 40 || receipt.SustainedRetrievalRequestsPerMinute != 12000 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildPreservesHonestCapacityAndCostShortfalls(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, load, input := validEvidence(now)
	input.SupportedConcurrentTenants = 20
	input.SustainedRetrievalRequestsPerMinute = 8000
	input.ApprovedMonthlyCostCeilingMicroUSD = 500000000
	input.Checks[3].Outcome = OutcomeFailed
	input.Checks[4].Outcome = OutcomeFailed
	input.Checks[7].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), load, digest("e"), input, digest("f"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.FailedCount != 3 || receipt.MetricBreachCount != 3 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryOverflowStaleUnsafeAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *retrievalload.Receipt, *Input){
		"contradictory readiness": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.Checks[0].Outcome = OutcomeFailed
		},
		"missing check": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.Checks = in.Checks[:7]
			in.Ready = false
		},
		"duplicate check": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.Checks[1].ID = in.Checks[0].ID
		},
		"approval after start": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.DecisionApprovedAt = in.AssessmentStartedAt.Add(time.Second)
		},
		"assessment before load": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.AssessmentStartedAt = now.Add(-3 * time.Hour)
			in.DecisionApprovedAt = now.Add(-4 * time.Hour)
		},
		"overlong": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.AssessmentCompletedAt = in.AssessmentStartedAt.Add(8 * 24 * time.Hour)
		},
		"stale": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.GeneratedAt = now.Add(-25 * time.Hour)
		},
		"unsafe assessment": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.AssessmentID = "finance@example.test"
		},
		"wrong derived cost": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.EstimatedWorstCaseMonthlyCostMicroUSD++
		},
		"overflow": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.BetaAccountCap = 100000000
			in.VariableMonthlyCostPerTenantMicroUSD = 1000000000000000
		},
		"passing headroom contradiction": func(_ *platformchange.Receipt, _ *retrievalload.Receipt, in *Input) {
			in.SupportedConcurrentTenants = 1
			in.Ready = false
		},
		"load binding": func(_ *platformchange.Receipt, load *retrievalload.Receipt, _ *Input) {
			load.ReleaseReceiptSHA256 = digest("9")
		},
		"change binding": func(change *platformchange.Receipt, _ *retrievalload.Receipt, _ *Input) {
			change.PlanReceiptSHA256 = digest("9")
		},
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, load, input := validEvidence(now)
			mutate(&change, &load, &input)
			if _, err := build(inventory, plan, change, release, digest("d"), load, digest("e"), input, digest("f"), now); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capacity-receipt.json")
	if err := Publish(path, Receipt{Input: Input{Schema: ReceiptSchemaV1}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("existing destination replaced")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, retrievalload.Receipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "staging-inventory", ReceiptSHA256: digest("a")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "staging-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "staging-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-8 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", ReleaseID: "staging-release", StartedAt: now.Add(-7 * time.Hour), CompletedAt: now.Add(-6 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	load := validLoadReceipt(now, inventory, plan, change, release, digest("d"))
	return inventory, plan, change, release, load, validInput(now, inventory, plan, change, release, digest("d"), load, digest("e"))
}

func validInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, load retrievalload.Receipt, loadDigest string) Input {
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", AssessmentID: "capacity-assessment-1", CapacityModelVersion: "capacity-v1", EntitlementVersion: "entitlement-v1", EconomicsVersion: "economics-v1", BetaCapVersion: "beta-cap-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, RetrievalLoadRunID: load.RunID, RetrievalLoadReceiptSHA256: loadDigest, InstalledLaunchPolicySHA256: digest("2"), EntitlementSnapshotSHA256: digest("3"), CapacityReportSHA256: digest("4"), EconomicsReportSHA256: digest("5"), DecisionSHA256: digest("6"), DecisionApprovedAt: now.Add(-5 * time.Hour), AssessmentStartedAt: now.Add(-90 * time.Minute), AssessmentCompletedAt: now.Add(-45 * time.Minute), GeneratedAt: now.Add(-15 * time.Minute), BetaAccountCap: 100, PlannedPeakConcurrentTenants: 30, SupportedConcurrentTenants: 40, PlannedPeakRetrievalRequestsPerMinute: 10000, SustainedRetrievalRequestsPerMinute: 12000, FixedMonthlyCostMicroUSD: 100000000, VariableMonthlyCostPerTenantMicroUSD: 5000000, EstimatedWorstCaseMonthlyCostMicroUSD: 600000000, ApprovedMonthlyCostCeilingMicroUSD: 750000000, Ready: true, Checks: checks}
}

func validLoadReceipt(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) retrievalload.Receipt {
	checks := make([]retrievalload.Check, 0, len(retrievalload.RequiredChecks()))
	for _, id := range retrievalload.RequiredChecks() {
		checks = append(checks, retrievalload.Check{ID: id, Outcome: retrievalload.OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return retrievalload.Receipt{Schema: retrievalload.ReceiptSchemaV1, Classification: "staging_external", Environment: "staging", RunID: "load-run-1", WorkloadVersion: "workload-v1", DeploymentSiteVersion: "site-v1", ModelRouteVersion: "route-v1", TargetVersion: "target-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, WorkloadManifestSHA256: digest("2"), LoadReportSHA256: digest("3"), ModelCostReportSHA256: digest("4"), TargetDecisionSHA256: digest("5"), InputSHA256: digest("6"), TargetApprovedAt: now.Add(-5 * time.Hour), RunStartedAt: now.Add(-4 * time.Hour), RunCompletedAt: now.Add(-3 * time.Hour), GeneratedAt: now.Add(-150 * time.Minute), CollectedAt: now.Add(-2 * time.Hour), CorpusSourceCount: 100, CorpusPassageCount: 20000, RequestCount: 5000, Concurrency: 32, ErrorCount: 0, ModelCallCount: 5000, P50LatencyMicroseconds: 100000, P95LatencyMicroseconds: 600000, P99LatencyMicroseconds: 700000, SearchP95TargetMicroseconds: 800000, MaximumModelCostMicroUSDPer1000Requests: 250000, ObservedModelCostMicroUSDPer1000Requests: 180000, Ready: true, CheckCount: 8, PassedCount: 8, Checks: checks}
}

func validLoadInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) retrievalload.Input {
	checks := make([]retrievalload.Check, 0, len(retrievalload.RequiredChecks()))
	for _, id := range retrievalload.RequiredChecks() {
		checks = append(checks, retrievalload.Check{ID: id, Outcome: retrievalload.OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return retrievalload.Input{Schema: retrievalload.InputSchemaV1, Classification: "staging_external", Environment: "staging", RunID: "load-run-1", WorkloadVersion: "workload-v1", DeploymentSiteVersion: "site-v1", ModelRouteVersion: "route-v1", TargetVersion: "target-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, WorkloadManifestSHA256: digest("2"), LoadReportSHA256: digest("3"), ModelCostReportSHA256: digest("4"), TargetDecisionSHA256: digest("5"), TargetApprovedAt: now.Add(-7 * time.Hour), RunStartedAt: now.Add(-6 * time.Hour), RunCompletedAt: now.Add(-5 * time.Hour), GeneratedAt: now.Add(-4 * time.Hour), CorpusSourceCount: 100, CorpusPassageCount: 20000, RequestCount: 5000, Concurrency: 32, ModelCallCount: 5000, P50LatencyMicroseconds: 100000, P95LatencyMicroseconds: 600000, P99LatencyMicroseconds: 700000, MaximumModelCostMicroUSDPer1000Requests: 250000, ObservedModelCostMicroUSDPer1000Requests: 180000, Ready: true, Checks: checks}
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

func assertReceiptSchemaSurface(t *testing.T, root string, receipt Receipt) {
	t.Helper()
	contents, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(contents, &object); err != nil {
		t.Fatal(err)
	}
	schemaContents, err := os.ReadFile(filepath.Join(root, "api", "evidence", "v1", "staging-capacity-economics-receipt.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schemaContents, &schema); err != nil {
		t.Fatal(err)
	}
	if len(object) != len(schema.Required) {
		t.Fatalf("receipt properties=%d required=%d", len(object), len(schema.Required))
	}
	for _, key := range schema.Required {
		if _, exists := object[key]; !exists {
			t.Fatalf("receipt missing schema-required property %q", key)
		}
	}
}
func releaseMap() map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest("a") }
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": "staging-release", "started_at": "2026-08-10T05:00:00Z", "completed_at": "2026-08-10T06:00:00Z", "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
}
