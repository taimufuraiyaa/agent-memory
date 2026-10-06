package evaluation

import (
	"fmt"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

func TestGraphRAGDay1Day10JourneyUsesRealTypedExpansionWithoutFalseAuthorship(t *testing.T) {
	scope := core.GraphScope{WorkspaceID: "book-journey"}
	evidence := func(id string) core.GraphEvidence {
		return core.GraphEvidence{Scope: scope, CanonicalKind: "memory", CanonicalID: id, CanonicalFingerprint: "sha256:" + id, OccurrenceCount: 1}
	}
	authorized := map[string]struct{}{
		graphretrieval.GraphAuthorizationKey(evidence("book-a")): {},
		graphretrieval.GraphAuthorizationKey(evidence("day-10")): {},
	}
	started := time.Now()
	result, err := graphretrieval.ExpandLocalGraph(graphretrieval.GraphLocalRequest{
		Scope: scope,
		Seeds: []graphretrieval.GraphLocalSeed{{CanonicalKind: "memory", CanonicalID: "day-10", Score: 1}},
		Nodes: []graphretrieval.GraphLocalNode{
			{ID: "book-a-node", Trust: core.GraphTrustApproved, Evidence: []core.GraphEvidence{evidence("book-a")}},
			{ID: "day-10-node", Trust: core.GraphTrustReviewed, Evidence: []core.GraphEvidence{evidence("day-10")}},
		},
		Edges:              []graphretrieval.GraphLocalEdge{{ID: "day10-part-of-book-a", SourceID: "day-10-node", TargetID: "book-a-node", Kind: core.GraphRelationshipMembership, Trust: core.GraphTrustReviewed, Weight: .95, Evidence: []core.GraphEvidence{evidence("book-a"), evidence("day-10")}}},
		AuthorizedEvidence: authorized,
		Limits:             graphretrieval.DefaultGraphLocalLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 75*time.Millisecond {
		t.Fatalf("local graph selection exceeded 75ms: %s", elapsed)
	}
	if len(result.Paths) != 1 || len(result.Paths[0].Hops) != 1 || result.Paths[0].Hops[0].ReasonCode != "typed_membership" || result.Paths[0].Hops[0].Kind != core.GraphRelationshipMembership {
		t.Fatalf("Day-10 relationship was not a bounded membership association: %+v", result)
	}
}

func TestGraphRAGLargeCorpusGlobalSelectionUsesAuthorizedEvidenceWithinBudget(t *testing.T) {
	scope := core.GraphScope{WorkspaceID: "large-corpus"}
	const candidates = 10_000
	communities := make([]graphretrieval.GraphGlobalCommunity, 0, candidates)
	authorized := make(map[string]struct{}, candidates)
	for index := 0; index < candidates; index++ {
		id := fmt.Sprintf("memory-%05d", index)
		evidence := core.GraphEvidence{Scope: scope, CanonicalKind: "memory", CanonicalID: id, CanonicalFingerprint: "sha256:" + id, OccurrenceCount: 1}
		authorized[graphretrieval.GraphAuthorizationKey(evidence)] = struct{}{}
		communities = append(communities, graphretrieval.GraphGlobalCommunity{ID: fmt.Sprintf("community-%05d", index), Level: index % 4, Rank: float64(candidates-index) / candidates, Trust: core.GraphTrustApproved, Fresh: true, SourceCount: 1, Title: "recurring failure pattern", Summary: "navigation only", Evidence: []core.GraphEvidence{evidence}})
	}
	started := time.Now()
	result, err := graphretrieval.SelectGlobalCommunities(graphretrieval.GraphGlobalRequest{Scope: scope, Query: "most recurring failure pattern across all incidents", Candidates: communities, AuthorizedEvidence: authorized, Limits: graphretrieval.DefaultGraphGlobalLimits()})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("global selection exceeded 250ms: %s", elapsed)
	}
	if len(result.Communities) == 0 || len(result.Evidence) == 0 || len(result.Communities) > graphretrieval.DefaultGraphGlobalLimits().MaxCommunities {
		t.Fatalf("large-corpus selection violated coverage limits: communities=%d evidence=%d", len(result.Communities), len(result.Evidence))
	}
	for _, item := range result.Evidence {
		if _, ok := authorized[graphretrieval.GraphAuthorizationKey(item)]; !ok {
			t.Fatal("unauthorized evidence escaped large-corpus selection")
		}
	}
}
