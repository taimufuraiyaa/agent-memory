package application

import (
	"sort"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphReviewedRecord struct {
	TargetKind       string
	TargetID         string
	Trust            core.GraphTrustState
	RecordVersion    int64
	EvidenceIdentity string
	AgentMemoryOwned bool
}

type GraphReviewCarryRequest struct {
	Entities           []core.GraphEntity
	Edges              []core.GraphEdge
	Previous           []GraphReviewedRecord
	EvidenceIdentities map[string]string
}

type GraphReviewCarryDecision struct {
	TargetKind       string
	TargetID         string
	PreviousTargetID string
	Trust            core.GraphTrustState
	ReasonCode       string
}

type GraphReviewCarryAmbiguity struct {
	TargetKind   string
	TargetID     string
	CandidateIDs []string
}

type GraphReviewCarryResult struct {
	Entities                 []core.GraphEntity
	Edges                    []core.GraphEdge
	Carried                  []GraphReviewCarryDecision
	Ambiguous                []GraphReviewCarryAmbiguity
	PreservedApprovedEdgeIDs []string
}

func CarryGraphReviewState(request GraphReviewCarryRequest) GraphReviewCarryResult {
	result := GraphReviewCarryResult{
		Entities: append([]core.GraphEntity(nil), request.Entities...),
		Edges:    append([]core.GraphEdge(nil), request.Edges...),
	}
	previousByStable := make(map[string]GraphReviewedRecord, len(request.Previous))
	previousByEvidence := map[string][]GraphReviewedRecord{}
	currentEdges := map[string]struct{}{}
	for _, edge := range request.Edges {
		currentEdges[edge.ID] = struct{}{}
	}
	for _, previous := range request.Previous {
		key := previous.TargetKind + ":" + previous.TargetID
		previousByStable[key] = previous
		if previous.EvidenceIdentity != "" {
			previousByEvidence[previous.TargetKind+":"+previous.EvidenceIdentity] = append(previousByEvidence[previous.TargetKind+":"+previous.EvidenceIdentity], previous)
		}
		if previous.TargetKind == "edge" && previous.Trust == core.GraphTrustApproved && previous.AgentMemoryOwned {
			if _, present := currentEdges[previous.TargetID]; !present {
				result.PreservedApprovedEdgeIDs = append(result.PreservedApprovedEdgeIDs, previous.TargetID)
			}
		}
	}
	for index := range result.Entities {
		trust, decision, ambiguity := graphCarriedTrust("entity", result.Entities[index].ID, result.Entities[index].Trust, request.EvidenceIdentities, previousByStable, previousByEvidence)
		result.Entities[index].Trust = trust
		if decision != nil {
			result.Carried = append(result.Carried, *decision)
		}
		if ambiguity != nil {
			result.Ambiguous = append(result.Ambiguous, *ambiguity)
		}
	}
	for index := range result.Edges {
		trust, decision, ambiguity := graphCarriedTrust("edge", result.Edges[index].ID, result.Edges[index].Trust, request.EvidenceIdentities, previousByStable, previousByEvidence)
		result.Edges[index].Trust = trust
		if decision != nil {
			result.Carried = append(result.Carried, *decision)
		}
		if ambiguity != nil {
			result.Ambiguous = append(result.Ambiguous, *ambiguity)
		}
	}
	sort.Strings(result.PreservedApprovedEdgeIDs)
	return result
}

func graphCarriedTrust(kind, id string, current core.GraphTrustState, evidenceIdentities map[string]string, byStable map[string]GraphReviewedRecord, byEvidence map[string][]GraphReviewedRecord) (core.GraphTrustState, *GraphReviewCarryDecision, *GraphReviewCarryAmbiguity) {
	if previous, ok := byStable[kind+":"+id]; ok {
		return previous.Trust, &GraphReviewCarryDecision{kind, id, previous.TargetID, previous.Trust, "stable_identity"}, nil
	}
	evidenceIdentity := evidenceIdentities[kind+":"+id]
	if evidenceIdentity == "" {
		return current, nil, nil
	}
	candidates := byEvidence[kind+":"+evidenceIdentity]
	if len(candidates) == 1 {
		previous := candidates[0]
		return previous.Trust, &GraphReviewCarryDecision{kind, id, previous.TargetID, previous.Trust, "evidence_identity"}, nil
	}
	if len(candidates) > 1 {
		ids := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			ids = append(ids, candidate.TargetID)
		}
		sort.Strings(ids)
		return current, nil, &GraphReviewCarryAmbiguity{kind, id, ids}
	}
	return current, nil, nil
}
