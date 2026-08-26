package graphworker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestProcessAdapterBuildsContentBoundRequestWithoutProviderSecrets(t *testing.T) {
	adapter, err := NewProcessAdapter(ProcessAdapterConfig{
		Executable: "/opt/adapter/bin/agent-memory-graphrag", JobRoot: filepath.Join(t.TempDir(), "jobs"),
		CompletionProvider: "openai", CompletionModel: "completion-v1", EmbeddingProvider: "openai", EmbeddingModel: "embedding-v1",
		CompletionAPIKey: "completion-secret", EmbeddingAPIKey: "embedding-secret", ProducerIdentity: "graph-worker",
		BuildDigest: "sha256:build", AttestationSignature: "workload-signature", Timeout: time.Hour, MaxOutputBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := []byte("{\"id\":\"memory-a\",\"text\":\"Book A\",\"kind\":\"memory\",\"correlation_token\":\"corr-a\"}\n")
	correlations := []byte("{\"corr-a\":{\"canonical_kind\":\"memory\",\"canonical_id\":\"memory-a\",\"canonical_fingerprint\":\"sha256:a\"}}")
	request := AdapterRequest{Scope: core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, JobID: "job-a", ConfigurationID: "configuration-a", RevisionID: "revision-a", Mode: contracts.GraphIndexModeFull, Projection: projection, Correlations: correlations, ProjectionManifest: []byte("manifest")}
	encoded, err := adapter.requestEnvelope("/graph-job/job-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsBytes(encoded, []byte("completion-secret")) || containsBytes(encoded, []byte("embedding-secret")) {
		t.Fatal("adapter request was empty or contained provider credentials")
	}
	var envelope map[string]any
	if err := json.Unmarshal(encoded, &envelope); err != nil || envelope["command"] != "full-index" {
		t.Fatalf("invalid adapter envelope: %s", encoded)
	}
}

func TestProcessAdapterRejectsIncrementalWithoutImmutableBaseArtifact(t *testing.T) {
	adapter := &ProcessAdapter{configuration: ProcessAdapterConfig{JobRoot: t.TempDir()}}
	_, err := adapter.Index(t.Context(), AdapterRequest{Mode: contracts.GraphIndexModeIncremental})
	if err == nil {
		t.Fatal("incomplete incremental request was accepted")
	}
}

func TestProcessAdapterBindsIncrementalRequestAndStagesVerifiedBaseState(t *testing.T) {
	jobRoot := t.TempDir()
	adapter := &ProcessAdapter{configuration: ProcessAdapterConfig{JobRoot: jobRoot, CompletionProvider: "openai", CompletionModel: "completion", EmbeddingProvider: "openai", EmbeddingModel: "embedding", ProducerIdentity: "worker", BuildDigest: "sha256:build", AttestationSignature: "signature"}}
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	state := map[string][]byte{"entities.parquet": []byte("immutable-state")}
	manifest, err := contracts.BuildGraphAdapterStateManifest(scope, "revision-base", state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	request := AdapterRequest{Scope: scope, JobID: "job-update", ConfigurationID: "configuration-a", RevisionID: "revision-update", BaseRevisionID: "revision-base", Mode: contracts.GraphIndexModeIncremental,
		Projection: []byte("{\"id\":\"memory-a\",\"text\":\"Day 10\",\"kind\":\"memory\",\"correlation_token\":\"corr-a\"}\n"), Correlations: []byte("{\"corr-a\":{\"canonical_kind\":\"memory\",\"canonical_id\":\"memory-a\",\"canonical_fingerprint\":\"sha256:a\"}}"), ProjectionManifest: []byte("manifest"), BaseState: state, BaseStateManifest: manifest}
	encoded, err := adapter.requestEnvelope(jobRoot, request)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(encoded, &envelope); err != nil || envelope["command"] != "incremental-update" {
		t.Fatalf("incremental command not bound: %s", encoded)
	}
	if err := stageProcessAdapterBase(jobRoot, request); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(jobRoot, "output", "entities.parquet"))
	if err != nil || string(staged) != "immutable-state" {
		t.Fatalf("base state not staged: %q %v", staged, err)
	}
}

func containsBytes(value, target []byte) bool {
	for index := 0; index+len(target) <= len(value); index++ {
		if string(value[index:index+len(target)]) == string(target) {
			return true
		}
	}
	return false
}
