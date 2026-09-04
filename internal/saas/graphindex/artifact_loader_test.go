package graphindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type graphArtifactObjectFixture map[string][]byte

func (f graphArtifactObjectFixture) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := f[key]
	if !ok {
		return nil, context.Canceled
	}
	return append([]byte(nil), value...), nil
}

func TestObjectArtifactLoaderValidatesAndNormalizesStableRecords(t *testing.T) {
	prefix := "graph-artifacts/staging/tenant-a/workspace-a/job-a/revision-a/"
	objects, manifest := hostedObjectArtifactFixture(t, prefix)
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	loader, err := NewObjectArtifactLoader(objects, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	loaded, first, err := loader.LoadNormalized(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := loader.LoadNormalized(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, manifest) || len(first.Entities) != 2 || len(first.Edges) != 1 || len(first.Communities) != 1 {
		t.Fatalf("incomplete normalized batch: %#v", first)
	}
	if first.Entities[0].Entity.ID != second.Entities[0].Entity.ID || first.Edges[0].Edge.ID != second.Edges[0].Edge.ID || first.Communities[0].Community.ID != second.Communities[0].Community.ID {
		t.Fatal("normalization identities were not deterministic")
	}
	if first.Scope.TenantID != "tenant-a" || first.Entities[0].Entity.Scope != first.Scope || first.Communities[0].Report.CanGroundClaim() {
		t.Fatal("hosted scope or derived-report trust boundary was lost")
	}
}

func TestObjectArtifactLoaderRejectsTamperedObject(t *testing.T) {
	prefix := "graph-artifacts/staging/tenant-a/workspace-a/job-a/revision-a/"
	objects, _ := hostedObjectArtifactFixture(t, prefix)
	objects[prefix+"entities.jsonl"] = append(objects[prefix+"entities.jsonl"], ' ')
	loader, _ := NewObjectArtifactLoader(objects, time.Now)
	if _, _, err := loader.LoadNormalized(context.Background(), prefix); err == nil {
		t.Fatal("tampered hosted graph artifact was accepted")
	}
}

func hostedObjectArtifactFixture(t *testing.T, prefix string) (graphArtifactObjectFixture, contracts.GraphArtifactManifest) {
	t.Helper()
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	files := map[string][]byte{
		"entities.jsonl":          []byte("{\"id\":\"e1\",\"name\":\"Book A\",\"type\":\"book\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m1\",\"canonical_fingerprint\":\"sha256:m1\"}]}\n{\"id\":\"e2\",\"name\":\"Chapter B\",\"type\":\"chapter\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m2\",\"canonical_fingerprint\":\"sha256:m2\"}]}\n"),
		"relationships.jsonl":     []byte("{\"id\":\"r1\",\"source_id\":\"e2\",\"target_id\":\"e1\",\"kind\":\"membership\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m2\",\"canonical_fingerprint\":\"sha256:m2\"}]}\n"),
		"communities.jsonl":       []byte("{\"id\":\"c1\",\"parent_id\":\"\",\"entity_ids\":[\"e1\",\"e2\"]}\n"),
		"community_reports.jsonl": []byte("{\"id\":\"report-1\",\"community_id\":\"c1\",\"title\":\"Book knowledge\",\"summary\":\"Chapter B belongs to Book A.\",\"evidence\":[{\"canonical_kind\":\"memory\",\"canonical_id\":\"m1\",\"canonical_fingerprint\":\"sha256:m1\"},{\"canonical_kind\":\"memory\",\"canonical_id\":\"m2\",\"canonical_fingerprint\":\"sha256:m2\"}]}\n"),
	}
	outputs := make([]contracts.GraphArtifactFile, 0, len(files))
	for _, name := range []string{"entities.jsonl", "relationships.jsonl", "communities.jsonl", "community_reports.jsonl"} {
		value := files[name]
		digest := sha256.Sum256(value)
		outputs = append(outputs, contracts.GraphArtifactFile{Name: name, Kind: name, Required: true, Bytes: int64(len(value)), Rows: int64(bytesLines(value)), SchemaFingerprint: "sha256:schema", ContentHash: "sha256:" + hex.EncodeToString(digest[:])})
	}
	manifest := contracts.GraphArtifactManifest{
		ContractVersion: contracts.GraphAdapterContractV1, ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1,
		Scope: scope, ConfigurationID: "configuration-a", JobID: "job-a", RevisionID: "revision-a",
		AdapterName: "adapter", AdapterVersion: "0.1.0", GraphRAGVersion: "3.1.2", PythonVersion: "3.13.7",
		EnvironmentFingerprint: "sha256:environment", InputManifestHash: "sha256:input", ConfigurationFingerprint: "sha256:configuration", PromptFingerprint: "sha256:prompt",
		IndexMethod: core.GraphIndexStandard, Mode: contracts.GraphIndexModeFull, Outputs: outputs, Models: []string{"index-model"},
		DurationMillis: 1, Status: contracts.GraphArtifactCompleted, CompletedAt: time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC),
		Attestation: contracts.GraphArtifactAttestation{ProducerIdentity: "graph-worker", BuildDigest: "sha256:build", Signature: "signature"},
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	objects := graphArtifactObjectFixture{prefix + "manifest.json": append(encoded, '\n')}
	for name, value := range files {
		objects[prefix+name] = value
	}
	return objects, manifest
}

func bytesLines(value []byte) int {
	count := 0
	for _, current := range value {
		if current == '\n' {
			count++
		}
	}
	return count
}
