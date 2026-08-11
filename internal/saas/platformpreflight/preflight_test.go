package platformpreflight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

func TestEvaluateAcceptsCompleteContentFreeSnapshot(t *testing.T) {
	receipt, err := Evaluate(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Environment != platforminventory.Staging || receipt.Namespace != "agent-memory-staging" || receipt.InventoryReceiptSHA256 != strings.Repeat("a", 64) || len(receipt.Checks) != len(requiredCheckIDs) {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	for _, check := range receipt.Checks {
		if check.Outcome != OutcomePassed {
			t.Fatalf("check %s outcome=%s, want passed", check.ID, check.Outcome)
		}
	}
}

func TestEvaluateEmitsFixedFailedChecksForDrift(t *testing.T) {
	tests := map[string]func(*Snapshot){
		"namespace":        func(value *Snapshot) { value.NamespaceExists = false },
		"service_accounts": func(value *Snapshot) { delete(value.ServiceAccounts, "agent-memory-worker") },
		"secret_contracts": func(value *Snapshot) { delete(value.Secrets, "agent-memory-api-secrets") },
		"network_policy":   func(value *Snapshot) { delete(value.NetworkPolicies, "default-deny") },
		"private_service":  func(value *Snapshot) { value.ServiceTypes["agent-memory-api"] = "LoadBalancer" },
		"workload_identity": func(value *Snapshot) {
			workload := value.Workloads["agent-memory-api"]
			workload.ServiceAccount = "default"
			value.Workloads["agent-memory-api"] = workload
		},
		"immutable_images": func(value *Snapshot) {
			workload := value.Workloads["agent-memory-api"]
			workload.Image = "agent-memory-api:latest"
			value.Workloads["agent-memory-api"] = workload
		},
		"ready_workloads": func(value *Snapshot) {
			workload := value.Workloads["agent-memory-api"]
			workload.ReadyReplicas = 1
			value.Workloads["agent-memory-api"] = workload
		},
	}
	for checkID, mutate := range tests {
		t.Run(checkID, func(t *testing.T) {
			snapshot := validSnapshot()
			mutate(&snapshot)
			receipt, err := Evaluate(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Ready || outcomeFor(receipt, checkID) != OutcomeFailed {
				t.Fatalf("receipt did not fail only through fixed check %q: %+v", checkID, receipt)
			}
		})
	}
}

func TestEvaluateRejectsMismatchedOrUnsafeIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*Snapshot){
		"environment namespace mismatch": func(value *Snapshot) { value.Namespace = "agent-memory-production" },
		"unknown environment":            func(value *Snapshot) { value.Environment = "development" },
		"blank context":                  func(value *Snapshot) { value.KubernetesContext = "" },
		"unsafe inventory locator":       func(value *Snapshot) { value.InventoryID = "customer@example.com" },
		"missing inventory binding":      func(value *Snapshot) { value.InventoryReceiptSHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := validSnapshot()
			mutate(&snapshot)
			if _, err := Evaluate(snapshot); err == nil {
				t.Fatal("unsafe snapshot was accepted")
			}
		})
	}
}

func TestLoadRejectsSubstitutedOrContradictoryReceipt(t *testing.T) {
	receipt, err := Evaluate(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	inventory := platforminventory.Inventory{Environment: platforminventory.Staging, InventoryID: receipt.InventoryID, ReceiptSHA256: receipt.InventoryReceiptSHA256}
	loaded, err := Load(path, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Ready || loaded.ReceiptSHA256 == "" || loaded.InventoryReceiptSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("loaded receipt=%+v", loaded)
	}

	for name, mutate := range map[string]func(*Receipt){
		"malformed inventory digest":   func(value *Receipt) { value.InventoryReceiptSHA256 = "bad" },
		"substituted inventory digest": func(value *Receipt) { value.InventoryReceiptSHA256 = strings.Repeat("b", 64) },
		"missing check":                func(value *Receipt) { value.Checks = value.Checks[:7] },
		"duplicate check":              func(value *Receipt) { value.Checks[7].ID = value.Checks[0].ID },
		"non canonical order":          func(value *Receipt) { value.Checks[0], value.Checks[1] = value.Checks[1], value.Checks[0] },
		"contradictory readiness":      func(value *Receipt) { value.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := receipt
			candidate.Checks = append([]Check(nil), receipt.Checks...)
			mutate(&candidate)
			candidatePath := filepath.Join(t.TempDir(), "candidate.json")
			contents, marshalErr := json.Marshal(candidate)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if writeErr := os.WriteFile(candidatePath, contents, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if _, loadErr := Load(candidatePath, inventory); loadErr == nil {
				t.Fatal("invalid preflight receipt was accepted")
			}
		})
	}
}

func TestLoadRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	receipt, err := Evaluate(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	inventory := platforminventory.Inventory{Environment: platforminventory.Staging, InventoryID: receipt.InventoryID, ReceiptSHA256: receipt.InventoryReceiptSHA256}
	contents, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(t.TempDir(), "unknown.json")
	contents = append(contents[:len(contents)-1], []byte(`,"unknown":true}`)...)
	if err := os.WriteFile(unknown, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(unknown, inventory); err == nil {
		t.Fatal("unknown receipt field was accepted")
	}

	valid := filepath.Join(t.TempDir(), "valid.json")
	if err := Publish(valid, receipt); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "receipt-link.json")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link, inventory); err == nil {
		t.Fatal("symlink receipt was accepted")
	}
}

func TestPublishIsAtomicPrivateAndCreateOnly(t *testing.T) {
	receipt, err := Evaluate(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode=%o, want 600", info.Mode().Perm())
	}
	if err := Publish(path, receipt); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, receipt); err == nil {
		t.Fatal("symlink receipt destination was accepted")
	}
}

func outcomeFor(receipt Receipt, id string) Outcome {
	for _, check := range receipt.Checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}

func validSnapshot() Snapshot {
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	return Snapshot{
		Environment:            platforminventory.Staging,
		KubernetesContext:      "kind-agent-memory",
		Namespace:              "agent-memory-staging",
		InventoryID:            "inventory-20260810",
		InventoryReceiptSHA256: strings.Repeat("a", 64),
		CollectedAt:            time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		NamespaceExists:        true,
		ServiceAccounts: map[string]bool{
			"agent-memory-api": true, "agent-memory-worker": true,
			"agent-memory-reconciler": true, "agent-memory-migration": true,
		},
		Secrets: map[string]bool{
			"agent-memory-api-secrets": true, "agent-memory-worker-secrets": true,
			"agent-memory-reconciler-secrets": true, "agent-memory-migration-secrets": true,
		},
		NetworkPolicies: map[string]bool{
			"default-deny": true, "allow-api-edge-ingress": true,
			"allow-dns-and-managed-services": true, "allow-observability-scrape": true,
		},
		ServiceTypes: map[string]string{"agent-memory-api": "ClusterIP"},
		Workloads: map[string]Workload{
			"agent-memory-api":        {ServiceAccount: "agent-memory-api", Image: "registry.local/agent-memory-api@" + digest, DesiredReplicas: 2, ReadyReplicas: 2},
			"agent-memory-worker":     {ServiceAccount: "agent-memory-worker", Image: "registry.local/agent-memory-worker@" + digest, DesiredReplicas: 2, ReadyReplicas: 2},
			"agent-memory-reconciler": {ServiceAccount: "agent-memory-reconciler", Image: "registry.local/agent-memory-reconciler@" + digest, DesiredReplicas: 1, ReadyReplicas: 1},
		},
	}
}
