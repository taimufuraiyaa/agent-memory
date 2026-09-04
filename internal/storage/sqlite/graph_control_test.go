package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphControlMigrationIsAdditiveAndDisabledByDefault(t *testing.T) {
	t.Parallel()
	store := openGraphControlStore(t)

	for _, table := range []string{"graph_configurations", "graph_revisions", "graph_jobs", "graph_change_journal"} {
		var count int
		if err := store.db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s was not created", table)
		}
	}

	var configurations int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM graph_configurations`).Scan(&configurations); err != nil {
		t.Fatal(err)
	}
	if configurations != 0 {
		t.Fatalf("migration must not enable graph indexing, got %d configurations", configurations)
	}
}

func TestGraphJobIdempotencyReturnsExistingJob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphControlStore(t)
	configuration := graphConfigurationFixture()
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}

	job := graphJobFixture("job-1", "revision-1", "same-subject")
	first, created, err := store.EnqueueGraphJob(ctx, job)
	if err != nil || !created {
		t.Fatalf("enqueue first job: created=%v err=%v", created, err)
	}
	duplicate := graphJobFixture("job-2", "revision-2", "same-subject")
	second, created, err := store.EnqueueGraphJob(ctx, duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID || second.RevisionID != first.RevisionID {
		t.Fatalf("duplicate enqueue = %#v created=%v, want original %#v", second, created, first)
	}
}

func TestGraphConfigurationIdentityCannotMoveAcrossWorkspaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphControlStore(t)
	configuration := graphConfigurationFixture()
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}

	configuration.Scope.WorkspaceID = "workspace-b"
	if err := store.UpsertGraphConfiguration(ctx, configuration); !errors.Is(err, ErrGraphScopeConflict) {
		t.Fatalf("cross-workspace configuration update error = %v", err)
	}

	var workspace string
	if err := store.db.QueryRowContext(ctx, `SELECT workspace FROM graph_configurations WHERE id = ?`, configuration.ID).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if workspace != "workspace-a" {
		t.Fatalf("configuration moved to %q", workspace)
	}
}

func TestGraphActivationCompareAndSwapPublishesOnlyReadyRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphControlStore(t)
	configuration := graphConfigurationFixture()
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}

	notReady := graphRevisionFixture("revision-indexing", core.GraphRevisionIndexing)
	if err := store.CreateGraphRevision(ctx, notReady); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateGraphRevision(ctx, core.GraphActivation{
		Scope: configuration.Scope, ConfigurationID: configuration.ID, CandidateRevision: notReady.ID,
	}); !errors.Is(err, ErrGraphRevisionNotReady) {
		t.Fatalf("activate indexing revision error = %v", err)
	}

	first := graphRevisionFixture("revision-1", core.GraphRevisionReady)
	if err := store.CreateGraphRevision(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateGraphRevision(ctx, core.GraphActivation{
		Scope: configuration.Scope, ConfigurationID: configuration.ID, CandidateRevision: first.ID,
	}); err != nil {
		t.Fatalf("activate first revision: %v", err)
	}

	second := graphRevisionFixture("revision-2", core.GraphRevisionReady)
	if err := store.CreateGraphRevision(ctx, second); err != nil {
		t.Fatal(err)
	}
	stale := core.GraphActivation{
		Scope: configuration.Scope, ConfigurationID: configuration.ID,
		ExpectedRevision: "revision-stale", CandidateRevision: second.ID,
	}
	if err := store.ActivateGraphRevision(ctx, stale); !errors.Is(err, ErrGraphRevisionConflict) {
		t.Fatalf("stale activation error = %v", err)
	}

	active, previous, err := store.ActiveGraphRevisions(ctx, configuration.Scope, configuration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active != first.ID || previous != "" {
		t.Fatalf("failed activation changed pointers: active=%q previous=%q", active, previous)
	}
}

func TestGraphChangeJournalDeduplicatesCanonicalFingerprint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphControlStore(t)
	change := GraphChangeRecord{
		ID: "change-1", WorkspaceID: "workspace-a", SubjectKind: "memory", SubjectID: "memory-1",
		SubjectFingerprint: "sha256:memory-1", ProjectionVersion: "projection-v1",
		ConfigurationVersion: "configuration-v1", ChangeKind: "upsert", OccurredAt: time.Now().UTC(),
	}
	created, err := store.AppendGraphChange(ctx, change)
	if err != nil || !created {
		t.Fatalf("append first change: created=%v err=%v", created, err)
	}
	change.ID = "change-2"
	created, err = store.AppendGraphChange(ctx, change)
	if err != nil || created {
		t.Fatalf("append duplicate change: created=%v err=%v", created, err)
	}
}

func openGraphControlStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "graph-control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func graphConfigurationFixture() core.GraphConfiguration {
	now := time.Now().UTC()
	return core.GraphConfiguration{
		ID: "configuration-1", Scope: core.GraphScope{WorkspaceID: "workspace-a"}, Version: 1,
		Enabled: true, AdapterName: "agent-memory-graphrag", AdapterVersion: "0.1.0",
		IndexMethod: core.GraphIndexStandard, ProjectionVersion: "projection-v1",
		ArtifactSchemaVersion: "graph-artifact/v1", PromptFingerprint: "sha256:prompts",
		ModelRoute: "index-text-primary", CreatedAt: now, UpdatedAt: now,
	}
}

func graphJobFixture(id, revisionID, key string) core.GraphJob {
	now := time.Now().UTC()
	return core.GraphJob{
		ID: id, Scope: core.GraphScope{WorkspaceID: "workspace-a"}, ConfigurationID: "configuration-1",
		RevisionID: revisionID, IdempotencyKey: key, State: core.GraphJobQueued, CreatedAt: now, UpdatedAt: now,
	}
}

func graphRevisionFixture(id string, state core.GraphRevisionState) core.GraphRevision {
	now := time.Now().UTC()
	return core.GraphRevision{
		ID: id, Scope: core.GraphScope{WorkspaceID: "workspace-a"}, ConfigurationID: "configuration-1",
		State: state, Cutoff: core.GraphWatermark{Sequence: 1, EventTime: now, Digest: "sha256:cutoff"},
		CreatedAt: now, UpdatedAt: now,
	}
}
