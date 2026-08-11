package operationalsafety

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

func TestCollectValidatesCompleteOperationalSafetyChain(t *testing.T) {
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
	baselinePath := writeJSON(t, "baseline.json", releaseMap("baseline-release", "2026-08-10T07:00:00Z", "2026-08-10T07:10:00Z", "passed", false, false, false))
	attemptPath := writeJSON(t, "attempt.json", releaseMap("failed-release", "2026-08-10T07:15:00Z", "2026-08-10T07:30:00Z", "failed", true, true, true))
	pair, err := platformrollback.LoadPair(baselinePath, attemptPath)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := platformrollback.Evaluate(pair, restoredSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	rollbackPath := writeJSON(t, "rollback.json", rollback)
	rollbackDigest := digestFile(t, rollbackPath)
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	inputPath := writeJSON(t, "drills.json", validInput(now, inventory, plan, change, pair, rollbackDigest))
	receipt, err := Collect(inventoryPath, planPath, changePath, baselinePath, attemptPath, rollbackPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.DrillCount != 2 || receipt.CheckCount != 14 || receipt.PassedCount != 14 || receipt.RollbackReceiptSHA256 != rollbackDigest {
		t.Fatalf("unexpected operational-safety receipt: %+v", receipt)
	}
}

func TestBuildCanonicalizesChecksAndPreservesValidUnreadyOutcomes(t *testing.T) {
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	inventory, plan, change, pair, rollback, input := validEvidence(now)
	input.Drills[0].Checks[0], input.Drills[0].Checks[6] = input.Drills[0].Checks[6], input.Drills[0].Checks[0]
	receipt, err := build(inventory, plan, change, pair, rollback, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Drills[0].Checks[0].ID != RequiredChecks(DrillManagedSecretRotation)[0] {
		t.Fatalf("unexpected canonical receipt: %+v", receipt)
	}
	input.Drills[0].Checks[1].Outcome = OutcomeFailed
	input.Drills[1].Checks[2].Outcome = OutcomeInconclusive
	input.Ready = false
	receipt, err = build(inventory, plan, change, pair, rollback, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.FailedCount != 1 || receipt.InconclusiveCount != 1 || receipt.PassedCount != 12 {
		t.Fatalf("unexpected unready receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteStaleAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *platformrollback.Receipt, *Input){
		"contradictory readiness": func(_ *platformchange.Receipt, _ *platformrollback.Receipt, input *Input) {
			input.Drills[0].Checks[0].Outcome = OutcomeFailed
		},
		"missing check": func(_ *platformchange.Receipt, _ *platformrollback.Receipt, input *Input) {
			input.Drills[0].Checks = input.Drills[0].Checks[:6]
			input.Ready = false
		},
		"duplicate check": func(_ *platformchange.Receipt, _ *platformrollback.Receipt, input *Input) {
			input.Drills[0].Checks[1].ID = input.Drills[0].Checks[0].ID
		},
		"stale input": func(_ *platformchange.Receipt, _ *platformrollback.Receipt, input *Input) {
			input.GeneratedAt = now.Add(-25 * time.Hour)
		},
		"future completion": func(_ *platformchange.Receipt, _ *platformrollback.Receipt, input *Input) {
			input.Drills[1].CompletedAt = now.Add(time.Minute)
		},
		"change binding": func(change *platformchange.Receipt, _ *platformrollback.Receipt, _ *Input) {
			change.PlanReceiptSHA256 = strings.Repeat("f", 64)
		},
		"rollback not ready": func(_ *platformchange.Receipt, rollback *platformrollback.Receipt, _ *Input) {
			rollback.Ready = false
			rollback.Deployments[0].Outcome = platformrollback.OutcomeNotReady
		},
		"rollback before failed attempt": func(_ *platformchange.Receipt, rollback *platformrollback.Receipt, _ *Input) {
			rollback.CollectedAt = now.Add(-5 * time.Hour)
		},
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, pair, rollback, input := validEvidence(now)
			mutate(&change, &rollback, &input)
			if _, err := build(inventory, plan, change, pair, rollback, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operational-safety.json")
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err == nil {
		t.Fatal("existing destination replaced")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "receipt-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, Receipt{Schema: ReceiptSchemaV1}); err == nil {
		t.Fatal("symlink destination accepted")
	}
}

func TestExampleInputContainsCompleteCanonicalDrills(t *testing.T) {
	path := filepath.Join(repositoryRoot(t), "docs", "saas", "staging-operational-safety-drills.example.json")
	var input Input
	if _, err := decodeStrictRegular(path, &input); err != nil {
		t.Fatal(err)
	}
	drills, passed, failed, inconclusive, err := validateDrills(input.Drills, time.Date(2026, 8, 10, 7, 10, 0, 0, time.UTC), input.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(drills) != 2 || passed != 14 || failed != 0 || inconclusive != 0 || !input.Ready {
		t.Fatalf("unexpected example input: %+v", input)
	}
}

func TestLoadReceiptRecomputesAggregates(t *testing.T) {
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	inventory, plan, change, pair, rollback, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, pair, rollback, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "receipt.json", receipt)
	loaded, err := Load(path)
	if err != nil || !loaded.Ready || loaded.PassedCount != 14 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	receipt.PassedCount = 13
	if _, err := Load(writeJSON(t, "contradictory.json", receipt)); err == nil {
		t.Fatal("contradictory aggregates accepted")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.Pair, platformrollback.Receipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "inventory-1", ReceiptSHA256: strings.Repeat("a", 64)}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "plan-1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: strings.Repeat("b", 64)}
	change := platformchange.Receipt{
		Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "change-1", InventoryID: inventory.InventoryID,
		InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256,
		ReceiptSHA256: strings.Repeat("c", 64), GeneratedAt: now.Add(-8 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded},
		Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean},
	}
	pair := platformrollback.Pair{
		Baseline:              platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", KubernetesContext: "staging-context", ReleaseID: "baseline-release", StartedAt: now.Add(-6 * time.Hour), CompletedAt: now.Add(-5 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}},
		Attempt:               platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", KubernetesContext: "staging-context", ReleaseID: "failed-release", StartedAt: now.Add(-4 * time.Hour), CompletedAt: now.Add(-3 * time.Hour), Outcome: "failed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "failed"}, Rollback: platformrollback.ReleaseRollback{Attempted: true, Succeeded: true}},
		BaselineReceiptSHA256: strings.Repeat("4", 64), AttemptReceiptSHA256: strings.Repeat("5", 64),
	}
	rollback := platformrollback.Receipt{Schema: platformrollback.ReceiptSchemaV1, Ready: true, Environment: "staging", Namespace: "agent-memory-staging", KubernetesContext: "staging-context", BaselineReleaseID: pair.Baseline.ReleaseID, BaselineReceiptSHA256: pair.BaselineReceiptSHA256, FailedAttemptReleaseID: pair.Attempt.ReleaseID, FailedAttemptReceiptSHA256: pair.AttemptReceiptSHA256, CollectedAt: now.Add(-2 * time.Hour), Deployments: []platformrollback.DeploymentResult{{Name: platformrollback.DeploymentAPI, Outcome: platformrollback.OutcomeRestored, Revision: "8"}, {Name: platformrollback.DeploymentWorker, Outcome: platformrollback.OutcomeRestored, Revision: "8"}, {Name: platformrollback.DeploymentReconciler, Outcome: platformrollback.OutcomeRestored, Revision: "8"}}}
	input := validInput(now, inventory, plan, change, pair, strings.Repeat("d", 64))
	return inventory, plan, change, pair, rollback, input
}

func validInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, pair platformrollback.Pair, rollbackDigest string) Input {
	drills := []Drill{
		{ID: DrillManagedSecretRotation, StartedAt: now.Add(-90 * time.Minute), CompletedAt: now.Add(-60 * time.Minute), Checks: passedChecks(DrillManagedSecretRotation)},
		{ID: DrillHumanOperatorAccess, StartedAt: now.Add(-55 * time.Minute), CompletedAt: now.Add(-30 * time.Minute), Checks: passedChecks(DrillHumanOperatorAccess)},
	}
	return Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", BundleID: "safety-bundle-1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, BaselineReleaseID: pair.Baseline.ReleaseID, BaselineReceiptSHA256: pair.BaselineReceiptSHA256, FailedAttemptReleaseID: pair.Attempt.ReleaseID, FailedAttemptReceiptSHA256: pair.AttemptReceiptSHA256, RollbackReceiptSHA256: rollbackDigest, Ready: true, GeneratedAt: now.Add(-15 * time.Minute), Drills: drills}
}

func passedChecks(drill DrillID) []Check {
	checks := make([]Check, 0, 7)
	for _, id := range RequiredChecks(drill) {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: strings.Repeat("1", 64)})
	}
	return checks
}

func restoredSnapshot() platformrollback.Snapshot {
	return platformrollback.Snapshot{KubernetesContext: "staging-context", CollectedAt: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC), Deployments: map[platformrollback.DeploymentName]platformrollback.LiveDeployment{
		platformrollback.DeploymentAPI:        {Image: baselineImage("api"), Revision: "8", DesiredReplicas: 2, ReadyReplicas: 2},
		platformrollback.DeploymentWorker:     {Image: baselineImage("worker"), Revision: "8", DesiredReplicas: 2, ReadyReplicas: 2},
		platformrollback.DeploymentReconciler: {Image: baselineImage("reconciler"), Revision: "8", DesiredReplicas: 1, ReadyReplicas: 1},
	}}
}

func releaseMap(releaseID, startedAt, completedAt, outcome string, attempted, succeeded, changedImages bool) map[string]any {
	images := map[string]any{"api": baselineImage("api"), "worker": baselineImage("worker"), "reconciler": baselineImage("reconciler"), "migrate": baselineImage("migrate")}
	if changedImages {
		images["api"], images["worker"], images["reconciler"] = attemptedImage("api"), attemptedImage("worker"), attemptedImage("reconciler")
	}
	rollouts := "healthy"
	if outcome == "failed" {
		rollouts = "failed"
	}
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": releaseID, "started_at": startedAt, "completed_at": completedAt, "outcome": outcome, "images": images, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": rollouts}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "7"}, {"name": "agent-memory-worker", "revision": "7"}, {"name": "agent-memory-reconciler", "revision": "7"}}, "rollback": map[string]any{"attempted": attempted, "succeeded": succeeded}}
}

func baselineImage(name string) string {
	return "registry.example/agent-memory-" + name + "@sha256:" + strings.Repeat("a", 64)
}
func attemptedImage(name string) string {
	return "registry.example/agent-memory-" + name + "@sha256:" + strings.Repeat("b", 64)
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
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}
