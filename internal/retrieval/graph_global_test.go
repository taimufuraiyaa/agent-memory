package retrieval

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphGlobalSelectsDiverseGroundedCommunitiesAndReportsCoverage(t *testing.T) {
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	evidence := func(id string) core.GraphEvidence {
		return core.GraphEvidence{Scope: scope, CanonicalKind: "memory", CanonicalID: id, CanonicalFingerprint: "sha256:" + id, OccurrenceCount: 1}
	}
	authorized := map[string]struct{}{}
	for _, id := range []string{"m1", "m2", "m3"} {
		authorized[GraphAuthorizationKey(evidence(id))] = struct{}{}
	}
	result, err := SelectGlobalCommunities(GraphGlobalRequest{Scope: scope, Candidates: []GraphGlobalCommunity{
		{ID: "c1", Level: 0, Rank: 0.9, Trust: core.GraphTrustApproved, Fresh: true, SourceCount: 2, UnresolvedCount: 1, Summary: "payment patterns", Evidence: []core.GraphEvidence{evidence("m1"), evidence("m2")}},
		{ID: "c2", Level: 0, Rank: 0.89, Trust: core.GraphTrustApproved, Fresh: true, SourceCount: 1, Summary: "duplicate source", Evidence: []core.GraphEvidence{evidence("m1")}},
		{ID: "c3", Level: 1, Rank: 0.7, Trust: core.GraphTrustReviewed, Fresh: true, SourceCount: 1, Summary: "checkout patterns", Evidence: []core.GraphEvidence{evidence("m3")}},
	}, AuthorizedEvidence: authorized, Limits: GraphGlobalLimits{MaxCommunities: 2, MaxEvidence: 8}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Communities) != 2 || result.Communities[0].ID != "c1" || result.Communities[1].ID != "c3" {
		t.Fatalf("global diversity = %#v", result.Communities)
	}
	if result.CoveredSources != 3 || result.UnresolvedEvidence != 1 || len(result.Evidence) != 3 {
		t.Fatalf("global coverage = %#v", result)
	}
}

func TestGraphGlobalRejectsReportWithoutAuthorizedCanonicalEvidence(t *testing.T) {
	result, err := SelectGlobalCommunities(GraphGlobalRequest{
		Scope:              core.GraphScope{WorkspaceID: "workspace-a"},
		Candidates:         []GraphGlobalCommunity{{ID: "report-only", Rank: 1, Trust: core.GraphTrustApproved, Fresh: true, Summary: "unsupported generated report"}},
		AuthorizedEvidence: map[string]struct{}{}, Limits: GraphGlobalLimits{MaxCommunities: 4, MaxEvidence: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Communities) != 0 || len(result.Evidence) != 0 {
		t.Fatalf("report text escaped as grounding: %#v", result)
	}
}
