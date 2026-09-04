package objectcustody

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type graphArtifactObjectFixture struct{ values map[string][]byte }

func (s *graphArtifactObjectFixture) PutImmutable(_ context.Context, key string, value []byte, _ time.Time) error {
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	if _, ok := s.values[key]; ok {
		return ErrGraphObjectAlreadyExists
	}
	s.values[key] = append([]byte(nil), value...)
	return nil
}
func (s *graphArtifactObjectFixture) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), value...), nil
}

func TestGraphObjectCustodyDerivesPrefixesAndCoalescesExactReplay(t *testing.T) {
	now := time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)
	objects := &graphArtifactObjectFixture{}
	custody := NewGraphArtifactCustody(objects, func() time.Time { return now })
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	value := []byte("normalized entities\n")
	manifest := graphArtifactManifestFixture(scope, "job-a", "revision-a", value)
	prefix, coalesced, err := custody.Stage(context.Background(), scope, "job-a", "revision-a", map[string][]byte{"entities.jsonl": value}, manifest)
	if err != nil || coalesced || prefix != "graph-artifacts/staging/tenant-a/workspace-a/job-a/revision-a/" {
		t.Fatalf("stage prefix=%q coalesced=%v err=%v", prefix, coalesced, err)
	}
	_, coalesced, err = custody.Stage(context.Background(), scope, "job-a", "revision-a", map[string][]byte{"entities.jsonl": value}, manifest)
	if err != nil || !coalesced {
		t.Fatalf("exact replay was not coalesced: %v %v", coalesced, err)
	}
	if _, _, err := custody.Stage(context.Background(), scope, "job-a", "revision-a", map[string][]byte{"entities.jsonl": []byte("forged")}, manifest); err == nil {
		t.Fatal("modified replay was accepted")
	}
}

func TestGraphObjectCustodyRejectsCrossWorkspaceIdentity(t *testing.T) {
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "../workspace-b"}
	if _, err := GraphArtifactStagingPrefix(scope, "job-a", "revision-a"); err == nil {
		t.Fatal("path traversal identity accepted")
	}
}

func TestGraphAdapterStateCustodyBindsRevisionAndRejectsMutation(t *testing.T) {
	now := time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)
	objects := &graphArtifactObjectFixture{}
	custody := NewGraphArtifactCustody(objects, func() time.Time { return now })
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	files := map[string][]byte{"entities.parquet": []byte("opaque-adapter-state")}
	manifest, err := contracts.BuildGraphAdapterStateManifest(scope, "revision-a", files, now)
	if err != nil {
		t.Fatal(err)
	}
	if coalesced, err := custody.StageAdapterState(context.Background(), scope, "revision-a", files, manifest); err != nil || coalesced {
		t.Fatalf("stage state: coalesced=%v err=%v", coalesced, err)
	}
	loaded, loadedManifest, err := custody.ReadAdapterState(context.Background(), scope, "revision-a")
	if err != nil || loadedManifest.RevisionID != "revision-a" || string(loaded["entities.parquet"]) != "opaque-adapter-state" {
		t.Fatalf("read state: %#v %#v %v", loaded, loadedManifest, err)
	}
	objects.values["graph-artifacts/state/tenant-a/workspace-a/revision-a/entities.parquet"] = []byte("mutated")
	if _, _, err := custody.ReadAdapterState(context.Background(), scope, "revision-a"); err == nil {
		t.Fatal("mutated adapter state was accepted")
	}
}

func graphArtifactManifestFixture(scope core.GraphScope, jobID, revisionID string, value []byte) contracts.GraphArtifactManifest {
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(value))
	now := time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)
	return contracts.GraphArtifactManifest{ContractVersion: contracts.GraphAdapterContractV1, ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1, Scope: scope, ConfigurationID: "configuration-a", JobID: jobID, RevisionID: revisionID, AdapterName: "adapter", AdapterVersion: "0.1.0", GraphRAGVersion: "3.1.2", PythonVersion: "3.13.7", EnvironmentFingerprint: "sha256:environment", InputManifestHash: "sha256:input", ConfigurationFingerprint: "sha256:configuration", PromptFingerprint: "sha256:prompt", IndexMethod: core.GraphIndexStandard, Mode: contracts.GraphIndexModeFull, Outputs: []contracts.GraphArtifactFile{{Name: "entities.jsonl", Kind: "entities", Required: true, Bytes: int64(len(value)), Rows: 1, SchemaFingerprint: "sha256:schema", ContentHash: digest}}, Models: []string{"index-model"}, DurationMillis: 1, Status: contracts.GraphArtifactCompleted, CompletedAt: now, Attestation: contracts.GraphArtifactAttestation{ProducerIdentity: "graph-worker", BuildDigest: "sha256:build", Signature: "signature"}}
}
