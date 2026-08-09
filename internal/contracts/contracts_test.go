package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}

func readFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{repositoryRoot(t)}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestOpenAPIContractDeclaresHostedBoundaries(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal(readFile(t, "api", "openapi", "saas-v1.yaml"), &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	if document["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v, want 3.1.0", document["openapi"])
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("paths must be an object")
	}
	for _, path := range []string{
		"/v1/attestations/rights",
		"/v1/memories",
		"/v1/search",
		"/v1/sources/uploads",
		"/v1/operations/{operation_id}",
	} {
		if _, exists := paths[path]; !exists {
			t.Errorf("missing required hosted path %s", path)
		}
	}
	components, ok := document["components"].(map[string]any)
	if !ok {
		t.Fatal("components must be an object")
	}
	securitySchemes, ok := components["securitySchemes"].(map[string]any)
	if !ok || securitySchemes["BearerAuth"] == nil {
		t.Fatal("BearerAuth security scheme is required")
	}
}

func TestEventEnvelopeSchemaRequiresIsolationAndCorrelation(t *testing.T) {
	var schema struct {
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(readFile(t, "api", "events", "v1", "envelope.schema.json"), &schema); err != nil {
		t.Fatalf("parse event schema: %v", err)
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{
		"spec_version", "event_id", "event_type", "occurred_at", "tenant_id",
		"actor", "request_id", "correlation_id", "producer", "subject", "data",
	} {
		if !required[field] {
			t.Errorf("event envelope does not require %q", field)
		}
		if schema.Properties[field] == nil {
			t.Errorf("event envelope does not define %q", field)
		}
	}
	if additional, ok := schema.Properties["data"]["additionalProperties"].(bool); !ok || additional {
		t.Fatal("event data must reject undeclared fields to prevent content leakage")
	}
}

func TestKubernetesReleaseReceiptSchemaIsContentFreeAndComplete(t *testing.T) {
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(readFile(t, "api", "evidence", "v1", "kubernetes-release-receipt.schema.json"), &schema); err != nil {
		t.Fatalf("parse Kubernetes release receipt schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("release receipt must reject undeclared fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{
		"schema", "environment", "namespace", "kubernetes_context", "release_id",
		"started_at", "completed_at", "outcome", "images", "migration", "rollouts",
		"deployments", "rollback",
	} {
		if !required[field] || schema.Properties[field] == nil {
			t.Errorf("release receipt contract must require and define %q", field)
		}
	}
	for _, forbidden := range []string{"secret", "environment_variables", "logs", "token", "payload"} {
		if schema.Properties[forbidden] != nil {
			t.Errorf("release receipt exposes forbidden field %q", forbidden)
		}
	}
}

func TestCompatibilityMapExists(t *testing.T) {
	contents := string(readFile(t, "api", "compatibility.md"))
	for _, marker := range []string{
		"Authentication and tenant selection",
		"Idempotency",
		"Pagination",
		"Error envelope",
		"Long-running operations",
		"Local compatibility map",
	} {
		if !contains(contents, marker) {
			t.Errorf("compatibility contract missing section %q", marker)
		}
	}
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
