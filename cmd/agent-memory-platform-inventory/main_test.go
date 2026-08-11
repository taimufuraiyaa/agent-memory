package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunEmitsContentFreeReadyReport(t *testing.T) {
	path := writeInventory(t, validInventoryJSON)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--inventory", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var report report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Ready || report.Environment != "staging" || report.ComponentCount != 8 || report.FailureDomainCount != 1 || len(report.EnabledExternalIntegrations) != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	for _, forbidden := range []string{"site-a", "admin-a", "platform_operations", "inventory-20260809"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("report leaked inventory detail %q", forbidden)
		}
	}
}

func TestRunRejectsInvalidArgumentsAndInventory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("missing arguments code=%d, want 2", code)
	}

	invalid := strings.Replace(validInventoryJSON, `"kind":"postgres","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":false`, `"kind":"postgres","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":true`, 1)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--inventory", writeInventory(t, invalid)}, &stdout, &stderr); code != 1 {
		t.Fatalf("invalid inventory code=%d, want 1", code)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "site-a") {
		t.Fatalf("invalid inventory output is not content-free: stdout=%q stderr=%q", stdout.String(), stderr.String())
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

const validInventoryJSON = `{"schema":"agent-memory-self-managed-platform-inventory-v1","environment":"staging","inventory_id":"inventory-20260809","generated_at":"2026-08-09T12:00:00Z","administrative_domain_id":"admin-a","site_id":"site-a","failure_domains":[{"id":"fd-a"}],"components":[{"kind":"kubernetes","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":false},{"kind":"identity","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":true},{"kind":"postgres","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":false},{"kind":"object_storage","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":false},{"kind":"queue","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":false},{"kind":"secrets","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":false},{"kind":"observability","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":false},{"kind":"backup","owner_group":"platform_operations","version":"v1","replicas":2,"failure_domain_ids":["fd-a"],"public_ingress":false}],"external_integrations":[{"kind":"payment","enabled":false,"owner_group":"finance"},{"kind":"email","enabled":false,"owner_group":"product_operations"},{"kind":"model","enabled":false,"owner_group":"privacy_security"}]}`
