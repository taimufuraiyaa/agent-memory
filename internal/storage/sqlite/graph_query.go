package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var _ contracts.GraphQueryStore = (*Store)(nil)

func (s *Store) LoadActiveGraphSnapshot(ctx context.Context, scope core.GraphScope, maxNodes, maxEdges, maxCommunities int) (contracts.GraphQuerySnapshot, error) {
	if err := scope.Validate(); err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	if maxNodes < 1 || maxNodes > 4096 || maxEdges < 1 || maxEdges > 16384 || maxCommunities < 0 || maxCommunities > 2048 {
		return contracts.GraphQuerySnapshot{}, fmt.Errorf("graph query snapshot limits are invalid")
	}
	snapshot := contracts.GraphQuerySnapshot{Scope: scope}
	var pending int
	var configurationVersion int64
	var configurationUpdated, reviewEpoch, deletionEpoch string
	err := s.db.QueryRowContext(ctx, `SELECT id,active_revision_id,version,updated_at,
		(SELECT count(*) FROM graph_change_journal journal WHERE journal.workspace=configuration.workspace AND journal.processed_revision_id=''),
		COALESCE((SELECT MAX(created_at) FROM graph_reviews review WHERE review.tenant_id=configuration.tenant_id AND review.workspace=configuration.workspace),''),
		COALESCE((SELECT MAX(deleted_at) FROM graph_deletion_tombstones tombstone WHERE tombstone.tenant_id=configuration.tenant_id AND tombstone.workspace=configuration.workspace),'')
		FROM graph_configurations configuration WHERE tenant_id=? AND workspace=? AND enabled=1 AND active_revision_id<>'' ORDER BY version DESC,id LIMIT 1`, scope.TenantID, scope.WorkspaceID).Scan(&snapshot.ConfigurationID, &snapshot.RevisionID, &configurationVersion, &configurationUpdated, &pending, &reviewEpoch, &deletionEpoch)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	snapshot.Fresh = pending == 0
	snapshot.CacheIdentity = core.FingerprintText(strings.Join([]string{snapshot.ConfigurationID, fmt.Sprint(configurationVersion), configurationUpdated, snapshot.RevisionID, reviewEpoch, deletionEpoch}, "\x00"))
	rows, err := s.db.QueryContext(ctx, `SELECT entity.id,entity.tenant_id,entity.workspace,entity.trust,entity.record_version,entity.first_revision_id,entity.last_revision_id,entity.superseded_by,entity.created_at,entity.updated_at,
		version.external_id,version.name,version.entity_type,version.description,version.aliases_json,version.occurrence_count,version.degree
		FROM graph_entities entity JOIN graph_entity_versions version ON version.entity_id=entity.id AND version.revision_id=?
		WHERE entity.tenant_id=? AND entity.workspace=? AND entity.trust IN ('proposed','reviewed','approved') ORDER BY entity.id LIMIT ?`, snapshot.RevisionID, scope.TenantID, scope.WorkspaceID, maxNodes)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	for rows.Next() {
		var record contracts.GraphQueryNode
		var aliases, createdAt, updatedAt string
		if err := rows.Scan(&record.Entity.ID, &record.Entity.Scope.TenantID, &record.Entity.Scope.WorkspaceID, &record.Entity.Trust, &record.RecordVersion, &record.Entity.FirstRevisionID, &record.Entity.LastRevisionID, &record.Entity.SupersededBy, &createdAt, &updatedAt,
			&record.Version.ExternalID, &record.Version.Name, &record.Version.EntityType, &record.Version.Description, &aliases, &record.Version.OccurrenceCount, &record.Version.Degree); err != nil {
			rows.Close()
			return contracts.GraphQuerySnapshot{}, err
		}
		record.Version.EntityID, record.Version.RevisionID = record.Entity.ID, snapshot.RevisionID
		record.Entity.CreatedAt, err = parseGraphTime(createdAt)
		if err == nil {
			record.Entity.UpdatedAt, err = parseGraphTime(updatedAt)
		}
		if err == nil {
			err = json.Unmarshal([]byte(aliases), &record.Version.Aliases)
		}
		if err != nil {
			rows.Close()
			return contracts.GraphQuerySnapshot{}, err
		}
		snapshot.Nodes = append(snapshot.Nodes, record)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	entityEvidence, err := s.graphQueryEvidenceByTarget(ctx, "graph_entity_evidence", "entity_id", scope, snapshot.RevisionID)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	for index := range snapshot.Nodes {
		snapshot.Nodes[index].Evidence = entityEvidence[snapshot.Nodes[index].Entity.ID]
	}
	edgeRows, err := s.db.QueryContext(ctx, `SELECT edge.id,edge.tenant_id,edge.workspace,edge.source_entity_id,edge.target_entity_id,edge.normalized_kind,edge.external_kind,edge.trust,edge.record_version,edge.first_revision_id,edge.last_revision_id,edge.created_at,edge.updated_at,
		version.external_id,version.description,version.weight,version.origin,version.provenance_approved
		FROM graph_edges edge JOIN graph_edge_versions version ON version.edge_id=edge.id AND version.revision_id=?
		WHERE edge.tenant_id=? AND edge.workspace=? AND edge.trust IN ('proposed','reviewed','approved') ORDER BY edge.id LIMIT ?`, snapshot.RevisionID, scope.TenantID, scope.WorkspaceID, maxEdges)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	for edgeRows.Next() {
		var record contracts.GraphQueryEdge
		var createdAt, updatedAt string
		if err := edgeRows.Scan(&record.Edge.ID, &record.Edge.Scope.TenantID, &record.Edge.Scope.WorkspaceID, &record.Edge.SourceEntityID, &record.Edge.TargetEntityID, &record.Edge.NormalizedKind, &record.Edge.ExternalKind, &record.Edge.Trust, &record.RecordVersion, &record.Edge.FirstRevisionID, &record.Edge.LastRevisionID, &createdAt, &updatedAt,
			&record.Version.ExternalID, &record.Version.Description, &record.Version.Weight, &record.Version.Origin, &record.Version.ProvenanceApproved); err != nil {
			edgeRows.Close()
			return contracts.GraphQuerySnapshot{}, err
		}
		record.Version.EdgeID, record.Version.RevisionID = record.Edge.ID, snapshot.RevisionID
		record.Edge.CreatedAt, err = parseGraphTime(createdAt)
		if err == nil {
			record.Edge.UpdatedAt, err = parseGraphTime(updatedAt)
		}
		if err != nil {
			edgeRows.Close()
			return contracts.GraphQuerySnapshot{}, err
		}
		snapshot.Edges = append(snapshot.Edges, record)
	}
	edgeRows.Close()
	if err := edgeRows.Err(); err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	edgeEvidence, err := s.graphQueryEvidenceByTarget(ctx, "graph_edge_evidence", "edge_id", scope, snapshot.RevisionID)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	for index := range snapshot.Edges {
		snapshot.Edges[index].Evidence = edgeEvidence[snapshot.Edges[index].Edge.ID]
	}
	if maxCommunities > 0 {
		snapshot.Communities, err = s.graphQueryCommunities(ctx, scope, snapshot.RevisionID, maxCommunities)
	}
	return snapshot, err
}

func (s *Store) graphQueryEvidenceByTarget(ctx context.Context, table, targetColumn string, scope core.GraphScope, revisionID string) (map[string][]core.GraphEvidence, error) {
	if (table != "graph_entity_evidence" || targetColumn != "entity_id") && (table != "graph_edge_evidence" || targetColumn != "edge_id") {
		return nil, fmt.Errorf("unsupported graph evidence query")
	}
	query := `SELECT ` + targetColumn + `,evidence_id,tenant_id,workspace,canonical_kind,canonical_id,canonical_fingerprint,locator,occurrence_count FROM ` + table + ` WHERE tenant_id=? AND workspace=? AND revision_id=? ORDER BY ` + targetColumn + `,evidence_id`
	rows, err := s.db.QueryContext(ctx, query, scope.TenantID, scope.WorkspaceID, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]core.GraphEvidence{}
	for rows.Next() {
		var targetID string
		var evidence core.GraphEvidence
		if err := rows.Scan(&targetID, &evidence.ID, &evidence.Scope.TenantID, &evidence.Scope.WorkspaceID, &evidence.CanonicalKind, &evidence.CanonicalID, &evidence.CanonicalFingerprint, &evidence.Locator, &evidence.OccurrenceCount); err != nil {
			return nil, err
		}
		result[targetID] = append(result[targetID], evidence)
	}
	return result, rows.Err()
}

func (s *Store) graphQueryCommunities(ctx context.Context, scope core.GraphScope, revisionID string, limit int) ([]contracts.GraphQueryCommunity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT community.id,community.configuration_id,community.external_id,community.parent_id,community.level,community.entity_count,community.edge_count,community.source_count,community.unresolved_count,community.membership_fingerprint,community.evidence_fingerprint,
		report.id,report.title,report.summary,report.findings_json,report.rank,report.trust,report.admission_state,report.stale,report.evidence_count,report.unresolved_count,report.model_route,report.model_fingerprint,report.prompt_fingerprint,report.membership_fingerprint,report.evidence_fingerprint,report.review_version
		FROM graph_communities community JOIN graph_reports report ON report.community_id=community.id AND report.revision_id=community.revision_id
		WHERE community.tenant_id=? AND community.workspace=? AND community.revision_id=? AND report.trust IN ('proposed','reviewed','approved') AND report.admission_state='admitted' AND report.stale=0 ORDER BY report.rank DESC,community.id LIMIT ?`, scope.TenantID, scope.WorkspaceID, revisionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []contracts.GraphQueryCommunity
	for rows.Next() {
		var record contracts.GraphQueryCommunity
		var findings string
		record.Community.Scope, record.Community.RevisionID = scope, revisionID
		record.Report.Scope, record.Report.RevisionID = scope, revisionID
		if err := rows.Scan(&record.Community.ID, &record.Community.ConfigurationID, &record.Community.ExternalID, &record.Community.ParentID, &record.Community.Level, &record.Community.EntityCount, &record.Community.EdgeCount, &record.Community.SourceCount, &record.Community.UnresolvedCount, &record.Community.MembershipFingerprint, &record.Community.EvidenceFingerprint,
			&record.Report.ID, &record.Report.Title, &record.Report.Summary, &findings, &record.Report.Rank, &record.Report.Trust, &record.Report.AdmissionState, &record.Report.Stale, &record.Report.EvidenceCount, &record.Report.UnresolvedCount, &record.Report.ModelRoute, &record.Report.ModelFingerprint, &record.Report.PromptFingerprint, &record.Report.MembershipFingerprint, &record.Report.EvidenceFingerprint, &record.Report.ReviewVersion); err != nil {
			return nil, err
		}
		record.Report.CommunityID = record.Community.ID
		if err := json.Unmarshal([]byte(findings), &record.Report.Findings); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	memberRows, err := s.db.QueryContext(ctx, `SELECT community_id,kind,target_id FROM graph_community_members WHERE revision_id=? ORDER BY community_id,kind,target_id`, revisionID)
	if err != nil {
		return nil, err
	}
	defer memberRows.Close()
	members := map[string][]contracts.GraphCommunityMember{}
	for memberRows.Next() {
		var communityID string
		var member contracts.GraphCommunityMember
		if err := memberRows.Scan(&communityID, &member.Kind, &member.TargetID); err != nil {
			return nil, err
		}
		members[communityID] = append(members[communityID], member)
	}
	if err := memberRows.Err(); err != nil {
		return nil, err
	}
	for index := range result {
		result[index].Members = members[result[index].Community.ID]
	}
	return result, nil
}

// ResolveGraphCanonicalMemories re-authorizes artifact references against the
// current canonical store. An old graph revision therefore cannot resurrect a
// deleted, superseded, cross-workspace, or content-changed memory.
func (s *Store) ResolveGraphCanonicalMemories(ctx context.Context, scope core.GraphScope, evidence []core.GraphEvidence) (map[string]core.MemoryEntry, map[string]struct{}, error) {
	if err := scope.Validate(); err != nil {
		return nil, nil, err
	}
	byID := map[string][]core.GraphEvidence{}
	for _, item := range evidence {
		if item.Scope != scope || item.CanonicalKind != "memory" || strings.TrimSpace(item.CanonicalID) == "" {
			continue
		}
		byID[item.CanonicalID] = append(byID[item.CanonicalID], item)
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	memories := make(map[string]core.MemoryEntry, len(ids))
	authorized := make(map[string]struct{})
	for _, id := range ids {
		memory, err := s.GetMemory(ctx, id)
		if err != nil {
			continue
		}
		if memory.Workspace != scope.WorkspaceID || memory.SupersededBy != nil {
			continue
		}
		fingerprint := core.FingerprintText(memory.Content)
		for _, item := range byID[id] {
			if item.CanonicalFingerprint == fingerprint {
				memories[id] = *memory
				authorized[graphEvidenceAuthorizationKey(item)] = struct{}{}
			}
		}
	}
	return memories, authorized, nil
}

func graphEvidenceAuthorizationKey(evidence core.GraphEvidence) string {
	return evidence.Scope.TenantID + "\x00" + evidence.Scope.WorkspaceID + "\x00" + evidence.CanonicalKind + "\x00" + evidence.CanonicalID + "\x00" + evidence.CanonicalFingerprint
}
