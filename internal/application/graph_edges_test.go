package application

import (
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphEdgeImportStartsInferredEdgesProposed(t *testing.T) {
	t.Parallel()
	request := graphEdgeImportRequest()
	request.Candidates = []core.GraphRelationshipCandidate{graphRelationshipCandidate(request.Scope, "supports")}

	result, err := NewGraphEdgeImporter().Import(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Edges) != 1 || result.Edges[0].Trust != core.GraphTrustProposed {
		t.Fatalf("inferred edge was not proposed: %#v", result)
	}
	if result.Edges[0].NormalizedKind != string(core.GraphRelationshipSupports) {
		t.Fatalf("normalized kind = %q", result.Edges[0].NormalizedKind)
	}
}

func TestGraphEdgeImportApprovesDeterministicProvenanceOnly(t *testing.T) {
	t.Parallel()
	request := graphEdgeImportRequest()
	candidate := graphRelationshipCandidate(request.Scope, "member of")
	candidate.Origin = core.GraphRelationshipOriginDeterministic
	candidate.ProvenanceApproved = true
	request.Candidates = []core.GraphRelationshipCandidate{candidate}

	result, err := NewGraphEdgeImporter().Import(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Edges[0].Trust != core.GraphTrustApproved || result.Edges[0].NormalizedKind != string(core.GraphRelationshipMembership) {
		t.Fatalf("deterministic provenance not approved: %#v", result.Edges[0])
	}
}

func TestGraphEdgeImportQuarantinesUnresolvedOrUnauthorizedEvidence(t *testing.T) {
	t.Parallel()
	request := graphEdgeImportRequest()
	unauthorized := graphRelationshipCandidate(request.Scope, "supports")
	unauthorized.Evidence[0].CanonicalFingerprint = "sha256:unauthorized"
	unresolved := graphRelationshipCandidate(request.Scope, "supports")
	unresolved.ExternalID = "relationship-unresolved"
	unresolved.TargetEntityID = "missing-entity"
	request.Candidates = []core.GraphRelationshipCandidate{unauthorized, unresolved}

	result, err := NewGraphEdgeImporter().Import(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Edges) != 0 || len(result.Quarantined) != 2 {
		t.Fatalf("unsafe relationship imported: %#v", result)
	}
}

func TestGraphEdgeImportIsDeterministicAndPreservesExternalKind(t *testing.T) {
	t.Parallel()
	request := graphEdgeImportRequest()
	candidate := graphRelationshipCandidate(request.Scope, "causes/supports")
	request.Candidates = []core.GraphRelationshipCandidate{candidate}

	first, err := NewGraphEdgeImporter().Import(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewGraphEdgeImporter().Import(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Edges[0].ID != second.Edges[0].ID || first.Edges[0].NormalizedKind != string(core.GraphRelationshipExternal) || first.Edges[0].ExternalKind != "causes/supports" {
		t.Fatalf("external relationship was collapsed or unstable: %#v / %#v", first.Edges[0], second.Edges[0])
	}
}

func graphEdgeImportRequest() GraphEdgeImportRequest {
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	evidence := core.GraphEvidence{ID: "evidence-1", Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-10", CanonicalFingerprint: "sha256:memory-10", OccurrenceCount: 1}
	return GraphEdgeImportRequest{
		Scope: scope, RevisionID: "revision-2", Now: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC),
		EntityIDs:          map[string]struct{}{"entity-a": {}, "entity-b": {}},
		AuthorizedEvidence: map[string]struct{}{GraphEvidenceAuthorizationKey(evidence): {}},
	}
}

func graphRelationshipCandidate(scope core.GraphScope, kind string) core.GraphRelationshipCandidate {
	return core.GraphRelationshipCandidate{
		Scope: scope, RevisionID: "revision-2", ExternalID: "relationship-1",
		SourceEntityID: "entity-a", TargetEntityID: "entity-b", ExternalKind: kind,
		Description: "Evidence-bound relationship", Weight: 0.8, Origin: core.GraphRelationshipOriginInferred,
		Evidence: []core.GraphEvidence{{ID: "evidence-1", Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-10", CanonicalFingerprint: "sha256:memory-10", OccurrenceCount: 1}},
	}
}
