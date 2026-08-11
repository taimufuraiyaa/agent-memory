package retrievalrisk

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
	inputPath := writeJSON(t, "review.json", validInput(now, inventory, plan, change, release, releaseDigest))
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.DomainCount != 7 || receipt.PassedCount != 7 || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.BlindCorpusSHA256 != digest("5") {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildCanonicalizesCompleteReadyReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Domains[0], input.Domains[6] = input.Domains[6], input.Domains[0]
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Domains[0].ID != RequiredDomains()[0] || receipt.TimingSampleCountPerClass != 80 || receipt.ObservedTimingDeltaMicroseconds != 4500 || receipt.FindingCount != 0 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildPreservesHonestUnreadyLeaksTimingAndRisk(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.ResultLeakCount, input.CountLeakCount, input.CacheLeakCount = 1, 2, 1
	input.ObservedTimingDeltaMicroseconds = 12000
	for _, index := range []int{1, 2, 3, 4} {
		input.Domains[index].Outcome = OutcomeFailed
		input.Domains[index].FindingCount = 1
	}
	input.Domains[6].Outcome = OutcomeInconclusive
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.FailedCount != 4 || receipt.InconclusiveCount != 1 || receipt.FindingCount != 4 || receipt.RiskBreachCount != 4 {
		t.Fatalf("unexpected unready receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteStaleUnsafeAndMisboundReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *Input){
		"contradictory readiness": func(_ *platformchange.Receipt, input *Input) { input.Domains[0].Outcome = OutcomeInconclusive },
		"passed with finding":     func(_ *platformchange.Receipt, input *Input) { input.Domains[0].FindingCount = 1; input.Ready = false },
		"missing domain":          func(_ *platformchange.Receipt, input *Input) { input.Domains = input.Domains[:6]; input.Ready = false },
		"duplicate domain":        func(_ *platformchange.Receipt, input *Input) { input.Domains[1].ID = input.Domains[0].ID },
		"passed result with leak": func(_ *platformchange.Receipt, input *Input) { input.ResultLeakCount = 1; input.Ready = false },
		"passed timing breach": func(_ *platformchange.Receipt, input *Input) {
			input.ObservedTimingDeltaMicroseconds = 12000
			input.Ready = false
		},
		"wrong tenant count": func(_ *platformchange.Receipt, input *Input) { input.TenantCount = 3 },
		"pre-release review": func(_ *platformchange.Receipt, input *Input) { input.ReviewStartedAt = now.Add(-3 * time.Hour) },
		"overlong review": func(_ *platformchange.Receipt, input *Input) {
			input.ReviewCompletedAt = input.ReviewStartedAt.Add(15 * 24 * time.Hour)
		},
		"stale input":    func(_ *platformchange.Receipt, input *Input) { input.GeneratedAt = now.Add(-25 * time.Hour) },
		"unsafe review":  func(_ *platformchange.Receipt, input *Input) { input.ReviewID = "reviewer@example.test" },
		"change binding": func(change *platformchange.Receipt, _ *Input) { change.PlanReceiptSHA256 = digest("9") },
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
	path := filepath.Join(t.TempDir(), "risk-receipt.json")
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
	return inventory, plan, change, release, validInput(now, inventory, plan, change, release, digest("d"))
}

func validInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) Input {
	domains := make([]Domain, 0, len(RequiredDomains()))
	for _, id := range RequiredDomains() {
		domains = append(domains, Domain{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", ReviewID: "retrieval-risk-review-1", CorpusVersion: "blind-corpus-v1", TimingMethodVersion: "timing-method-v1", ToleranceVersion: "risk-tolerance-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, BlindCorpusSHA256: digest("5"), TimingReportSHA256: digest("6"), CacheReviewSHA256: digest("7"), RiskToleranceDecisionSHA256: digest("8"), ReviewStartedAt: now.Add(-90 * time.Minute), ReviewCompletedAt: now.Add(-45 * time.Minute), GeneratedAt: now.Add(-15 * time.Minute), TenantCount: 2, CaseCount: 500, TimingSampleCountPerClass: 80, MaximumTimingDeltaMicroseconds: 10000, ObservedTimingDeltaMicroseconds: 4500, Ready: true, Domains: domains}
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
