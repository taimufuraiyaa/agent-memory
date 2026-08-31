package contracts_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func unsafePathBackedEvidenceReaderFunctions(contents []byte) []string {
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "evidence.go", contents, 0)
	if err != nil {
		return []string{"<parse-error>"}
	}
	var unsafe []string
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		start := fileSet.Position(function.Body.Pos()).Offset
		end := fileSet.Position(function.Body.End()).Offset
		body := contents[start:end]
		isPathBackedReader := bytes.Contains(body, []byte("os.Lstat(path)")) &&
			bytes.Contains(body, []byte("os.Open(")) &&
			bytes.Contains(body, []byte("io.LimitReader"))
		if !isPathBackedReader {
			continue
		}
		usesSharedRevalidation := bytes.Contains(body, []byte("validateUnchangedOpenedPath("))
		missingLocalRevalidation := !usesSharedRevalidation &&
			(bytes.Count(body, []byte(".Stat()")) < 2 || bytes.Count(body, []byte(".ModTime()")) < 6)
		if bytes.Contains(body, []byte("os.Stat(path)")) || missingLocalRevalidation {
			unsafe = append(unsafe, function.Name.Name)
		}
	}
	return unsafe
}

func TestUnsafePathBackedEvidenceReaderFunctions(t *testing.T) {
	unsafeSource := []byte(`package evidence
func decode(path string) {
	validated, _ := os.Lstat(path)
	file, _ := os.Open(path)
	opened, _ := file.Stat()
	_, _ = io.ReadAll(io.LimitReader(file, 10))
	pathAfterRead, _ := os.Lstat(path)
	_, _ = validated, pathAfterRead
}`)
	if got := unsafePathBackedEvidenceReaderFunctions(unsafeSource); len(got) != 1 || got[0] != "decode" {
		t.Fatalf("unsafe functions = %v, want [decode]", got)
	}

	safeSource := []byte(`package evidence
func decode(path string) {
	validated, _ := os.Lstat(path)
	file, _ := os.Open(path)
	opened, _ := file.Stat()
	_, _ = io.ReadAll(io.LimitReader(file, 10))
	openedAfterRead, _ := file.Stat()
	pathAfterRead, _ := os.Lstat(path)
	_ = opened.ModTime().Equal(validated.ModTime())
	_ = openedAfterRead.ModTime().Equal(opened.ModTime())
	_ = pathAfterRead.ModTime().Equal(opened.ModTime())
}`)
	if got := unsafePathBackedEvidenceReaderFunctions(safeSource); len(got) != 0 {
		t.Fatalf("safe functions reported unsafe: %v", got)
	}
}

func TestSaaSEvidenceReadersUseNonFollowingPostReadPathChecks(t *testing.T) {
	var unsafe []string
	for _, root := range []string{
		filepath.Join(repositoryRoot(t), "internal", "saas"),
		filepath.Join(repositoryRoot(t), "cmd"),
	} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, function := range unsafePathBackedEvidenceReaderFunctions(contents) {
				relative := strings.TrimPrefix(path, repositoryRoot(t)+string(filepath.Separator))
				unsafe = append(unsafe, relative+":"+function)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(unsafe) != 0 {
		t.Fatalf("production SaaS evidence readers must revalidate the opened descriptor and non-following path metadata after reading: %s", strings.Join(unsafe, ", "))
	}
}

func TestSaaSEvidencePublishersUseDescriptorRootedDirectories(t *testing.T) {
	var unsafe []string
	for _, root := range []string{
		filepath.Join(repositoryRoot(t), "internal", "saas"),
		filepath.Join(repositoryRoot(t), "cmd"),
	} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			legacyPublisher := bytes.Contains(contents, []byte("os.Stat(directory)")) ||
				bytes.Contains(contents, []byte("os.CreateTemp(")) ||
				bytes.Contains(contents, []byte("os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL"))
			if legacyPublisher {
				relative := strings.TrimPrefix(path, repositoryRoot(t)+string(filepath.Separator))
				unsafe = append(unsafe, relative)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(unsafe) != 0 {
		t.Fatalf("production SaaS evidence publishers must anchor writes to a non-symlink directory descriptor: %s", strings.Join(unsafe, ", "))
	}
}

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
		"/v1/source-statuses",
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

func TestHostedMemorySearchContractUsesClosedAllowlistedResults(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal(readFile(t, "api", "openapi", "saas-v1.yaml"), &document); err != nil {
		t.Fatal(err)
	}
	components, ok := document["components"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI components must be an object")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI schemas must be an object")
	}
	item, ok := schemas["MemorySearchItem"].(map[string]any)
	if !ok || item["additionalProperties"] != false {
		t.Fatal("MemorySearchItem must be a closed object")
	}
	properties, ok := item["properties"].(map[string]any)
	if !ok {
		t.Fatal("MemorySearchItem properties must be an object")
	}
	for _, required := range []string{"id", "workspace_id", "type", "content", "source_kind", "entities", "tags", "keywords", "confidence", "storage_tier", "created_at", "updated_at", "score"} {
		if properties[required] == nil {
			t.Errorf("MemorySearchItem missing %q", required)
		}
	}
	for _, forbidden := range []string{"tenant_id", "source", "file_path", "note_path", "session_id", "content_hash", "request_hash", "idempotency_key"} {
		if properties[forbidden] != nil {
			t.Errorf("MemorySearchItem exposes forbidden field %q", forbidden)
		}
	}
}

func TestPrivacyRetentionContractExposesReviewablePurpose(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal(readFile(t, "api", "openapi", "saas-v1.yaml"), &document); err != nil {
		t.Fatal(err)
	}
	components := document["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	policy, ok := schemas["RetainedClass"].(map[string]any)
	if !ok || policy["additionalProperties"] != false {
		t.Fatal("RetainedClass must be a closed object")
	}
	properties := policy["properties"].(map[string]any)
	for _, required := range []string{"data_class", "purpose", "policy_version", "owner", "trigger", "duration_seconds", "deletion_method", "hold_behavior", "customer_impact"} {
		if properties[required] == nil {
			t.Errorf("RetainedClass missing %q", required)
		}
	}
	for _, forbidden := range []string{"connection_url", "database", "tenant_id", "path", "sql", "content"} {
		if properties[forbidden] != nil {
			t.Errorf("RetainedClass exposes forbidden field %q", forbidden)
		}
	}
	paths := document["paths"].(map[string]any)
	privacyPath := paths["/v1/privacy"].(map[string]any)
	get := privacyPath["get"].(map[string]any)
	responses := get["responses"].(map[string]any)
	okResponse := responses["200"].(map[string]any)
	content := okResponse["content"].(map[string]any)
	media := content["application/json"].(map[string]any)
	responseSchema := media["schema"].(map[string]any)
	if responseSchema["$ref"] != "#/components/schemas/PrivacyOverviewEnvelope" {
		t.Fatal("privacy endpoint must reference the typed privacy overview envelope")
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

func TestSelfManagedPlatformInventorySchemaIsStrictAndComplete(t *testing.T) {
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Required             []string
		Properties           map[string]json.RawMessage
	}
	if err := json.Unmarshal(readFile(t, "api", "evidence", "v1", "self-managed-platform-inventory.schema.json"), &schema); err != nil {
		t.Fatalf("parse platform inventory schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("platform inventory schema must reject unknown fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{"environment", "administrative_domain_id", "site_id", "failure_domains", "components", "external_integrations"} {
		if !required[field] || schema.Properties[field] == nil {
			t.Errorf("platform inventory schema must require and define %q", field)
		}
	}
	for _, forbidden := range []string{"endpoint", "address", "credential", "secret", "customer", "content", "owner_name"} {
		if schema.Properties[forbidden] != nil {
			t.Errorf("platform inventory schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestKubernetesPlatformPreflightReceiptSchemaIsContentFreeAndComplete(t *testing.T) {
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(readFile(t, "api", "evidence", "v1", "kubernetes-platform-preflight-receipt.schema.json"), &schema); err != nil {
		t.Fatalf("parse Kubernetes platform preflight schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("platform preflight receipt must reject unknown fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{"environment", "kubernetes_context", "namespace", "inventory_id", "inventory_receipt_sha256", "collected_at", "checks"} {
		if !required[field] || schema.Properties[field] == nil {
			t.Errorf("platform preflight receipt must require and define %q", field)
		}
	}
	for _, forbidden := range []string{"secrets", "configmaps", "environment_variables", "logs", "events", "pods", "payload", "endpoint", "address"} {
		if schema.Properties[forbidden] != nil {
			t.Errorf("platform preflight receipt exposes forbidden field %q", forbidden)
		}
	}
}

func TestSelfManagedInfrastructurePlanSchemaIsContentFreeAndComplete(t *testing.T) {
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(readFile(t, "api", "evidence", "v1", "self-managed-infrastructure-plan.schema.json"), &schema); err != nil {
		t.Fatalf("parse self-managed infrastructure plan schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("infrastructure plan schema must reject unknown fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{"environment", "plan_id", "inventory_id", "inventory_receipt_sha256", "source_revision", "source_bundle_sha256", "raw_plan_sha256", "toolchain", "capabilities"} {
		if !required[field] || schema.Properties[field] == nil {
			t.Errorf("infrastructure plan schema must require and define %q", field)
		}
	}
	var capability struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema.Defs["capability"], &capability); err != nil {
		t.Fatalf("parse infrastructure capability schema: %v", err)
	}
	if capability.AdditionalProperties || len(capability.Properties.ID.Enum) != 21 {
		t.Fatalf("infrastructure capability schema must define exactly 21 closed IDs, got %d", len(capability.Properties.ID.Enum))
	}
	for _, forbidden := range []string{"endpoint", "address", "credential", "secret_value", "backend", "command", "path", "owner_name", "customer", "content", "raw_plan"} {
		if schema.Properties[forbidden] != nil {
			t.Errorf("infrastructure plan schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestSelfManagedInfrastructureChangeSchemaIsContentFreeAndComplete(t *testing.T) {
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(readFile(t, "api", "evidence", "v1", "self-managed-infrastructure-change.schema.json"), &schema); err != nil {
		t.Fatalf("parse self-managed infrastructure change schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("infrastructure change schema must reject unknown fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{"environment", "change_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "apply", "rollback", "resource_inventory", "drift", "capabilities"} {
		if !required[field] || schema.Properties[field] == nil {
			t.Errorf("infrastructure change schema must require and define %q", field)
		}
	}
	var capability struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema.Defs["capability_result"], &capability); err != nil {
		t.Fatalf("parse infrastructure change capability schema: %v", err)
	}
	if capability.AdditionalProperties || len(capability.Properties.ID.Enum) != 21 {
		t.Fatalf("infrastructure change schema must define exactly 21 closed capability IDs, got %d", len(capability.Properties.ID.Enum))
	}
	for _, forbidden := range []string{"endpoint", "address", "credential", "secret_value", "backend", "command", "path", "owner_name", "customer", "content", "raw_output", "resources"} {
		if schema.Properties[forbidden] != nil {
			t.Errorf("infrastructure change schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestProductionPrivateAuthorityExposureSchemaIsContentFreeAndComplete(t *testing.T) {
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(readFile(t, "api", "evidence", "v1", "production-private-authority-exposure.schema.json"), &schema); err != nil {
		t.Fatalf("parse production exposure schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("production exposure schema must reject unknown fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{"environment", "exposure_id", "inventory_id", "inventory_receipt_sha256", "change_id", "change_receipt_sha256", "firewall_export_sha256", "scan", "targets"} {
		if !required[field] || schema.Properties[field] == nil {
			t.Errorf("production exposure schema must require and define %q", field)
		}
	}
	var target struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
			Outcome struct {
				Enum []string `json:"enum"`
			} `json:"outcome"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema.Defs["target"], &target); err != nil {
		t.Fatalf("parse production exposure target schema: %v", err)
	}
	if target.AdditionalProperties || len(target.Properties.ID.Enum) != 7 || len(target.Properties.Outcome.Enum) != 3 {
		t.Fatalf("production exposure schema must close 7 targets and 3 outcomes, got %d/%d", len(target.Properties.ID.Enum), len(target.Properties.Outcome.Enum))
	}
	for _, forbidden := range []string{"endpoint", "address", "port", "credential", "command", "path", "origin", "owner_name", "reviewed", "approved", "independent", "customer", "content", "raw_output"} {
		if schema.Properties[forbidden] != nil {
			t.Errorf("production exposure schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestStagingRollbackVerificationSchemaIsContentFreeAndComplete(t *testing.T) {
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(readFile(t, "api", "evidence", "v1", "staging-rollback-verification-receipt.schema.json"), &schema); err != nil {
		t.Fatalf("parse staging rollback verification schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("staging rollback verification schema must reject unknown fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{"environment", "namespace", "kubernetes_context", "baseline_release_id", "baseline_receipt_sha256", "failed_attempt_release_id", "failed_attempt_receipt_sha256", "collected_at", "deployments"} {
		if !required[field] || schema.Properties[field] == nil {
			t.Errorf("staging rollback verification schema must require and define %q", field)
		}
	}
	var deployment struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			Name struct {
				Enum []string `json:"enum"`
			} `json:"name"`
			Outcome struct {
				Enum []string `json:"enum"`
			} `json:"outcome"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema.Defs["deployment"], &deployment); err != nil {
		t.Fatalf("parse staging rollback deployment schema: %v", err)
	}
	if deployment.AdditionalProperties || len(deployment.Properties.Name.Enum) != 3 || len(deployment.Properties.Outcome.Enum) != 4 {
		t.Fatalf("rollback schema must close 3 deployments and 4 outcomes, got %d/%d", len(deployment.Properties.Name.Enum), len(deployment.Properties.Outcome.Enum))
	}
	for _, forbidden := range []string{"image", "secret", "configmap", "pod", "log", "event", "environment_variables", "token", "customer", "content", "raw_output"} {
		if schema.Properties[forbidden] != nil {
			t.Errorf("staging rollback verification schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestStagingEdgeTelemetrySchemaIsContentFreeAndComplete(t *testing.T) {
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(readFile(t, "api", "evidence", "v1", "staging-edge-telemetry-receipt.schema.json"), &schema); err != nil {
		t.Fatalf("parse staging edge telemetry schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("staging edge telemetry schema must reject unknown fields")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range []string{"environment", "release_id", "release_receipt_sha256", "request_id", "trace_id", "started_at", "completed_at", "checks"} {
		if !required[field] || schema.Properties[field] == nil {
			t.Errorf("staging edge telemetry schema must require and define %q", field)
		}
	}
	var check struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
			Outcome struct {
				Enum []string `json:"enum"`
			} `json:"outcome"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema.Defs["check"], &check); err != nil {
		t.Fatalf("parse staging edge telemetry check schema: %v", err)
	}
	if check.AdditionalProperties || len(check.Properties.ID.Enum) != 3 || len(check.Properties.Outcome.Enum) != 2 {
		t.Fatalf("edge telemetry schema must close 3 checks and 2 outcomes, got %d/%d", len(check.Properties.ID.Enum), len(check.Properties.Outcome.Enum))
	}
	for _, forbidden := range []string{"url", "endpoint", "body", "payload", "tenant", "actor", "source", "credential", "authorization", "log", "span", "raw_output"} {
		if schema.Properties[forbidden] != nil {
			t.Errorf("staging edge telemetry schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestStagingClientJourneySchemasAreClosedAndContentFree(t *testing.T) {
	inputBytes := readFile(t, "api", "evidence", "v1", "staging-client-journey.schema.json")
	bundleBytes := readFile(t, "api", "evidence", "v1", "staging-client-journey-bundle.schema.json")
	for name, contents := range map[string][]byte{"input": inputBytes, "bundle": bundleBytes} {
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Required             []string                   `json:"required"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse staging journey %s schema: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("staging journey %s schema must reject unknown fields", name)
		}
		for _, field := range []string{"environment", "release_id", "release_receipt_sha256"} {
			if schema.Properties[field] == nil {
				t.Errorf("staging journey %s schema missing %q", name, field)
			}
		}
		var check struct {
			AdditionalProperties bool `json:"additionalProperties"`
			Properties           struct {
				ID struct {
					Enum []string `json:"enum"`
				} `json:"id"`
				Outcome struct {
					Enum []string `json:"enum"`
				} `json:"outcome"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schema.Defs["check"], &check); err != nil {
			t.Fatalf("parse staging journey %s check: %v", name, err)
		}
		if check.AdditionalProperties || len(check.Properties.ID.Enum) != 5 || len(check.Properties.Outcome.Enum) != 2 {
			t.Errorf("staging journey %s schema must close five checks and two outcomes", name)
		}
		for _, forbidden := range []string{"token", "cookie", "tenant_id", "account_id", "workspace_id", "memory_id", "export_id", "credential_id", "content", "query", "result", "url", "header", "browser_storage", "raw_audit", "raw_telemetry", "log"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("staging journey %s schema exposes forbidden field %q", name, forbidden)
			}
		}
	}
}

func TestSelfManagedPostgresRestoreSchemasAreClosedAndContentFree(t *testing.T) {
	inputBytes := readFile(t, "api", "evidence", "v1", "self-managed-postgres-restore-drill.schema.json")
	receiptBytes := readFile(t, "api", "evidence", "v1", "self-managed-postgres-restore-receipt.schema.json")
	for name, contents := range map[string][]byte{"input": inputBytes, "receipt": receiptBytes} {
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse PostgreSQL restore %s schema: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("PostgreSQL restore %s schema must reject unknown fields", name)
		}
		for _, field := range []string{"environment", "inventory_id", "inventory_receipt_sha256", "change_id", "change_receipt_sha256", "backup", "timeline", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("PostgreSQL restore %s schema missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"credential", "password", "token", "endpoint", "database_name", "tenant_id", "object_path", "backup_path", "sql", "row_content", "log", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("PostgreSQL restore %s schema exposes forbidden field %q", name, forbidden)
			}
		}
	}
	var input struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		t.Fatal(err)
	}
	var check struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
			Outcome struct {
				Enum []string `json:"enum"`
			} `json:"outcome"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(input.Defs["check"], &check); err != nil {
		t.Fatal(err)
	}
	if check.AdditionalProperties || len(check.Properties.ID.Enum) != 10 || len(check.Properties.Outcome.Enum) != 2 {
		t.Fatalf("PostgreSQL restore checks must close ten checks and two outcomes")
	}
}

func TestStagingFormatIngestionSchemasAreClosedAndContentFree(t *testing.T) {
	inputBytes := readFile(t, "api", "evidence", "v1", "staging-format-ingestion.schema.json")
	receiptBytes := readFile(t, "api", "evidence", "v1", "staging-format-ingestion-receipt.schema.json")
	for name, contents := range map[string][]byte{"input": inputBytes, "receipt": receiptBytes} {
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse staging format %s schema: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("staging format %s schema must reject unknown fields", name)
		}
		for _, field := range []string{"environment", "release_id", "release_receipt_sha256", "ready", "generated_at", "runs"} {
			if schema.Properties[field] == nil {
				t.Errorf("staging format %s schema missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"filename", "title", "checksum", "tenant_id", "account_id", "workspace_id", "object_key", "path", "url", "credential", "authorization", "header", "query", "result", "log", "raw_audit", "raw_job", "raw_projection", "source_bytes", "extracted_text"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("staging format %s schema exposes forbidden field %q", name, forbidden)
			}
		}
	}
	var input struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		t.Fatal(err)
	}
	var check struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
			Outcome struct {
				Enum []string `json:"enum"`
			} `json:"outcome"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(input.Defs["check"], &check); err != nil {
		t.Fatal(err)
	}
	if check.AdditionalProperties || len(check.Properties.ID.Enum) != 7 || len(check.Properties.Outcome.Enum) != 2 {
		t.Fatalf("staging format schema must close seven checks and two outcomes")
	}
	var run struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			Format struct {
				Enum []string `json:"enum"`
			} `json:"format"`
			MediaType struct {
				Enum []string `json:"enum"`
			} `json:"media_type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(input.Defs["run"], &run); err != nil {
		t.Fatal(err)
	}
	if run.AdditionalProperties || len(run.Properties.Format.Enum) != 4 || len(run.Properties.MediaType.Enum) != 4 {
		t.Fatalf("staging format schema must close four format/media types")
	}
}

func TestSelfManagedRetentionInventorySchemaIsClosedAndContentFree(t *testing.T) {
	contents := readFile(t, "api", "evidence", "v1", "self-managed-retention-inventory.schema.json")
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatalf("parse retention inventory schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("retention inventory schema must reject unknown fields")
	}
	for _, field := range []string{"classification", "environment", "inventory_id", "inventory_receipt_sha256", "change_id", "change_receipt_sha256", "collected_at", "policy_count", "policies_sha256", "policies"} {
		if schema.Properties[field] == nil {
			t.Errorf("retention inventory schema missing %q", field)
		}
	}
	var policy struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema.Defs["policy"], &policy); err != nil {
		t.Fatal(err)
	}
	if policy.AdditionalProperties || len(policy.Properties) != 11 {
		t.Fatalf("retention policy schema must close exactly eleven review fields")
	}
	for _, field := range []string{"data_class", "purpose", "policy_version", "owner", "trigger", "duration_seconds", "deletion_method", "hold_behavior", "migration_plan", "customer_impact", "effective_at"} {
		if policy.Properties[field] == nil {
			t.Errorf("retention policy schema missing %q", field)
		}
	}
	for _, forbidden := range []string{"credential", "password", "token", "endpoint", "connection_url", "database", "tenant_id", "account_id", "workspace_id", "object_key", "path", "sql", "row_content", "customer_content", "log", "raw_output"} {
		if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
			t.Errorf("retention inventory schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestSelfManagedBackupExpirySchemasAreClosedAndContentFree(t *testing.T) {
	inputBytes := readFile(t, "api", "evidence", "v1", "self-managed-backup-expiry-drill.schema.json")
	receiptBytes := readFile(t, "api", "evidence", "v1", "self-managed-backup-expiry-receipt.schema.json")
	for name, contents := range map[string][]byte{"input": inputBytes, "receipt": receiptBytes} {
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse backup expiry %s schema: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("backup expiry %s schema must reject unknown fields", name)
		}
		for _, field := range []string{"environment", "drill_id", "backup_id", "inventory_id", "inventory_receipt_sha256", "change_id", "change_receipt_sha256", "retention_inventory_receipt_sha256", "policies_sha256", "backup_policy_version", "backup_retention_seconds", "ready", "generated_at", "timeline", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("backup expiry %s schema missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "account_id", "workspace_id", "record_id", "object_key", "object_path", "backup_path", "manifest", "encryption_key", "credential", "password", "token", "endpoint", "provider", "database", "sql", "row_content", "customer_content", "log", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("backup expiry %s schema exposes forbidden field %q", name, forbidden)
			}
		}
	}
	var input struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		t.Fatal(err)
	}
	var check struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
			Outcome struct {
				Enum []string `json:"enum"`
			} `json:"outcome"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(input.Defs["check"], &check); err != nil {
		t.Fatal(err)
	}
	if check.AdditionalProperties || len(check.Properties.ID.Enum) != 7 || len(check.Properties.Outcome.Enum) != 2 {
		t.Fatal("backup expiry check schema must close seven checks and two outcomes")
	}
}

func TestStagingObjectCustodySchemasAreClosedAndContentFree(t *testing.T) {
	inputBytes := readFile(t, "api", "evidence", "v1", "staging-object-custody-review.schema.json")
	receiptBytes := readFile(t, "api", "evidence", "v1", "staging-object-custody-receipt.schema.json")
	for name, contents := range map[string][]byte{"input": inputBytes, "receipt": receiptBytes} {
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse object custody %s schema: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("object custody %s schema must reject unknown fields", name)
		}
		for _, field := range []string{"environment", "review_id", "inventory_id", "inventory_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "ready", "generated_at", "review", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("object custody %s schema missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "account_id", "workspace_id", "source_id", "filename", "object_key", "object_path", "bucket", "resource_name", "credential", "password", "token", "endpoint", "command", "policy_body", "log_content", "trace_content", "customer_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("object custody %s schema exposes forbidden field %q", name, forbidden)
			}
		}
	}
	var input struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		t.Fatal(err)
	}
	var check struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
			Outcome struct {
				Enum []string `json:"enum"`
			} `json:"outcome"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(input.Defs["check"], &check); err != nil {
		t.Fatal(err)
	}
	if check.AdditionalProperties || len(check.Properties.ID.Enum) != 10 || len(check.Properties.Outcome.Enum) != 2 {
		t.Fatal("object custody check schema must close ten checks and two outcomes")
	}
}

func TestStagingTenantIsolationReviewSchemasAreClosedAndContentFree(t *testing.T) {
	inputBytes := readFile(t, "api", "evidence", "v1", "staging-tenant-isolation-review.schema.json")
	receiptBytes := readFile(t, "api", "evidence", "v1", "staging-tenant-isolation-receipt.schema.json")
	for name, contents := range map[string][]byte{"input": inputBytes, "receipt": receiptBytes} {
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse tenant isolation %s schema: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("tenant isolation %s schema must reject unknown fields", name)
		}
		for _, field := range []string{"environment", "review_id", "inventory_id", "inventory_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "ready", "generated_at", "review", "domains"} {
			if schema.Properties[field] == nil {
				t.Errorf("tenant isolation %s schema missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"reviewer_id", "tenant_id", "account_id", "workspace_id", "source_id", "corpus", "query", "identifier", "cache_key", "timing_sample", "timing_threshold", "sql", "policy", "topology", "endpoint", "credential", "password", "token", "log", "trace", "customer_content", "finding_text", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("tenant isolation %s schema exposes forbidden field %q", name, forbidden)
			}
		}
	}
	var input struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		t.Fatal(err)
	}
	var domain struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			ID struct {
				Enum []string `json:"enum"`
			} `json:"id"`
			Outcome struct {
				Enum []string `json:"enum"`
			} `json:"outcome"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(input.Defs["domain"], &domain); err != nil {
		t.Fatal(err)
	}
	if domain.AdditionalProperties || len(domain.Properties.ID.Enum) != 6 || len(domain.Properties.Outcome.Enum) != 3 {
		t.Fatal("tenant isolation domain schema must close six domains and three outcomes")
	}
}

func TestSafePlatformLaunchStateSchemaIsClosedAndContentFree(t *testing.T) {
	contents := readFile(t, "api", "evidence", "v1", "staging-safe-platform-launch-state.schema.json")
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatalf("parse safe-platform launch-state schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("safe-platform launch-state schema must reject unknown fields")
	}
	for _, field := range []string{"classification", "environment", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "collected_at", "phase", "signup_enabled", "invitation_required", "policy_version", "policy_updated_at", "ready"} {
		if schema.Properties[field] == nil {
			t.Errorf("safe-platform launch-state schema missing %q", field)
		}
	}
	for _, forbidden := range []string{"actor", "reviewer", "reason", "country", "account_cap", "source_cap", "trial_days", "rate", "feature_flags", "tenant", "customer", "invitation", "endpoint", "topology", "credential", "password", "token", "sql", "row", "log", "content", "raw_output"} {
		if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
			t.Errorf("safe-platform launch-state schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestStagingOperationalSafetySchemasAreClosedAndContentFree(t *testing.T) {
	inputBytes := readFile(t, "api", "evidence", "v1", "staging-operational-safety-drills.schema.json")
	receiptBytes := readFile(t, "api", "evidence", "v1", "staging-operational-safety-receipt.schema.json")
	for name, contents := range map[string][]byte{"input": inputBytes, "receipt": receiptBytes} {
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse operational-safety %s schema: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("operational-safety %s schema must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "bundle_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "baseline_release_id", "baseline_receipt_sha256", "failed_attempt_release_id", "failed_attempt_receipt_sha256", "rollback_receipt_sha256", "ready", "generated_at", "drills"} {
			if schema.Properties[field] == nil {
				t.Errorf("operational-safety %s schema missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"operator_id", "reviewer_id", "account_id", "tenant_id", "source_id", "secret_name", "secret_version", "secret_value", "credential", "password", "token", "ticket", "command", "endpoint", "kubernetes_context", "topology", "audit_row", "log", "trace", "sql", "customer_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("operational-safety %s schema exposes forbidden field %q", name, forbidden)
			}
		}
		var drill struct {
			AdditionalProperties bool `json:"additionalProperties"`
			Properties           struct {
				ID struct {
					Enum []string `json:"enum"`
				} `json:"id"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schema.Defs["drill"], &drill); err != nil {
			t.Fatal(err)
		}
		if drill.AdditionalProperties || len(drill.Properties.ID.Enum) != 2 {
			t.Fatalf("operational-safety %s drill schema must close exactly two drills", name)
		}
		var check struct {
			AdditionalProperties bool `json:"additionalProperties"`
			Properties           struct {
				Outcome struct {
					Enum []string `json:"enum"`
				} `json:"outcome"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schema.Defs["check"], &check); err != nil {
			t.Fatal(err)
		}
		if check.AdditionalProperties || len(check.Properties.Outcome.Enum) != 3 {
			t.Fatalf("operational-safety %s check schema must close three outcomes", name)
		}
	}
}

func TestProductionSupportStaffingSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"production-support-staffing-input.schema.json", "production-support-staffing-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse support staffing schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("support staffing schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "review_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "required_coverage_minutes", "primary_covered_minutes", "backup_covered_minutes", "primary_slot_count", "backup_slot_count", "ready", "drills", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("support staffing schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"channel_address", "channel_name", "person", "email", "phone", "chat_handle", "schedule", "rotation", "ticket_id", "case_id", "incident_id", "customer_id", "tenant_id", "account_id", "credential", "password", "token", "message", "payload", "log", "trace", "source_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("support staffing schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for definition, count := range map[string]int{"drill": 2, "check": 6} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
				Properties           struct {
					ID struct {
						Enum []string `json:"enum"`
					} `json:"id"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties || len(item.Properties.ID.Enum) != count {
				t.Fatalf("support staffing %s %s must close %d IDs", name, definition, count)
			}
		}
		if strings.Contains(name, "receipt") {
			var result struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs["drill_result"], &result); err != nil {
				t.Fatal(err)
			}
			if result.AdditionalProperties {
				t.Fatal("support staffing drill result must reject unknown fields")
			}
		}
	}
}

func TestProductionBetaSLOSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"production-beta-slo-input.schema.json", "production-beta-slo-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse beta SLO schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("beta SLO schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "observation_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "metric_export_sha256", "query_manifest_sha256", "window_decision_sha256", "window_start", "window_end", "minimum_window_seconds", "ready", "metrics", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("beta SLO schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "customer_id", "account_id", "source_id", "request_id", "trace_id", "log_id", "sample_series_id", "query_expression", "promql", "label", "endpoint", "person", "credential", "password", "token", "payload", "source_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("beta SLO schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		var metricIDs struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(schema.Defs["metric_id"], &metricIDs); err != nil {
			t.Fatal(err)
		}
		var checkIDs struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(schema.Defs["check_id"], &checkIDs); err != nil {
			t.Fatal(err)
		}
		if len(metricIDs.Enum) != 6 || len(checkIDs.Enum) != 6 {
			t.Fatalf("beta SLO schema %s must close six metric and check IDs", name)
		}
		for _, definition := range []string{"metric", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("beta SLO schema %s definition %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestProductionGAScorecardSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"production-ga-scorecard-input.schema.json", "production-ga-scorecard-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse GA scorecard schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("GA scorecard schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "scorecard_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "scorecard_export_sha256", "query_manifest_sha256", "window_decision_sha256", "target_decision_sha256", "product_domain_review_sha256", "window_start", "window_end", "approved_cost_per_active_tenant_microusd", "ready", "metrics", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("GA scorecard schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "customer_id", "account_id", "source_id", "request_id", "trace_id", "log_id", "query_expression", "promql", "endpoint", "person", "credential", "password", "token", "payload", "source_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("GA scorecard schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for definition, count := range map[string]int{"metric_id": 13, "check_id": 7} {
			var values struct {
				Enum []string `json:"enum"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &values); err != nil {
				t.Fatal(err)
			}
			if len(values.Enum) != count {
				t.Fatalf("GA scorecard schema %s must close %d %s values", name, count, definition)
			}
		}
		for _, definition := range []string{"metric", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("GA scorecard schema %s %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestProductionGADrillSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"production-ga-drill-input.schema.json", "production-ga-drill-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse GA drill schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("GA drill schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"review_id", "policy_version", "scorecard_id", "scorecard_receipt_sha256", "inventory_id", "plan_id", "change_id", "release_id", "drill_manifest_sha256", "repetition_policy_sha256", "accountable_review_sha256", "ready", "drills", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("GA drill schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "account_id", "source_id", "operator_id", "reviewer_id", "request_id", "trace_id", "log_id", "credential_id", "password", "token", "payload", "source_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("GA drill schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for definition, count := range map[string]int{"scenario": 4, "check_id": 7} {
			var values struct {
				Enum []string `json:"enum"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &values); err != nil {
				t.Fatal(err)
			}
			if len(values.Enum) != count {
				t.Fatalf("GA drill schema %s must close %d %s values", name, count, definition)
			}
		}
		for _, definition := range []string{"drill", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("GA drill schema %s %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestGAApprovalExportSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"ga-approval-export-input.schema.json", "ga-approval-export-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse GA approval schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("GA approval schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"export_id", "review_id", "inventory_id", "release_id", "scorecard_receipt_sha256", "drill_receipt_sha256", "ga_evidence_bundle_sha256", "trust_bundle_sha256", "approval_export_sha256", "ready", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("GA approval schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"owner", "key_id", "evidence_ref", "public_key", "private_key", "signature", "filename", "tenant_id", "account_id", "url", "contact", "credential", "password", "token", "log", "trace", "payload", "source_content"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("GA approval schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		var checkIDs struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(schema.Defs["check_id"], &checkIDs); err != nil {
			t.Fatal(err)
		}
		if len(checkIDs.Enum) != 8 {
			t.Fatalf("GA approval schema %s must close eight check IDs", name)
		}
		var check struct {
			AdditionalProperties bool `json:"additionalProperties"`
		}
		if err := json.Unmarshal(schema.Defs["check"], &check); err != nil {
			t.Fatal(err)
		}
		if check.AdditionalProperties {
			t.Fatalf("GA approval schema %s check must reject unknown fields", name)
		}
	}
	manifest := readFile(t, "api", "evidence", "v1", "ga-approval-export-manifest.schema.json")
	var schema struct {
		AdditionalProperties bool                       `json:"additionalProperties"`
		Defs                 map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(manifest, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Fatal("GA approval manifest must reject unknown fields")
	}
	var file struct {
		AdditionalProperties bool `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema.Defs["file"], &file); err != nil {
		t.Fatal(err)
	}
	if file.AdditionalProperties {
		t.Fatal("GA approval manifest file must reject unknown fields")
	}
}

func TestStagingSecurityClosureSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"staging-security-closure-input.schema.json", "staging-security-closure-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse security closure schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("security closure schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"review_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "source_manifest_sha256", "finding_register_sha256", "classification_policy_sha256", "retest_report_sha256", "security_review_sha256", "ready", "sources", "findings", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("security closure schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"finding_id", "finding_text", "cve", "package", "component", "image", "endpoint", "reviewer", "tenant_id", "account_id", "customer_source_id", "request_id", "trace_id", "log_id", "credential", "password", "token", "payload", "source_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("security closure schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for definition, count := range map[string]int{"source_id": 4, "check_id": 7} {
			var values struct {
				Enum []string `json:"enum"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &values); err != nil {
				t.Fatal(err)
			}
			if len(values.Enum) != count {
				t.Fatalf("security closure schema %s must close %d %s values", name, count, definition)
			}
		}
		for _, definition := range []string{"source", "finding", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("security closure schema %s %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestStagingMigrationCohortSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"staging-migration-cohort-input.schema.json", "staging-migration-cohort-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse migration cohort schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("migration cohort schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"cohort_id", "dataset_version", "consent_version", "importer_version", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "cohort_decision_sha256", "cohort_report_sha256", "account_count", "library_count", "source_count", "expected_item_count", "failed_item_count", "unexplained_loss_count", "duplicate_publication_count", "formats", "size_buckets", "ready", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("migration cohort schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "account_id", "workspace_id", "source_id", "memory_id", "note_id", "filename", "failure_message", "reason_code", "operator_id", "reviewer_id", "person", "email", "credential", "password", "token", "payload", "source_content", "raw_report"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("migration cohort schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for _, definition := range []string{"format_coverage", "size_coverage", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("migration cohort schema %s %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestStagingMigrationAcceptanceSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"staging-migration-acceptance-input.schema.json", "staging-migration-acceptance-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse migration acceptance schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("migration acceptance schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"acceptance_id", "rollback_plan_version", "cohort_id", "cohort_receipt_sha256", "parity_evaluation_id", "parity_receipt_sha256", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "dataset_version", "rollback_plan_sha256", "tabletop_report_sha256", "acceptance_decision_sha256", "ready", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("migration acceptance schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "account_id", "workspace_id", "source_id", "item_id", "credential", "endpoint", "participant", "reviewer_id", "email", "password", "token", "deletion_receipt", "report_content", "log", "trace", "payload"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("migration acceptance schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		var check struct {
			AdditionalProperties bool `json:"additionalProperties"`
		}
		if err := json.Unmarshal(schema.Defs["check"], &check); err != nil {
			t.Fatal(err)
		}
		if check.AdditionalProperties {
			t.Fatalf("migration acceptance schema %s check must reject unknown fields", name)
		}
	}
}

func TestLaunchScopeSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"launch-scope-input.schema.json", "launch-scope-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse launch-scope schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("launch-scope schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"scope_decision_id", "scope_decision_version", "jurisdiction_policy_version", "legal_review_version", "risk_register_version", "decision_register_sha256", "launch_scope_decision_sha256", "jurisdiction_memo_sha256", "policy_manifest_sha256", "legal_review_sha256", "risk_register_sha256", "launch_country_count", "minimum_age_years", "support_language_count", "notice_jurisdiction_count", "blocking_risk_count", "unowned_risk_count", "deferred_risk_count", "ready", "legal_positions", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("launch-scope schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"launch_countries", "country_codes", "country_name", "jurisdictions", "jurisdiction_name", "support_languages", "legal_text", "policy_copy", "risk_description", "person", "reviewer", "email", "organization", "signature", "public_key", "evidence_ref", "file_path", "payload"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("launch-scope schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for _, definition := range []string{"legal_position", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("launch-scope schema %s %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestExternalIntegrationReviewSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"external-integration-review-input.schema.json", "external-integration-review-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse external-integration schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("external-integration schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"review_id", "policy_version", "traffic_review_version", "inventory_id", "inventory_receipt_sha256", "data_policy_sha256", "integration_manifest_sha256", "review_decision_sha256", "ready", "integrations", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("external-integration schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"provider_name", "destination", "endpoint", "account_id", "tenant_id", "person", "email_address", "contract", "traffic_sample", "request_id", "invoice_id", "message", "prompt", "passage", "credential", "secret", "signature", "public_key", "evidence_ref", "file_path", "payload"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("external-integration schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for _, definition := range []string{"integration", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("external-integration schema %s %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestFinalMVPReadinessSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"final-mvp-readiness-input.schema.json", "final-mvp-readiness-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse final MVP readiness schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("final MVP readiness schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"readiness_id", "program_version", "review_decision_sha256", "generated_at"} {
			if schema.Properties[field] == nil {
				t.Errorf("final MVP readiness schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"dossier_path", "evidence_ref", "owner", "person", "email", "signature", "public_key", "private_key", "provider", "customer", "tenant_id", "account_id", "credential", "secret", "token", "payload", "source_content", "raw_report"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("final MVP readiness schema %s exposes forbidden field %q", name, forbidden)
			}
		}
	}
	receipt := readFile(t, "api", "evidence", "v1", "final-mvp-readiness-receipt.schema.json")
	var schema struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(receipt, &schema); err != nil {
		t.Fatal(err)
	}
	for definition, count := range map[string]int{"foundational_control_id": 49, "gate_id": 8} {
		var values struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(schema.Defs[definition], &values); err != nil {
			t.Fatal(err)
		}
		if len(values.Enum) != count {
			t.Fatalf("final MVP readiness %s must close %d values", definition, count)
		}
	}
	var gate struct {
		AdditionalProperties bool `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema.Defs["gate"], &gate); err != nil || gate.AdditionalProperties {
		t.Fatalf("final MVP readiness gate must be closed: %v", err)
	}
}

func TestProductionBetaOperationsSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"production-beta-operations-input.schema.json", "production-beta-operations-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse beta operations schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("beta operations schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "assessment_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "beta_slo_observation_id", "beta_slo_receipt_sha256", "window_start", "window_end", "deletion_receipt_export_sha256", "notice_case_export_sha256", "anomaly_case_export_sha256", "support_case_export_sha256", "sample_manifest_sha256", "target_decision_sha256", "ready", "domains", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("beta operations schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "customer_id", "account_id", "source_id", "request_id", "trace_id", "case_id", "receipt_id", "person", "email", "credential", "password", "token", "payload", "source_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("beta operations schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for definition, count := range map[string]int{"domain_id": 4, "check_id": 9} {
			var ids struct {
				Enum []string `json:"enum"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &ids); err != nil {
				t.Fatal(err)
			}
			if len(ids.Enum) != count {
				t.Fatalf("beta operations schema %s must close %d %s values", name, count, definition)
			}
		}
		for _, definition := range []string{"domain", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("beta operations schema %s definition %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestProductionBetaIntegritySchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"production-beta-integrity-input.schema.json", "production-beta-integrity-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse beta integrity schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("beta integrity schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "review_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "beta_slo_observation_id", "beta_slo_receipt_sha256", "beta_operations_assessment_id", "beta_operations_receipt_sha256", "window_start", "window_end", "audit_database_chain_report_sha256", "audit_archive_reconciliation_sha256", "isolation_signal_export_sha256", "audit_integrity_signal_export_sha256", "anomaly_report_sha256", "residual_risk_decision_sha256", "audit_event_count", "chain_verified_event_count", "chain_break_count", "archive_expected_count", "isolation_signal_count", "audit_integrity_signal_count", "anomaly_finding_count", "ready", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("beta integrity schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "customer_id", "account_id", "event_id", "finding_id", "incident_id", "request_id", "trace_id", "case_id", "source_id", "credential_id", "person", "email", "rule_expression", "query", "endpoint", "message", "evidence_ref", "signature", "payload", "log", "trace", "source_content", "raw_output"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("beta integrity schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		var checkIDs struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(schema.Defs["check_id"], &checkIDs); err != nil {
			t.Fatal(err)
		}
		if len(checkIDs.Enum) != 9 {
			t.Fatalf("beta integrity schema %s must close nine check IDs", name)
		}
		var check struct {
			AdditionalProperties bool `json:"additionalProperties"`
		}
		if err := json.Unmarshal(schema.Defs["check"], &check); err != nil {
			t.Fatal(err)
		}
		if check.AdditionalProperties {
			t.Fatalf("beta integrity schema %s check must reject unknown fields", name)
		}
	}
}

func TestProductionLaunchAssetSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"production-launch-assets-input.schema.json", "production-launch-assets-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse launch assets schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("launch assets schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "review_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "manifest_sha256", "accountable_review_sha256", "snapshot_at", "ready", "assets", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("launch assets schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"url", "hostname", "copy", "email", "person", "contact", "destination", "endpoint", "credential", "password", "token", "log", "trace", "payload", "raw_probe"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("launch assets schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		for definition, count := range map[string]int{"asset_id": 7, "owner_group": 5, "check_id": 9} {
			var enum struct {
				Enum []string `json:"enum"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &enum); err != nil {
				t.Fatal(err)
			}
			if len(enum.Enum) != count {
				t.Fatalf("launch assets schema %s must close %d %s values", name, count, definition)
			}
		}
		for _, definition := range []string{"asset", "check"} {
			var item struct {
				AdditionalProperties bool `json:"additionalProperties"`
			}
			if err := json.Unmarshal(schema.Defs[definition], &item); err != nil {
				t.Fatal(err)
			}
			if item.AdditionalProperties {
				t.Fatalf("launch assets schema %s definition %s must reject unknown fields", name, definition)
			}
		}
	}
}

func TestProductionPublicBetaGateSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"production-public-beta-gate-input.schema.json", "production-public-beta-gate-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse public beta gate schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("public beta gate schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "gate_review_id", "inventory_id", "inventory_receipt_sha256", "plan_id", "plan_receipt_sha256", "change_id", "change_receipt_sha256", "release_id", "release_receipt_sha256", "billing_reconciliation_id", "billing_receipt_sha256", "beta_slo_observation_id", "beta_slo_receipt_sha256", "beta_operations_assessment_id", "beta_operations_receipt_sha256", "beta_integrity_review_id", "beta_integrity_receipt_sha256", "window_start", "window_end", "abuse_export_sha256", "cost_export_sha256", "signup_attempt_count", "abuse_finding_count", "active_tenant_count", "actual_window_cost_microusd", "ready", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("public beta gate schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"tenant_id", "account_id", "finding_id", "attempt_id", "invoice_id", "event_id", "operator_id", "reviewer_id", "url", "query", "contact", "signature", "credential", "password", "token", "log", "trace", "payload", "raw_export"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("public beta gate schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		var checkIDs struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(schema.Defs["check_id"], &checkIDs); err != nil {
			t.Fatal(err)
		}
		if len(checkIDs.Enum) != 9 {
			t.Fatalf("public beta gate schema %s must close nine check IDs", name)
		}
		var check struct {
			AdditionalProperties bool `json:"additionalProperties"`
		}
		if err := json.Unmarshal(schema.Defs["check"], &check); err != nil {
			t.Fatal(err)
		}
		if check.AdditionalProperties {
			t.Fatalf("public beta gate schema %s check must reject unknown fields", name)
		}
	}
}

func TestPublicBetaApprovalExportSchemasAreClosedAndContentFree(t *testing.T) {
	for _, name := range []string{"public-beta-approval-export-input.schema.json", "public-beta-approval-export-receipt.schema.json"} {
		contents := readFile(t, "api", "evidence", "v1", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Defs                 map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse approval export schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("approval export schema %s must reject unknown fields", name)
		}
		for _, field := range []string{"classification", "environment", "export_id", "review_id", "inventory_id", "release_id", "launch_asset_receipt_sha256", "beta_gate_receipt_sha256", "trust_bundle_sha256", "approval_export_sha256", "ready", "checks"} {
			if schema.Properties[field] == nil {
				t.Errorf("approval export schema %s missing %q", name, field)
			}
		}
		for _, forbidden := range []string{"owner", "key_id", "evidence_ref", "public_key", "private_key", "signature", "filename", "tenant_id", "account_id", "url", "contact", "credential", "password", "token", "log", "trace", "payload", "source_content"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("approval export schema %s exposes forbidden field %q", name, forbidden)
			}
		}
		var checkIDs struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(schema.Defs["check_id"], &checkIDs); err != nil {
			t.Fatal(err)
		}
		if len(checkIDs.Enum) != 8 {
			t.Fatalf("approval export schema %s must close eight check IDs", name)
		}
	}
	manifest := readFile(t, "api", "evidence", "v1", "public-beta-approval-export-manifest.schema.json")
	var manifestSchema struct {
		AdditionalProperties bool `json:"additionalProperties"`
	}
	if err := json.Unmarshal(manifest, &manifestSchema); err != nil || manifestSchema.AdditionalProperties {
		t.Fatalf("approval export manifest must be closed: %v", err)
	}
}

func TestSkillOrchestratorProductionReleaseSchemasAreClosedAndCryptographicallyBound(t *testing.T) {
	expectations := map[string][]string{
		"skill-orchestrator-configuration-receipt.schema.json": {
			"schema", "receipt_id", "release_id", "build_digest", "migration_digest", "configuration", "signer_id", "signed_at", "signing_key_id", "signature",
		},
		"skill-orchestrator-production-release-evidence.schema.json": {
			"schema", "release_id", "build_digest", "migration_digest", "policy_digest", "rollout", "drills", "rollback_slo_millis", "generated_at", "signer_id", "signing_key_id", "signature",
		},
		"skill-orchestrator-product-approval.schema.json": {
			"schema", "approval_id", "release_id", "build_digest", "migration_digest", "policy_digest", "configuration_digest", "release_evidence_digest", "approver_id", "approver_role", "approved_at", "expires_at", "signing_key_id", "signature",
		},
	}
	for name, requiredFields := range expectations {
		contents := readFile(t, "api", "evidence", "v2", name)
		var schema struct {
			AdditionalProperties bool                       `json:"additionalProperties"`
			Required             []string                   `json:"required"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(contents, &schema); err != nil {
			t.Fatalf("parse skill orchestrator release schema %s: %v", name, err)
		}
		if schema.AdditionalProperties {
			t.Fatalf("skill orchestrator release schema %s must reject unknown fields", name)
		}
		required := make(map[string]bool, len(schema.Required))
		for _, field := range schema.Required {
			required[field] = true
		}
		for _, field := range requiredFields {
			if !required[field] || schema.Properties[field] == nil {
				t.Errorf("skill orchestrator release schema %s must require and define %q", name, field)
			}
		}
		for _, forbidden := range []string{"content", "prompt", "credential", "password", "token", "private_key", "raw_output", "filesystem_path"} {
			if bytes.Contains(contents, []byte(`"`+forbidden+`"`)) {
				t.Errorf("skill orchestrator release schema %s exposes forbidden field %q", name, forbidden)
			}
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

func TestSaaSArchitectureDoesNotRequireExternalCloudInfrastructure(t *testing.T) {
	documents := [][]string{
		{"docs", "saas", "self-managed-architecture.md"},
		{"docs", "saas", "implementation-status.md"},
		{"docs", "saas", "external-evidence-matrix.md"},
		{"api", "evidence", "v1", "external-control-catalog.json"},
	}
	architecture := string(readFile(t, "docs", "saas", "self-managed-architecture.md"))
	for _, required := range []string{
		"Self-managed Agent Memory platform",
		"Self-managed OIDC identity",
		"Optional payment processor",
		"Optional transactional email service",
		"Optional external model API",
	} {
		if !strings.Contains(architecture, required) {
			t.Errorf("self-managed architecture is missing boundary %q", required)
		}
	}
	forbidden := []string{
		"Select managed providers",
		"selected cloud's infrastructure layer",
		"provider account/project",
		"provider firewall/private-endpoint",
		"managed-provider configuration",
		"provider-specific managed production infrastructure",
		"selected provider's multi-AZ class",
	}

	for _, parts := range documents {
		contents := string(readFile(t, parts...))
		for _, phrase := range forbidden {
			if strings.Contains(contents, phrase) {
				t.Errorf("%s retains external-cloud deployment assumption %q", filepath.Join(parts...), phrase)
			}
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
