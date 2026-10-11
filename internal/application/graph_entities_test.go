package application

import (
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphEntityReconciliationConvergesDayOneAndDayTenEvidence(t *testing.T) {
	t.Parallel()
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	existing := graphExistingEntity(scope, "stable-book-a", "Book A", "work", "passage-1", "book-a")
	candidate := graphEntityCandidate(scope, "revision-10", "external-book-a-v10", "Book A", "work", "memory-10", "book-a")

	result, err := NewGraphEntityReconciler().Reconcile(GraphEntityReconciliationRequest{
		Scope: scope, RevisionID: "revision-10", Existing: []GraphExistingEntity{existing}, Candidates: []core.GraphEntityCandidate{candidate}, Now: graphEntityTestTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Entities[0].ID; got != "stable-book-a" {
		t.Fatalf("day-10 entity reconciled to %q", got)
	}
	if result.Decisions[0].ReasonCode != "compatible_evidence" {
		t.Fatalf("reason = %q", result.Decisions[0].ReasonCode)
	}
}

func TestGraphEntityReconciliationKeepsIncompatibleSameNamesSeparate(t *testing.T) {
	t.Parallel()
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	existing := graphExistingEntity(scope, "stable-jordan-person", "Jordan", "person", "memory-1", "biography-a")
	candidate := graphEntityCandidate(scope, "revision-2", "external-jordan-place", "Jordan", "place", "memory-2", "geography-b")

	result, err := NewGraphEntityReconciler().Reconcile(GraphEntityReconciliationRequest{
		Scope: scope, RevisionID: "revision-2", Existing: []GraphExistingEntity{existing}, Candidates: []core.GraphEntityCandidate{candidate}, Now: graphEntityTestTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Entities[0].ID == existing.Entity.ID || result.Decisions[0].ReasonCode != "same_name_conflict" {
		t.Fatalf("same-name conflict collapsed: %#v", result)
	}
}

func TestGraphEntityReconciliationUsesApprovedAliasesOnly(t *testing.T) {
	t.Parallel()
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	existing := graphExistingEntity(scope, "stable-nyc", "New York City", "place", "passage-1", "atlas-a")
	existing.ApprovedAliases = []string{"NYC"}
	candidate := graphEntityCandidate(scope, "revision-2", "external-nyc", "NYC", "place", "passage-2", "atlas-b")

	result, err := NewGraphEntityReconciler().Reconcile(GraphEntityReconciliationRequest{
		Scope: scope, RevisionID: "revision-2", Existing: []GraphExistingEntity{existing}, Candidates: []core.GraphEntityCandidate{candidate}, Now: graphEntityTestTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Entities[0].ID != "stable-nyc" || result.Decisions[0].ReasonCode != "approved_alias" {
		t.Fatalf("approved alias not applied: %#v", result)
	}
}

func TestGraphEntityReconciliationIsDeterministicAndRecordsLineage(t *testing.T) {
	t.Parallel()
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	candidate := graphEntityCandidate(scope, "revision-2", "external-a", "Retry Handler", "service", "memory-10", "checkout")
	candidate.ApprovedMergeEntityID = "stable-retry-handler"
	existing := graphExistingEntity(scope, "stable-retry-handler", "Retry Handler", "service", "memory-1", "checkout")

	request := GraphEntityReconciliationRequest{Scope: scope, RevisionID: "revision-2", Existing: []GraphExistingEntity{existing}, Candidates: []core.GraphEntityCandidate{candidate}, Now: graphEntityTestTime()}
	first, err := NewGraphEntityReconciler().Reconcile(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewGraphEntityReconciler().Reconcile(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Entities[0].ID != second.Entities[0].ID || first.Entities[0].ID != "stable-retry-handler" {
		t.Fatalf("non-deterministic identity: %q / %q", first.Entities[0].ID, second.Entities[0].ID)
	}
	if len(first.Lineage) != 1 || first.Lineage[0].Kind != core.GraphEntityLineageMerge {
		t.Fatalf("merge lineage missing: %#v", first.Lineage)
	}
}

func graphExistingEntity(scope core.GraphScope, id, name, entityType, canonicalID, locator string) GraphExistingEntity {
	now := graphEntityTestTime().Add(-time.Hour)
	return GraphExistingEntity{
		Entity:   core.GraphEntity{ID: id, Scope: scope, Trust: core.GraphTrustProposed, FirstRevisionID: "revision-1", LastRevisionID: "revision-1", CreatedAt: now, UpdatedAt: now},
		Version:  core.GraphEntityVersion{EntityID: id, RevisionID: "revision-1", ExternalID: "external-old", Name: name, EntityType: entityType, OccurrenceCount: 1},
		Evidence: []core.GraphEvidence{{ID: "evidence-old", Scope: scope, CanonicalKind: "memory", CanonicalID: canonicalID, CanonicalFingerprint: "sha256:" + canonicalID, Locator: locator, OccurrenceCount: 1}},
	}
}

func graphEntityCandidate(scope core.GraphScope, revisionID, externalID, name, entityType, canonicalID, locator string) core.GraphEntityCandidate {
	return core.GraphEntityCandidate{
		Scope: scope, RevisionID: revisionID, ExternalID: externalID, Name: name, EntityType: entityType,
		Evidence: []core.GraphEvidence{{ID: "evidence-" + canonicalID, Scope: scope, CanonicalKind: "memory", CanonicalID: canonicalID, CanonicalFingerprint: "sha256:" + canonicalID, Locator: locator, OccurrenceCount: 1}},
	}
}

func graphEntityTestTime() time.Time { return time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC) }
