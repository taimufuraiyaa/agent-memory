package application

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphEdgeImportRequest struct {
	Scope              core.GraphScope
	RevisionID         string
	EntityIDs          map[string]struct{}
	AuthorizedEvidence map[string]struct{}
	Candidates         []core.GraphRelationshipCandidate
	Now                time.Time
}

type GraphQuarantinedRelationship struct {
	ExternalID string
	ReasonCode string
}

type GraphEdgeImportResult struct {
	Edges       []core.GraphEdge
	Versions    []core.GraphEdgeVersion
	Evidence    map[string][]core.GraphEvidence
	Quarantined []GraphQuarantinedRelationship
}

type GraphEdgeImporter struct{}

func NewGraphEdgeImporter() *GraphEdgeImporter { return &GraphEdgeImporter{} }

func (i *GraphEdgeImporter) Import(request GraphEdgeImportRequest) (GraphEdgeImportResult, error) {
	if err := request.Scope.Validate(); err != nil {
		return GraphEdgeImportResult{}, err
	}
	if strings.TrimSpace(request.RevisionID) == "" || request.Now.IsZero() {
		return GraphEdgeImportResult{}, fmt.Errorf("graph edge import revision and time are required")
	}
	candidates := append([]core.GraphRelationshipCandidate(nil), request.Candidates...)
	sort.Slice(candidates, func(a, b int) bool { return candidates[a].ExternalID < candidates[b].ExternalID })
	result := GraphEdgeImportResult{Evidence: make(map[string][]core.GraphEvidence)}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return GraphEdgeImportResult{}, err
		}
		if candidate.Scope != request.Scope || candidate.RevisionID != request.RevisionID {
			return GraphEdgeImportResult{}, fmt.Errorf("graph relationship candidate scope or revision mismatch")
		}
		if _, ok := request.EntityIDs[candidate.SourceEntityID]; !ok {
			result.Quarantined = append(result.Quarantined, GraphQuarantinedRelationship{candidate.ExternalID, "unresolved_source_entity"})
			continue
		}
		if _, ok := request.EntityIDs[candidate.TargetEntityID]; !ok {
			result.Quarantined = append(result.Quarantined, GraphQuarantinedRelationship{candidate.ExternalID, "unresolved_target_entity"})
			continue
		}
		authorized := true
		for _, evidence := range candidate.Evidence {
			if _, ok := request.AuthorizedEvidence[GraphEvidenceAuthorizationKey(evidence)]; !ok {
				authorized = false
				break
			}
		}
		if !authorized {
			result.Quarantined = append(result.Quarantined, GraphQuarantinedRelationship{candidate.ExternalID, "unauthorized_evidence"})
			continue
		}
		kind := core.NormalizeGraphRelationshipKind(candidate.ExternalKind)
		edgeID := deterministicGraphEdgeID(candidate, kind)
		if _, duplicate := seen[edgeID]; duplicate {
			result.Evidence[edgeID] = mergeGraphEvidence(result.Evidence[edgeID], candidate.Evidence)
			continue
		}
		seen[edgeID] = struct{}{}
		trust := core.GraphTrustProposed
		if candidate.Origin == core.GraphRelationshipOriginDeterministic && candidate.ProvenanceApproved {
			trust = core.GraphTrustApproved
		}
		edge := core.GraphEdge{
			ID: edgeID, Scope: request.Scope, SourceEntityID: candidate.SourceEntityID, TargetEntityID: candidate.TargetEntityID,
			NormalizedKind: string(kind), ExternalKind: strings.TrimSpace(candidate.ExternalKind), Trust: trust,
			FirstRevisionID: request.RevisionID, LastRevisionID: request.RevisionID, CreatedAt: request.Now.UTC(), UpdatedAt: request.Now.UTC(),
		}
		version := core.GraphEdgeVersion{
			EdgeID: edgeID, RevisionID: request.RevisionID, ExternalID: candidate.ExternalID,
			Description: strings.TrimSpace(candidate.Description), Weight: candidate.Weight,
			Origin: candidate.Origin, ProvenanceApproved: candidate.ProvenanceApproved,
		}
		result.Edges = append(result.Edges, edge)
		result.Versions = append(result.Versions, version)
		result.Evidence[edgeID] = mergeGraphEvidence(nil, candidate.Evidence)
	}
	return result, nil
}

func GraphEvidenceAuthorizationKey(evidence core.GraphEvidence) string {
	return evidence.Scope.TenantID + "\x00" + evidence.Scope.WorkspaceID + "\x00" + evidence.CanonicalKind + "\x00" + evidence.CanonicalID + "\x00" + evidence.CanonicalFingerprint
}

func deterministicGraphEdgeID(candidate core.GraphRelationshipCandidate, kind core.GraphRelationshipKind) string {
	hash := sha256.New()
	for _, value := range []string{candidate.Scope.TenantID, candidate.Scope.WorkspaceID, candidate.SourceEntityID, candidate.TargetEntityID, string(kind)} {
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}
	if kind == core.GraphRelationshipExternal {
		hash.Write([]byte(strings.ToLower(strings.TrimSpace(candidate.ExternalKind))))
		hash.Write([]byte{0})
	}
	evidence := append([]core.GraphEvidence(nil), candidate.Evidence...)
	sort.Slice(evidence, func(a, b int) bool {
		return GraphEvidenceAuthorizationKey(evidence[a]) < GraphEvidenceAuthorizationKey(evidence[b])
	})
	for _, item := range evidence {
		hash.Write([]byte(GraphEvidenceAuthorizationKey(item)))
		hash.Write([]byte{0})
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, hash.Sum(nil)).String()
}
