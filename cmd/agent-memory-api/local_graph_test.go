package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/api"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestRegisteredProjectGraphWorkerReindexesOnlyItsSQLiteStore(t *testing.T) {
	service, store := newLocalGraphWorkerFixture(t, "complete")
	defer store.Close()
	ctx := context.Background()
	input := api.LocalProjectGraphOperationInput{
		Workspace: "project-a", ConfigurationID: localGraphConfigurationID, Action: application.GraphOperationRebuild,
		IdempotencyKey: "project-a-reindex", TenantID: "owner-tenant", AccountID: "owner-account", Actor: "owner-subject",
	}
	accepted, err := service.OperateGraph(ctx, input)
	if err != nil || !accepted.Accepted || accepted.Job == nil {
		t.Fatalf("queue project Reindex: result=%#v err=%v", accepted, err)
	}
	coalesced, err := service.OperateGraph(ctx, input)
	if err != nil || !coalesced.Coalesced || coalesced.Job == nil || coalesced.Job.ID != accepted.Job.ID {
		t.Fatalf("duplicate project Reindex was not coalesced: result=%#v err=%v", coalesced, err)
	}
	if processed, err := service.runLocalGraphWorkerOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("process project Reindex: processed=%d err=%v", processed, err)
	}
	active, previous, err := store.ActiveGraphRevisions(ctx, core.GraphScope{WorkspaceID: "project-a"}, localGraphConfigurationID)
	if err != nil || active != accepted.RevisionID || previous != "" {
		t.Fatalf("active project revision = %q (previous %q), err %v", active, previous, err)
	}
	status, err := store.GraphIndexStatus(ctx, core.GraphScope{WorkspaceID: "project-a"}, localGraphConfigurationID)
	if err != nil || status.State != "ready" || status.LastJobState != core.GraphJobCompleted {
		t.Fatalf("project graph status = %#v, err %v", status, err)
	}
	if memory, err := store.GetMemory(ctx, "memory-a"); err != nil || memory.Content != "Project A stores a useful indexing fact." {
		t.Fatalf("canonical memory changed during reindex: memory=%#v err=%v", memory, err)
	}
}

func TestRegisteredProjectGraphStatusIsUnconfiguredWithoutProvisioning(t *testing.T) {
	service, store := newLocalGraphWorkerFixture(t, "complete")
	defer store.Close()
	ctx := context.Background()
	readiness, err := service.GraphReadiness(ctx, "project-a", localGraphConfigurationID)
	if err != nil || !readiness.Ready {
		t.Fatalf("local runtime readiness = %#v, err %v", readiness, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		status, err := service.GraphStatus(ctx, "project-a", localGraphConfigurationID)
		if err != nil || status.State != "unconfigured" || status.ConfigurationID != localGraphConfigurationID {
			t.Fatalf("unconfigured project graph status = %#v, err %v", status, err)
		}
	}
	if _, err := store.GraphIndexStatus(ctx, core.GraphScope{WorkspaceID: "project-a"}, localGraphConfigurationID); !errors.Is(err, contracts.ErrGraphOperationNotFound) {
		t.Fatalf("status read provisioned project graph configuration: %v", err)
	}
	queue, err := service.GraphQueue(ctx)
	if err != nil || queue.ProjectsScanned != 1 || queue.ProjectsUnavailable != 0 || len(queue.Jobs) != 0 {
		t.Fatalf("empty project queue was not reported accurately: queue=%+v err=%v", queue, err)
	}
}

func TestRegisteredProjectGraphQueueShowsActiveJobsAcrossProjectsAndPartialReads(t *testing.T) {
	service, projectAStore := newLocalGraphWorkerFixture(t, "complete")
	defer projectAStore.Close()
	ctx := context.Background()

	projectBRoot := t.TempDir()
	if _, err := service.manager.Init(ctx, workspace.InitOptions{CWD: projectBRoot, ProjectName: "project-b", NoRule: true}); err != nil {
		t.Fatal(err)
	}
	projectB, err := service.manager.Project("project-b")
	if err != nil {
		t.Fatal(err)
	}
	projectBStore, err := sqlite.Open(ctx, projectB.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer projectBStore.Close()

	for _, workspaceName := range []string{"project-a", "project-b"} {
		result, operationErr := service.OperateGraph(ctx, api.LocalProjectGraphOperationInput{
			Workspace: workspaceName, ConfigurationID: localGraphConfigurationID, Action: application.GraphOperationRebuild,
			IdempotencyKey: workspaceName + "-queue-visible", TenantID: "owner-tenant", AccountID: "owner-account", Actor: "owner-subject",
		})
		if operationErr != nil || !result.Accepted || result.Job == nil {
			t.Fatalf("queue %s reindex: result=%#v err=%v", workspaceName, result, operationErr)
		}
	}
	claimed, err := projectBStore.ClaimGraphJobs(ctx, core.GraphScope{WorkspaceID: "project-b"}, "test-worker", 1, time.Minute, time.Now().UTC())
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim project-b job: jobs=%#v err=%v", claimed, err)
	}

	projectCRoot := t.TempDir()
	if _, err := service.manager.Init(ctx, workspace.InitOptions{CWD: projectCRoot, ProjectName: "project-c", NoRule: true}); err != nil {
		t.Fatal(err)
	}
	projectC, err := service.manager.Project("project-c")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectC.DBPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	queue, err := service.GraphQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queue.ProjectsScanned != 3 || queue.ProjectsUnavailable != 1 || len(queue.UnavailableProjectNames) != 1 || queue.UnavailableProjectNames[0] != "project-c" {
		t.Fatalf("queue scan did not report project-level failure: %+v", queue)
	}
	if len(queue.Jobs) != 2 || queue.Jobs[0].Workspace != "project-b" || queue.Jobs[0].State != string(core.GraphJobRunning) || queue.Jobs[1].Workspace != "project-a" || queue.Jobs[1].State != string(core.GraphJobQueued) {
		t.Fatalf("queue scan did not expose queued and running work: %+v", queue.Jobs)
	}
	for _, job := range queue.Jobs {
		if job.JobID == "" || job.AgeSeconds < 0 || job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
			t.Fatalf("queue row lacks bounded task status: %+v", job)
		}
	}
}

func TestRegisteredProjectGraphWorkerFailureKeepsPreviousRevisionAndCanonicalMemory(t *testing.T) {
	service, store := newLocalGraphWorkerFixture(t, "fail")
	defer store.Close()
	ctx := context.Background()
	scope := core.GraphScope{WorkspaceID: "project-a"}
	now := time.Now().UTC()
	configuration := core.GraphConfiguration{
		ID: localGraphConfigurationID, Scope: scope, Version: 1, Enabled: true,
		AdapterName: "agent-memory-graphrag-adapter", AdapterVersion: contracts.SupportedGraphAdapterVersion,
		IndexMethod: core.GraphIndexStandard, ProjectionVersion: localGraphProjection,
		ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1, PromptFingerprint: localGraphPrompt,
		ModelRoute: localGraphModelRoute(service.graphConfig), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	previous := core.GraphRevision{
		ID: "previous-active", Scope: scope, ConfigurationID: localGraphConfigurationID,
		State: core.GraphRevisionReady, Cutoff: core.GraphWatermark{Sequence: 1, EventTime: now, Digest: "sha256:previous"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateGraphRevision(ctx, previous); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateGraphRevision(ctx, core.GraphActivation{Scope: scope, ConfigurationID: localGraphConfigurationID, CandidateRevision: previous.ID}); err != nil {
		t.Fatal(err)
	}
	accepted, err := service.OperateGraph(ctx, api.LocalProjectGraphOperationInput{
		Workspace: "project-a", ConfigurationID: localGraphConfigurationID, Action: application.GraphOperationRebuild,
		ExpectedRevision: previous.ID, IdempotencyKey: "project-a-failed-reindex",
		TenantID: "owner-tenant", AccountID: "owner-account", Actor: "owner-subject",
	})
	if err != nil || !accepted.Accepted || accepted.Job == nil {
		t.Fatalf("queue project Reindex: result=%#v err=%v", accepted, err)
	}
	if processed, err := service.runLocalGraphWorkerOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("record failed project Reindex: processed=%d err=%v", processed, err)
	}
	active, prior, err := store.ActiveGraphRevisions(ctx, scope, localGraphConfigurationID)
	if err != nil || active != previous.ID || prior != "" {
		t.Fatalf("failed reindex replaced previous revision: active=%q prior=%q err=%v", active, prior, err)
	}
	status, err := store.GraphIndexStatus(ctx, scope, localGraphConfigurationID)
	if err != nil || status.State != string(core.GraphJobFailed) || status.LastJobState != core.GraphJobFailed {
		t.Fatalf("failed reindex status = %#v, err %v", status, err)
	}
	if memory, err := store.GetMemory(ctx, "memory-a"); err != nil || memory.Content != "Project A stores a useful indexing fact." {
		t.Fatalf("failed reindex changed canonical memory: memory=%#v err=%v", memory, err)
	}
}

func TestRegisteredProjectGraphWorkerRejectsMalformedArtifact(t *testing.T) {
	service, store := newLocalGraphWorkerFixture(t, "malformed")
	defer store.Close()
	ctx := context.Background()
	accepted, err := service.OperateGraph(ctx, api.LocalProjectGraphOperationInput{
		Workspace: "project-a", ConfigurationID: localGraphConfigurationID, Action: application.GraphOperationRebuild,
		IdempotencyKey: "project-a-malformed-artifact", TenantID: "owner-tenant", AccountID: "owner-account", Actor: "owner-subject",
	})
	if err != nil || !accepted.Accepted {
		t.Fatalf("queue project Reindex: result=%#v err=%v", accepted, err)
	}
	if processed, err := service.runLocalGraphWorkerOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("record malformed project artifact: processed=%d err=%v", processed, err)
	}
	active, _, err := store.ActiveGraphRevisions(ctx, core.GraphScope{WorkspaceID: "project-a"}, localGraphConfigurationID)
	if err != nil || active != "" {
		t.Fatalf("malformed artifact activated revision %q: %v", active, err)
	}
	status, err := store.GraphIndexStatus(ctx, core.GraphScope{WorkspaceID: "project-a"}, localGraphConfigurationID)
	if err != nil || status.LastJobState != core.GraphJobFailed {
		t.Fatalf("malformed artifact job state = %#v, err %v", status, err)
	}
}

// TestLocalGraphAdapterHelper is invoked as a tiny subprocess by the fake adapter
// wrapper. The environment variable is set by that wrapper after the runner has
// applied its restricted adapter environment.
func TestLocalGraphAdapterHelper(t *testing.T) {
	mode := os.Getenv("AGENT_MEMORY_TEST_GRAPH_ADAPTER_MODE")
	if mode == "" {
		return
	}
	args := os.Args
	separator := -1
	for index, arg := range args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(args) {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_command_missing"})
	}
	commandArgs := args[separator+1:]
	if commandArgs[0] == string(application.LocalGraphReadiness) {
		writeLocalGraphAdapterResponse(map[string]any{
			"contract_version": "graph-adapter/v1", "state": "ready",
			"adapter": "agent-memory-graphrag-adapter", "adapter_version": contracts.SupportedGraphAdapterVersion,
		})
	}
	requestPath := ""
	for index := 0; index+1 < len(commandArgs); index++ {
		if commandArgs[index] == "--request" {
			requestPath = commandArgs[index+1]
			break
		}
	}
	if requestPath == "" {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_request_missing"})
	}
	if mode == "fail" {
		writeLocalGraphAdapterResponse(map[string]any{"contract_version": "graph-adapter/v1", "state": "failed", "reason_code": "fixture_failure"})
	}
	if mode != "complete" && mode != "malformed" {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_mode_invalid"})
	}
	requestBytes, err := os.ReadFile(requestPath)
	if err != nil {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_request_unreadable"})
	}
	var envelope struct {
		JobRoot string `json:"job_root"`
		Request struct {
			Scope                core.GraphScope              `json:"scope"`
			ConfigurationID      string                       `json:"configuration_id"`
			JobID                string                       `json:"job_id"`
			RevisionID           string                       `json:"revision_id"`
			Method               string                       `json:"method"`
			InputManifestHash    string                       `json:"input_manifest_hash"`
			PromptFingerprint    string                       `json:"prompt_fingerprint"`
			ProducerIdentity     string                       `json:"producer_identity"`
			BuildDigest          string                       `json:"build_digest"`
			AttestationSignature string                       `json:"attestation_signature"`
			CompletionModel      string                       `json:"completion_model"`
			EmbeddingModel       string                       `json:"embedding_model"`
			Correlations         map[string]map[string]string `json:"correlations"`
		} `json:"request"`
	}
	if err := json.Unmarshal(requestBytes, &envelope); err != nil {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_request_invalid"})
	}
	tokens := make([]string, 0, len(envelope.Request.Correlations))
	for token := range envelope.Request.Correlations {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	if len(tokens) == 0 {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_correlations_missing"})
	}
	evidence, err := json.Marshal([]map[string]string{envelope.Request.Correlations[tokens[0]]})
	if err != nil {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_evidence_invalid"})
	}
	entityBytes, err := json.Marshal(map[string]any{"id": "entity-a", "name": "Project A", "type": "workspace", "evidence": json.RawMessage(evidence)})
	if err != nil {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_entity_invalid"})
	}
	entityBytes = append(entityBytes, '\n')
	root := filepath.Join(envelope.JobRoot, "output", "normalized")
	if err := os.MkdirAll(root, 0o700); err != nil {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_output_unavailable"})
	}
	files := map[string][]byte{"entities.jsonl": entityBytes, "relationships.jsonl": {}}
	outputs := make([]contracts.GraphArtifactFile, 0, len(files))
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o600); err != nil {
			writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_output_write_failed"})
		}
		digest := sha256.Sum256(contents)
		outputs = append(outputs, contracts.GraphArtifactFile{
			Name: name, Kind: strings.TrimSuffix(name, ".jsonl"), Required: true, Bytes: int64(len(contents)),
			Rows: int64(strings.Count(string(contents), "\n")), SchemaFingerprint: "sha256:fixture-schema", ContentHash: "sha256:" + hex.EncodeToString(digest[:]),
		})
	}
	manifest := contracts.GraphArtifactManifest{
		ContractVersion: contracts.GraphAdapterContractV1, ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1,
		Scope: envelope.Request.Scope, ConfigurationID: envelope.Request.ConfigurationID, JobID: envelope.Request.JobID,
		RevisionID: envelope.Request.RevisionID, AdapterName: "agent-memory-graphrag-adapter", AdapterVersion: contracts.SupportedGraphAdapterVersion,
		GraphRAGVersion: "3.1.2", PythonVersion: "3.12.0", EnvironmentFingerprint: "sha256:fixture-environment",
		InputManifestHash: envelope.Request.InputManifestHash, ConfigurationFingerprint: "sha256:fixture-config",
		PromptFingerprint: envelope.Request.PromptFingerprint, IndexMethod: core.GraphIndexStandard,
		Mode: contracts.GraphIndexModeFull, Outputs: outputs, Models: []string{envelope.Request.CompletionModel, envelope.Request.EmbeddingModel},
		Status: contracts.GraphArtifactCompleted, CompletedAt: time.Now().UTC(),
		Attestation: contracts.GraphArtifactAttestation{ProducerIdentity: envelope.Request.ProducerIdentity, BuildDigest: envelope.Request.BuildDigest, Signature: envelope.Request.AttestationSignature},
	}
	if mode == "malformed" {
		manifest.Outputs[0].ContentHash = "sha256:tampered"
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_manifest_invalid"})
	}
	manifestPath := filepath.Join(root, "artifact-manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		writeLocalGraphAdapterResponse(map[string]any{"state": "failed", "reason_code": "fixture_manifest_write_failed"})
	}
	writeLocalGraphAdapterResponse(map[string]any{"contract_version": "graph-adapter/v1", "state": "completed", "artifact_manifest": manifestPath})
}

func writeLocalGraphAdapterResponse(value map[string]any) {
	payload, _ := json.Marshal(value)
	_, _ = fmt.Fprintln(os.Stdout, string(payload))
	os.Exit(0)
}

func newLocalGraphWorkerFixture(t *testing.T, mode string) (*localProjectService, *sqlite.Store) {
	t.Helper()
	baseDir := t.TempDir()
	manager, err := workspace.NewManager(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()
	if _, err := manager.Init(context.Background(), workspace.InitOptions{CWD: projectRoot, ProjectName: "project-a", NoRule: true}); err != nil {
		t.Fatal(err)
	}
	project, err := manager.Project("project-a")
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), project.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.UpsertMemory(context.Background(), &core.MemoryEntry{
		ID: "memory-a", Type: core.SemanticMemory, Content: "Project A stores a useful indexing fact.",
		Workspace: "project-a", Confidence: 1, StorageTier: core.TierVector,
		CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	adapter := filepath.Join(t.TempDir(), "fake-graphrag-adapter")
	if err := writeLocalGraphFakeAdapter(t, adapter, mode); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	graphConfig := config.DefaultGraphConfig(baseDir)
	graphConfig.Enabled = true
	graphConfig.Executable = adapter
	graphConfig.JobRoot = filepath.Join(baseDir, "graph-jobs")
	graphConfig.CompletionProvider, graphConfig.CompletionModel = "test", "completion-model"
	graphConfig.EmbeddingProvider, graphConfig.EmbeddingModel = "test", "embedding-model"
	graphConfig.TimeoutSeconds = 10
	service := &localProjectService{manager: manager, graphConfig: graphConfig, graphDataDir: baseDir, graphSigner: privateKey}
	return service, store
}

func writeLocalGraphFakeAdapter(t *testing.T, path, mode string) error {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	contents := fmt.Sprintf("#!/bin/sh\nexport AGENT_MEMORY_TEST_GRAPH_ADAPTER_MODE=%s\nexec %s -test.run=TestLocalGraphAdapterHelper -- \"$@\"\n", quote(mode), quote(executable))
	return os.WriteFile(path, []byte(contents), 0o700)
}
