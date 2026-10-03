package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	localsqlite "github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestGraphStoreParity(t *testing.T) {
	ctx := context.Background()
	t.Run("sqlite", func(t *testing.T) {
		store, err := localsqlite.Open(ctx, filepath.Join(t.TempDir(), "graph-parity.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.Close() }()
		runGraphStoreParity(t, store, core.GraphScope{WorkspaceID: "workspace-a"})
	})
}

func runGraphStoreParity(t *testing.T, repository contracts.GraphRepository, scope core.GraphScope) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	configurationID, revisionID, jobID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	configuration := core.GraphConfiguration{
		ID: configurationID, Scope: scope, Version: 1, Enabled: true,
		AdapterName: "agent-memory-graphrag", AdapterVersion: "0.1.0", IndexMethod: core.GraphIndexStandard,
		ProjectionVersion: "projection-v1", ArtifactSchemaVersion: "graph-artifact/v1",
		PromptFingerprint: "sha256:prompts", ModelRoute: "index-text-primary", CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	revision := core.GraphRevision{
		ID: revisionID, Scope: scope, ConfigurationID: configurationID, State: core.GraphRevisionReady,
		Cutoff: core.GraphWatermark{Sequence: 1, EventTime: now, Digest: "sha256:cutoff"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.CreateGraphRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	job := core.GraphJob{
		ID: jobID, Scope: scope, ConfigurationID: configurationID, RevisionID: revisionID,
		IdempotencyKey: "parity-key", State: core.GraphJobQueued, CreatedAt: now, UpdatedAt: now,
	}
	first, created, err := repository.EnqueueGraphJob(ctx, job)
	if err != nil || !created {
		t.Fatalf("first enqueue: created=%v err=%v", created, err)
	}
	job.ID = uuid.NewString()
	duplicate, created, err := repository.EnqueueGraphJob(ctx, job)
	if err != nil || created || duplicate.ID != first.ID {
		t.Fatalf("duplicate enqueue = %#v created=%v err=%v", duplicate, created, err)
	}
	claimed, err := repository.ClaimGraphJobs(ctx, scope, "parity-worker", 1, time.Minute, now)
	if err != nil || len(claimed) != 1 || claimed[0].Attempt != 1 {
		t.Fatalf("claim = %#v err=%v", claimed, err)
	}
	if err := repository.CancelGraphJob(ctx, scope, claimed[0].ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateGraphRevision(ctx, core.GraphActivation{
		Scope: scope, ConfigurationID: configurationID, CandidateRevision: revisionID,
	}); err != nil {
		t.Fatal(err)
	}
	active, previous, err := repository.ActiveGraphRevisions(ctx, scope, configurationID)
	if err != nil || active != revisionID || previous != "" {
		t.Fatalf("active=%q previous=%q err=%v", active, previous, err)
	}
	if err := repository.DeleteGraphWorkspace(ctx, scope); err != nil {
		t.Fatal(err)
	}
}
