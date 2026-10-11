package retrieval

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestLocalGraphExpansionUsesMultipleSeedsAndSeparatesConflicts(t *testing.T) {
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	evidence := func(id string) core.GraphEvidence {
		return core.GraphEvidence{Scope: scope, CanonicalKind: "memory", CanonicalID: id, CanonicalFingerprint: "sha256:" + id, OccurrenceCount: 1}
	}
	nodes := []GraphLocalNode{
		{ID: "book", Trust: core.GraphTrustApproved, Evidence: []core.GraphEvidence{evidence("book-a")}},
		{ID: "chapter", Trust: core.GraphTrustReviewed, Evidence: []core.GraphEvidence{evidence("day-10")}},
		{ID: "claim", Trust: core.GraphTrustApproved, Evidence: []core.GraphEvidence{evidence("claim")}},
	}
	edges := []GraphLocalEdge{
		{ID: "membership", SourceID: "chapter", TargetID: "book", Kind: core.GraphRelationshipMembership, Trust: core.GraphTrustReviewed, Weight: 0.9, Evidence: []core.GraphEvidence{evidence("day-10")}},
		{ID: "conflict", SourceID: "claim", TargetID: "book", Kind: core.GraphRelationshipContradicts, Trust: core.GraphTrustApproved, Weight: 1, Evidence: []core.GraphEvidence{evidence("claim")}},
	}
	authorized := map[string]struct{}{}
	for _, id := range []string{"book-a", "day-10", "claim"} {
		authorized[GraphAuthorizationKey(evidence(id))] = struct{}{}
	}
	result, err := ExpandLocalGraph(GraphLocalRequest{
		Scope: scope, Seeds: []GraphLocalSeed{{CanonicalKind: "memory", CanonicalID: "day-10", Score: 0.9}, {CanonicalKind: "memory", CanonicalID: "claim", Score: 0.8}},
		Nodes: nodes, Edges: edges, AuthorizedEvidence: authorized, Limits: DefaultGraphLocalLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || result.Paths[0].Seed.CanonicalID != "day-10" || result.Paths[0].Hops[0].ReasonCode != "typed_membership" {
		t.Fatalf("membership path missing: %#v", result)
	}
	foundClaimConflict := false
	for _, conflict := range result.Conflicts {
		foundClaimConflict = foundClaimConflict || conflict.Seed.CanonicalID == "claim" && conflict.Hop.Kind == core.GraphRelationshipContradicts
	}
	if !foundClaimConflict {
		t.Fatalf("conflict was not separated: %#v", result.Conflicts)
	}
}

func TestLocalGraphExpansionExcludesRejectedStaleAndUnauthorizedRecords(t *testing.T) {
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	evidence := core.GraphEvidence{Scope: scope, CanonicalKind: "memory", CanonicalID: "m1", CanonicalFingerprint: "sha256:m1", OccurrenceCount: 1}
	request := GraphLocalRequest{
		Scope: scope, Seeds: []GraphLocalSeed{{CanonicalKind: "memory", CanonicalID: "m1", Score: 1}},
		Nodes:              []GraphLocalNode{{ID: "a", Trust: core.GraphTrustApproved, Evidence: []core.GraphEvidence{evidence}}, {ID: "b", Trust: core.GraphTrustStale, Evidence: []core.GraphEvidence{evidence}}},
		Edges:              []GraphLocalEdge{{ID: "edge", SourceID: "a", TargetID: "b", Kind: core.GraphRelationshipSupports, Trust: core.GraphTrustRejected, Weight: 1, Evidence: []core.GraphEvidence{evidence}}},
		AuthorizedEvidence: map[string]struct{}{}, Limits: DefaultGraphLocalLimits(),
	}
	result, err := ExpandLocalGraph(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 0 || len(result.Conflicts) != 0 {
		t.Fatalf("ineligible graph state escaped traversal: %#v", result)
	}
}

func TestLocalGraphExpansionBoundsFanoutDepthAndSeedMonopoly(t *testing.T) {
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	var nodes []GraphLocalNode
	var edges []GraphLocalEdge
	authorized := map[string]struct{}{}
	for seed := 0; seed < 2; seed++ {
		canonical := "seed-" + string(rune('a'+seed))
		evidence := core.GraphEvidence{Scope: scope, CanonicalKind: "memory", CanonicalID: canonical, CanonicalFingerprint: "sha256:" + canonical, OccurrenceCount: 1}
		authorized[GraphAuthorizationKey(evidence)] = struct{}{}
		root := "root-" + canonical
		nodes = append(nodes, GraphLocalNode{ID: root, Trust: core.GraphTrustApproved, Evidence: []core.GraphEvidence{evidence}})
		for index := 0; index < 10; index++ {
			target := canonical + "-target-" + string(rune('a'+index))
			nodes = append(nodes, GraphLocalNode{ID: target, Trust: core.GraphTrustApproved, Evidence: []core.GraphEvidence{evidence}})
			edges = append(edges, GraphLocalEdge{ID: root + target, SourceID: root, TargetID: target, Kind: core.GraphRelationshipSupports, Trust: core.GraphTrustApproved, Weight: 1, Evidence: []core.GraphEvidence{evidence}})
		}
	}
	result, err := ExpandLocalGraph(GraphLocalRequest{Scope: scope, Seeds: []GraphLocalSeed{{"memory", "seed-a", 1}, {"memory", "seed-b", 0.9}}, Nodes: nodes, Edges: edges, AuthorizedEvidence: authorized, Limits: GraphLocalLimits{MaxSeeds: 2, MaxDepth: 1, MaxFanout: 2, MaxPaths: 4, MaxEvidence: 8}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, path := range result.Paths {
		seen[path.Seed.CanonicalID]++
		if len(path.Hops) > 1 {
			t.Fatal("depth limit exceeded")
		}
	}
	if seen["seed-a"] == 0 || seen["seed-b"] == 0 || len(result.Paths) > 4 || seen["seed-a"] > 2 || seen["seed-b"] > 2 {
		t.Fatalf("bounded multi-seed diversity failed: paths=%#v seen=%v", result.Paths, seen)
	}
}
