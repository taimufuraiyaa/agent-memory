package graphindex

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphworker"
)

type ProjectionWork struct {
	Job               core.GraphJob
	ExpectedRevision  string
	BaseRevisionID    string
	IndexMethod       core.GraphIndexMethod
	ProjectionVersion string
	PromptFingerprint string
	ModelRoute        string
	Cutoff            core.GraphWatermark
	Records           []application.GraphProjectionRecord
}

type ProjectionRepository interface {
	ScheduleDueGraphWork(context.Context, time.Time) (int, error)
	ClaimGraphProjection(context.Context, string, time.Duration, time.Time) (ProjectionWork, bool, error)
	FinishGraphProjection(context.Context, ProjectionWork, string, time.Time) error
	FailGraphProjection(context.Context, ProjectionWork, string, time.Time) error
}

type ProjectionBundleStore interface {
	Put(context.Context, core.GraphScope, string, map[string][]byte, application.GraphBundleManifest) (string, error)
}

type GraphJobPublisher interface {
	PublishJob(context.Context, graphworker.JobEnvelope) (bool, error)
}

type Dispatcher struct {
	repository ProjectionRepository
	bundles    ProjectionBundleStore
	jobs       GraphJobPublisher
	owner      string
	lease      time.Duration
	signingKey ed25519.PrivateKey
	now        func() time.Time
}

func NewDispatcher(repository ProjectionRepository, bundles ProjectionBundleStore, jobs GraphJobPublisher, owner string, lease time.Duration, signingKey ed25519.PrivateKey, now func() time.Time) (*Dispatcher, error) {
	if repository == nil || bundles == nil || jobs == nil || owner == "" || lease <= 0 || len(signingKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("hosted graph dispatcher dependencies are incomplete")
	}
	if now == nil {
		now = time.Now
	}
	return &Dispatcher{repository: repository, bundles: bundles, jobs: jobs, owner: owner, lease: lease, signingKey: append(ed25519.PrivateKey(nil), signingKey...), now: now}, nil
}

func (d *Dispatcher) RunOnce(ctx context.Context) (bool, error) {
	now := d.now().UTC()
	_, scheduleErr := d.repository.ScheduleDueGraphWork(ctx, now)
	work, claimed, err := d.repository.ClaimGraphProjection(ctx, d.owner, d.lease, now)
	if err != nil || !claimed {
		return false, errors.Join(scheduleErr, err)
	}
	mode := contracts.GraphIndexModeFull
	if work.BaseRevisionID != "" {
		mode = contracts.GraphIndexModeIncremental
	}
	projection, err := application.NewGraphProjectionBuilder().Build(application.GraphProjectionRequest{
		Scope: work.Job.Scope, ConfigurationID: work.Job.ConfigurationID, JobID: work.Job.ID, RevisionID: work.Job.RevisionID,
		Mode: mode, BaseRevisionID: work.BaseRevisionID, ProjectionPolicyVersion: work.ProjectionVersion, Cutoff: work.Cutoff,
		PromptFingerprint: work.PromptFingerprint, ModelRoutes: []string{work.ModelRoute}, CreatedAt: now,
		ExpiresAt: now.Add(application.GraphProjectionRetention), ProducerIdentity: d.owner, Records: work.Records,
	})
	if err != nil {
		return true, errors.Join(scheduleErr, d.repository.FailGraphProjection(ctx, work, "projection_rejected", d.now().UTC()))
	}
	correlations, err := json.Marshal(projection.Correlations)
	if err != nil {
		return true, err
	}
	input := application.GraphBundleInput{Scope: work.Job.Scope, RevisionID: work.Job.RevisionID, Projection: projection.DocumentsJSONL, Correlation: correlations, CreatedAt: now, ExpiresAt: now.Add(application.GraphProjectionRetention)}
	manifest, err := application.BuildGraphBundleManifest(input, d.signingKey)
	if err != nil {
		return true, err
	}
	if _, err := d.bundles.Put(ctx, work.Job.Scope, work.Job.RevisionID, map[string][]byte{"projection.jsonl": input.Projection, "correlation.jsonl": input.Correlation}, manifest); err != nil {
		return true, errors.Join(scheduleErr, d.repository.FailGraphProjection(ctx, work, "projection_custody_failed", d.now().UTC()))
	}
	envelope := graphworker.JobEnvelope{Scope: work.Job.Scope, JobID: work.Job.ID, ConfigurationID: work.Job.ConfigurationID, RevisionID: work.Job.RevisionID, ProjectionRevisionID: work.Job.RevisionID, ExpectedRevision: work.ExpectedRevision, BaseRevisionID: work.BaseRevisionID, Mode: mode, Attempt: work.Job.Attempt, CreatedAt: work.Job.CreatedAt, Limits: graphworker.DefaultWorkspaceLimits()}
	if _, err := d.jobs.PublishJob(ctx, envelope); err != nil {
		return true, errors.Join(scheduleErr, d.repository.FailGraphProjection(ctx, work, "queue_publish_failed", d.now().UTC()))
	}
	return true, errors.Join(scheduleErr, d.repository.FinishGraphProjection(ctx, work, projection.Manifest.Files[0].ContentHash, d.now().UTC()))
}
