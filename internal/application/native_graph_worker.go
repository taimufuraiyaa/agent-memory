package application

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

const nativeGraphPrompt = contracts.NativeGraphPromptFingerprint

type nativeGraphRunner interface {
	Run(context.Context, LocalGraphCommand, any) (LocalGraphRunResult, error)
}

// NativeGraphWorker executes one local adapter at a time. The daemon owns
// workspace discovery; SQLite owns scheduling, leases and publication fences.
type NativeGraphWorker struct {
	configuration             config.GraphConfig
	runner                    nativeGraphRunner
	owner                     string
	signer                    ed25519.PrivateKey
	batch                     int
	maxWait, lease, heartbeat time.Duration
	maxAttempts               int
	now                       func() time.Time
}

func NewNativeGraphWorker(configuration config.GraphConfig) *NativeGraphWorker {
	_, signer, _ := ed25519.GenerateKey(rand.Reader)
	limits := DefaultGraphLimits()
	return &NativeGraphWorker{configuration: configuration, runner: NewLocalGraphRunner(configuration), owner: "native-graph-" + uuid.NewString(), signer: signer,
		batch: limits.BatchChanges, maxWait: limits.MaxWait, lease: time.Minute, heartbeat: 10 * time.Second, maxAttempts: limits.MaxAttempts, now: time.Now}
}

// RunWorkspace makes one coordinator/worker pass. It never runs on a write or
// query request, and an opted-out runtime does not schedule or claim work.
func (w *NativeGraphWorker) RunWorkspace(ctx context.Context, store *sqlite.Store, workspace string) error {
	if !w.configuration.Enabled {
		return nil
	}
	scope := core.GraphScope{WorkspaceID: workspace}
	ids, err := store.EnabledNativeGraphConfigurations(ctx, scope)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	protected, err := store.NativeGraphProtectedRevisions(ctx, scope)
	if err != nil {
		return err
	}
	if err = w.pruneStates(scope, protected); err != nil {
		return err
	}
	for _, id := range ids {
		if err = store.AlignNativeGraphPrompt(ctx, scope, id, nativeGraphPrompt, w.now().UTC()); err != nil {
			return err
		}
		status, statusErr := store.GraphIndexStatus(ctx, scope, id)
		if statusErr != nil {
			return statusErr
		}
		if status.Fresh {
			continue
		}
		work, err := store.LoadNativeGraphWork(ctx, scope, id, DefaultGraphLimits().MaxChanges)
		if err != nil {
			return err
		}
		if !contracts.GraphAdapterCompatible(work.Configuration.AdapterName, work.Configuration.AdapterVersion, work.Configuration.ArtifactSchemaVersion) {
			continue
		}
		if _, err = store.ScheduleNativeGraphWork(ctx, work, w.batch, w.maxWait, w.maxAttempts, w.now().UTC()); err != nil {
			return err
		}
	}
	jobs, err := store.ClaimGraphJobs(ctx, scope, w.owner, 1, w.lease, w.now().UTC())
	if err != nil || len(jobs) == 0 {
		return err
	}
	job := jobs[0]
	work, err := store.LoadNativeGraphWork(ctx, scope, job.ConfigurationID, DefaultGraphLimits().MaxChanges)
	if err != nil {
		return w.fail(store, job, "configuration_unavailable", err)
	}
	jobCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(w.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				if err := store.RenewNativeGraphLease(jobCtx, job, work.Configuration.Version, w.lease, w.now().UTC()); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	err = w.process(jobCtx, store, job, work)
	cancel()
	<-done
	if err != nil {
		return w.fail(store, job, "index_execution_failed", err)
	}
	return nil
}

func (w *NativeGraphWorker) fail(store *sqlite.Store, job core.GraphJob, code string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = store.FailNativeGraphJob(ctx, job, w.maxAttempts, w.now().UTC())
	_, _ = store.AppendAuditEvent(ctx, sqlite.AuditEventInput{Workspace: job.Scope.WorkspaceID, Operation: "graph_index", Outcome: "failure", Actor: "native-worker", TargetType: "graph_job", TargetIDs: []string{job.ID}, Reason: code, OccurredAt: w.now().UTC()})
	return fmt.Errorf("native graph job %s: %s: %w", job.ID, code, cause)
}

func (w *NativeGraphWorker) process(ctx context.Context, store *sqlite.Store, job core.GraphJob, work sqlite.NativeGraphWork) error {
	revision, err := store.GetGraphRevision(ctx, job.Scope, job.ConfigurationID, job.RevisionID)
	if err != nil {
		return err
	}
	if revision.State != core.GraphRevisionQueued {
		return fmt.Errorf("interrupted revision requires a fresh retry")
	}
	if revision.PreviousRevisionID != work.ActiveRevisionID {
		return sqlite.ErrGraphRevisionConflict
	}
	if len(work.Memories) == 0 {
		return fmt.Errorf("no eligible graph records")
	}
	buildDigest, err := nativeGraphBuildDigest(w.configuration)
	if err != nil {
		return err
	}
	mode, command := contracts.GraphIndexModeFull, LocalGraphFullIndex
	baseDir := ""
	if !work.Rebuild && revision.BaseRevisionID == work.ActiveRevisionID && work.ActiveRevisionID != "" {
		baseDir, _ = w.loadState(job.Scope, work.ActiveRevisionID, buildDigest)
		if baseDir != "" {
			mode, command = contracts.GraphIndexModeIncremental, LocalGraphIncremental
		}
	}
	now := w.now().UTC()
	records := make([]GraphProjectionRecord, 0, len(work.Memories))
	for _, m := range work.Memories {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		records = append(records, GraphProjectionRecord{ID: m.ID, Kind: GraphProjectionMemory, Content: m.Content, Fingerprint: core.FingerprintText(m.Content), EventTime: m.UpdatedAt, Authorized: true, Exportable: true})
	}
	baseRevision := ""
	if mode == contracts.GraphIndexModeIncremental {
		baseRevision = work.ActiveRevisionID
	}
	projection, err := NewGraphProjectionBuilder().Build(GraphProjectionRequest{Scope: job.Scope, ConfigurationID: job.ConfigurationID, JobID: job.ID, RevisionID: job.RevisionID, Mode: mode, BaseRevisionID: baseRevision,
		ProjectionPolicyVersion: work.Configuration.ProjectionVersion, Cutoff: work.Watermark, PromptFingerprint: nativeGraphPrompt, ModelRoutes: []string{work.Configuration.ModelRoute}, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), ProducerIdentity: w.owner, Records: records})
	if err != nil {
		return err
	}
	manifestBytes, err := projection.Manifest.CanonicalJSON()
	if err != nil {
		return err
	}
	manifestHash := nativeGraphHash(manifestBytes)
	signature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(w.signer, []byte(job.Scope.WorkspaceID+"\x00"+job.ConfigurationID+"\x00"+job.ID+"\x00"+job.RevisionID+"\x00"+manifestHash+"\x00"+buildDigest)))
	documents, err := nativeGraphDocuments(projection, work.ChangedIDs, mode)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		return fmt.Errorf("incremental projection has no eligible new documents")
	}
	if err = store.SetGraphRevisionCutoff(ctx, job.Scope, job.ConfigurationID, job.RevisionID, work.Watermark, manifestHash, now); err != nil {
		return err
	}
	if err = store.TransitionGraphRevision(ctx, job.Scope, job.ConfigurationID, job.RevisionID, core.GraphRevisionQueued, core.GraphRevisionProjecting, now); err != nil {
		return err
	}
	if err = store.TransitionGraphRevision(ctx, job.Scope, job.ConfigurationID, job.RevisionID, core.GraphRevisionProjecting, core.GraphRevisionIndexing, w.now().UTC()); err != nil {
		return err
	}
	payload := map[string]any{"scope": job.Scope, "configuration_id": job.ConfigurationID, "job_id": job.ID, "revision_id": job.RevisionID, "method": "standard", "documents": documents, "correlations": projection.Correlations,
		"completion_provider": w.configuration.CompletionProvider, "completion_model": w.configuration.CompletionModel, "embedding_provider": w.configuration.EmbeddingProvider, "embedding_model": w.configuration.EmbeddingModel,
		"input_manifest_hash": manifestHash, "prompt_fingerprint": nativeGraphPrompt, "producer_identity": w.owner, "build_digest": buildDigest, "attestation_signature": signature}
	if baseDir != "" {
		payload["base_revision_id"] = baseRevision
		payload["native_base_job_dir"] = baseDir
	}
	response, err := w.runner.Run(ctx, command, payload)
	retain := false
	defer func() {
		if response.JobDir != "" && !retain {
			_ = os.RemoveAll(response.JobDir)
		}
	}()
	if err != nil {
		return err
	}
	if response.State != "completed" {
		return fmt.Errorf("adapter state %s (%s)", response.State, safeNativeGraphReason(response.ReasonCode))
	}
	if err = store.RenewNativeGraphLease(ctx, job, work.Configuration.Version, w.lease, w.now().UTC()); err != nil {
		return err
	}
	if err = store.TransitionGraphRevision(ctx, job.Scope, job.ConfigurationID, job.RevisionID, core.GraphRevisionIndexing, core.GraphRevisionValidating, w.now().UTC()); err != nil {
		return err
	}
	artifactRoot := filepath.Join(response.JobDir, "output", "normalized")
	data, err := readNativeGraphFile(filepath.Join(artifactRoot, "artifact-manifest.json"), 1<<20)
	if err != nil {
		return err
	}
	var manifest contracts.GraphArtifactManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	bindings := []struct {
		name    string
		matches bool
	}{
		{"scope", manifest.Scope == job.Scope}, {"configuration", manifest.ConfigurationID == job.ConfigurationID}, {"job", manifest.JobID == job.ID}, {"revision", manifest.RevisionID == job.RevisionID},
		{"mode", manifest.Mode == mode}, {"status", manifest.Status == contracts.GraphArtifactCompleted}, {"adapter", manifest.AdapterName == "agent-memory-graphrag-adapter" && manifest.AdapterVersion == contracts.SupportedGraphAdapterVersion},
		{"method", manifest.IndexMethod == core.GraphIndexStandard}, {"input", manifest.InputManifestHash == manifestHash}, {"prompt", manifest.PromptFingerprint == nativeGraphPrompt},
		{"producer", manifest.Attestation.ProducerIdentity == w.owner}, {"build", manifest.Attestation.BuildDigest == buildDigest}, {"signature", manifest.Attestation.Signature == signature},
		{"models", len(manifest.Models) == 2 && manifest.Models[0] == w.configuration.CompletionModel && manifest.Models[1] == w.configuration.EmbeddingModel},
	}
	for _, binding := range bindings {
		if !binding.matches {
			return fmt.Errorf("adapter artifact identity mismatch: %s", binding.name)
		}
	}
	communities, reports := false, false
	for _, output := range manifest.Outputs {
		communities = communities || output.Name == "communities.jsonl"
		reports = reports || output.Name == "community_reports.jsonl"
	}
	if communities != reports {
		return fmt.Errorf("incomplete community artifacts")
	}
	validated, err := validation.ValidateGraphArtifact(ctx, artifactRoot, manifest, validation.GraphArtifactPolicy{CommunitiesEnabled: communities, ReportsEnabled: reports})
	if err != nil {
		return err
	}
	// Supplier IDs and fingerprints must resolve to this immutable projection;
	// syntactically valid fabricated evidence is never authorized by the adapter.
	allowed := map[string]bool{}
	for _, ref := range projection.Correlations {
		allowed[string(ref.CanonicalKind)+"\x00"+ref.CanonicalID+"\x00"+ref.CanonicalFingerprint] = true
	}
	check := func(values []validation.GraphArtifactEvidence) error {
		for _, v := range values {
			if !allowed[v.CanonicalKind+"\x00"+v.CanonicalID+"\x00"+v.CanonicalFingerprint] {
				return fmt.Errorf("unresolved projected canonical evidence")
			}
		}
		return nil
	}
	for _, entity := range validated.Entities {
		if err = check(entity.Evidence); err != nil {
			return err
		}
	}
	for _, edge := range validated.Relationships {
		if err = check(edge.Evidence); err != nil {
			return err
		}
	}
	for _, report := range validated.Reports {
		if err = check(report.Evidence); err != nil {
			return err
		}
	}
	previous := map[string]core.GraphReport{}
	existing := []GraphExistingEntity{}
	if work.ActiveRevisionID != "" {
		catalog, catalogErr := store.LoadGraphEntityRevisionCatalog(ctx, job.Scope, work.ActiveRevisionID)
		if catalogErr != nil {
			return catalogErr
		}
		for _, record := range catalog {
			existing = append(existing, GraphExistingEntity{Entity: record.Entity, Version: record.Version, Evidence: record.Evidence})
		}
	}
	if snapshot, loadErr := store.LoadActiveGraphSnapshot(ctx, job.Scope, 4096, 16384, 256); loadErr == nil {
		for _, community := range snapshot.Communities {
			stable := deterministicGraphCommunityID(job.Scope, community.Community.Level, community.Community.MembershipFingerprint)
			previous[stable] = community.Report
		}
	}
	batch, err := normalizeNativeGraphArtifact(validated, previous, w.now().UTC(), existing...)
	if err != nil {
		return err
	}
	if err = store.TransitionGraphRevision(ctx, job.Scope, job.ConfigurationID, job.RevisionID, core.GraphRevisionValidating, core.GraphRevisionImporting, w.now().UTC()); err != nil {
		return err
	}
	if err = NewGraphImportService(store).Import(ctx, GraphImportRequest{Batch: batch, EvidenceResolved: true, AdmissionPassed: true, ReviewCarryForwardComplete: true, EvaluationPassed: true}); err != nil {
		return err
	}
	artifactBytes, err := manifest.CanonicalJSON()
	if err != nil {
		return err
	}
	if err = store.SetGraphRevisionArtifactHash(ctx, job.Scope, job.ConfigurationID, job.RevisionID, nativeGraphHash(artifactBytes), w.now().UTC()); err != nil {
		return err
	}
	// State is staged before publication; only a fenced active revision may be
	// used as a future base. Missing raw tables cause a safe full rebuild.
	if err = w.saveState(job.Scope, job.RevisionID, response.JobDir, buildDigest); err == nil {
		retain = true
	}
	if err = store.PublishNativeGraphJob(ctx, job, work.Configuration.Version, work.ActiveRevisionID, work.JournalIDs, w.now().UTC()); err != nil {
		retain = false
		_ = os.Remove(w.statePath(job.Scope, job.RevisionID))
		return err
	}
	return nil
}

func nativeGraphDocuments(projection GraphProjection, changed map[string]bool, mode contracts.GraphIndexMode) ([]map[string]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(projection.DocumentsJSONL)))
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	documents := []map[string]string{}
	for scanner.Scan() {
		var row struct {
			Text  string `json:"text"`
			Token string `json:"correlation_token"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, err
		}
		if mode == contracts.GraphIndexModeIncremental && !changed[projection.Correlations[row.Token].CanonicalID] {
			continue
		}
		documents = append(documents, map[string]string{"id": row.Token, "title": "memory", "text": row.Text})
	}
	return documents, scanner.Err()
}

func nativeGraphBuildDigest(configuration config.GraphConfig) (string, error) {
	executable, err := readNativeGraphFile(configuration.Executable, 1<<30)
	if err != nil {
		return "", err
	}
	routes, _ := json.Marshal([]string{configuration.CompletionProvider, configuration.CompletionModel, configuration.EmbeddingProvider, configuration.EmbeddingModel, nativeGraphPrompt})
	return nativeGraphHash(append(executable, routes...)), nil
}

func nativeGraphHash(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}
func safeNativeGraphReason(code string) string {
	if len(code) > 80 {
		return "adapter_failed"
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return "adapter_failed"
		}
	}
	return code
}

func readNativeGraphFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("native graph file is unavailable or unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("native graph file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("native graph file limit exceeded")
	}
	return data, nil
}
