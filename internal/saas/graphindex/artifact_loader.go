package graphindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/validation"
)

// GraphArtifactObjectReader is the read-only capability needed by the hosted
// importer. It intentionally cannot list, overwrite, or delete objects.
type GraphArtifactObjectReader interface {
	Get(context.Context, string) ([]byte, error)
}

// ObjectArtifactLoader validates adapter-owned objects and converts their
// revision-local identifiers into Agent Memory's stable normalized records.
type ObjectArtifactLoader struct {
	objects GraphArtifactObjectReader
	now     func() time.Time
}

func NewObjectArtifactLoader(objects GraphArtifactObjectReader, now func() time.Time) (*ObjectArtifactLoader, error) {
	if objects == nil {
		return nil, fmt.Errorf("graph artifact object reader is required")
	}
	if now == nil {
		now = time.Now
	}
	return &ObjectArtifactLoader{objects: objects, now: now}, nil
}

func (l *ObjectArtifactLoader) LoadNormalized(ctx context.Context, prefix string) (contracts.GraphArtifactManifest, contracts.GraphRevisionImportBatch, error) {
	if l == nil || l.objects == nil || !validArtifactPrefix(prefix) {
		return contracts.GraphArtifactManifest{}, contracts.GraphRevisionImportBatch{}, fmt.Errorf("invalid graph artifact loader or prefix")
	}
	manifestBytes, err := l.objects.Get(ctx, prefix+"manifest.json")
	if err != nil {
		return contracts.GraphArtifactManifest{}, contracts.GraphRevisionImportBatch{}, err
	}
	var manifest contracts.GraphArtifactManifest
	if err := decodeSingleJSON(manifestBytes, &manifest); err != nil {
		return contracts.GraphArtifactManifest{}, contracts.GraphRevisionImportBatch{}, fmt.Errorf("decode graph artifact manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return contracts.GraphArtifactManifest{}, contracts.GraphRevisionImportBatch{}, err
	}
	files := make(map[string][]byte, len(manifest.Outputs))
	communities, reports := false, false
	for _, output := range manifest.Outputs {
		contents, getErr := l.objects.Get(ctx, prefix+output.Name)
		if getErr != nil {
			return contracts.GraphArtifactManifest{}, contracts.GraphRevisionImportBatch{}, getErr
		}
		files[output.Name] = contents
		communities = communities || output.Name == "communities.jsonl"
		reports = reports || output.Name == "community_reports.jsonl"
	}
	if communities != reports {
		return contracts.GraphArtifactManifest{}, contracts.GraphRevisionImportBatch{}, fmt.Errorf("graph communities and reports must be supplied together")
	}
	artifact, err := validation.ValidateGraphArtifactFiles(ctx, manifest, files, validation.GraphArtifactPolicy{CommunitiesEnabled: communities, ReportsEnabled: reports})
	if err != nil {
		return contracts.GraphArtifactManifest{}, contracts.GraphRevisionImportBatch{}, err
	}
	batch, err := normalizeHostedGraphArtifact(artifact, l.now().UTC())
	if err != nil {
		return contracts.GraphArtifactManifest{}, contracts.GraphRevisionImportBatch{}, err
	}
	return manifest, batch, nil
}

func validArtifactPrefix(prefix string) bool {
	return strings.HasSuffix(prefix, "/") && !strings.Contains(prefix, "..") && path.Clean(prefix) != "." && !strings.HasPrefix(prefix, "/")
}

func decodeSingleJSON(value []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple JSON values")
	} else if err != io.EOF {
		return err
	}
	return nil
}

func normalizeHostedGraphArtifact(artifact validation.ValidatedGraphArtifact, now time.Time) (contracts.GraphRevisionImportBatch, error) {
	manifest := artifact.Manifest
	entityCandidates := make([]core.GraphEntityCandidate, 0, len(artifact.Entities))
	for _, item := range artifact.Entities {
		entityCandidates = append(entityCandidates, core.GraphEntityCandidate{
			Scope: manifest.Scope, RevisionID: manifest.RevisionID, ExternalID: item.ID,
			Name: item.Name, EntityType: item.Type, OccurrenceCount: 1,
			Evidence: hostedGraphEvidence(manifest.Scope, item.Evidence),
		})
	}
	entities, err := application.NewGraphEntityReconciler().Reconcile(application.GraphEntityReconciliationRequest{
		Scope: manifest.Scope, RevisionID: manifest.RevisionID, Candidates: entityCandidates, Now: now,
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
		evidence := hostedGraphEvidence(manifest.Scope, item.Evidence)
		for _, value := range evidence {
			authorized[application.GraphEvidenceAuthorizationKey(value)] = struct{}{}
		}
		relationshipCandidates = append(relationshipCandidates, core.GraphRelationshipCandidate{
			Scope: manifest.Scope, RevisionID: manifest.RevisionID, ExternalID: item.ID,
			SourceEntityID: stableEntities[item.SourceID], TargetEntityID: stableEntities[item.TargetID],
			ExternalKind: item.Kind, Description: item.Kind, Weight: 0.5,
			Origin: core.GraphRelationshipOriginInferred, Evidence: evidence,
		})
	}
	edges, err := application.NewGraphEdgeImporter().Import(application.GraphEdgeImportRequest{
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
		communities, importErr := application.NewGraphCommunityImporter().Import(application.GraphCommunityImportRequest{
			Scope: manifest.Scope, ConfigurationID: manifest.ConfigurationID, RevisionID: manifest.RevisionID,
			EntityIDs: entityIDs, EdgeIDs: edgeIDs, Candidates: communityCandidates, PreviousReports: map[string]core.GraphReport{},
			ModelRoute: manifest.Models[0], ModelFingerprint: manifest.EnvironmentFingerprint,
			PromptFingerprint: manifest.PromptFingerprint, ReviewVersion: 0, Now: now,
		})
		if importErr != nil {
			return contracts.GraphRevisionImportBatch{}, importErr
		}
		for index, community := range communities.Communities {
			communityRecords = append(communityRecords, contracts.GraphCommunityImportRecord{Community: community, Members: communities.Members[community.ID], Report: communities.Reports[index]})
		}
	}
	return contracts.GraphRevisionImportBatch{
		Scope: manifest.Scope, ConfigurationID: manifest.ConfigurationID, RevisionID: manifest.RevisionID,
		Entities: entityRecords, Edges: edgeRecords, Communities: communityRecords,
		ExpectedEntities: len(entityRecords), ExpectedEdges: len(edgeRecords), ExpectedCommunities: len(communityRecords),
	}, nil
}

func hostedGraphEvidence(scope core.GraphScope, values []validation.GraphArtifactEvidence) []core.GraphEvidence {
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
		return application.GraphEvidenceAuthorizationKey(result[i]) < application.GraphEvidenceAuthorizationKey(result[j])
	})
	return result
}
