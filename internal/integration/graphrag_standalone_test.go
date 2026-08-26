package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestGraphRAGStandaloneIndexLifecycle(t *testing.T) {
	assertPackagedGraphRAGAdapterReady(t)
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "standalone.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	scope := core.GraphScope{WorkspaceID: "book-workspace"}
	now := time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)
	configuration := core.GraphConfiguration{ID: "default", Scope: scope, Version: 1, Enabled: true, AdapterName: "agent-memory-graphrag-adapter", AdapterVersion: "0.1.0", IndexMethod: core.GraphIndexStandard, ProjectionVersion: "v1", ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1, PromptFingerprint: "sha256:book-journey", ModelRoute: "index-only", CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}

	// Canonical Day-1 memory is immediately searchable before any graph job.
	memory := &core.MemoryEntry{ID: "memory-day-1", Type: core.SemanticMemory, Content: "Book A retry design", Workspace: scope.WorkspaceID, Confidence: 1, StorageTier: core.TierVector, CreatedAt: now, UpdatedAt: now, LastAccessedAt: now, Keywords: []core.MemoryTerm{{Term: "book_a", Display: "Book A", Source: core.TermSourceExplicit, NormalizationVersion: "v1", ExtractorVersion: "fixture-v1"}}}
	if err := store.UpsertMemory(ctx, memory); err != nil {
		t.Fatal(err)
	}
	matches, err := store.SearchMemoryTerms(ctx, sqlite.TermSearchQuery{Workspace: scope.WorkspaceID, Terms: []string{"book_a"}, NormalizationVersion: "v1"})
	if err != nil || len(matches) != 1 {
		t.Fatalf("basic search unavailable before graph: matches=%v err=%v", matches, err)
	}

	day1 := graphActivationBatch(scope, configuration.ID, "revision-day-1", []string{"edge-day-1"}, now)
	createImportActivate(t, ctx, store, configuration.ID, day1, "")

	// Day-10 is attached through explicit deterministic membership evidence. It
	// is deliberately not represented as authorship or as book-source content.
	day10 := graphActivationBatch(scope, configuration.ID, "revision-day-10", []string{"edge-membership-day-10", "edge-supports-day-10"}, now.Add(10*24*time.Hour))
	for index := range day10.Entities {
		day10.Entities[index].Evidence[0].CanonicalID = "memory-day-10"
		day10.Entities[index].Evidence[0].CanonicalFingerprint = "sha256:day-10"
	}
	day10.Edges[0].Edge.NormalizedKind = string(core.GraphRelationshipMembership)
	day10.Edges[0].Edge.ExternalKind = "part_of"
	day10.Edges[0].Edge.Trust = core.GraphTrustApproved
	day10.Edges[0].Version.Origin = core.GraphRelationshipOriginDeterministic
	day10.Edges[0].Version.ProvenanceApproved = true
	day10.Edges[0].Version.Description = "explicit Book A membership"
	day10.Edges[0].Evidence[0].CanonicalID = "memory-day-10"
	day10.Edges[0].Evidence[0].CanonicalFingerprint = "sha256:day-10"
	day10.Edges[1].Evidence[0].CanonicalID = "memory-day-10"
	day10.Edges[1].Evidence[0].CanonicalFingerprint = "sha256:day-10"
	createImportActivate(t, ctx, store, configuration.ID, day10, "revision-day-1")
	edges := graphQueryableEdgeIDs(t, store, scope)
	if len(edges) != 2 {
		t.Fatalf("Day-10 normalized relationships missing: %v", edges)
	}
	if err := store.ReviewGraphRecord(ctx, core.GraphReview{ID: "review-supports", Scope: scope, Action: core.GraphReviewApprove, TargetKind: "edge", TargetID: "edge-supports-day-10", From: core.GraphTrustProposed, To: core.GraphTrustApproved, ExpectedVersion: 1, ReviewerID: "operator", CreatedAt: now.Add(10 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	// A queued update can be cancelled without changing the active revision.
	operations := application.NewGraphOperationService(store)
	started, err := operations.Operate(ctx, application.GraphOperationRequest{Scope: scope, ConfigurationID: configuration.ID, Action: application.GraphOperationUpdate, IdempotencyKey: "cancel-me", ExpectedRevision: "revision-day-10", Actor: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Operate(ctx, application.GraphOperationRequest{Scope: scope, ConfigurationID: configuration.ID, Action: application.GraphOperationCancel, JobID: started.Job.ID, Actor: "integration"}); err != nil {
		t.Fatal(err)
	}
	active, _, err := store.ActiveGraphRevisions(ctx, scope, configuration.ID)
	if err != nil || active != "revision-day-10" {
		t.Fatalf("cancellation changed active revision: %q %v", active, err)
	}

	// Restart retains active data, and rollback restores the complete Day-1 revision.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	activation := application.NewGraphActivationService(store)
	if err := activation.Rollback(ctx, scope, configuration.ID); err != nil {
		t.Fatal(err)
	}
	if got := graphQueryableEdgeIDs(t, store, scope); len(got) != 1 || got[0] != "edge-day-1" {
		t.Fatalf("rollback failed after restart: %v", got)
	}
	if err := activation.Activate(ctx, core.GraphActivation{Scope: scope, ConfigurationID: configuration.ID, ExpectedRevision: "revision-day-1", CandidateRevision: "revision-day-10"}); err != nil {
		t.Fatal(err)
	}

	// Canonical deletion removes Day-10 support immediately and tombstones old
	// artifacts, so replay cannot resurrect the relationship.
	deletion := application.NewGraphLifecycleService(store)
	deletedAt := now.Add(11 * 24 * time.Hour)
	impact, err := deletion.DeleteCanonicalEvidence(ctx, application.GraphDeletionRequest{Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-day-10", DeletedAt: deletedAt, RepairDeadline: deletedAt.Add(15 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if impact.AffectedEdges < 2 {
		t.Fatalf("deletion did not repair Day-10 edges: %#v", impact)
	}
	replayed := day10
	replayed.RevisionID = "revision-replay"
	for index := range replayed.Entities {
		replayed.Entities[index].Entity.LastRevisionID = "revision-replay"
		replayed.Entities[index].Version.RevisionID = "revision-replay"
	}
	for index := range replayed.Edges {
		replayed.Edges[index].Edge.LastRevisionID = "revision-replay"
		replayed.Edges[index].Version.RevisionID = "revision-replay"
	}
	if err := store.CreateGraphRevision(ctx, core.GraphRevision{ID: "revision-replay", Scope: scope, ConfigurationID: configuration.ID, State: core.GraphRevisionImporting, Cutoff: core.GraphWatermark{EventTime: now.Add(10 * 24 * time.Hour)}, CreatedAt: now.Add(10 * 24 * time.Hour), UpdatedAt: now.Add(10 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := application.NewGraphImportService(store).Import(ctx, application.GraphImportRequest{Batch: replayed, EvidenceResolved: true, AdmissionPassed: true, ReviewCarryForwardComplete: true, EvaluationPassed: true}); err == nil {
		t.Fatal("pre-deletion artifact replay resurrected deleted evidence")
	}

	// Canonical-only rebuild succeeds with Day-1 evidence after native artifacts
	// are absent; no graph query API is used anywhere in this journey.
	rebuild := graphActivationBatch(scope, configuration.ID, "revision-canonical-rebuild", []string{"edge-canonical-rebuild"}, deletedAt.Add(time.Hour))
	createImportActivate(t, ctx, store, configuration.ID, rebuild, "revision-day-10")
	if got := graphQueryableEdgeIDs(t, store, scope); len(got) != 1 || got[0] != "edge-canonical-rebuild" {
		t.Fatalf("canonical rebuild failed: %v", got)
	}
	_ = store.Close()
}

func createImportActivate(t *testing.T, ctx context.Context, store *sqlite.Store, configurationID string, batch contracts.GraphRevisionImportBatch, expected string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.CreateGraphRevision(ctx, core.GraphRevision{ID: batch.RevisionID, Scope: batch.Scope, ConfigurationID: configurationID, BaseRevisionID: expected, State: core.GraphRevisionImporting, Cutoff: core.GraphWatermark{EventTime: now}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := application.NewGraphImportService(store).Import(ctx, application.GraphImportRequest{Batch: batch, EvidenceResolved: true, AdmissionPassed: true, ReviewCarryForwardComplete: true, EvaluationPassed: true}); err != nil {
		t.Fatal(err)
	}
	if err := application.NewGraphActivationService(store).Activate(ctx, core.GraphActivation{Scope: batch.Scope, ConfigurationID: configurationID, ExpectedRevision: expected, CandidateRevision: batch.RevisionID}); err != nil {
		t.Fatal(err)
	}
}

func assertPackagedGraphRAGAdapterReady(t *testing.T) {
	t.Helper()
	executable := os.Getenv("AGENT_MEMORY_GRAPHRAG_ADAPTER")
	if executable == "" {
		cwd, _ := os.Getwd()
		executable = filepath.Clean(filepath.Join(cwd, "..", "..", "tools", "graphrag-adapter", ".venv", "bin", "agent-memory-graphrag"))
	}
	if info, err := os.Stat(executable); err != nil || !info.Mode().IsRegular() {
		t.Skip("packaged GraphRAG adapter is not installed; set AGENT_MEMORY_GRAPHRAG_ADAPTER in the production journey")
	}
	output, err := exec.Command(executable, "readiness").CombinedOutput()
	if err != nil {
		t.Fatalf("packaged adapter readiness: %v: %s", err, output)
	}
	var response struct {
		State           string `json:"state"`
		GraphRAGVersion string `json:"graphrag_version"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	if response.State != "ready" || response.GraphRAGVersion != "3.1.2" {
		t.Fatalf("unexpected packaged adapter: %s", output)
	}
}
