package application

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

// normalizeNativeGraphArtifact translates validated revision-local adapter IDs
// into scoped stable identities and preserves eligible report reviews.
func normalizeNativeGraphArtifact(artifact validation.ValidatedGraphArtifact, previousReports map[string]core.GraphReport, now time.Time, existing ...GraphExistingEntity) (contracts.GraphRevisionImportBatch, error) {
	manifest := artifact.Manifest
	entityCandidates := make([]core.GraphEntityCandidate, 0, len(artifact.Entities))
	for _, item := range artifact.Entities {
		entityCandidates = append(entityCandidates, core.GraphEntityCandidate{
			Scope: manifest.Scope, RevisionID: manifest.RevisionID, ExternalID: item.ID,
			Name: item.Name, EntityType: item.Type, OccurrenceCount: 1,
			Evidence: nativeGraphEvidence(manifest.Scope, item.Evidence),
		})
	}
	entities, err := NewGraphEntityReconciler().Reconcile(GraphEntityReconciliationRequest{
		Scope: manifest.Scope, RevisionID: manifest.RevisionID, Candidates: entityCandidates, Existing: existing, Now: now,
	})
	if err != nil {
		return contracts.GraphRevisionImportBatch{}, err
	}
	stableEntities := make(map[string]string, len(entities.Decisions))
	entityIDs := make(map[string]struct{}, len(entities.Entities))
	entityRecords := make([]contracts.GraphEntityImportRecord, 0, len(entities.Entities))
	for _, decision := range entities.Decisions {
		stableEntities[decision.ExternalID] = decision.StableEntityID
	}
	for index, entity := range entities.Entities {
		entityIDs[entity.ID] = struct{}{}
		entityRecords = append(entityRecords, contracts.GraphEntityImportRecord{Entity: entity, Version: entities.Versions[index], Evidence: entities.Evidence[entity.ID]})
	}

	authorized := map[string]struct{}{}
	relationshipCandidates := make([]core.GraphRelationshipCandidate, 0, len(artifact.Relationships))
	for _, item := range artifact.Relationships {
		kind, description := item.Kind, item.Description
		if description == "" {
			description = kind
		}
		if len(kind) > 256 {
			kind = "external"
		}
		evidence := nativeGraphEvidence(manifest.Scope, item.Evidence)
		for _, value := range evidence {
			authorized[GraphEvidenceAuthorizationKey(value)] = struct{}{}
		}
		relationshipCandidates = append(relationshipCandidates, core.GraphRelationshipCandidate{
			Scope: manifest.Scope, RevisionID: manifest.RevisionID, ExternalID: item.ID,
			SourceEntityID: stableEntities[item.SourceID], TargetEntityID: stableEntities[item.TargetID],
			ExternalKind: kind, Description: description, Weight: 0.5,
			Origin: core.GraphRelationshipOriginInferred, Evidence: evidence,
		})
	}
	edges, err := NewGraphEdgeImporter().Import(GraphEdgeImportRequest{
		Scope: manifest.Scope, RevisionID: manifest.RevisionID, EntityIDs: entityIDs,
		AuthorizedEvidence: authorized, Candidates: relationshipCandidates, Now: now,
	})
	if err != nil {
		return contracts.GraphRevisionImportBatch{}, err
	}
	if len(edges.Quarantined) != 0 {
		return contracts.GraphRevisionImportBatch{}, fmt.Errorf("validated graph relationships were quarantined")
	}
	edgeIDs := make(map[string]struct{}, len(edges.Edges))
	edgeRecords := make([]contracts.GraphEdgeImportRecord, 0, len(edges.Edges))
	for index, edge := range edges.Edges {
		edgeIDs[edge.ID] = struct{}{}
		edgeRecords = append(edgeRecords, contracts.GraphEdgeImportRecord{Edge: edge, Version: edges.Versions[index], Evidence: edges.Evidence[edge.ID]})
	}

	reportByCommunity := make(map[string]validation.GraphArtifactReport, len(artifact.Reports))
	for _, report := range artifact.Reports {
		if _, duplicate := reportByCommunity[report.CommunityID]; duplicate {
			return contracts.GraphRevisionImportBatch{}, fmt.Errorf("duplicate graph report for community")
		}
		reportByCommunity[report.CommunityID] = report
	}
	if len(reportByCommunity) != len(artifact.Communities) {
		return contracts.GraphRevisionImportBatch{}, fmt.Errorf("graph community report count mismatch")
	}
	communityCandidates := make([]core.GraphCommunityCandidate, 0, len(artifact.Communities))
	for _, item := range artifact.Communities {
		report, ok := reportByCommunity[item.ID]
		if !ok {
			return contracts.GraphRevisionImportBatch{}, fmt.Errorf("graph community report is unresolved")
		}
		members := make([]string, 0, len(item.EntityIDs))
		fingerprints := make([]string, 0)
		evidenceKeys := map[string]struct{}{}
		for _, externalID := range item.EntityIDs {
			members = append(members, stableEntities[externalID])
		}
		for _, evidence := range report.Evidence {
			key := evidence.CanonicalKind + "\x00" + evidence.CanonicalID + "\x00" + evidence.CanonicalFingerprint
			evidenceKeys[key] = struct{}{}
			fingerprints = append(fingerprints, evidence.CanonicalFingerprint)
		}
		communityCandidates = append(communityCandidates, core.GraphCommunityCandidate{
			ExternalID: item.ID, ParentExternalID: item.ParentID, EntityIDs: members,
			EvidenceFingerprints: fingerprints, SourceCount: len(evidenceKeys),
			Report: core.GraphReportCandidate{ExternalID: report.ID, Title: report.Title, Summary: report.Summary, Rank: 0.5, AdmissionState: core.GraphReportAdmitted},
		})
	}
	communityRecords := make([]contracts.GraphCommunityImportRecord, 0, len(communityCandidates))
	if len(communityCandidates) > 0 {
		communities, importErr := NewGraphCommunityImporter().Import(GraphCommunityImportRequest{
			Scope: manifest.Scope, ConfigurationID: manifest.ConfigurationID, RevisionID: manifest.RevisionID,
			EntityIDs: entityIDs, EdgeIDs: edgeIDs, Candidates: communityCandidates, PreviousReports: previousReports,
			ModelRoute: manifest.Models[0], ModelFingerprint: core.FingerprintText(strings.Join(manifest.Models, "\x00")),
			PromptFingerprint: manifest.PromptFingerprint, ReviewVersion: 0, Now: now,
		})
		if importErr != nil {
			return contracts.GraphRevisionImportBatch{}, importErr
		}
		for index, community := range communities.Communities {
			report := communities.Reports[index]
			// A newly generated report is current; only its human review can be
			// carried forward, and only when its evidence and model are unchanged.
			report.Stale = false
			report.ReviewVersion = 1
			if prior, ok := previousReports[community.ID]; ok && prior.MembershipFingerprint == report.MembershipFingerprint && prior.EvidenceFingerprint == report.EvidenceFingerprint && prior.ModelFingerprint == report.ModelFingerprint && prior.PromptFingerprint == report.PromptFingerprint {
				report.Trust = prior.Trust
				report.ReviewVersion = prior.ReviewVersion
			}
			communityRecords = append(communityRecords, contracts.GraphCommunityImportRecord{Community: community, Members: communities.Members[community.ID], Report: report})
		}
	}
	// SQLite's community rows contain a revision. Use immutable occurrence IDs
	// so staging an import cannot overwrite the active revision's communities.
	communityOccurrences := map[string]string{}
	for _, record := range communityRecords {
		communityOccurrences[record.Community.ID] = uuid.NewSHA1(uuid.NameSpaceOID, []byte(record.Community.ID+"\x00"+manifest.RevisionID)).String()
	}
	for i := range communityRecords {
		record := &communityRecords[i]
		record.Community.ID = communityOccurrences[record.Community.ID]
		record.Community.ParentID = communityOccurrences[record.Community.ParentID]
		record.Report.CommunityID = record.Community.ID
	}
	return contracts.GraphRevisionImportBatch{
		Scope: manifest.Scope, ConfigurationID: manifest.ConfigurationID, RevisionID: manifest.RevisionID,
		Entities: entityRecords, Edges: edgeRecords, Communities: communityRecords,
		ExpectedEntities: len(entityRecords), ExpectedEdges: len(edgeRecords), ExpectedCommunities: len(communityRecords),
	}, nil
}

func nativeGraphEvidence(scope core.GraphScope, values []validation.GraphArtifactEvidence) []core.GraphEvidence {
	result := make([]core.GraphEvidence, 0, len(values))
	for _, value := range values {
		digest := sha256.Sum256([]byte(scope.TenantID + "\x00" + scope.WorkspaceID + "\x00" + value.CanonicalKind + "\x00" + value.CanonicalID + "\x00" + value.CanonicalFingerprint))
		result = append(result, core.GraphEvidence{
			ID: uuid.NewSHA1(uuid.NameSpaceOID, digest[:]).String(), Scope: scope,
			CanonicalKind: value.CanonicalKind, CanonicalID: value.CanonicalID,
			CanonicalFingerprint: value.CanonicalFingerprint, OccurrenceCount: 1,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return GraphEvidenceAuthorizationKey(result[i]) < GraphEvidenceAuthorizationKey(result[j])
	})
	return result
}
