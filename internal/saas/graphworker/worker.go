package graphworker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	baseobservability "github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/objectcustody"
)

type JobEnvelope struct {
	Scope                core.GraphScope                        `json:"scope"`
	JobID                string                                 `json:"job_id"`
	ConfigurationID      string                                 `json:"configuration_id"`
	RevisionID           string                                 `json:"revision_id"`
	ProjectionRevisionID string                                 `json:"projection_revision_id"`
	ExpectedRevision     string                                 `json:"expected_revision,omitempty"`
	BaseRevisionID       string                                 `json:"base_revision_id,omitempty"`
	Mode                 contracts.GraphIndexMode               `json:"mode"`
	Attempt              int                                    `json:"attempt"`
	CreatedAt            time.Time                              `json:"created_at"`
	Limits               baseobservability.GraphWorkspaceLimits `json:"limits"`
}
type AdapterRequest struct {
	Scope                                        core.GraphScope
	JobID, ConfigurationID, RevisionID           string
	BaseRevisionID                               string
	Mode                                         contracts.GraphIndexMode
	Projection, Correlations, ProjectionManifest []byte
	BaseState                                    map[string][]byte
	BaseStateManifest                            contracts.GraphAdapterStateManifest
}
type AdapterResult struct {
	Files         map[string][]byte
	Manifest      contracts.GraphArtifactManifest
	StateFiles    map[string][]byte
	StateManifest contracts.GraphAdapterStateManifest
}
type CompletionEvent struct {
	ID               string          `json:"id"`
	Scope            core.GraphScope `json:"scope"`
	JobID            string          `json:"job_id"`
	ConfigurationID  string          `json:"configuration_id"`
	RevisionID       string          `json:"revision_id"`
	ExpectedRevision string          `json:"expected_revision,omitempty"`
	ArtifactPrefix   string          `json:"artifact_prefix,omitempty"`
	Status           string          `json:"status"`
	FailureCode      string          `json:"failure_code,omitempty"`
}

type Queue interface {
	Claim(context.Context, string, int, time.Duration, time.Time) ([]JobEnvelope, error)
	Ack(context.Context, JobEnvelope) error
	Release(context.Context, JobEnvelope, string) error
}
type Adapter interface {
	Index(context.Context, AdapterRequest) (AdapterResult, error)
}
type EventSink interface {
	Emit(context.Context, CompletionEvent) (bool, error)
}
type ArtifactCustody interface {
	ReadProjection(context.Context, core.GraphScope, string) ([]byte, []byte, []byte, error)
	ReadAdapterState(context.Context, core.GraphScope, string) (map[string][]byte, contracts.GraphAdapterStateManifest, error)
	Stage(context.Context, core.GraphScope, string, string, map[string][]byte, contracts.GraphArtifactManifest) (string, bool, error)
	StageAdapterState(context.Context, core.GraphScope, string, map[string][]byte, contracts.GraphAdapterStateManifest) (bool, error)
}

type Worker struct {
	queue   Queue
	custody ArtifactCustody
	adapter Adapter
	events  EventSink
	owner   string
	lease   time.Duration
	now     func() time.Time
	observe func(baseobservability.GraphObservation) error
}

func New(queue Queue, custody ArtifactCustody, adapter Adapter, events EventSink, owner string, lease time.Duration, now func() time.Time, observers ...func(baseobservability.GraphObservation) error) (*Worker, error) {
	if queue == nil || custody == nil || adapter == nil || events == nil || strings.TrimSpace(owner) == "" || lease <= 0 {
		return nil, fmt.Errorf("graph worker dependencies are incomplete")
	}
	if now == nil {
		now = time.Now
	}
	worker := &Worker{queue: queue, custody: custody, adapter: adapter, events: events, owner: owner, lease: lease, now: now}
	if len(observers) > 0 {
		worker.observe = observers[0]
	}
	return worker, nil
}

func (w *Worker) RunOnce(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 32 {
		return 0, fmt.Errorf("graph worker claim limit is outside policy")
	}
	jobs, err := w.queue.Claim(ctx, w.owner, limit, w.lease, w.now().UTC())
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, job := range jobs {
		if err := w.process(ctx, job); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (w *Worker) process(ctx context.Context, job JobEnvelope) error {
	started := w.now().UTC()
	observation := baseobservability.GraphObservation{Stage: "index", Mode: string(job.Mode), Route: "none", Outcome: "failed"}
	if !job.CreatedAt.IsZero() && started.After(job.CreatedAt) {
		observation.QueueAge = started.Sub(job.CreatedAt)
	}
	defer func() {
		if observation.Outcome == "failed" && job.Attempt >= 5 {
			observation.DeadLetter = true
		}
		if w.observe != nil {
			observation.Duration = w.now().UTC().Sub(started)
			_ = w.observe(observation)
		}
	}()
	if err := validateJob(job); err != nil {
		_ = w.queue.Release(ctx, job, "invalid_job")
		return nil
	}
	projection, correlations, manifest, err := w.custody.ReadProjection(ctx, job.Scope, job.ProjectionRevisionID)
	if err != nil {
		observation.Reason = "read_failed"
		return w.fail(ctx, job, "projection_unavailable")
	}
	preflight := baseobservability.GraphPreflight{
		PendingRecords: countProjectionRecords(projection),
		InputTokens:    estimateProjectionTokens(projection),
		ArtifactBytes:  int64(len(projection) + len(correlations) + len(manifest)),
	}
	observation.Records = preflight.PendingRecords
	observation.CoalescedRecords = preflight.PendingRecords
	observation.ProjectionBytes = preflight.ArtifactBytes
	if err := baseobservability.CheckGraphPreflight(job.Limits, preflight); err != nil {
		observation.Outcome = "rejected"
		observation.Reason = "limit_exceeded"
		observation.Rejected = 1
		return w.fail(ctx, job, "limit_exceeded")
	}
	var baseState map[string][]byte
	var baseManifest contracts.GraphAdapterStateManifest
	if job.Mode == contracts.GraphIndexModeIncremental {
		baseState, baseManifest, err = w.custody.ReadAdapterState(ctx, job.Scope, job.BaseRevisionID)
		if err != nil {
			return w.fail(ctx, job, "base_state_unavailable")
		}
	}
	result, err := w.adapter.Index(ctx, AdapterRequest{Scope: job.Scope, JobID: job.JobID, ConfigurationID: job.ConfigurationID, RevisionID: job.RevisionID, BaseRevisionID: job.BaseRevisionID, Mode: job.Mode, Projection: projection, Correlations: correlations, ProjectionManifest: manifest, BaseState: baseState, BaseStateManifest: baseManifest})
	if err != nil {
		observation.Reason = "adapter_failed"
		return w.fail(ctx, job, "adapter_failed")
	}
	prefix, _, err := w.custody.Stage(ctx, job.Scope, job.JobID, job.RevisionID, result.Files, result.Manifest)
	if err != nil {
		observation.Outcome = "rejected"
		observation.Reason = "artifact_rejected"
		observation.Rejected = 1
		return w.fail(ctx, job, "artifact_rejected")
	}
	if _, err := w.custody.StageAdapterState(ctx, job.Scope, job.RevisionID, result.StateFiles, result.StateManifest); err != nil {
		observation.Outcome = "rejected"
		observation.Reason = "artifact_rejected"
		observation.Rejected = 1
		return w.fail(ctx, job, "adapter_state_rejected")
	}
	observation.Outcome = "completed"
	observation.InputTokens = result.Manifest.Usage.InputTokens
	observation.CostMicroUSD = result.Manifest.Usage.EstimatedCostMicros
	observation.CacheHit = result.Manifest.Usage.CacheHits > 0
	observation.CacheObserved = true
	for _, output := range result.Manifest.Outputs {
		observation.NormalizedBytes += output.Bytes
		switch output.Kind {
		case "entities":
			observation.Entities += output.Rows
		case "relationships":
			observation.Relationships += output.Rows
		}
	}
	for _, file := range result.StateManifest.Files {
		observation.AdapterStateBytes += file.Bytes
	}
	event := completion(job, "completed", "", prefix)
	if _, err := w.events.Emit(ctx, event); err != nil {
		return err
	}
	return w.queue.Ack(ctx, job)
}

func (w *Worker) fail(ctx context.Context, job JobEnvelope, code string) error {
	if _, err := w.events.Emit(ctx, completion(job, "failed", code, "")); err != nil {
		return err
	}
	return w.queue.Release(ctx, job, code)
}

func completion(job JobEnvelope, status, code, prefix string) CompletionEvent {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(job.Scope.TenantID+"\x00"+job.Scope.WorkspaceID+"\x00"+job.JobID+"\x00"+job.RevisionID+"\x00"+status)))
	return CompletionEvent{ID: "gce-" + digest[:24], Scope: job.Scope, JobID: job.JobID, ConfigurationID: job.ConfigurationID, RevisionID: job.RevisionID, ExpectedRevision: job.ExpectedRevision, ArtifactPrefix: prefix, Status: status, FailureCode: code}
}

func validateJob(job JobEnvelope) error {
	if err := job.Scope.Validate(); err != nil || job.Scope.TenantID == "" {
		return fmt.Errorf("hosted graph scope is required")
	}
	for _, value := range []string{job.JobID, job.ConfigurationID, job.RevisionID, job.ProjectionRevisionID} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "/\\") {
			return fmt.Errorf("invalid graph worker identity")
		}
	}
	if job.Mode != contracts.GraphIndexModeFull && job.Mode != contracts.GraphIndexModeIncremental {
		return fmt.Errorf("invalid graph worker mode")
	}
	if job.Mode == contracts.GraphIndexModeIncremental && strings.TrimSpace(job.BaseRevisionID) == "" {
		return fmt.Errorf("incremental graph worker base revision is required")
	}
	if job.Mode == contracts.GraphIndexModeFull && strings.TrimSpace(job.BaseRevisionID) != "" {
		return fmt.Errorf("full graph worker job cannot bind base revision")
	}
	if job.CreatedAt.IsZero() || job.CreatedAt.After(time.Now().UTC().Add(time.Minute)) {
		return fmt.Errorf("graph worker creation time is invalid")
	}
	if err := baseobservability.CheckGraphPreflight(job.Limits, baseobservability.GraphPreflight{}); err != nil {
		return fmt.Errorf("graph worker limits are invalid: %w", err)
	}
	return nil
}

func DefaultWorkspaceLimits() baseobservability.GraphWorkspaceLimits {
	return baseobservability.GraphWorkspaceLimits{
		MaxPendingRecords: 5_000,
		MaxInputTokens:    2_000_000,
		MaxCostMicroUSD:   100_000_000,
		MaxArtifactBytes:  contracts.MaxGraphProjectionBytes,
	}
}

func countProjectionRecords(projection []byte) int64 {
	var records int64
	for _, value := range projection {
		if value == '\n' {
			records++
		}
	}
	if len(projection) > 0 && projection[len(projection)-1] != '\n' {
		records++
	}
	return records
}

func estimateProjectionTokens(projection []byte) int64 {
	// This intentionally over-estimates common UTF-8 prose before any model
	// call. Provider-reported usage remains authoritative for accounting.
	return int64(len(projection)+2) / 3
}

var _ ArtifactCustody = (*objectcustody.GraphArtifactCustody)(nil)
