package platforminventory

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadComputesExactReceiptDigest(t *testing.T) {
	contents := validInventory("staging")
	inventory, err := Load(writeInventory(t, contents))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(contents)))
	if inventory.ReceiptSHA256 != want {
		t.Fatalf("receipt digest=%q, want %q", inventory.ReceiptSHA256, want)
	}
}

func TestCanonicalExampleRemainsValid(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
	inventory, err := Load(filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Environment != Staging {
		t.Fatalf("example environment=%q, want staging", inventory.Environment)
	}
}

func TestLoadAcceptsContentFreeStagingAndProductionInventories(t *testing.T) {
	for _, environment := range []string{"staging", "production"} {
		t.Run(environment, func(t *testing.T) {
			path := writeInventory(t, validInventory(environment))
			inventory, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if inventory.Environment != Environment(environment) || len(inventory.Components) != len(requiredComponentKinds) {
				t.Fatalf("unexpected inventory: %+v", inventory)
			}
		})
	}
}

func TestLoadRejectsUnsafeFilesAndUnknownFields(t *testing.T) {
	path := writeInventory(t, validInventory("staging"))
	link := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("symlink inventory was accepted")
	}

	unknown := strings.Replace(validInventory("staging"), `"schema":`, `"unknown":true,"schema":`, 1)
	if _, err := Load(writeInventory(t, unknown)); err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestLoadRejectsMissingDuplicateAndUnknownComponents(t *testing.T) {
	missing := strings.Replace(validInventory("staging"), componentJSON("kubernetes", `["fd-a"]`, false)+",", "", 1)
	if _, err := Load(writeInventory(t, missing)); err == nil {
		t.Fatal("missing component was accepted")
	}

	duplicate := strings.Replace(validInventory("staging"), componentJSON("backup", `["fd-a"]`, false), componentJSON("postgres", `["fd-a"]`, false), 1)
	if _, err := Load(writeInventory(t, duplicate)); err == nil {
		t.Fatal("duplicate component was accepted")
	}

	unknown := strings.Replace(validInventory("staging"), `"kind":"backup"`, `"kind":"cdn"`, 1)
	if _, err := Load(writeInventory(t, unknown)); err == nil {
		t.Fatal("unknown component was accepted")
	}
}

func TestLoadRejectsUnsafeIngressAndFailureDomainClaims(t *testing.T) {
	publicPostgres := strings.Replace(validInventory("staging"), componentJSON("postgres", `["fd-a"]`, false), componentJSON("postgres", `["fd-a"]`, true), 1)
	if _, err := Load(writeInventory(t, publicPostgres)); err == nil {
		t.Fatal("public PostgreSQL ingress was accepted")
	}

	unknownDomain := strings.Replace(validInventory("staging"), componentJSON("queue", `["fd-a"]`, false), componentJSON("queue", `["fd-missing"]`, false), 1)
	if _, err := Load(writeInventory(t, unknownDomain)); err == nil {
		t.Fatal("undeclared failure domain was accepted")
	}

	oneDomainProduction := strings.ReplaceAll(validInventory("production"), `{"id":"fd-a"},{"id":"fd-b"}`, `{"id":"fd-a"}`)
	oneDomainProduction = strings.ReplaceAll(oneDomainProduction, `["fd-a","fd-b"]`, `["fd-a"]`)
	if _, err := Load(writeInventory(t, oneDomainProduction)); err == nil {
		t.Fatal("single-domain production inventory was accepted")
	}

	nonRedundantPostgres := strings.Replace(validInventory("production"), componentJSON("postgres", `["fd-a","fd-b"]`, false), componentJSON("postgres", `["fd-a"]`, false), 1)
	if _, err := Load(writeInventory(t, nonRedundantPostgres)); err == nil {
		t.Fatal("single-domain production PostgreSQL was accepted")
	}
}

func writeInventory(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validInventory(environment string) string {
	domains := `[{"id":"fd-a"}]`
	componentDomains := `["fd-a"]`
	if environment == "production" {
		domains = `[{"id":"fd-a"},{"id":"fd-b"}]`
		componentDomains = `["fd-a","fd-b"]`
	}
	components := make([]string, 0, len(requiredComponentKinds))
	for _, kind := range requiredComponentKinds {
		components = append(components, componentJSON(string(kind), componentDomains, kind == ComponentIdentity))
	}
	return `{"schema":"agent-memory-self-managed-platform-inventory-v1","environment":"` + environment + `","inventory_id":"inventory-20260809","generated_at":"2026-08-09T12:00:00Z","administrative_domain_id":"admin-a","site_id":"site-a","failure_domains":` + domains + `,"components":[` + strings.Join(components, ",") + `],"external_integrations":[{"kind":"payment","enabled":false,"owner_group":"finance"},{"kind":"email","enabled":false,"owner_group":"product_operations"},{"kind":"model","enabled":false,"owner_group":"privacy_security"}]}`
}

func componentJSON(kind, domains string, public bool) string {
	return `{"kind":"` + kind + `","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":` + domains + `,"public_ingress":` + map[bool]string{true: "true", false: "false"}[public] + `}`
}
