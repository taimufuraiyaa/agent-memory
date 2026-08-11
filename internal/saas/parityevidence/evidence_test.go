package parityevidence

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
	input := validInput(now, inventory, plan, change, release, releaseDigest)
	inputPath := writeJSON(t, "parity.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.CheckCount != 8 || receipt.PassedCount != 8 || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.ParityReportSHA256 != digest("f") {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildCanonicalizesChecksAndPreservesMetrics(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Checks[0], input.Checks[7] = input.Checks[7], input.Checks[0]
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Checks[0].ID != RequiredChecks()[0] || receipt.MinimumTopKOverlap != .95 || receipt.ObservedTopKOverlap != .98 || receipt.MaximumNormalizedScoreDelta != .2 || receipt.ObservedMaximumNormalizedScoreDelta != .1 {
		t.Fatalf("unexpected canonical receipt: %+v", receipt)
	}
}

func TestBuildPreservesHonestUnreadyChecksAndMetricBreaches(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Checks[0].Outcome = OutcomeFailed
	input.Checks[1].Outcome = OutcomeFailed
	input.Checks[2].Outcome = OutcomeInconclusive
	input.ObservedTopKOverlap = .90
	input.ObservedMaximumNormalizedScoreDelta = .25
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.FailedCount != 2 || receipt.InconclusiveCount != 1 || receipt.MetricBreachCount != 2 {
		t.Fatalf("unexpected unready receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteStaleUnsafeAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *Input){
		"contradictory readiness": func(_ *platformchange.Receipt, input *Input) { input.Checks[0].Outcome = OutcomeFailed },
		"missing check":           func(_ *platformchange.Receipt, input *Input) { input.Checks = input.Checks[:7]; input.Ready = false },
		"duplicate check":         func(_ *platformchange.Receipt, input *Input) { input.Checks[1].ID = input.Checks[0].ID },
		"unknown outcome": func(_ *platformchange.Receipt, input *Input) {
			input.Checks[0].Outcome = "unknown"
			input.Ready = false
		},
		"threshold after start": func(_ *platformchange.Receipt, input *Input) {
			input.ThresholdApprovedAt = input.EvaluationStartedAt.Add(time.Second)
		},
		"evaluation before release": func(_ *platformchange.Receipt, input *Input) {
			input.EvaluationStartedAt = now.Add(-3 * time.Hour)
			input.ThresholdApprovedAt = now.Add(-4 * time.Hour)
		},
		"evaluation overlong": func(_ *platformchange.Receipt, input *Input) {
			input.EvaluationCompletedAt = input.EvaluationStartedAt.Add(25 * time.Hour)
		},
		"invalid overlap":   func(_ *platformchange.Receipt, input *Input) { input.MinimumTopKOverlap = 1.01 },
		"stale input":       func(_ *platformchange.Receipt, input *Input) { input.GeneratedAt = now.Add(-25 * time.Hour) },
		"unsafe evaluation": func(_ *platformchange.Receipt, input *Input) { input.EvaluationID = "person@example.test" },
		"change binding":    func(change *platformchange.Receipt, _ *Input) { change.PlanReceiptSHA256 = digest("9") },
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
	path := filepath.Join(t.TempDir(), "parity-receipt.json")
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

func TestLoadReadyReceiptRevalidatesCanonicalContentAndExactDigest(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "ready-parity-receipt.json", receipt)
	loaded, exactDigest, err := LoadReadyReceipt(path)
	if err != nil || loaded.EvaluationID != receipt.EvaluationID || exactDigest != digestFile(t, path) {
		t.Fatalf("loaded=%+v digest=%q err=%v", loaded, exactDigest, err)
	}
	unready := receipt
	unready.Ready = false
	if _, _, err := LoadReadyReceipt(writeJSON(t, "unready-parity-receipt.json", unready)); err == nil {
		t.Fatal("unready parity receipt accepted")
	}
	tampered := receipt
	tampered.ObservedTopKOverlap = .10
	if _, _, err := LoadReadyReceipt(writeJSON(t, "tampered-parity-receipt.json", tampered)); err == nil {
		t.Fatal("tampered parity receipt accepted")
	}
	linked := filepath.Join(t.TempDir(), "parity-link.json")
	if err := os.Symlink(path, linked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReadyReceipt(linked); err == nil {
		t.Fatal("symlink parity receipt accepted")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "staging-inventory", ReceiptSHA256: digest("a")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "staging-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "staging-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-4 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", KubernetesContext: "staging-context", ReleaseID: "staging-release", StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-2 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	return inventory, plan, change, release, validInput(now, inventory, plan, change, release, digest("d"))
}

func validInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) Input {
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", EvaluationID: "parity-evaluation-1", ThresholdVersion: "threshold-v1", DatasetVersion: "representative-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, ThresholdDecisionSHA256: digest("5"), ParityReportSHA256: digest("f"), ThresholdApprovedAt: now.Add(-3 * time.Hour), EvaluationStartedAt: now.Add(-90 * time.Minute), EvaluationCompletedAt: now.Add(-45 * time.Minute), GeneratedAt: now.Add(-15 * time.Minute), CaseCount: 250, MinimumTopKOverlap: .95, MaximumNormalizedScoreDelta: .2, ObservedTopKOverlap: .98, ObservedMaximumNormalizedScoreDelta: .1, Ready: true, Checks: checks}
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
