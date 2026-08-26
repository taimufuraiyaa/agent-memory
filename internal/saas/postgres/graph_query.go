package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var _ contracts.GraphQueryStore = (*GraphIndexRepository)(nil)

func (r *GraphIndexRepository) LoadActiveGraphSnapshot(ctx context.Context, scope core.GraphScope, maxNodes, maxEdges, maxCommunities int) (contracts.GraphQuerySnapshot, error) {
	if maxNodes < 1 || maxNodes > 4096 || maxEdges < 1 || maxEdges > 16384 || maxCommunities < 0 || maxCommunities > 2048 {
		return contracts.GraphQuerySnapshot{}, fmt.Errorf("graph query snapshot limits are invalid")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot := contracts.GraphQuerySnapshot{Scope: scope}
	var pending int
	var version int64
	var configurationUpdated, reviewEpoch, deletionEpoch string
	err = tx.QueryRow(ctx, `SELECT id::text,active_revision_id::text,version,updated_at::text,
		(SELECT count(*) FROM saas_graph_change_journal journal WHERE journal.tenant_id=configuration.tenant_id AND journal.workspace_id=configuration.workspace_id AND journal.processed_revision_id IS NULL),
		COALESCE((SELECT MAX(created_at)::text FROM saas_graph_reviews review WHERE review.tenant_id=configuration.tenant_id AND review.workspace_id=configuration.workspace_id),''),
		COALESCE((SELECT MAX(deleted_at)::text FROM saas_graph_deletion_tombstones tombstone WHERE tombstone.tenant_id=configuration.tenant_id AND tombstone.workspace_id=configuration.workspace_id),'')
		FROM saas_graph_configurations configuration WHERE workspace_id=$1::uuid AND enabled=true AND active_revision_id IS NOT NULL ORDER BY version DESC,id LIMIT 1`, scope.WorkspaceID).
		Scan(&snapshot.ConfigurationID, &snapshot.RevisionID, &version, &configurationUpdated, &pending, &reviewEpoch, &deletionEpoch)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	snapshot.Fresh = pending == 0
	snapshot.CacheIdentity = core.FingerprintText(strings.Join([]string{snapshot.ConfigurationID, fmt.Sprint(version), configurationUpdated, snapshot.RevisionID, reviewEpoch, deletionEpoch}, "\x00"))

	rows, err := tx.Query(ctx, `SELECT entity.id::text,entity.tenant_id::text,entity.workspace_id::text,entity.trust,entity.record_version,entity.first_revision_id::text,entity.last_revision_id::text,COALESCE(entity.superseded_by::text,''),entity.created_at,entity.updated_at,
		version.external_id,version.name,version.entity_type,version.description,version.aliases,version.occurrence_count,version.degree
		FROM saas_graph_entities entity JOIN saas_graph_entity_versions version ON version.tenant_id=entity.tenant_id AND version.entity_id=entity.id AND version.revision_id=$1::uuid
		WHERE entity.workspace_id=$2::uuid AND entity.trust IN ('proposed','reviewed','approved') ORDER BY entity.id LIMIT $3`, snapshot.RevisionID, scope.WorkspaceID, maxNodes)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	for rows.Next() {
		var record contracts.GraphQueryNode
		var aliases []byte
		if err := rows.Scan(&record.Entity.ID, &record.Entity.Scope.TenantID, &record.Entity.Scope.WorkspaceID, &record.Entity.Trust, &record.RecordVersion, &record.Entity.FirstRevisionID, &record.Entity.LastRevisionID, &record.Entity.SupersededBy, &record.Entity.CreatedAt, &record.Entity.UpdatedAt,
			&record.Version.ExternalID, &record.Version.Name, &record.Version.EntityType, &record.Version.Description, &aliases, &record.Version.OccurrenceCount, &record.Version.Degree); err != nil {
			rows.Close()
			return contracts.GraphQuerySnapshot{}, err
		}
		record.Version.EntityID, record.Version.RevisionID = record.Entity.ID, snapshot.RevisionID
		if err := json.Unmarshal(aliases, &record.Version.Aliases); err != nil {
			rows.Close()
			return contracts.GraphQuerySnapshot{}, err
		}
		snapshot.Nodes = append(snapshot.Nodes, record)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	entityEvidence, err := hostedGraphEvidence(ctx, tx, "saas_graph_entity_evidence", "entity_id", scope, snapshot.RevisionID)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	for index := range snapshot.Nodes {
		snapshot.Nodes[index].Evidence = entityEvidence[snapshot.Nodes[index].Entity.ID]
	}

	edgeRows, err := tx.Query(ctx, `SELECT edge.id::text,edge.tenant_id::text,edge.workspace_id::text,edge.source_entity_id::text,edge.target_entity_id::text,edge.normalized_kind,edge.external_kind,edge.trust,edge.record_version,edge.first_revision_id::text,edge.last_revision_id::text,edge.created_at,edge.updated_at,
		version.external_id,version.description,version.weight,version.origin,version.provenance_approved
		FROM saas_graph_edges edge JOIN saas_graph_edge_versions version ON version.tenant_id=edge.tenant_id AND version.edge_id=edge.id AND version.revision_id=$1::uuid
		WHERE edge.workspace_id=$2::uuid AND edge.trust IN ('proposed','reviewed','approved') ORDER BY edge.id LIMIT $3`, snapshot.RevisionID, scope.WorkspaceID, maxEdges)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	for edgeRows.Next() {
		var record contracts.GraphQueryEdge
		if err := edgeRows.Scan(&record.Edge.ID, &record.Edge.Scope.TenantID, &record.Edge.Scope.WorkspaceID, &record.Edge.SourceEntityID, &record.Edge.TargetEntityID, &record.Edge.NormalizedKind, &record.Edge.ExternalKind, &record.Edge.Trust, &record.RecordVersion, &record.Edge.FirstRevisionID, &record.Edge.LastRevisionID, &record.Edge.CreatedAt, &record.Edge.UpdatedAt,
			&record.Version.ExternalID, &record.Version.Description, &record.Version.Weight, &record.Version.Origin, &record.Version.ProvenanceApproved); err != nil {
			edgeRows.Close()
			return contracts.GraphQuerySnapshot{}, err
		}
		record.Version.EdgeID, record.Version.RevisionID = record.Edge.ID, snapshot.RevisionID
		snapshot.Edges = append(snapshot.Edges, record)
	}
	edgeRows.Close()
	if err := edgeRows.Err(); err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	edgeEvidence, err := hostedGraphEvidence(ctx, tx, "saas_graph_edge_evidence", "edge_id", scope, snapshot.RevisionID)
	if err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	for index := range snapshot.Edges {
		snapshot.Edges[index].Evidence = edgeEvidence[snapshot.Edges[index].Edge.ID]
	}
	if maxCommunities > 0 {
		snapshot.Communities, err = hostedGraphCommunities(ctx, tx, scope, snapshot.RevisionID, maxCommunities)
		if err != nil {
			return contracts.GraphQuerySnapshot{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return contracts.GraphQuerySnapshot{}, err
	}
	return snapshot, nil
}

func hostedGraphEvidence(ctx context.Context, tx pgx.Tx, table, targetColumn string, scope core.GraphScope, revisionID string) (map[string][]core.GraphEvidence, error) {
	if (table != "saas_graph_entity_evidence" || targetColumn != "entity_id") && (table != "saas_graph_edge_evidence" || targetColumn != "edge_id") {
		return nil, fmt.Errorf("unsupported hosted graph evidence query")
	}
	rows, err := tx.Query(ctx, `SELECT `+targetColumn+`::text,evidence_id::text,tenant_id::text,workspace_id::text,canonical_kind,canonical_id,canonical_fingerprint,locator,occurrence_count FROM `+table+` WHERE workspace_id=$1::uuid AND revision_id=$2::uuid ORDER BY `+targetColumn+`,evidence_id`, scope.WorkspaceID, revisionID)
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

func hostedGraphCommunities(ctx context.Context, tx pgx.Tx, scope core.GraphScope, revisionID string, limit int) ([]contracts.GraphQueryCommunity, error) {
	rows, err := tx.Query(ctx, `SELECT community.id::text,community.configuration_id::text,community.external_id,COALESCE(community.parent_id::text,''),community.level,community.entity_count,community.edge_count,community.source_count,community.unresolved_count,community.membership_fingerprint,community.evidence_fingerprint,
		report.id::text,report.title,report.summary,report.findings,report.rank,report.trust,report.admission_state,report.stale,report.evidence_count,report.unresolved_count,report.model_route,report.model_fingerprint,report.prompt_fingerprint,report.membership_fingerprint,report.evidence_fingerprint,report.review_version
		FROM saas_graph_communities community JOIN saas_graph_reports report ON report.tenant_id=community.tenant_id AND report.community_id=community.id AND report.revision_id=community.revision_id
		WHERE community.workspace_id=$1::uuid AND community.revision_id=$2::uuid AND report.trust IN ('proposed','reviewed','approved') AND report.admission_state='admitted' AND report.stale=false ORDER BY report.rank DESC,community.id LIMIT $3`, scope.WorkspaceID, revisionID, limit)
	if err != nil {
		return nil, err
	}
	var result []contracts.GraphQueryCommunity
	for rows.Next() {
		var record contracts.GraphQueryCommunity
		var findings []byte
		record.Community.Scope, record.Community.RevisionID = scope, revisionID
		record.Report.Scope, record.Report.RevisionID = scope, revisionID
		if err := rows.Scan(&record.Community.ID, &record.Community.ConfigurationID, &record.Community.ExternalID, &record.Community.ParentID, &record.Community.Level, &record.Community.EntityCount, &record.Community.EdgeCount, &record.Community.SourceCount, &record.Community.UnresolvedCount, &record.Community.MembershipFingerprint, &record.Community.EvidenceFingerprint,
			&record.Report.ID, &record.Report.Title, &record.Report.Summary, &findings, &record.Report.Rank, &record.Report.Trust, &record.Report.AdmissionState, &record.Report.Stale, &record.Report.EvidenceCount, &record.Report.UnresolvedCount, &record.Report.ModelRoute, &record.Report.ModelFingerprint, &record.Report.PromptFingerprint, &record.Report.MembershipFingerprint, &record.Report.EvidenceFingerprint, &record.Report.ReviewVersion); err != nil {
			rows.Close()
			return nil, err
		}
		record.Report.CommunityID = record.Community.ID
		if err := json.Unmarshal(findings, &record.Report.Findings); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, record)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	members, err := tx.Query(ctx, `SELECT community_id::text,kind,target_id FROM saas_graph_community_members WHERE workspace_id=$1::uuid AND revision_id=$2::uuid ORDER BY community_id,kind,target_id`, scope.WorkspaceID, revisionID)
	if err != nil {
		return nil, err
	}
	defer members.Close()
	byCommunity := map[string][]contracts.GraphCommunityMember{}
	for members.Next() {
		var id string
		var member contracts.GraphCommunityMember
		if err := members.Scan(&id, &member.Kind, &member.TargetID); err != nil {
			return nil, err
		}
		byCommunity[id] = append(byCommunity[id], member)
	}
	if err := members.Err(); err != nil {
		return nil, err
	}
	for index := range result {
		result[index].Members = byCommunity[result[index].Community.ID]
	}
	return result, nil
}

func (r *GraphIndexRepository) ResolveGraphCanonicalMemories(ctx context.Context, scope core.GraphScope, evidence []core.GraphEvidence) (map[string]core.MemoryEntry, map[string]struct{}, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	byID := map[string][]core.GraphEvidence{}
	for _, item := range evidence {
		if item.Scope == scope && item.CanonicalKind == "memory" && strings.TrimSpace(item.CanonicalID) != "" {
			byID[item.CanonicalID] = append(byID[item.CanonicalID], item)
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := map[string]core.MemoryEntry{}
	authorized := map[string]struct{}{}
	if len(ids) == 0 {
		return result, authorized, tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `SELECT id::text,memory_type,content,workspace_id::text,source,entities,tags,keywords,outcome,confidence,storage_tier,created_at,updated_at,content_hash FROM saas_memories WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND id::text=ANY($2::text[]) ORDER BY id`, scope.WorkspaceID, ids)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var memory core.MemoryEntry
		var sourceJSON, keywordsJSON, outcomeJSON []byte
		var contentHash string
		if err := rows.Scan(&memory.ID, &memory.Type, &memory.Content, &memory.Workspace, &sourceJSON, &memory.Entities, &memory.Tags, &keywordsJSON, &outcomeJSON, &memory.Confidence, &memory.StorageTier, &memory.CreatedAt, &memory.UpdatedAt, &contentHash); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if err := json.Unmarshal(sourceJSON, &memory.Source); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if err := json.Unmarshal(keywordsJSON, &memory.Keywords); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if len(outcomeJSON) > 0 {
			memory.Outcome = &core.Outcome{}
			if err := json.Unmarshal(outcomeJSON, memory.Outcome); err != nil {
				rows.Close()
				return nil, nil, err
			}
		}
		for _, item := range byID[memory.ID] {
			if item.CanonicalFingerprint == contentHash || item.CanonicalFingerprint == core.FingerprintText(memory.Content) {
				result[memory.ID] = memory
				authorized[hostedGraphAuthorizationKey(item)] = struct{}{}
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return result, authorized, nil
}

func hostedGraphAuthorizationKey(evidence core.GraphEvidence) string {
	return evidence.Scope.TenantID + "\x00" + evidence.Scope.WorkspaceID + "\x00" + evidence.CanonicalKind + "\x00" + evidence.CanonicalID + "\x00" + evidence.CanonicalFingerprint
}
