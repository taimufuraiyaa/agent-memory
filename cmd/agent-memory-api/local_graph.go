package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/api"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphindex"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

const (
	localGraphConfigurationID = "default"
	localGraphProjection      = "sqlite-memory-v1"
	localGraphPrompt          = "sha256:35635c8c6d1aad0005484e2b0a9f3c8424d173bdf682e49e43ccfeefc89e3f99"
	localGraphWorkerOwner     = "local-project-graph-worker"
	localOllamaLoopbackURL    = "http://127.0.0.1:11434"
	localOllamaGatewayURL     = "http://host.docker.internal:11434"
)

var _ api.LocalProjectGraphService = (*localProjectService)(nil)

func (service *localProjectService) GraphReadiness(ctx context.Context, workspaceName, configurationID string) (application.GraphIndexReadiness, error) {
	if _, err := service.manager.Project(workspaceName); err != nil {
		return application.GraphIndexReadiness{}, err
	}
	readiness := service.localGraphRuntimeReadiness(ctx, configurationID)
	return readiness, nil
}

func (service *localProjectService) localGraphRuntimeReadiness(ctx context.Context, configurationID string) application.GraphIndexReadiness {
	readiness := application.GraphIndexReadiness{
		ConfigurationID: strings.TrimSpace(configurationID), Enabled: service.graphConfig.Enabled,
		AdapterName: "agent-memory-graphrag-adapter", AdapterVersion: contracts.SupportedGraphAdapterVersion,
		ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1,
	}
	if readiness.ConfigurationID == "" {
		readiness.ConfigurationID = localGraphConfigurationID
	}
	switch {
	case service.graphConfigReason != "":
		readiness.State, readiness.ReasonCode, readiness.Reason = "unavailable", "invalid_graph_settings_file", service.graphConfigReason
	case !service.graphConfig.Enabled:
		readiness.State, readiness.ReasonCode, readiness.Reason = "disabled", "graph_index_disabled", "Graph indexing is disabled in local configuration."
	case !filepath.IsAbs(service.graphConfig.Executable):
		readiness.State, readiness.ReasonCode, readiness.Reason = "unavailable", "adapter_not_configured", "Configure an absolute GraphRAG adapter executable."
	case strings.TrimSpace(service.graphConfig.CompletionProvider) == "" || strings.TrimSpace(service.graphConfig.CompletionModel) == "" || strings.TrimSpace(service.graphConfig.EmbeddingProvider) == "" || strings.TrimSpace(service.graphConfig.EmbeddingModel) == "":
		readiness.State, readiness.ReasonCode, readiness.Reason = "unavailable", "model_route_not_configured", "Configure GraphRAG completion and embedding model routes."
	default:
		if err := service.graphConfig.Validate(service.graphDataDir); err != nil {
			readiness.State, readiness.ReasonCode, readiness.Reason = "unavailable", "invalid_graph_configuration", "The local GraphRAG configuration is outside supported policy."
			return readiness
		}
		info, err := os.Lstat(service.graphConfig.Executable)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			readiness.State, readiness.ReasonCode, readiness.Reason = "unavailable", "adapter_unavailable", "The configured GraphRAG adapter executable is unavailable."
			return readiness
		}
		if err := localOllamaModelsAvailable(ctx, localGraphOllamaEndpoint(), localGraphOllamaModels(service.graphConfig)); err != nil {
			readiness.State, readiness.ReasonCode, readiness.Reason = "unavailable", "local_model_unavailable", "The configured local Ollama models are not available."
			return readiness
		}
		readiness.Compatible = contracts.GraphAdapterCompatible(readiness.AdapterName, readiness.AdapterVersion, readiness.ArtifactSchemaVersion)
		readiness.Ready = readiness.Compatible
		if !readiness.Compatible {
			readiness.State, readiness.ReasonCode, readiness.Reason = "incompatible", "unsupported_adapter_contract", "The configured GraphRAG adapter contract is unsupported."
		} else {
			readiness.State = "ready"
		}
	}
	return readiness
}

func (service *localProjectService) GraphStatus(ctx context.Context, workspaceName, configurationID string) (application.GraphIndexStatus, error) {
	if _, err := service.manager.Project(workspaceName); err != nil {
		return application.GraphIndexStatus{}, err
	}
	configurationID = strings.TrimSpace(configurationID)
	if configurationID == "" {
		configurationID = localGraphConfigurationID
	}
	store, err := service.openProjectStore(ctx, workspaceName)
	if err != nil {
		return application.GraphIndexStatus{}, err
	}
	defer store.Close()
	status, err := store.GraphIndexStatus(ctx, core.GraphScope{WorkspaceID: workspaceName}, configurationID)
	if !errors.Is(err, contracts.ErrGraphOperationNotFound) {
		return status, err
	}
	readiness := service.localGraphRuntimeReadiness(ctx, configurationID)
	return application.GraphIndexStatus{
		ConfigurationID: configurationID, ConfigurationVersion: 1, Enabled: readiness.Enabled,
		State: "unconfigured", AdapterName: readiness.AdapterName, AdapterVersion: readiness.AdapterVersion,
		Compatible: readiness.Compatible, ArtifactSchemaVersion: readiness.ArtifactSchemaVersion,
		AuthorizedOperations: []application.GraphOperationAction{application.GraphOperationRebuild},
	}, nil
}

func (service *localProjectService) GraphQueue(ctx context.Context) (api.LocalProjectGraphQueue, error) {
	if service == nil || service.manager == nil {
		return api.LocalProjectGraphQueue{}, errors.New("local project registry is unavailable")
	}
	projects, err := service.manager.ProjectNames()
	if err != nil {
		return api.LocalProjectGraphQueue{}, err
	}
	queue := api.LocalProjectGraphQueue{
		Jobs:                    make([]api.LocalProjectGraphQueueJob, 0),
		ProjectsScanned:         len(projects),
		UnavailableProjectNames: make([]string, 0),
	}
	now := time.Now().UTC()
	for _, workspaceName := range projects {
		if err := ctx.Err(); err != nil {
			return api.LocalProjectGraphQueue{}, err
		}
		status, statusErr := service.GraphStatus(ctx, workspaceName, localGraphConfigurationID)
		if statusErr != nil {
			queue.ProjectsUnavailable++
			queue.UnavailableProjectNames = append(queue.UnavailableProjectNames, workspaceName)
			continue
		}
		job := status.CurrentJob
		if job == nil || (job.State != core.GraphJobQueued && job.State != core.GraphJobRunning) {
			continue
		}
		stateSince := job.CreatedAt
		if job.State == core.GraphJobRunning {
			stateSince = job.UpdatedAt
		}
		age := int64(now.Sub(stateSince).Seconds())
		if age < 0 {
			age = 0
		}
		queue.Jobs = append(queue.Jobs, api.LocalProjectGraphQueueJob{
			Workspace: workspaceName, JobID: job.ID, State: string(job.State),
			CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, AgeSeconds: age,
			PendingRecords: status.PendingRecords,
		})
	}
	sort.SliceStable(queue.Jobs, func(i, j int) bool {
		if queue.Jobs[i].State != queue.Jobs[j].State {
			return queue.Jobs[i].State == string(core.GraphJobRunning)
		}
		return queue.Jobs[i].CreatedAt.Before(queue.Jobs[j].CreatedAt)
	})
	return queue, nil
}

func localGraphOllamaEndpoint() string {
	if os.Getenv("AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY") == "true" {
		return localOllamaGatewayURL
	}
	return localOllamaLoopbackURL
}

func localGraphOllamaModels(configuration config.GraphConfig) []string {
	models := make([]string, 0, 2)
	if configuration.CompletionProvider == "ollama" || configuration.CompletionProvider == "ollama_chat" {
		models = append(models, configuration.CompletionModel)
	}
	if configuration.EmbeddingProvider == "ollama" || configuration.EmbeddingProvider == "ollama_chat" {
		if len(models) == 0 || models[0] != configuration.EmbeddingModel {
			models = append(models, configuration.EmbeddingModel)
		}
	}
	return models
}

func localOllamaModelsAvailable(ctx context.Context, endpoint string, requiredModels []string) error {
	if len(requiredModels) == 0 {
		return nil
	}
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return localOllamaModelsAvailableWithClient(ctx, client, endpoint, requiredModels)
}

func localOllamaModelsAvailableWithClient(ctx context.Context, client *http.Client, endpoint string, requiredModels []string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/tags", nil)
	if err != nil {
		return errors.New("local Ollama endpoint is unavailable")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return errors.New("local Ollama endpoint is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("local Ollama model inventory is unavailable")
	}
	var inventory struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&inventory); err != nil {
		return errors.New("local Ollama model inventory is invalid")
	}
	available := make(map[string]struct{}, len(inventory.Models))
	for _, model := range inventory.Models {
		available[model.Name] = struct{}{}
	}
	for _, required := range requiredModels {
		if _, ok := available[required]; !ok {
			return errors.New("a configured local Ollama model is unavailable")
		}
	}
	return nil
}

func (service *localProjectService) OperateGraph(ctx context.Context, input api.LocalProjectGraphOperationInput) (application.GraphOperationResult, error) {
	if strings.TrimSpace(input.TenantID) == "" || strings.TrimSpace(input.AccountID) == "" || strings.TrimSpace(input.Actor) == "" ||
		input.Action != application.GraphOperationRebuild || input.ConfigurationID != localGraphConfigurationID || strings.TrimSpace(input.IdempotencyKey) == "" {
		return application.GraphOperationResult{}, fmt.Errorf("authenticated project reindex scope is required")
	}
	readiness, err := service.GraphReadiness(ctx, input.Workspace, input.ConfigurationID)
	if err != nil || !readiness.Ready {
		return application.GraphOperationResult{}, fmt.Errorf("local GraphRAG is not ready")
	}
	preflight, err := application.NewLocalGraphRunner(service.graphConfig).Run(ctx, application.LocalGraphReadiness, nil)
	if err != nil || preflight.State != "ready" {
		if preflight.JobDir != "" {
			_ = os.RemoveAll(preflight.JobDir)
		}
		return application.GraphOperationResult{}, fmt.Errorf("local GraphRAG readiness check failed")
	}
	if preflight.JobDir != "" {
		_ = os.RemoveAll(preflight.JobDir)
	}
	if adapter, _ := preflight.Response["adapter"].(string); adapter != "" && adapter != readiness.AdapterName {
		return application.GraphOperationResult{}, fmt.Errorf("local GraphRAG adapter identity is unsupported")
	}
	if version, _ := preflight.Response["adapter_version"].(string); version != "" && version != readiness.AdapterVersion {
		return application.GraphOperationResult{}, fmt.Errorf("local GraphRAG adapter version is unsupported")
	}
	store, err := service.openProjectStore(ctx, input.Workspace)
	if err != nil {
		return application.GraphOperationResult{}, err
	}
	defer store.Close()
	now := time.Now().UTC()
	modelRoute := localGraphModelRoute(service.graphConfig)
	configuration := core.GraphConfiguration{
		ID: localGraphConfigurationID, Scope: core.GraphScope{WorkspaceID: input.Workspace}, Version: 1, Enabled: true,
		AdapterName: readiness.AdapterName, AdapterVersion: readiness.AdapterVersion, IndexMethod: core.GraphIndexStandard,
		ProjectionVersion: localGraphProjection, ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1,
		PromptFingerprint: localGraphPrompt, ModelRoute: modelRoute, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		return application.GraphOperationResult{}, err
	}
	return application.NewGraphOperationService(store).Operate(ctx, application.GraphOperationRequest{
		Scope: configuration.Scope, ConfigurationID: configuration.ID, Action: input.Action,
		ExpectedRevision: input.ExpectedRevision, IdempotencyKey: input.IdempotencyKey, Actor: input.Actor,
	})
}

func localGraphModelRoute(configuration config.GraphConfig) string {
	return configuration.CompletionProvider + "/" + configuration.CompletionModel + ";" + configuration.EmbeddingProvider + "/" + configuration.EmbeddingModel
}

func (service *localProjectService) StartGraphWorker(ctx context.Context) {
	if service == nil || !service.graphConfig.Enabled || ctx == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if _, err := service.runLocalGraphWorkerOnce(ctx); err != nil && ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "local project graph worker cycle failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (service *localProjectService) runLocalGraphWorkerOnce(ctx context.Context) (int, error) {
	projects, err := service.manager.ProjectNames()
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, workspaceName := range projects {
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		store, openErr := service.openProjectStore(ctx, workspaceName)
		if openErr != nil {
			return processed, openErr
		}
		scope := core.GraphScope{WorkspaceID: workspaceName}
		lease := time.Duration(service.graphConfig.TimeoutSeconds)*time.Second + 5*time.Minute
		jobs, claimErr := store.ClaimGraphJobs(ctx, scope, localGraphWorkerOwner, 1, lease, time.Now().UTC())
		if claimErr != nil {
			_ = store.Close()
			return processed, claimErr
		}
		for _, job := range jobs {
			if processErr := service.processLocalGraphJob(ctx, store, workspaceName, job); processErr != nil {
				if ctx.Err() != nil {
					_ = store.Close()
					return processed, ctx.Err()
				}
				if failErr := failLocalGraphJob(ctx, store, job); failErr != nil {
					_ = store.Close()
					return processed, errors.Join(processErr, failErr)
				}
			}
			processed++
		}
		if closeErr := store.Close(); closeErr != nil {
			return processed, closeErr
		}
	}
	return processed, nil
}

func (service *localProjectService) processLocalGraphJob(ctx context.Context, store *sqlite.Store, workspaceName string, job core.GraphJob) error {
	scope := core.GraphScope{WorkspaceID: workspaceName}
	revision, err := store.GetGraphRevision(ctx, scope, job.ConfigurationID, job.RevisionID)
	if err != nil {
		return err
	}
	if active, _, activeErr := store.ActiveGraphRevisions(ctx, scope, job.ConfigurationID); activeErr != nil {
		return activeErr
	} else if active == job.RevisionID {
		return store.CompleteGraphJob(ctx, scope, job.ID, localGraphWorkerOwner, time.Now().UTC())
	}
	memories, err := store.ListMemoriesByWorkspace(ctx, workspaceName)
	if err != nil {
		return err
	}
	if len(memories) == 0 || len(memories) > 5000 {
		return fmt.Errorf("project memory count is outside local graph policy")
	}
	now := time.Now().UTC()
	records := make([]application.GraphProjectionRecord, 0, len(memories))
	fingerprints := make([]string, 0, len(memories))
	for _, memory := range memories {
		if memory.StorageTier == core.TierCold || strings.TrimSpace(memory.Content) == "" {
			continue
		}
		eventTime := memory.UpdatedAt
		if eventTime.IsZero() {
			eventTime = memory.CreatedAt
		}
		if eventTime.IsZero() {
			eventTime = now
		}
		fingerprint := core.FingerprintText(memory.Content)
		records = append(records, application.GraphProjectionRecord{
			ID: memory.ID, Kind: application.GraphProjectionMemory, Content: memory.Content, Fingerprint: fingerprint,
			EventTime: eventTime, Authorized: true, Exportable: true,
		})
		fingerprints = append(fingerprints, fingerprint)
	}
	if len(records) == 0 || len(records) > 5000 {
		return fmt.Errorf("project has no eligible memories or exceeds local graph policy")
	}
	sort.Strings(fingerprints)
	cutoffHash := sha256.Sum256([]byte(strings.Join(fingerprints, "\x00")))
	cutoff := core.GraphWatermark{Sequence: int64(len(records)), EventTime: now, Digest: "sha256:" + hex.EncodeToString(cutoffHash[:])}
	projection, err := application.NewGraphProjectionBuilder().Build(application.GraphProjectionRequest{
		Scope: scope, ConfigurationID: job.ConfigurationID, JobID: job.ID, RevisionID: job.RevisionID,
		Mode: contracts.GraphIndexModeFull, ProjectionPolicyVersion: localGraphProjection, Cutoff: cutoff,
		PromptFingerprint: localGraphPrompt, ModelRoutes: []string{localGraphModelRoute(service.graphConfig)},
		CreatedAt: now, ExpiresAt: now.Add(application.GraphProjectionRetention), ProducerIdentity: localGraphWorkerOwner, Records: records,
	})
	if err != nil {
		return err
	}
	manifestBytes, err := projection.Manifest.CanonicalJSON()
	if err != nil {
		return err
	}
	manifestHash := sha256.Sum256(manifestBytes)
	buildDigest, err := localGraphExecutableDigest(service.graphConfig.Executable)
	if err != nil {
		return err
	}
	signaturePayload, _ := json.Marshal(struct {
		Scope           core.GraphScope `json:"scope"`
		ConfigurationID string          `json:"configuration_id"`
		JobID           string          `json:"job_id"`
		RevisionID      string          `json:"revision_id"`
		ManifestHash    string          `json:"manifest_hash"`
		BuildDigest     string          `json:"build_digest"`
	}{scope, job.ConfigurationID, job.ID, job.RevisionID, "sha256:" + hex.EncodeToString(manifestHash[:]), buildDigest})
	signature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(service.graphSigner, signaturePayload))
	correlations, err := json.Marshal(projection.Correlations)
	if err != nil {
		return err
	}
	documents, err := localGraphDocuments(projection.DocumentsJSONL)
	if err != nil {
		return err
	}
	if err := store.SetGraphRevisionCutoff(ctx, scope, job.ConfigurationID, job.RevisionID, cutoff, projection.Manifest.Files[0].ContentHash, now); err != nil {
		return err
	}
	if err := store.TransitionGraphRevision(ctx, scope, job.ConfigurationID, job.RevisionID, core.GraphRevisionQueued, core.GraphRevisionProjecting, now); err != nil {
		return err
	}
	if err := store.TransitionGraphRevision(ctx, scope, job.ConfigurationID, job.RevisionID, core.GraphRevisionProjecting, core.GraphRevisionIndexing, time.Now().UTC()); err != nil {
		return err
	}
	response, err := application.NewLocalGraphRunner(service.graphConfig).Run(ctx, application.LocalGraphFullIndex, map[string]any{
		"scope": scope, "job_id": job.ID, "configuration_id": job.ConfigurationID, "revision_id": job.RevisionID,
		"method": "standard", "documents": documents, "correlations": json.RawMessage(correlations),
		"completion_provider": service.graphConfig.CompletionProvider, "completion_model": service.graphConfig.CompletionModel,
		"embedding_provider": service.graphConfig.EmbeddingProvider, "embedding_model": service.graphConfig.EmbeddingModel,
		"input_manifest_hash": "sha256:" + hex.EncodeToString(manifestHash[:]), "prompt_fingerprint": localGraphPrompt,
		"producer_identity": localGraphWorkerOwner,
		"build_digest":      buildDigest, "attestation_signature": signature,
	})
	if response.JobDir != "" {
		defer os.RemoveAll(response.JobDir)
	}
	if err != nil {
		return err
	}
	if response.State != "completed" {
		return fmt.Errorf("GraphRAG adapter did not complete (state=%q, reason=%q)", response.State, response.ReasonCode)
	}
	if err := store.TransitionGraphRevision(ctx, scope, job.ConfigurationID, job.RevisionID, core.GraphRevisionIndexing, core.GraphRevisionValidating, time.Now().UTC()); err != nil {
		return err
	}
	artifactRoot := filepath.Join(response.JobDir, "output", "normalized")
	manifestPath := filepath.Join(artifactRoot, "artifact-manifest.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 || manifestInfo.Size() < 1 || manifestInfo.Size() > 1<<20 {
		return fmt.Errorf("GraphRAG artifact manifest is unavailable")
	}
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return err
	}
	var artifactManifest contracts.GraphArtifactManifest
	decodeErr := json.NewDecoder(manifestFile).Decode(&artifactManifest)
	closeErr := manifestFile.Close()
	if decodeErr != nil || closeErr != nil {
		return errors.Join(decodeErr, closeErr)
	}
	mismatches := make([]string, 0, 12)
	if artifactManifest.Scope != scope {
		mismatches = append(mismatches, "scope")
	}
	if artifactManifest.ConfigurationID != job.ConfigurationID {
		mismatches = append(mismatches, "configuration_id")
	}
	if artifactManifest.JobID != job.ID {
		mismatches = append(mismatches, "job_id")
	}
	if artifactManifest.RevisionID != job.RevisionID {
		mismatches = append(mismatches, "revision_id")
	}
	if artifactManifest.Mode != contracts.GraphIndexModeFull {
		mismatches = append(mismatches, "mode")
	}
	if artifactManifest.Status != contracts.GraphArtifactCompleted {
		mismatches = append(mismatches, "status")
	}
	if artifactManifest.AdapterName != "agent-memory-graphrag-adapter" {
		mismatches = append(mismatches, "adapter_name")
	}
	if artifactManifest.AdapterVersion != contracts.SupportedGraphAdapterVersion {
		mismatches = append(mismatches, "adapter_version")
	}
	if artifactManifest.IndexMethod != core.GraphIndexStandard {
		mismatches = append(mismatches, "index_method")
	}
	if artifactManifest.InputManifestHash != "sha256:"+hex.EncodeToString(manifestHash[:]) {
		mismatches = append(mismatches, "input_manifest_hash")
	}
	if artifactManifest.Attestation.ProducerIdentity != localGraphWorkerOwner {
		mismatches = append(mismatches, "producer_identity")
	}
	if artifactManifest.Attestation.BuildDigest != buildDigest {
		mismatches = append(mismatches, "build_digest")
	}
	if artifactManifest.Attestation.Signature != signature {
		mismatches = append(mismatches, "attestation_signature")
	}
	if artifactManifest.PromptFingerprint != localGraphPrompt {
		mismatches = append(mismatches, "prompt_fingerprint")
	}
	if len(mismatches) != 0 {
		return fmt.Errorf("GraphRAG artifact identity mismatch: %s", strings.Join(mismatches, ", "))
	}
	if len(artifactManifest.Models) != 2 || artifactManifest.Models[0] != service.graphConfig.CompletionModel || artifactManifest.Models[1] != service.graphConfig.EmbeddingModel {
		return fmt.Errorf("GraphRAG artifact model routes do not match the accepted project configuration")
	}
	communities, reports := localGraphHasOutput(artifactManifest, "communities.jsonl"), localGraphHasOutput(artifactManifest, "community_reports.jsonl")
	if communities != reports {
		return fmt.Errorf("GraphRAG community artifacts are incomplete")
	}
	validated, err := validation.ValidateGraphArtifact(ctx, artifactRoot, artifactManifest, validation.GraphArtifactPolicy{CommunitiesEnabled: communities, ReportsEnabled: reports})
	if err != nil {
		return err
	}
	batch, err := graphindex.NormalizeValidatedGraphArtifact(validated, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := store.TransitionGraphRevision(ctx, scope, job.ConfigurationID, job.RevisionID, core.GraphRevisionValidating, core.GraphRevisionImporting, time.Now().UTC()); err != nil {
		return err
	}
	if err := application.NewGraphImportService(store).Import(ctx, application.GraphImportRequest{
		Batch: batch, EvidenceResolved: true, AdmissionPassed: true, ReviewCarryForwardComplete: true, EvaluationPassed: true,
	}); err != nil {
		return err
	}
	artifactBytes, err := artifactManifest.CanonicalJSON()
	if err != nil {
		return err
	}
	artifactHash := sha256.Sum256(artifactBytes)
	if err := store.SetGraphRevisionArtifactHash(ctx, scope, job.ConfigurationID, job.RevisionID, "sha256:"+hex.EncodeToString(artifactHash[:]), time.Now().UTC()); err != nil {
		return err
	}
	if err := store.ActivateGraphRevision(ctx, core.GraphActivation{
		Scope: scope, ConfigurationID: job.ConfigurationID, ExpectedRevision: revision.PreviousRevisionID, CandidateRevision: job.RevisionID,
	}); err != nil {
		return err
	}
	return store.CompleteGraphJob(ctx, scope, job.ID, localGraphWorkerOwner, time.Now().UTC())
}

func failLocalGraphJob(ctx context.Context, store *sqlite.Store, job core.GraphJob) error {
	revision, err := store.GetGraphRevision(ctx, job.Scope, job.ConfigurationID, job.RevisionID)
	if err == nil && revision.State != core.GraphRevisionFailed && revision.State != core.GraphRevisionCancelled && revision.State != core.GraphRevisionActive && revision.State != core.GraphRevisionPrevious {
		if transitionErr := store.TransitionGraphRevision(ctx, job.Scope, job.ConfigurationID, job.RevisionID, revision.State, core.GraphRevisionFailed, time.Now().UTC()); transitionErr != nil {
			err = errors.Join(err, transitionErr)
		}
	}
	return errors.Join(err, store.FailGraphJob(ctx, job.Scope, job.ID, localGraphWorkerOwner, time.Now().UTC()))
}

func localGraphDocuments(contents []byte) ([]map[string]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	documents := make([]map[string]string, 0)
	for scanner.Scan() {
		var row struct {
			Text  string                          `json:"text"`
			Kind  application.GraphProjectionKind `json:"kind"`
			Token string                          `json:"correlation_token"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil || strings.TrimSpace(row.Text) == "" || strings.TrimSpace(row.Token) == "" {
			return nil, fmt.Errorf("local graph projection document is invalid")
		}
		documents = append(documents, map[string]string{"id": row.Token, "title": string(row.Kind), "text": row.Text})
	}
	if err := scanner.Err(); err != nil || len(documents) == 0 || len(documents) > 5000 {
		return nil, fmt.Errorf("local graph projection document count is invalid")
	}
	return documents, nil
}

func localGraphHasOutput(manifest contracts.GraphArtifactManifest, name string) bool {
	for _, output := range manifest.Outputs {
		if output.Name == name {
			return true
		}
	}
	return false
}

func localGraphExecutableDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("configured graph executable is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() > 1<<30 {
		return "", fmt.Errorf("configured graph executable changed while opening")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
