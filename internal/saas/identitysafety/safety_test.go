package identitysafety

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
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	input := Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", BundleID: "identity-safety-bundle-1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, Ready: true, GeneratedAt: now.Add(-15 * time.Minute), Drills: []Drill{passedDrill(DrillIdentityProviderOutage, now.Add(-90*time.Minute), 1200, 600), passedDrill(DrillCredentialRevocation, now.Add(-60*time.Minute), 1200, 900)}}
	inputPath := writeJSON(t, "input.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.ReleaseReceiptSHA256 != releaseDigest || receipt.InputSHA256 != digestFile(t, inputPath) {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildCanonicalizesCompleteReadyEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Drills[0], input.Drills[1] = input.Drills[1], input.Drills[0]
	input.Drills[1].Checks[0], input.Drills[1].Checks[6] = input.Drills[1].Checks[6], input.Drills[1].Checks[0]
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.DrillCount != 2 || receipt.CheckCount != 15 || receipt.PassedCount != 15 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if receipt.Drills[0].ID != DrillIdentityProviderOutage || receipt.Drills[0].Checks[0].ID != RequiredChecks(DrillIdentityProviderOutage)[0] {
		t.Fatalf("receipt is not canonical: %+v", receipt.Drills)
	}
	if receipt.MaximumRTOSeconds != 900 || receipt.MaximumRTOTargetSeconds != 1200 || receipt.Drills[0].DetectionSeconds != 60 || receipt.Drills[0].AlertSeconds != 120 || receipt.Drills[0].ContainmentSeconds != 180 {
		t.Fatalf("durations not derived: %+v", receipt)
	}
}

func TestBuildPreservesHonestUnreadyOutcomesAndTargetBreach(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Drills[0].Checks[0].Outcome = OutcomeFailed
	input.Drills[1].Checks[1].Outcome = OutcomeInconclusive
	input.Drills[1].RTOTargetSeconds = 300
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.FailedCount != 1 || receipt.InconclusiveCount != 1 || receipt.TargetBreachCount != 1 {
		t.Fatalf("unexpected unready receipt: %+v", receipt)
	}
}

func TestBuildRejectsUnsafeContradictoryIncompleteAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *Input){
		"contradictory readiness": func(_ *platformchange.Receipt, input *Input) { input.Drills[0].Checks[0].Outcome = OutcomeFailed },
		"missing check": func(_ *platformchange.Receipt, input *Input) {
			input.Drills[0].Checks = input.Drills[0].Checks[:6]
			input.Ready = false
		},
		"duplicate drill": func(_ *platformchange.Receipt, input *Input) { input.Drills[1].ID = input.Drills[0].ID },
		"unknown outcome": func(_ *platformchange.Receipt, input *Input) {
			input.Drills[0].Checks[0].Outcome = "unknown"
			input.Ready = false
		},
		"time reversal": func(_ *platformchange.Receipt, input *Input) {
			input.Drills[0].AlertedAt = input.Drills[0].DetectedAt.Add(-time.Second)
		},
		"overlong target": func(_ *platformchange.Receipt, input *Input) { input.Drills[0].RTOTargetSeconds = 86401 },
		"stale input":     func(_ *platformchange.Receipt, input *Input) { input.GeneratedAt = now.Add(-25 * time.Hour) },
		"unsafe bundle":   func(_ *platformchange.Receipt, input *Input) { input.BundleID = "person@example.test" },
		"change binding":  func(change *platformchange.Receipt, _ *Input) { change.PlanReceiptSHA256 = digest("f") },
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

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity-safety.json")
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
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	if err := Publish(linked, Receipt{Schema: ReceiptSchemaV1}); err == nil {
		t.Fatal("symlink destination replaced")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "staging-inventory", ReceiptSHA256: digest("a")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "staging-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "staging-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-4 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", KubernetesContext: "staging-context", ReleaseID: "staging-release", StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-2 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	drills := []Drill{
		passedDrill(DrillIdentityProviderOutage, now.Add(-90*time.Minute), 1200, 600),
		passedDrill(DrillCredentialRevocation, now.Add(-60*time.Minute), 900, 900),
	}
	input := Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", BundleID: "identity-safety-bundle-1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("d"), Ready: true, GeneratedAt: now.Add(-15 * time.Minute), Drills: drills}
	return inventory, plan, change, release, input
}

func passedDrill(id DrillID, impairment time.Time, target, recovery int64) Drill {
	checks := make([]Check, 0, len(RequiredChecks(id)))
	for _, checkID := range RequiredChecks(id) {
		checks = append(checks, Check{ID: checkID, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return Drill{ID: id, ImpairmentAt: impairment, DetectedAt: impairment.Add(time.Minute), AlertedAt: impairment.Add(2 * time.Minute), ContainedAt: impairment.Add(3 * time.Minute), RecoveredAt: impairment.Add(time.Duration(recovery) * time.Second), RTOTargetSeconds: target, TargetApprovalSHA256: digest("2"), Checks: checks}
}

func digest(character string) string { return strings.Repeat(character, 64) }

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

func releaseMap() map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest("a") }
	return map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": "staging-release",
		"started_at": "2026-08-10T05:00:00Z", "completed_at": "2026-08-10T06:00:00Z", "outcome": "passed",
		"images":    map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")},
		"migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"},
		"deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}},
		"rollback":    map[string]any{"attempted": false, "succeeded": false},
	}
}
