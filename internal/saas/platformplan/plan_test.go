package platformplan

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

func TestLoadComputesExactReceiptDigest(t *testing.T) {
	inventory := validInventory(platforminventory.Staging)
	contents := validPlanJSON(inventory)
	plan, err := Load(writePlan(t, contents), inventory)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(contents)))
	if plan.ReceiptSHA256 != want {
		t.Fatalf("receipt digest=%q, want %q", plan.ReceiptSHA256, want)
	}
}

func TestCanonicalExampleRemainsValidAndReady(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
	inventory, err := platforminventory.Load(filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Load(filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json"), inventory)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(plan)
	if !assessment.Ready || assessment.CapabilityCount != 21 || assessment.ToolCount != 2 {
		t.Fatalf("unexpected assessment: %+v", assessment)
	}
}

func TestLoadAcceptsCompleteStagingAndProductionPlans(t *testing.T) {
	for _, environment := range []platforminventory.Environment{platforminventory.Staging, platforminventory.Production} {
		t.Run(string(environment), func(t *testing.T) {
			inventory := validInventory(environment)
			plan, err := Load(writePlan(t, validPlanJSON(inventory)), inventory)
			if err != nil {
				t.Fatal(err)
			}
			if !Assess(plan).Ready {
				t.Fatal("safe complete plan was not ready")
			}
		})
	}
}

func TestLoadRejectsUnsafeFileUnknownFieldsAndInventoryMismatch(t *testing.T) {
	inventory := validInventory(platforminventory.Staging)
	path := writePlan(t, validPlanJSON(inventory))
	link := filepath.Join(t.TempDir(), "plan.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link, inventory); err == nil {
		t.Fatal("symlink plan was accepted")
	}

	unknown := strings.Replace(validPlanJSON(inventory), `"schema":`, `"endpoint":"https://private.example","schema":`, 1)
	if _, err := Load(writePlan(t, unknown), inventory); err == nil {
		t.Fatal("unknown or topology-bearing field was accepted")
	}

	mismatch := strings.Replace(validPlanJSON(inventory), `"inventory_id":"inventory-staging"`, `"inventory_id":"inventory-other"`, 1)
	if _, err := Load(writePlan(t, mismatch), inventory); err == nil {
		t.Fatal("inventory mismatch was accepted")
	}

	digestMismatch := strings.Replace(validPlanJSON(inventory), inventory.ReceiptSHA256, strings.Repeat("e", 64), 1)
	if _, err := Load(writePlan(t, digestMismatch), inventory); err == nil {
		t.Fatal("inventory receipt digest mismatch was accepted")
	}
}

func TestLoadRejectsInvalidBindingAndStalePlan(t *testing.T) {
	inventory := validInventory(platforminventory.Staging)
	for name, mutate := range map[string]func(string) string{
		"invalid source digest": func(value string) string {
			return strings.Replace(value, strings.Repeat("a", 64), "not-a-digest", 1)
		},
		"duplicate tool": func(value string) string {
			return strings.Replace(value, `{"name":"helm","version":"v3"}]`, `{"name":"kustomize","version":"v5"}]`, 1)
		},
		"stale plan": func(value string) string {
			return strings.Replace(value, `"generated_at":"2026-08-10T01:00:00Z"`, `"generated_at":"2026-08-09T23:59:59Z"`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writePlan(t, mutate(validPlanJSON(inventory))), inventory); err == nil {
				t.Fatal("invalid plan binding was accepted")
			}
		})
	}
}

func TestLoadRejectsIncompleteDuplicateUnsafeAndUnderRedundantCapabilities(t *testing.T) {
	staging := validInventory(platforminventory.Staging)
	complete := validPlanJSON(staging)
	missing := strings.Replace(complete, `,{"id":"object_backup","action":"no_change","failure_domain_ids":["fd-a"],"public_ingress":false}`, "", 1)
	if _, err := Load(writePlan(t, missing), staging); err == nil {
		t.Fatal("missing capability was accepted")
	}

	duplicate := strings.Replace(complete, `"id":"object_backup"`, `"id":"postgres"`, 1)
	if _, err := Load(writePlan(t, duplicate), staging); err == nil {
		t.Fatal("duplicate capability was accepted")
	}

	publicDatabase := strings.Replace(complete, `"id":"postgres","action":"no_change","failure_domain_ids":["fd-a"],"public_ingress":false`, `"id":"postgres","action":"no_change","failure_domain_ids":["fd-a"],"public_ingress":true`, 1)
	if _, err := Load(writePlan(t, publicDatabase), staging); err == nil {
		t.Fatal("public database ingress was accepted")
	}

	unknownDomain := strings.Replace(complete, `"id":"durable_queue","action":"no_change","failure_domain_ids":["fd-a"]`, `"id":"durable_queue","action":"no_change","failure_domain_ids":["fd-missing"]`, 1)
	if _, err := Load(writePlan(t, unknownDomain), staging); err == nil {
		t.Fatal("unknown failure domain was accepted")
	}

	production := validInventory(platforminventory.Production)
	underRedundant := strings.Replace(validPlanJSON(production), `"id":"postgres","action":"no_change","failure_domain_ids":["fd-a","fd-b"]`, `"id":"postgres","action":"no_change","failure_domain_ids":["fd-a"]`, 1)
	if _, err := Load(writePlan(t, underRedundant), production); err == nil {
		t.Fatal("single-domain production capability was accepted")
	}
}

func TestAssessTreatsReplacementAndDeletionAsUnready(t *testing.T) {
	inventory := validInventory(platforminventory.Staging)
	contents := strings.Replace(validPlanJSON(inventory), `"id":"postgres","action":"no_change"`, `"id":"postgres","action":"replace"`, 1)
	contents = strings.Replace(contents, `"id":"object_backup","action":"no_change"`, `"id":"object_backup","action":"delete"`, 1)
	plan, err := Load(writePlan(t, contents), inventory)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(plan)
	if assessment.Ready || assessment.ActionCounts[ActionReplace] != 1 || assessment.ActionCounts[ActionDelete] != 1 {
		t.Fatalf("unexpected destructive assessment: %+v", assessment)
	}
}

func writePlan(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validInventory(environment platforminventory.Environment) platforminventory.Inventory {
	domains := []platforminventory.FailureDomain{{ID: "fd-a"}}
	if environment == platforminventory.Production {
		domains = append(domains, platforminventory.FailureDomain{ID: "fd-b"})
	}
	return platforminventory.Inventory{
		Schema:         platforminventory.SchemaV1,
		Environment:    environment,
		InventoryID:    "inventory-" + string(environment),
		GeneratedAt:    time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		ReceiptSHA256:  strings.Repeat("f", 64),
		FailureDomains: domains,
	}
}

func validPlanJSON(inventory platforminventory.Inventory) string {
	domains := []string{"fd-a"}
	if inventory.Environment == platforminventory.Production {
		domains = append(domains, "fd-b")
	}
	type capabilityJSON struct {
		ID               string   `json:"id"`
		Action           string   `json:"action"`
		FailureDomainIDs []string `json:"failure_domain_ids"`
		PublicIngress    bool     `json:"public_ingress"`
	}
	capabilities := make([]capabilityJSON, 0, len(requiredCapabilityIDs))
	for _, id := range requiredCapabilityIDs {
		capabilities = append(capabilities, capabilityJSON{
			ID:               string(id),
			Action:           "no_change",
			FailureDomainIDs: append([]string(nil), domains...),
			PublicIngress:    id == CapabilityEdgeIngress || id == CapabilityOIDCIdentity,
		})
	}
	payload := map[string]any{
		"schema":                   SchemaV1,
		"environment":              inventory.Environment,
		"plan_id":                  "plan-20260810",
		"inventory_id":             inventory.InventoryID,
		"inventory_receipt_sha256": inventory.ReceiptSHA256,
		"generated_at":             "2026-08-10T01:00:00Z",
		"source_revision":          "0123456789abcdef0123456789abcdef01234567",
		"source_bundle_sha256":     strings.Repeat("a", 64),
		"raw_plan_sha256":          strings.Repeat("b", 64),
		"toolchain": []map[string]string{
			{"name": "kustomize", "version": "v5"},
			{"name": "helm", "version": "v3"},
		},
		"capabilities": capabilities,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
