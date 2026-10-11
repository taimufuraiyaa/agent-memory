package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

func TestNativeGraphWorkerPublishesCompleteCanonicalGraph(t *testing.T) {
	ctx := context.Background()
	store, configuration, runtime := nativeWorkerFixture(t)
	memory := nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Billing depends on the pricing runtime.")
	worker := nativeTestWorker(runtime, &nativeArtifactRunner{root: runtime.JobRoot})

	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err != nil {
		t.Fatalf("index initial journal: %v", err)
	}
	status := nativeWorkerStatus(t, store, configuration)
	if status.ActiveRevisionID == "" || !status.Fresh || status.PendingRecords != 0 || status.LastJobState != core.GraphJobCompleted {
		t.Fatalf("initial revision was not fully published: %+v", status)
	}
	snapshot, err := store.LoadActiveGraphSnapshot(ctx, configuration.Scope, 32, 32, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) != 2 || len(snapshot.Edges) != 1 || len(snapshot.Communities) != 1 {
		t.Fatalf("complete normalized snapshot missing: %+v", snapshot)
	}
	for _, node := range snapshot.Nodes {
		if len(node.Evidence) != 1 || node.Evidence[0].CanonicalID != memory.ID || node.Evidence[0].CanonicalFingerprint != core.FingerprintText(memory.Content) {
			t.Fatalf("graph did not preserve original canonical evidence: %+v", node)
		}
	}
}

func TestNativeGraphWorkerLeavesConcurrentInsertPendingThenIndexesIt(t *testing.T) {
	ctx := context.Background()
	store, configuration, runtime := nativeWorkerFixture(t)
	first := nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Billing depends on the pricing runtime.")
	var later core.MemoryEntry
	runner := &nativeArtifactRunner{root: runtime.JobRoot}
	runner.before = func(_ context.Context, _ map[string]json.RawMessage) error {
		later = nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "The newly inserted throughput blueprint prices TPS capacity.")
		return nil
	}
	worker := nativeTestWorker(runtime, runner)
	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err != nil {
		t.Fatalf("index first snapshot: %v", err)
	}
	firstStatus := nativeWorkerStatus(t, store, configuration)
	if firstStatus.ActiveRevisionID == "" || firstStatus.PendingRecords != 1 || firstStatus.Fresh {
		t.Fatalf("concurrent insert was lost or graph advertised as fresh: %+v", firstStatus)
	}
	runner.before = nil
	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err != nil {
		t.Fatalf("index remaining journal: %v", err)
	}
	status := nativeWorkerStatus(t, store, configuration)
	if status.ActiveRevisionID == firstStatus.ActiveRevisionID || status.PreviousRevisionID != firstStatus.ActiveRevisionID || status.PendingRecords != 0 || !status.Fresh {
		t.Fatalf("subsequent complete revision did not replace first: %+v", status)
	}
	snapshot, err := store.LoadActiveGraphSnapshot(ctx, configuration.Scope, 32, 32, 8)
	if err != nil {
		t.Fatal(err)
	}
	evidenceIDs := make(map[string]bool)
	for _, node := range snapshot.Nodes {
		for _, evidence := range node.Evidence {
			evidenceIDs[evidence.CanonicalID] = true
		}
	}
	if !evidenceIDs[first.ID] || !evidenceIDs[later.ID] {
		t.Fatalf("replacement revision omitted canonical snapshot evidence: %v", evidenceIDs)
	}
}

func TestNativeGraphWorkerRejectsInvalidAdapterArtifacts(t *testing.T) {
	for _, fault := range []string{"unknown_canonical_id", "wrong_fingerprint", "missing_relationships", "wrong_revision", "wrong_model", "wrong_prompt", "corrupt_entity_file", "incomplete_communities", "failed_manifest"} {
		t.Run(fault, func(t *testing.T) {
			ctx := context.Background()
			store, configuration, runtime := nativeWorkerFixture(t)
			nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Billing depends on the pricing runtime.")
			worker := nativeTestWorker(runtime, &nativeArtifactRunner{root: runtime.JobRoot, fault: fault})

			if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err == nil {
				t.Fatal("invalid adapter artifact was accepted")
			}
			status := nativeWorkerStatus(t, store, configuration)
			if status.ActiveRevisionID != "" || status.PendingRecords != 1 || status.LastJobState != core.GraphJobFailed {
				t.Fatalf("invalid candidate published or journal acknowledged: %+v", status)
			}
		})
	}
}

func TestNativeGraphWorkerRequiresRuntimeAndWorkspaceEnablement(t *testing.T) {
	for _, disabled := range []string{"runtime", "workspace"} {
		t.Run(disabled, func(t *testing.T) {
			ctx := context.Background()
			store, configuration, runtime := nativeWorkerFixture(t)
			nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Billing depends on the pricing runtime.")
			if disabled == "runtime" {
				runtime.Enabled = false
			} else {
				configuration.Enabled = false
				if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
					t.Fatal(err)
				}
			}
			runner := &nativeArtifactRunner{root: runtime.JobRoot}
			worker := nativeTestWorker(runtime, runner)
			if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err != nil {
				t.Fatal(err)
			}
			status := nativeWorkerStatus(t, store, configuration)
			if runner.indexRuns() != 0 || status.ActiveRevisionID != "" || status.PendingRecords != 1 {
				t.Fatalf("disabled execution changed graph or invoked adapter: runs=%d status=%+v", runner.indexRuns(), status)
			}
		})
	}
}

func TestNativeGraphWorkerConfigurationDisableFencesPublication(t *testing.T) {
	ctx := context.Background()
	store, configuration, runtime := nativeWorkerFixture(t)
	nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Billing depends on the pricing runtime.")
	runner := &nativeArtifactRunner{root: runtime.JobRoot}
	runner.before = func(_ context.Context, _ map[string]json.RawMessage) error {
		configuration.Enabled = false
		return store.UpsertGraphConfiguration(ctx, configuration)
	}
	worker := nativeTestWorker(runtime, runner)
	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err == nil {
		t.Fatal("publication ignored in-flight configuration disable")
	}
	status := nativeWorkerStatus(t, store, configuration)
	if status.ActiveRevisionID != "" || status.PendingRecords != 1 {
		t.Fatalf("disabled configuration published a candidate: %+v", status)
	}
}

func TestNativeGraphWorkerBackoffAndAttemptLimitPreserveJournal(t *testing.T) {
	ctx := context.Background()
	store, configuration, runtime := nativeWorkerFixture(t)
	nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Billing depends on the pricing runtime.")
	runner := &nativeArtifactRunner{root: runtime.JobRoot, fault: "unknown_canonical_id"}
	worker := nativeTestWorker(runtime, runner)
	worker.maxAttempts = 2
	now := time.Now().UTC()
	worker.now = func() time.Time { return now }
	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err == nil {
		t.Fatal("first invalid artifact reported success")
	}
	failed := nativeWorkerStatus(t, store, configuration)
	if failed.LastJobState != core.GraphJobFailed || failed.PendingRecords != 1 {
		t.Fatalf("first attempt should fail without acknowledging source: %+v", failed)
	}
	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err != nil {
		t.Fatalf("backoff pass should leave durable work unchanged: %v", err)
	}
	backoff := nativeWorkerStatus(t, store, configuration)
	if backoff.LastJobID != failed.LastJobID || runner.indexRuns() != 1 {
		t.Fatalf("backoff immediately repeated expensive indexing: runs=%d status=%+v", runner.indexRuns(), backoff)
	}
	now = now.Add(time.Minute)
	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err == nil {
		t.Fatal("second invalid artifact reported success")
	}
	terminal := nativeWorkerStatus(t, store, configuration)
	if terminal.LastJobState != core.GraphJobDeadLetter || terminal.PendingRecords != 1 || terminal.ActiveRevisionID != "" {
		t.Fatalf("attempt limit did not retain a non-published journal: %+v", terminal)
	}
	now = now.Add(time.Hour)
	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err != nil {
		t.Fatalf("dead-letter pass should wait for operator action: %v", err)
	}
	if runner.indexRuns() != 2 {
		t.Fatalf("attempt limit repeated model work: runs=%d", runner.indexRuns())
	}
}

func TestNativeGraphWorkerShutdownCannotPublishPartialRevision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, configuration, runtime := nativeWorkerFixture(t)
	nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Billing depends on the pricing runtime.")
	runner := &nativeArtifactRunner{root: runtime.JobRoot}
	runner.before = func(runCtx context.Context, _ map[string]json.RawMessage) error {
		cancel()
		<-runCtx.Done()
		return runCtx.Err()
	}
	worker := nativeTestWorker(runtime, runner)
	if err := worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID); err == nil {
		t.Fatal("shutdown during indexing reported success")
	}
	status := nativeWorkerStatus(t, store, configuration)
	if status.ActiveRevisionID != "" || status.PendingRecords != 1 || status.LastJobState == core.GraphJobCompleted || status.CurrentJob != nil {
		t.Fatalf("shutdown activated or stranded partial output: %+v", status)
	}
}

func TestNativeGraphWorkerRenewsLeaseAndHonorsOperatorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, configuration, runtime := nativeWorkerFixture(t)
	nativeWorkerMemory(t, store, configuration.Scope.WorkspaceID, "Billing depends on the pricing runtime.")
	entered := make(chan struct{})
	runner := &nativeArtifactRunner{root: runtime.JobRoot}
	runner.before = func(runCtx context.Context, _ map[string]json.RawMessage) error {
		close(entered)
		<-runCtx.Done()
		return runCtx.Err()
	}
	worker := nativeTestWorker(runtime, runner)
	worker.lease = 200 * time.Millisecond
	worker.heartbeat = 10 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- worker.RunWorkspace(ctx, store, configuration.Scope.WorkspaceID) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never started adapter")
	}
	initial := nativeWorkerStatus(t, store, configuration)
	if initial.CurrentJob == nil || initial.CurrentJob.LeaseExpiresAt == nil {
		t.Fatalf("worker has no durable live lease: %+v", initial)
	}
	initialExpiry := *initial.CurrentJob.LeaseExpiresAt
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			status := nativeWorkerStatus(t, store, configuration)
			if status.CurrentJob != nil && status.CurrentJob.LeaseExpiresAt != nil && status.CurrentJob.LeaseExpiresAt.After(initialExpiry) {
				goto renewed
			}
		case <-deadline.C:
			t.Fatal("lease did not renew while adapter was running")
		}
	}
renewed:
	if err := store.CancelGraphJob(ctx, configuration.Scope, initial.CurrentJob.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled job reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("operator cancellation did not stop adapter")
	}
	status := nativeWorkerStatus(t, store, configuration)
	if status.ActiveRevisionID != "" || status.PendingRecords != 1 || status.LastJobState != core.GraphJobCancelled {
		t.Fatalf("cancelled job published or lost its terminal state: %+v", status)
	}
}

func TestNativeGraphWorkerOperatorRetryUsesFreshRevision(t *testing.T) {
	store, cfg, runtime := nativeWorkerFixture(t)
	ctx := context.Background()
	nativeWorkerMemory(t, store, cfg.Scope.WorkspaceID, "Pricing depends on runtime")
	runner := &nativeArtifactRunner{root: runtime.JobRoot, fault: "wrong_revision"}
	worker := nativeTestWorker(runtime, runner)
	if err := worker.RunWorkspace(ctx, store, cfg.Scope.WorkspaceID); err == nil {
		t.Fatal("invalid first attempt succeeded")
	}
	failed := nativeWorkerStatus(t, store, cfg)
	if _, err := NewGraphOperationService(store).Operate(ctx, GraphOperationRequest{Scope: cfg.Scope, ConfigurationID: cfg.ID, Action: GraphOperationRetry, JobID: failed.LastJobID}); err != nil {
		t.Fatal(err)
	}
	runner.fault = ""
	if err := worker.RunWorkspace(ctx, store, cfg.Scope.WorkspaceID); err != nil {
		t.Fatalf("operator retry could not rebuild failed revision: %v", err)
	}
	status := nativeWorkerStatus(t, store, cfg)
	if !status.Fresh || status.ActiveRevisionID == "" || status.LastJobState != core.GraphJobCompleted {
		t.Fatalf("retry not published: %+v", status)
	}
}

func TestNativeGraphWorkerKeepsStableEntityIdentitiesWhenEvidenceGrows(t *testing.T) {
	store, cfg, runtime := nativeWorkerFixture(t)
	ctx := context.Background()
	nativeWorkerMemory(t, store, cfg.Scope.WorkspaceID, "Pricing depends on runtime")
	worker := nativeTestWorker(runtime, &nativeArtifactRunner{root: runtime.JobRoot})
	if err := worker.RunWorkspace(ctx, store, cfg.Scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	first, err := store.LoadActiveGraphSnapshot(ctx, cfg.Scope, 32, 32, 8)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, node := range first.Nodes {
		ids[node.Entity.ID] = true
	}
	nativeWorkerMemory(t, store, cfg.Scope.WorkspaceID, "Runtime has new operating cost evidence")
	if err = worker.RunWorkspace(ctx, store, cfg.Scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	second, err := store.LoadActiveGraphSnapshot(ctx, cfg.Scope, 32, 32, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range second.Nodes {
		if !ids[node.Entity.ID] {
			t.Fatalf("evidence growth replaced stable identity: %s", node.Entity.ID)
		}
	}
}

func nativeWorkerFixture(t *testing.T) (*sqlite.Store, core.GraphConfiguration, config.GraphConfig) {
	t.Helper()
	root := t.TempDir()
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "native-worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	executable := filepath.Join(root, "synthetic-graph-adapter")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := config.DefaultGraphConfig(root)
	runtime.Enabled, runtime.Executable = true, executable
	runtime.CompletionProvider, runtime.CompletionModel = "ollama_chat", "synthetic-completion"
	runtime.EmbeddingProvider, runtime.EmbeddingModel = "ollama", "synthetic-embedding"
	now := time.Now().UTC()
	configuration := core.GraphConfiguration{
		ID: "default", Scope: core.GraphScope{WorkspaceID: "native-worker-test"}, Version: 1,
		Enabled: true, AdapterName: "agent-memory-graphrag", AdapterVersion: "0.1.0",
		IndexMethod: core.GraphIndexStandard, ProjectionVersion: "sqlite-memory-v1",
		ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1,
		PromptFingerprint:     "sha256:54293829a35e2f911cbd85bcdfc5733423c3f7dd12327869f22fc6daee6104fd",
		ModelRoute:            "ollama_chat/synthetic-completion;ollama/synthetic-embedding", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertGraphConfiguration(context.Background(), configuration); err != nil {
		t.Fatal(err)
	}
	return store, configuration, runtime
}

func nativeWorkerMemory(t *testing.T, store *sqlite.Store, workspace, content string) core.MemoryEntry {
	t.Helper()
	now := time.Now().UTC()
	memory := core.MemoryEntry{ID: uuid.NewString(), Workspace: workspace, Type: core.SemanticMemory, Content: content,
		Source: core.MemorySource{Type: core.SourceUserInput}, Confidence: 0.9, StorageTier: core.TierMarkdown,
		CreatedAt: now, UpdatedAt: now, LastAccessedAt: now}
	if err := store.InsertMemoryByHash(context.Background(), &memory, core.FingerprintText(memory.Content)); err != nil {
		t.Fatal(err)
	}
	return memory
}

func nativeTestWorker(configuration config.GraphConfig, runner *nativeArtifactRunner) *NativeGraphWorker {
	worker := NewNativeGraphWorker(configuration)
	worker.runner = runner
	worker.batch = 1
	worker.maxWait = time.Millisecond
	worker.lease = time.Minute
	worker.heartbeat = 5 * time.Millisecond
	return worker
}

func nativeWorkerStatus(t *testing.T, store *sqlite.Store, configuration core.GraphConfiguration) contracts.GraphIndexStatus {
	t.Helper()
	status, err := store.GraphIndexStatus(context.Background(), configuration.Scope, configuration.ID)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

// The fake is solely at the slow external adapter boundary. Production projection,
// validation, normalized import, leases and revision publication stay real.
type nativeArtifactRunner struct {
	root   string
	fault  string
	before func(context.Context, map[string]json.RawMessage) error
	mu     sync.Mutex
	runs   int
}

func (r *nativeArtifactRunner) indexRuns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs
}

func (r *nativeArtifactRunner) Run(ctx context.Context, command LocalGraphCommand, request any) (LocalGraphRunResult, error) {
	if command == LocalGraphReadiness {
		return LocalGraphRunResult{State: "ready"}, nil
	}
	r.mu.Lock()
	r.runs++
	r.mu.Unlock()
	payload, err := json.Marshal(request)
	if err != nil {
		return LocalGraphRunResult{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return LocalGraphRunResult{}, err
	}
	if r.before != nil {
		if err := r.before(ctx, fields); err != nil {
			return LocalGraphRunResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return LocalGraphRunResult{}, err
	}
	var scope core.GraphScope
	if err := json.Unmarshal(fields["scope"], &scope); err != nil {
		return LocalGraphRunResult{}, err
	}
	var correlations map[string]GraphCorrelationReference
	if err := json.Unmarshal(fields["correlations"], &correlations); err != nil {
		return LocalGraphRunResult{}, err
	}
	if len(correlations) == 0 {
		return LocalGraphRunResult{}, fmt.Errorf("synthetic adapter received no projected correlations")
	}
	tokens := make([]string, 0, len(correlations))
	for token := range correlations {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	evidence := make([]validation.GraphArtifactEvidence, 0, len(tokens))
	for _, token := range tokens {
		correlation := correlations[token]
		evidence = append(evidence, validation.GraphArtifactEvidence{CanonicalKind: string(correlation.CanonicalKind), CanonicalID: correlation.CanonicalID, CanonicalFingerprint: correlation.CanonicalFingerprint})
	}
	if r.fault == "unknown_canonical_id" {
		evidence[0].CanonicalID = uuid.NewString()
	}
	if r.fault == "wrong_fingerprint" {
		evidence[0].CanonicalFingerprint = core.FingerprintText("different canonical content")
	}
	entities := []validation.GraphArtifactEntity{
		{ID: "billing", Name: "Billing", Type: "service", Evidence: evidence},
		{ID: "runtime", Name: "Pricing runtime", Type: "service", Evidence: evidence},
	}
	relationships := []validation.GraphArtifactRelationship{{ID: "billing-runtime", SourceID: "billing", TargetID: "runtime", Kind: "depends_on", Evidence: evidence}}
	communities := []validation.GraphArtifactCommunity{{ID: "pricing", EntityIDs: []string{"billing", "runtime"}}}
	reports := []validation.GraphArtifactReport{{ID: "pricing-report", CommunityID: "pricing", Title: "Pricing dependency", Summary: "Billing depends on the pricing runtime.", Evidence: evidence}}
	files := make(map[string][]byte)
	for name, records := range map[string]any{"entities.jsonl": entities, "relationships.jsonl": relationships, "communities.jsonl": communities, "community_reports.jsonl": reports} {
		var values []json.RawMessage
		encoded, err := json.Marshal(records)
		if err != nil {
			return LocalGraphRunResult{}, err
		}
		if err := json.Unmarshal(encoded, &values); err != nil {
			return LocalGraphRunResult{}, err
		}
		for _, value := range values {
			files[name] = append(files[name], value...)
			files[name] = append(files[name], '\n')
		}
	}
	if r.fault == "missing_relationships" {
		delete(files, "relationships.jsonl")
	}
	if r.fault == "incomplete_communities" {
		delete(files, "community_reports.jsonl")
	}
	mode := contracts.GraphIndexModeFull
	if command == LocalGraphIncremental {
		mode = contracts.GraphIndexModeIncremental
	}
	manifest := contracts.GraphArtifactManifest{
		ContractVersion: contracts.GraphAdapterContractV1, ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1,
		Scope: scope, ConfigurationID: nativePayloadString(fields, "configuration_id"), JobID: nativePayloadString(fields, "job_id"), RevisionID: nativePayloadString(fields, "revision_id"),
		AdapterName: "agent-memory-graphrag-adapter", AdapterVersion: "0.1.0", GraphRAGVersion: "3.1.2", PythonVersion: "3.13.5", EnvironmentFingerprint: "sha256:synthetic-environment",
		InputManifestHash: nativePayloadString(fields, "input_manifest_hash"), ConfigurationFingerprint: "sha256:synthetic-configuration",
		PromptFingerprint: nativePayloadString(fields, "prompt_fingerprint"), IndexMethod: core.GraphIndexStandard, Mode: mode,
		Models: []string{nativePayloadString(fields, "completion_model"), nativePayloadString(fields, "embedding_model")}, Status: contracts.GraphArtifactCompleted, CompletedAt: time.Now().UTC(),
		Attestation: contracts.GraphArtifactAttestation{ProducerIdentity: nativePayloadString(fields, "producer_identity"), BuildDigest: nativePayloadString(fields, "build_digest"), Signature: nativePayloadString(fields, "attestation_signature")},
	}
	if r.fault == "wrong_revision" {
		manifest.RevisionID = "another-candidate"
	}
	if r.fault == "wrong_model" {
		manifest.Models[0] = "unapproved-model"
	}
	if r.fault == "wrong_prompt" {
		manifest.PromptFingerprint = "sha256:other-prompts"
	}
	if r.fault == "failed_manifest" {
		manifest.Status, manifest.FailureCode = contracts.GraphArtifactFailed, "synthetic_failure"
	}
	if err := os.MkdirAll(r.root, 0o700); err != nil {
		return LocalGraphRunResult{}, err
	}
	jobDir, err := os.MkdirTemp(r.root, ".synthetic-graph-job-")
	if err != nil {
		return LocalGraphRunResult{}, err
	}
	artifactRoot := filepath.Join(jobDir, "output", "normalized")
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		return LocalGraphRunResult{}, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := files[name]
		digest := sha256.Sum256(content)
		manifest.Outputs = append(manifest.Outputs, contracts.GraphArtifactFile{Name: name, Kind: strings.TrimSuffix(name, ".jsonl"), Required: true, Bytes: int64(len(content)), Rows: int64(strings.Count(string(content), "\n")), SchemaFingerprint: "sha256:normalized-v1", ContentHash: "sha256:" + hex.EncodeToString(digest[:])})
		if err := os.WriteFile(filepath.Join(artifactRoot, name), content, 0o600); err != nil {
			return LocalGraphRunResult{}, err
		}
	}
	if r.fault == "corrupt_entity_file" {
		if err := os.WriteFile(filepath.Join(artifactRoot, "entities.jsonl"), []byte("{}\n"), 0o600); err != nil {
			return LocalGraphRunResult{}, err
		}
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return LocalGraphRunResult{}, err
	}
	manifestPath := filepath.Join(artifactRoot, "artifact-manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		return LocalGraphRunResult{}, err
	}
	return LocalGraphRunResult{State: "completed", JobDir: jobDir, Response: map[string]any{"artifact_manifest": manifestPath}}, nil
}

func nativePayloadString(fields map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(fields[name], &value)
	return value
}
