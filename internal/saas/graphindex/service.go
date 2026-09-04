package graphindex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphworker"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/objectcustody"
)

type ArtifactLoader interface {
	LoadNormalized(context.Context, string) (contracts.GraphArtifactManifest, contracts.GraphRevisionImportBatch, error)
}
type CompletionLedger interface {
	ClaimGraphCompletion(context.Context, graphworker.CompletionEvent, string, time.Duration, time.Time) (bool, error)
	FinishGraphCompletion(context.Context, graphworker.CompletionEvent, string, time.Time) error
}
type Repository interface {
	contracts.GraphRevisionBatchStore
	PrepareGraphImport(context.Context, graphworker.CompletionEvent, contracts.GraphArtifactManifest, time.Time) (bool, error)
	RecordGraphFailure(context.Context, graphworker.CompletionEvent, time.Time) error
	ActivateGraphRevision(context.Context, core.GraphActivation) error
	AppendGraphOperatorAudit(context.Context, core.GraphScope, string, string, string, map[string]string) error
}

type Service struct {
	repository Repository
	artifacts  ArtifactLoader
	ledger     CompletionLedger
	owner      string
	lease      time.Duration
	now        func() time.Time
}

func NewService(repository Repository, artifacts ArtifactLoader, ledger CompletionLedger, owner string, lease time.Duration, now func() time.Time) (*Service, error) {
	if repository == nil || artifacts == nil || ledger == nil || strings.TrimSpace(owner) == "" || lease <= 0 {
		return nil, fmt.Errorf("hosted graph index dependencies are incomplete")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, artifacts: artifacts, ledger: ledger, owner: owner, lease: lease, now: now}, nil
}

func (s *Service) HandleCompletion(ctx context.Context, event graphworker.CompletionEvent) error {
	if err := event.Scope.Validate(); err != nil || event.Scope.TenantID == "" {
		return fmt.Errorf("invalid hosted graph completion scope")
	}
	if event.Status == "failed" && strings.TrimSpace(event.FailureCode) != "" && event.ArtifactPrefix == "" {
		return s.repository.RecordGraphFailure(ctx, event, s.now().UTC())
	}
	if event.Status != "completed" || strings.TrimSpace(event.FailureCode) != "" {
		return fmt.Errorf("graph completion event is not successful")
	}
	expectedPrefix, err := objectcustody.GraphArtifactStagingPrefix(event.Scope, event.JobID, event.RevisionID)
	if err != nil || event.ArtifactPrefix != expectedPrefix {
		return fmt.Errorf("forged graph artifact prefix")
	}
	claimed, err := s.ledger.ClaimGraphCompletion(ctx, event, s.owner, s.lease, s.now().UTC())
	if err != nil || !claimed {
		return err
	}
	manifest, batch, err := s.artifacts.LoadNormalized(ctx, expectedPrefix)
	if err != nil {
		return err
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	if manifest.Scope != event.Scope || manifest.JobID != event.JobID || manifest.RevisionID != event.RevisionID || batch.Scope != event.Scope || batch.ConfigurationID != event.ConfigurationID || batch.RevisionID != event.RevisionID {
		return fmt.Errorf("graph completion artifact identity mismatch")
	}
	importRequired, err := s.repository.PrepareGraphImport(ctx, event, manifest, s.now().UTC())
	if err != nil {
		return err
	}
	if importRequired {
		importer := application.NewGraphImportService(s.repository)
		if err := importer.Import(ctx, application.GraphImportRequest{Batch: batch, EvidenceResolved: true, AdmissionPassed: true, ReviewCarryForwardComplete: true, EvaluationPassed: true}); err != nil {
			return err
		}
	}
	if err := s.repository.ActivateGraphRevision(ctx, core.GraphActivation{Scope: event.Scope, ConfigurationID: event.ConfigurationID, ExpectedRevision: event.ExpectedRevision, CandidateRevision: event.RevisionID}); err != nil {
		return err
	}
	if err := s.repository.AppendGraphOperatorAudit(ctx, event.Scope, "graph_index.activate", "graph-worker", event.ID, map[string]string{"configuration_id": event.ConfigurationID, "revision_id": event.RevisionID, "job_id": event.JobID}); err != nil {
		return err
	}
	return s.ledger.FinishGraphCompletion(ctx, event, s.owner, s.now().UTC())
}
