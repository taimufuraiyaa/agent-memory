package application

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphReviewCarriesRejectionByStableIdentityImmediately(t *testing.T) {
	t.Parallel()
	edge := core.GraphEdge{ID: "edge-a", Trust: core.GraphTrustProposed}
	result := CarryGraphReviewState(GraphReviewCarryRequest{
		Edges:              []core.GraphEdge{edge},
		Previous:           []GraphReviewedRecord{{TargetKind: "edge", TargetID: "edge-a", Trust: core.GraphTrustRejected, EvidenceIdentity: "sha256:old"}},
		EvidenceIdentities: map[string]string{"edge:edge-a": "sha256:new"},
	})
	if result.Edges[0].Trust != core.GraphTrustRejected {
		t.Fatalf("upstream output reactivated rejection: %#v", result.Edges[0])
	}
}

func TestGraphReviewCarriesCompatibleDecisionByEvidenceIdentity(t *testing.T) {
	t.Parallel()
	entity := core.GraphEntity{ID: "entity-new", Trust: core.GraphTrustProposed}
	result := CarryGraphReviewState(GraphReviewCarryRequest{
		Entities:           []core.GraphEntity{entity},
		Previous:           []GraphReviewedRecord{{TargetKind: "entity", TargetID: "entity-old", Trust: core.GraphTrustApproved, EvidenceIdentity: "sha256:evidence"}},
		EvidenceIdentities: map[string]string{"entity:entity-new": "sha256:evidence"},
	})
	if result.Entities[0].Trust != core.GraphTrustApproved || len(result.Carried) != 1 {
		t.Fatalf("compatible review not carried: %#v", result)
	}
}

func TestGraphReviewSurfacesAmbiguousCarryForward(t *testing.T) {
	t.Parallel()
	result := CarryGraphReviewState(GraphReviewCarryRequest{
		Entities: []core.GraphEntity{{ID: "entity-new", Trust: core.GraphTrustProposed}},
		Previous: []GraphReviewedRecord{
			{TargetKind: "entity", TargetID: "entity-a", Trust: core.GraphTrustApproved, EvidenceIdentity: "sha256:same"},
			{TargetKind: "entity", TargetID: "entity-b", Trust: core.GraphTrustRejected, EvidenceIdentity: "sha256:same"},
		},
		EvidenceIdentities: map[string]string{"entity:entity-new": "sha256:same"},
	})
	if len(result.Ambiguous) != 1 || result.Entities[0].Trust != core.GraphTrustProposed {
		t.Fatalf("ambiguous review was guessed: %#v", result)
	}
}

func TestGraphReviewPreservesApprovedAgentMemoryEdgeOmittedUpstream(t *testing.T) {
	t.Parallel()
	result := CarryGraphReviewState(GraphReviewCarryRequest{
		Previous: []GraphReviewedRecord{{TargetKind: "edge", TargetID: "edge-approved", Trust: core.GraphTrustApproved, EvidenceIdentity: "sha256:evidence", AgentMemoryOwned: true}},
	})
	if len(result.PreservedApprovedEdgeIDs) != 1 || result.PreservedApprovedEdgeIDs[0] != "edge-approved" {
		t.Fatalf("approved owned edge omitted: %#v", result)
	}
}
