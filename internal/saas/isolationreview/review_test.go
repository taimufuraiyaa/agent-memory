package isolationreview

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

func TestCollectValidatesCompleteFileChain(t *testing.T) {
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
	releasePath := writeJSON(t, "release.json", validReleaseMap())
	releaseBytes, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	releaseDigest := fmt.Sprintf("%x", sha256.Sum256(releaseBytes))
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	reviewPath := writeJSON(t, "review.json", validInput(now, inventory, change, releaseDigest))
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, reviewPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || receipt.ChangeReceiptSHA256 != change.ReceiptSHA256 || receipt.ReleaseReceiptSHA256 != releaseDigest {
		t.Fatalf("unexpected file-chain receipt: %+v", receipt)
	}
}

func TestBuildAcceptsReadyReviewAndCanonicalizesDomains(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change, release, input := validEvidence(now)
	input.Domains[0], input.Domains[5] = input.Domains[5], input.Domains[0]

	receipt, err := build(inventory, change, release, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.DomainCount != 6 || receipt.PassedCount != 6 || receipt.FindingCount != 0 {
		t.Fatalf("unexpected assessment: %+v", receipt)
	}
	for index, id := range RequiredDomains() {
		if receipt.Domains[index].ID != id {
			t.Fatalf("domain %d=%q, want %q", index, receipt.Domains[index].ID, id)
		}
	}
}

func TestBuildPreservesFailedAndInconclusiveReviews(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change, release, input := validEvidence(now)
	input.Domains[1].Outcome, input.Domains[1].FindingCount = OutcomeFailed, 2
	input.Domains[5].Outcome = OutcomeInconclusive
	input.Ready = false
	receipt, err := build(inventory, change, release, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.FailedCount != 1 || receipt.InconclusiveCount != 1 || receipt.FindingCount != 2 {
		t.Fatalf("unexpected unready assessment: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryFindingsReadinessAndStaleInput(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change, release, input := validEvidence(now)
	input.Domains[0].FindingCount = 1
	if _, err := build(inventory, change, release, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now); err == nil {
		t.Fatal("passed domain with finding accepted")
	}
	_, _, _, input = validEvidence(now)
	input.Domains[0].Outcome, input.Ready = OutcomeInconclusive, true
	if _, err := build(inventory, change, release, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now); err == nil {
		t.Fatal("contradictory readiness accepted")
	}
	_, _, _, input = validEvidence(now)
	input.GeneratedAt = now.Add(-25 * time.Hour)
	input.Review.CompletedAt = input.GeneratedAt
	input.Review.StartedAt = input.GeneratedAt.Add(-time.Hour)
	if _, err := build(inventory, change, release, strings.Repeat("d", 64), input, strings.Repeat("e", 64), now); err == nil {
		t.Fatal("stale review accepted")
	}
}

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "isolation-review.json")
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
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "inventory-1", ReceiptSHA256: strings.Repeat("a", 64)}
	change := platformchange.Receipt{
		Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "change-1", InventoryID: inventory.InventoryID,
		InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: strings.Repeat("b", 64), GeneratedAt: now.Add(-4 * time.Hour),
		Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired},
		ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean},
	}
	release := platformrollback.ReleaseReceipt{
		Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", ReleaseID: "release-1",
		StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-2 * time.Hour), Outcome: "passed",
		Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"},
	}
	domains := make([]Domain, 0, len(RequiredDomains()))
	for _, id := range RequiredDomains() {
		domains = append(domains, Domain{ID: id, Outcome: OutcomePassed, EvidenceSHA256: strings.Repeat("1", 64)})
	}
	input := Input{
		Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", ReviewID: "isolation-review-1",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ChangeID: change.ChangeID,
		ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: strings.Repeat("d", 64),
		Ready: true, GeneratedAt: now.Add(-30 * time.Minute), Review: ReviewWindow{StartedAt: now.Add(-90 * time.Minute), CompletedAt: now.Add(-45 * time.Minute)}, Domains: domains,
	}
	return inventory, change, release, input
}

func validReleaseMap() map[string]any {
	image := "registry.example/agent-memory@sha256:" + strings.Repeat("a", 64)
	return map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging",
		"kubernetes_context": "staging-context", "release_id": "release-1", "started_at": "2026-08-10T05:00:00Z",
		"completed_at": "2026-08-10T06:00:00Z", "outcome": "passed",
		"images":    map[string]any{"api": image, "worker": image, "reconciler": image, "migrate": image},
		"migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"},
		"deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}},
		"rollback":    map[string]any{"attempted": false, "succeeded": false},
	}
}

func validInput(now time.Time, inventory platforminventory.Inventory, change platformchange.Receipt, releaseDigest string) Input {
	domains := make([]Domain, 0, len(RequiredDomains()))
	for _, id := range RequiredDomains() {
		domains = append(domains, Domain{ID: id, Outcome: OutcomePassed, EvidenceSHA256: strings.Repeat("1", 64)})
	}
	return Input{
		Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", ReviewID: "isolation-review-1",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ChangeID: change.ChangeID,
		ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: "release-1", ReleaseReceiptSHA256: releaseDigest, Ready: true,
		GeneratedAt: now.Add(-30 * time.Minute), Review: ReviewWindow{StartedAt: now.Add(-2 * time.Hour), CompletedAt: now.Add(-time.Hour)}, Domains: domains,
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

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}
