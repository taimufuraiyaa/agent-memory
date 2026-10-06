package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphImportRejectsFailedPreconditionsBeforeStaging(t *testing.T) {
	t.Parallel()
	store := &graphImportFakeStore{}
	request := graphImportRequestFixture()
	request.EvidenceResolved = false
	if err := NewGraphImportService(store).Import(context.Background(), request); err == nil {
		t.Fatal("unresolved evidence imported")
	}
	if store.calls != 0 {
		t.Fatal("store called before import preconditions passed")
	}
}

func TestGraphImportFailurePreservesInactiveRevisionAtomically(t *testing.T) {
	t.Parallel()
	store := &graphImportFakeStore{fail: errors.New("injected import failure")}
	err := NewGraphImportService(store).Import(context.Background(), graphImportRequestFixture())
	if !errors.Is(err, store.fail) {
		t.Fatalf("import error = %v", err)
	}
	if len(store.committed.Entities) != 0 {
		t.Fatal("partial import became visible")
	}
}

func TestGraphImportStagesCompleteRevision(t *testing.T) {
	t.Parallel()
	store := &graphImportFakeStore{}
	request := graphImportRequestFixture()
	if err := NewGraphImportService(store).Import(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(store.committed.Entities) != 1 || store.committed.RevisionID != request.Batch.RevisionID {
		t.Fatalf("incomplete committed batch: %#v", store.committed)
	}
}

type graphImportFakeStore struct {
	calls     int
	fail      error
	committed contracts.GraphRevisionImportBatch
}

func (s *graphImportFakeStore) ImportGraphRevisionBatch(_ context.Context, batch contracts.GraphRevisionImportBatch) error {
	s.calls++
	if s.fail != nil {
		return s.fail
	}
	s.committed = batch
	return nil
}

func graphImportRequestFixture() GraphImportRequest {
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	entity, version, evidence := graphApplicationEntityFixture(scope, "entity-a", "revision-2")
	return GraphImportRequest{
		Batch: contracts.GraphRevisionImportBatch{
			Scope: scope, ConfigurationID: "configuration-1", RevisionID: "revision-2",
			Entities:         []contracts.GraphEntityImportRecord{{Entity: entity, Version: version, Evidence: evidence}},
			ExpectedEntities: 1,
		},
		EvidenceResolved: true, AdmissionPassed: true, ReviewCarryForwardComplete: true, EvaluationPassed: true,
	}
}

func graphApplicationEntityFixture(scope core.GraphScope, id, revisionID string) (core.GraphEntity, core.GraphEntityVersion, []core.GraphEvidence) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	entity := core.GraphEntity{ID: id, Scope: scope, Trust: core.GraphTrustProposed, FirstRevisionID: revisionID, LastRevisionID: revisionID, CreatedAt: now, UpdatedAt: now}
	version := core.GraphEntityVersion{EntityID: id, RevisionID: revisionID, ExternalID: "external-" + id, Name: "Entity " + id, EntityType: "service", OccurrenceCount: 1}
	evidence := []core.GraphEvidence{{ID: "evidence-" + id, Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-" + id, CanonicalFingerprint: "sha256:" + id, OccurrenceCount: 1}}
	return entity, version, evidence
}
