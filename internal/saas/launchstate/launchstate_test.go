package launchstate

import (
	"context"
	"encoding/json"
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
	"github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestCollectEvidenceValidatesCompleteFileChain(t *testing.T) {
	root := repositoryRoot(t)
	inventoryPath := filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json")
	planPath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json")
	changePath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.example.json")
	releasePath := writeJSON(t, "release.json", validReleaseMap())
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	receipt, err := CollectEvidence(inventoryPath, planPath, changePath, releasePath, PolicyState{
		Phase: "internal_alpha", SignupEnabled: false, InvitationRequired: true,
		PolicyVersion: "launch-v1", UpdatedAt: now.Add(-time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.PlanReceiptSHA256 == "" || receipt.ChangeReceiptSHA256 == "" || receipt.ReleaseReceiptSHA256 == "" {
		t.Fatalf("unexpected launch-state receipt: %+v", receipt)
	}
}

func TestBuildAssessesSafeAndUnsafePolicyStates(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release := validEvidence(now)
	policy := PolicyState{Phase: "internal_alpha", SignupEnabled: false, InvitationRequired: true, PolicyVersion: "launch-v1", UpdatedAt: now.Add(-time.Minute)}
	receipt, err := build(inventory, plan, change, release, strings.Repeat("e", 64), policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Phase != "internal_alpha" || receipt.SignupEnabled || !receipt.InvitationRequired {
		t.Fatalf("unexpected safe assessment: %+v", receipt)
	}
	policy.SignupEnabled = true
	receipt, err = build(inventory, plan, change, release, strings.Repeat("e", 64), policy, now)
	if err != nil || receipt.Ready {
		t.Fatalf("enabled signup must be valid but unready: receipt=%+v err=%v", receipt, err)
	}
	policy.SignupEnabled = false
	policy.Phase = "private_beta"
	receipt, err = build(inventory, plan, change, release, strings.Repeat("e", 64), policy, now)
	if err != nil || receipt.Ready {
		t.Fatalf("private beta must be valid but unready: receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildRejectsMalformedFutureAndUnboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release := validEvidence(now)
	policy := PolicyState{Phase: "internal_alpha", InvitationRequired: true, PolicyVersion: "contains spaces", UpdatedAt: now.Add(-time.Minute)}
	if _, err := build(inventory, plan, change, release, strings.Repeat("e", 64), policy, now); err == nil {
		t.Fatal("malformed policy version accepted")
	}
	policy.PolicyVersion, policy.UpdatedAt = "launch-v1", now.Add(time.Second)
	if _, err := build(inventory, plan, change, release, strings.Repeat("e", 64), policy, now); err == nil {
		t.Fatal("future policy update accepted")
	}
	policy.UpdatedAt = now.Add(-time.Minute)
	change.PlanReceiptSHA256 = strings.Repeat("f", 64)
	if _, err := build(inventory, plan, change, release, strings.Repeat("e", 64), policy, now); err == nil {
		t.Fatal("unbound plan/change chain accepted")
	}
}

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch-state.json")
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

func TestCollectorQueryCannotExpandIntoCustomerTables(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source path")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(current), "launchstate.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "FROM saas_launch_policy WHERE singleton = true") {
		t.Fatal("collector must retain the fixed singleton launch-policy query")
	}
	for _, forbidden := range []string{"saas_accounts", "saas_tenants", "saas_launch_invitations", "saas_signup_attempts", "saas_signup_reservations", "saas_sources", "saas_memories"} {
		if strings.Contains(string(contents), forbidden) {
			t.Errorf("collector source references forbidden customer table %q", forbidden)
		}
	}
}

func TestExampleReceiptLoads(t *testing.T) {
	path := filepath.Join(repositoryRoot(t), "docs", "saas", "staging-safe-platform-launch-state.example.json")
	receipt, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Phase != "internal_alpha" {
		t.Fatalf("unexpected example receipt: %+v", receipt)
	}
}

func TestCollectReadsInstalledFailClosedPolicy(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Apply(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	pool.Close()
	root := repositoryRoot(t)
	receipt, err := Collect(ctx,
		filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json"),
		filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json"),
		filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.example.json"),
		writeJSON(t, "release.json", validReleaseMap()), connectionURL,
		time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.SignupEnabled || !receipt.InvitationRequired {
		t.Fatalf("unexpected installed policy receipt: %+v", receipt)
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "inventory-1", ReceiptSHA256: strings.Repeat("a", 64)}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "plan-1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: strings.Repeat("b", 64)}
	change := platformchange.Receipt{
		Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "change-1", InventoryID: inventory.InventoryID,
		InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256,
		ReceiptSHA256: strings.Repeat("c", 64), GeneratedAt: now.Add(-4 * time.Hour),
		Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired},
		ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean},
	}
	release := platformrollback.ReleaseReceipt{
		Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", ReleaseID: "release-1",
		StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-2 * time.Hour), Outcome: "passed",
		Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"},
	}
	return inventory, plan, change, release
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
