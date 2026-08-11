package platformchange

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
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
)

func TestCanonicalExampleRemainsValidAndReady(t *testing.T) {
	root := repositoryRoot(t)
	inventory, err := platforminventory.Load(filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := platformplan.Load(filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json"), inventory)
	if err != nil {
		t.Fatal(err)
	}
	change, err := Load(filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.example.json"), inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(change)
	if !assessment.Ready || assessment.CapabilityCount != 21 || assessment.ResourceCount != 42 {
		t.Fatalf("unexpected canonical assessment: %+v", assessment)
	}
}

func TestLoadComputesExactReceiptDigest(t *testing.T) {
	inventory, plan := validBindings(platforminventory.Staging)
	contents := validChangeJSON(inventory, plan)
	change, err := Load(writeChange(t, contents), inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(contents)))
	if change.ReceiptSHA256 != want {
		t.Fatalf("receipt digest=%q, want %q", change.ReceiptSHA256, want)
	}
}

func TestLoadAcceptsReadyStagingAndProductionReceipts(t *testing.T) {
	for _, environment := range []platforminventory.Environment{platforminventory.Staging, platforminventory.Production} {
		t.Run(string(environment), func(t *testing.T) {
			inventory, plan := validBindings(environment)
			change, err := Load(writeChange(t, validChangeJSON(inventory, plan)), inventory, plan)
			if err != nil {
				t.Fatal(err)
			}
			if !Assess(change).Ready {
				t.Fatal("complete clean change receipt was not ready")
			}
		})
	}
}

func TestLoadRejectsUnsafeFilesUnknownFieldsAndBindingMismatch(t *testing.T) {
	inventory, plan := validBindings(platforminventory.Staging)
	contents := validChangeJSON(inventory, plan)
	path := writeChange(t, contents)
	link := filepath.Join(t.TempDir(), "change.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link, inventory, plan); err == nil {
		t.Fatal("symlink change receipt was accepted")
	}

	unknown := strings.Replace(contents, `"schema":`, `"endpoint":"https://private.example","schema":`, 1)
	if _, err := Load(writeChange(t, unknown), inventory, plan); err == nil {
		t.Fatal("unknown or topology-bearing field was accepted")
	}

	for name, values := range map[string][2]string{
		"inventory digest": {inventory.ReceiptSHA256, strings.Repeat("a", 64)},
		"plan digest":      {plan.ReceiptSHA256, strings.Repeat("b", 64)},
		"plan id":          {plan.PlanID, "plan-other"},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := strings.Replace(contents, values[0], values[1], 1)
			if _, err := Load(writeChange(t, mutated), inventory, plan); err == nil {
				t.Fatal("change receipt binding mismatch was accepted")
			}
		})
	}
}

func TestLoadRejectsDestructivePlan(t *testing.T) {
	inventory, plan := validBindings(platforminventory.Staging)
	plan.Capabilities[5].Action = platformplan.ActionReplace
	if _, err := Load(writeChange(t, validChangeJSON(inventory, plan)), inventory, plan); err == nil {
		t.Fatal("change receipt for an unready destructive plan was accepted")
	}
}

func TestLoadRejectsImpossibleApplyFollowUpAndTimestampStates(t *testing.T) {
	inventory, plan := validBindings(platforminventory.Staging)
	base := validChangeJSON(inventory, plan)
	for name, mutate := range map[string]func(string) string{
		"success with rollback": func(value string) string {
			return strings.Replace(value, `"rollback":{"outcome":"not_required"}`, `"rollback":{"outcome":"succeeded","completed_at":"2026-08-10T02:30:00Z","raw_output_sha256":"`+strings.Repeat("f", 64)+`"}`, 1)
		},
		"success without inventory": func(value string) string {
			return replaceObject(value, "resource_inventory", `{"outcome":"not_collected"}`)
		},
		"success without drift": func(value string) string {
			return replaceObject(value, "drift", `{"outcome":"not_run"}`)
		},
		"stale apply": func(value string) string {
			return strings.Replace(value, `"completed_at":"2026-08-10T02:00:00Z"`, `"completed_at":"2026-08-10T00:30:00Z"`, 1)
		},
		"receipt before drift": func(value string) string {
			return strings.Replace(value, `"generated_at":"2026-08-10T04:00:00Z"`, `"generated_at":"2026-08-10T02:30:00Z"`, 1)
		},
		"drift before resource inventory": func(value string) string {
			return strings.Replace(value, `"checked_at":"2026-08-10T03:30:00Z"`, `"checked_at":"2026-08-10T02:30:00Z"`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeChange(t, mutate(base)), inventory, plan); err == nil {
				t.Fatal("impossible change receipt state was accepted")
			}
		})
	}
}

func TestLoadRejectsMissingDuplicateAndPlanInconsistentCapabilityResults(t *testing.T) {
	inventory, plan := validBindings(platforminventory.Staging)
	base := validChangeJSON(inventory, plan)
	missing := strings.Replace(base, `,{"id":"object_backup","outcome":"unchanged"}`, "", 1)
	if _, err := Load(writeChange(t, missing), inventory, plan); err == nil {
		t.Fatal("missing capability result was accepted")
	}

	duplicate := strings.Replace(base, `"id":"object_backup"`, `"id":"postgres"`, 1)
	if _, err := Load(writeChange(t, duplicate), inventory, plan); err == nil {
		t.Fatal("duplicate capability result was accepted")
	}

	wrong := strings.Replace(base, `"id":"postgres","outcome":"unchanged"`, `"id":"postgres","outcome":"applied"`, 1)
	if _, err := Load(writeChange(t, wrong), inventory, plan); err == nil {
		t.Fatal("capability outcome inconsistent with plan was accepted")
	}
}

func TestAssessAcceptsValidUnreadyDriftAndFailedRollbackStates(t *testing.T) {
	inventory, plan := validBindings(platforminventory.Staging)
	drifted := strings.Replace(validChangeJSON(inventory, plan), `"outcome":"clean"`, `"outcome":"drift_detected"`, 1)
	change, err := Load(writeChange(t, drifted), inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	if Assess(change).Ready {
		t.Fatal("drifted change receipt was ready")
	}

	plan.Capabilities[5].Action = platformplan.ActionUpdate
	failed := validChangeMap(inventory, plan)
	failed["apply"] = map[string]any{
		"outcome": "failed", "completed_at": "2026-08-10T02:00:00Z", "raw_output_sha256": strings.Repeat("c", 64),
	}
	failed["rollback"] = map[string]any{
		"outcome": "succeeded", "completed_at": "2026-08-10T02:30:00Z", "raw_output_sha256": strings.Repeat("f", 64),
	}
	failed["resource_inventory"] = map[string]any{"outcome": "not_collected"}
	failed["drift"] = map[string]any{"outcome": "not_run"}
	results := failed["capabilities"].([]map[string]string)
	for _, result := range results {
		if result["id"] == "postgres" {
			result["outcome"] = "rolled_back"
		}
	}
	encoded, err := json.Marshal(failed)
	if err != nil {
		t.Fatal(err)
	}
	change, err = Load(writeChange(t, string(encoded)), inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(change)
	if assessment.Ready || assessment.ApplyOutcome != ApplyFailed || assessment.RollbackOutcome != RollbackSucceeded {
		t.Fatalf("unexpected failed assessment: %+v", assessment)
	}
}

func writeChange(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "change.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validBindings(environment platforminventory.Environment) (platforminventory.Inventory, platformplan.Plan) {
	inventory := platforminventory.Inventory{
		Schema:        platforminventory.SchemaV1,
		Environment:   environment,
		InventoryID:   "inventory-" + string(environment),
		GeneratedAt:   time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		ReceiptSHA256: strings.Repeat("8", 64),
	}
	ids := []platformplan.CapabilityID{
		platformplan.CapabilityEdgeIngress, platformplan.CapabilityApplicationNetwork,
		platformplan.CapabilityDataNetwork, platformplan.CapabilityKubernetesCluster,
		platformplan.CapabilityOIDCIdentity, platformplan.CapabilityPostgres,
		platformplan.CapabilityQuarantineBucket, platformplan.CapabilityVaultBucket,
		platformplan.CapabilityExportBucket, platformplan.CapabilityDurableQueue,
		platformplan.CapabilityAPIIdentity, platformplan.CapabilityWorkerIdentity,
		platformplan.CapabilityReconcilerIdentity, platformplan.CapabilityMigrationIdentity,
		platformplan.CapabilityAPISecret, platformplan.CapabilityWorkerSecret,
		platformplan.CapabilityReconcilerSecret, platformplan.CapabilityMigrationSecret,
		platformplan.CapabilityTelemetry, platformplan.CapabilityPostgresBackup,
		platformplan.CapabilityObjectBackup,
	}
	capabilities := make([]platformplan.Capability, 0, len(ids))
	for _, id := range ids {
		capabilities = append(capabilities, platformplan.Capability{ID: id, Action: platformplan.ActionNoChange})
	}
	plan := platformplan.Plan{
		Schema:                 platformplan.SchemaV1,
		Environment:            environment,
		PlanID:                 "plan-20260810",
		InventoryID:            inventory.InventoryID,
		InventoryReceiptSHA256: inventory.ReceiptSHA256,
		GeneratedAt:            time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC),
		SourceRevision:         "0123456789abcdef0123456789abcdef01234567",
		SourceBundleSHA256:     strings.Repeat("a", 64),
		RawPlanSHA256:          strings.Repeat("b", 64),
		Capabilities:           capabilities,
		ReceiptSHA256:          strings.Repeat("9", 64),
	}
	return inventory, plan
}

func validChangeJSON(inventory platforminventory.Inventory, plan platformplan.Plan) string {
	encoded, err := json.Marshal(validChangeMap(inventory, plan))
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func validChangeMap(inventory platforminventory.Inventory, plan platformplan.Plan) map[string]any {
	results := make([]map[string]string, 0, len(plan.Capabilities))
	for _, capability := range plan.Capabilities {
		outcome := "unchanged"
		if capability.Action == platformplan.ActionCreate || capability.Action == platformplan.ActionUpdate {
			outcome = "applied"
		}
		results = append(results, map[string]string{"id": string(capability.ID), "outcome": outcome})
	}
	return map[string]any{
		"schema":                   SchemaV1,
		"environment":              inventory.Environment,
		"change_id":                "change-20260810",
		"inventory_id":             inventory.InventoryID,
		"inventory_receipt_sha256": inventory.ReceiptSHA256,
		"plan_id":                  plan.PlanID,
		"plan_receipt_sha256":      plan.ReceiptSHA256,
		"generated_at":             "2026-08-10T04:00:00Z",
		"apply": map[string]any{
			"outcome": "succeeded", "completed_at": "2026-08-10T02:00:00Z", "raw_output_sha256": strings.Repeat("c", 64),
		},
		"rollback": map[string]any{"outcome": "not_required"},
		"resource_inventory": map[string]any{
			"outcome": "collected", "collected_at": "2026-08-10T03:00:00Z", "sha256": strings.Repeat("d", 64), "resource_count": 42,
		},
		"drift": map[string]any{
			"outcome": "clean", "checked_at": "2026-08-10T03:30:00Z", "raw_output_sha256": strings.Repeat("e", 64),
		},
		"capabilities": results,
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}

func replaceObject(value, key, replacement string) string {
	start := strings.Index(value, `"`+key+`":{`)
	if start < 0 {
		return value
	}
	objectStart := strings.Index(value[start:], "{") + start
	depth := 0
	for index := objectStart; index < len(value); index++ {
		switch value[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return value[:objectStart] + replacement + value[index+1:]
			}
		}
	}
	return value
}
