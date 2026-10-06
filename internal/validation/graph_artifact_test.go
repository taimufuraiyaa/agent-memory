package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphArtifactValidatesHashesSchemasReferencesAndEvidence(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"entities.jsonl":      []byte("{\"id\":\"e1\",\"name\":\"Book A\",\"type\":\"book\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m1\",\"canonical_fingerprint\":\"sha256:m1\"}]}\n"),
		"relationships.jsonl": []byte("{\"id\":\"r1\",\"source_id\":\"e1\",\"target_id\":\"e1\",\"kind\":\"refines\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m1\",\"canonical_fingerprint\":\"sha256:m1\"}]}\n"),
	}
	manifest := graphArtifactFixture(t, root, files)
	validated, err := ValidateGraphArtifact(context.Background(), root, manifest, GraphArtifactPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(validated.Entities) != 1 || len(validated.Relationships) != 1 {
		t.Fatalf("validated=%+v", validated)
	}
}

func TestGraphArtifactRejectsMalformedDuplicateCrossReferenceAndEvidenceFree(t *testing.T) {
	tests := map[string]string{
		"duplicate":     "{\"id\":\"e1\",\"name\":\"A\",\"type\":\"book\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m1\",\"canonical_fingerprint\":\"f\"}]}\n{\"id\":\"e1\",\"name\":\"B\",\"type\":\"book\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m2\",\"canonical_fingerprint\":\"f\"}]}\n",
		"evidence-free": "{\"id\":\"e1\",\"name\":\"A\",\"type\":\"book\",\"evidence\":[]}\n",
	}
	for name, entities := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string][]byte{"entities.jsonl": []byte(entities), "relationships.jsonl": []byte("{\"id\":\"r1\",\"source_id\":\"e1\",\"target_id\":\"missing\",\"kind\":\"refines\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m1\",\"canonical_fingerprint\":\"f\"}]}\n")}
			manifest := graphArtifactFixture(t, root, files)
			if _, err := ValidateGraphArtifact(context.Background(), root, manifest, GraphArtifactPolicy{}); err == nil {
				t.Fatal("malicious artifact accepted")
			}
		})
	}
}

func TestGraphArtifactRejectsSymlinkOversizeAndSchemaDrift(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "entities.jsonl")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "entities.jsonl")); err != nil {
		t.Fatal(err)
	}
	manifest := graphArtifactFixtureWithoutWrite(map[string][]byte{"entities.jsonl": []byte("{}\n"), "relationships.jsonl": []byte("{}\n")})
	if err := os.WriteFile(filepath.Join(root, "relationships.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateGraphArtifact(context.Background(), root, manifest, GraphArtifactPolicy{}); err == nil {
		t.Fatal("symlink artifact accepted")
	}
	manifest.ArtifactSchemaVersion = "graph-artifact/v99"
	if _, err := ValidateGraphArtifact(context.Background(), root, manifest, GraphArtifactPolicy{}); err == nil {
		t.Fatal("schema drift accepted")
	}
}

func TestGraphArtifactRejectsCyclicCommunities(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"entities.jsonl":      []byte("{\"id\":\"e1\",\"name\":\"A\",\"type\":\"book\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m1\",\"canonical_fingerprint\":\"f\"}]}\n"),
		"relationships.jsonl": []byte("{\"id\":\"r1\",\"source_id\":\"e1\",\"target_id\":\"e1\",\"kind\":\"refines\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m1\",\"canonical_fingerprint\":\"f\"}]}\n"),
		"communities.jsonl":   []byte("{\"id\":\"c1\",\"parent_id\":\"c2\",\"entity_ids\":[\"e1\"]}\n{\"id\":\"c2\",\"parent_id\":\"c1\",\"entity_ids\":[\"e1\"]}\n"),
	}
	manifest := graphArtifactFixture(t, root, files)
	if _, err := ValidateGraphArtifact(context.Background(), root, manifest, GraphArtifactPolicy{CommunitiesEnabled: true}); err == nil {
		t.Fatal("cyclic community hierarchy accepted")
	}
}

func graphArtifactFixture(t *testing.T, root string, files map[string][]byte) contracts.GraphArtifactManifest {
	t.Helper()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return graphArtifactFixtureWithoutWrite(files)
}

func graphArtifactFixtureWithoutWrite(files map[string][]byte) contracts.GraphArtifactManifest {
	outputs := make([]contracts.GraphArtifactFile, 0, len(files))
	for name, contents := range files {
		digest := sha256.Sum256(contents)
		outputs = append(outputs, contracts.GraphArtifactFile{Name: name, Kind: strings.TrimSuffix(name, ".jsonl"), Required: true, Bytes: int64(len(contents)), Rows: int64(strings.Count(string(contents), "\n")), SchemaFingerprint: "sha256:schema-v1", ContentHash: "sha256:" + hex.EncodeToString(digest[:])})
	}
	return contracts.GraphArtifactManifest{ContractVersion: contracts.GraphAdapterContractV1, ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1, Scope: core.GraphScope{WorkspaceID: "workspace-a"}, ConfigurationID: "configuration-1", JobID: "job-1", RevisionID: "revision-1", AdapterName: "agent-memory-graphrag", AdapterVersion: "0.1.0", GraphRAGVersion: "3.1.2", PythonVersion: "3.13.5", EnvironmentFingerprint: "sha256:lock", InputManifestHash: "sha256:projection", ConfigurationFingerprint: "sha256:configuration", PromptFingerprint: "sha256:prompts", IndexMethod: core.GraphIndexStandard, Mode: contracts.GraphIndexModeFull, Outputs: outputs, Models: []string{"completion", "embedding"}, Status: contracts.GraphArtifactCompleted, CompletedAt: time.Now().UTC(), Attestation: contracts.GraphArtifactAttestation{ProducerIdentity: "adapter", BuildDigest: "sha256:image", Signature: "signature"}}
}
