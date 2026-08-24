package retentioninventory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
)

func TestBuildProducesCanonicalContentFreePolicyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change := readyBinding(now.Add(-time.Hour))
	policies := completePolicies(now.Add(-2 * time.Hour))
	for left, right := 0, len(policies)-1; left < right; left, right = left+1, right-1 {
		policies[left], policies[right] = policies[right], policies[left]
	}
	receipt, err := Build(inventory, change, policies, now)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "self_managed_external" || !receipt.Ready || receipt.PolicyCount != len(retention.DataClasses) || receipt.PoliciesSHA256 == "" {
		t.Fatalf("receipt = %+v", receipt)
	}
	for index := 1; index < len(receipt.Policies); index++ {
		if receipt.Policies[index-1].DataClass >= receipt.Policies[index].DataClass {
			t.Fatal("policies are not in canonical data-class order")
		}
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"postgres://", "tenant_id", "database", "sql", "path", "customer_content"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("receipt leaked forbidden token %q", forbidden)
		}
	}
}

func TestBuildRejectsUnreadyBindingIncompletePoliciesAndInvalidTime(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change := readyBinding(now.Add(-time.Hour))
	policies := completePolicies(now.Add(-2 * time.Hour))

	unready := change
	unready.Drift.Outcome = platformchange.DriftDetected
	if _, err := Build(inventory, unready, policies, now); err == nil {
		t.Fatal("Build() accepted unready platform change")
	}
	if _, err := Build(inventory, change, policies[1:], now); err == nil {
		t.Fatal("Build() accepted incomplete policy set")
	}
	if _, err := Build(inventory, change, policies, change.GeneratedAt.Add(-time.Second)); err == nil {
		t.Fatal("Build() accepted collection before applied change")
	}
}

func TestPublishIsCreateOnlyRegularAndMode0600(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change := readyBinding(now.Add(-time.Hour))
	receipt, err := Build(inventory, change, completePolicies(now.Add(-2*time.Hour)), now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, receipt); err == nil {
		t.Fatal("Publish() replaced an existing receipt")
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, receipt); err == nil {
		t.Fatal("Publish() accepted a symlink destination")
	}
}

func TestLoadRecomputesExactReceiptAndPolicyDigests(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change := readyBinding(now.Add(-time.Hour))
	receipt, err := Build(inventory, change, completePolicies(now.Add(-2*time.Hour)), now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(contents))
	if loaded.ReceiptSHA256 != want || loaded.PoliciesSHA256 != receipt.PoliciesSHA256 || len(loaded.Policies) != 12 {
		t.Fatalf("loaded=%+v want receipt digest=%s", loaded, want)
	}
}

func TestLoadRejectsUnknownFieldsTamperedDigestAndNonCanonicalPolicies(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	inventory, change := readyBinding(now.Add(-time.Hour))
	receipt, err := Build(inventory, change, completePolicies(now.Add(-2*time.Hour)), now)
	if err != nil {
		t.Fatal(err)
	}
	base, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{}
	var object map[string]any
	if err := json.Unmarshal(base, &object); err != nil {
		t.Fatal(err)
	}
	object["connection_url"] = "postgres://secret"
	tests["unknown field"], _ = json.Marshal(object)

	tampered := receipt
	tampered.PoliciesSHA256 = strings.Repeat("f", 64)
	tests["policy digest"], _ = json.Marshal(tampered)

	reordered := receipt
	reordered.Policies = append([]Policy(nil), receipt.Policies...)
	reordered.Policies[0], reordered.Policies[1] = reordered.Policies[1], reordered.Policies[0]
	canonical, _ := json.Marshal(reordered.Policies)
	reordered.PoliciesSHA256 = fmt.Sprintf("%x", sha256.Sum256(canonical))
	tests["policy order"], _ = json.Marshal(reordered)

	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipt.json")
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load() accepted invalid receipt")
			}
		})
	}
}

func TestIntegrationInstalledPoliciesBuildSchemaBoundReceipt(t *testing.T) {
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
	defer pool.Close()
	if err := postgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	policies, err := retention.NewRegistry(pool).ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Minute)
	inventory, change := readyBinding(now.Add(-time.Minute))
	receipt, err := Build(inventory, change, policies, now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "installed-retention-receipt.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Receipt
	if err := json.Unmarshal(contents, &decoded); err != nil || decoded.PolicyCount != 12 || len(decoded.Policies) != 12 || decoded.PoliciesSHA256 == "" {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func readyBinding(generatedAt time.Time) (platforminventory.Inventory, platformchange.Receipt) {
	inventory := platforminventory.Inventory{
		Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging,
		InventoryID: "inventory-20260810", ReceiptSHA256: strings.Repeat("a", 64),
	}
	count := 12
	collectedAt, checkedAt := generatedAt.Add(-2*time.Minute), generatedAt.Add(-time.Minute)
	change := platformchange.Receipt{
		Schema: platformchange.SchemaV1, Environment: inventory.Environment,
		ChangeID: "change-20260810", InventoryID: inventory.InventoryID,
		InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: strings.Repeat("b", 64),
		GeneratedAt:       generatedAt,
		Apply:             platformchange.Apply{Outcome: platformchange.ApplySucceeded, CompletedAt: generatedAt.Add(-3 * time.Minute), RawOutputSHA256: strings.Repeat("c", 64)},
		Rollback:          platformchange.Rollback{Outcome: platformchange.RollbackNotRequired},
		ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected, CollectedAt: &collectedAt, SHA256: strings.Repeat("d", 64), ResourceCount: &count},
		Drift:             platformchange.Drift{Outcome: platformchange.DriftClean, CheckedAt: &checkedAt, RawOutputSHA256: strings.Repeat("e", 64)},
	}
	return inventory, change
}

func completePolicies(effectiveAt time.Time) []retention.Policy {
	result := make([]retention.Policy, 0, len(retention.DataClasses))
	for _, dataClass := range retention.DataClasses {
		result = append(result, retention.Policy{
			DataClass: dataClass, Purpose: "operate the private service", Version: "retention-v1",
			Owner: "privacy", Trigger: "record_created", Duration: 24 * time.Hour,
			DeletionMethod: "hard_delete", HoldBehavior: "scoped_hold",
			MigrationPlan: "forward-only", CustomerImpact: "access ends at deletion",
			EffectiveAt: effectiveAt,
		})
	}
	return result
}
