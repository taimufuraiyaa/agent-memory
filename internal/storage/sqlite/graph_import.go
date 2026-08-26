package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var _ contracts.GraphRevisionBatchStore = (*Store)(nil)

func (s *Store) ImportGraphRevisionBatch(ctx context.Context, batch contracts.GraphRevisionImportBatch) error {
	if err := validateGraphRevisionBatch(batch); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state core.GraphRevisionState
	if err := tx.QueryRowContext(ctx, `SELECT state FROM graph_revisions
		WHERE tenant_id = ? AND workspace = ? AND configuration_id = ? AND id = ?`,
		batch.Scope.TenantID, batch.Scope.WorkspaceID, batch.ConfigurationID, batch.RevisionID).Scan(&state); err != nil {
		return err
	}
	if state != core.GraphRevisionImporting {
		return fmt.Errorf("graph revision must be importing, got %q", state)
	}
	for _, record := range batch.Entities {
		if err := rejectTombstonedGraphEvidence(ctx, tx, record.Evidence); err != nil {
			return err
		}
		if err := importGraphEntityOnTx(ctx, tx, record); err != nil {
			return err
		}
	}
	for _, record := range batch.Edges {
		if err := rejectTombstonedGraphEvidence(ctx, tx, record.Evidence); err != nil {
			return err
		}
		if err := importGraphEdgeOnTx(ctx, tx, record); err != nil {
			return err
		}
	}
	for _, record := range batch.Communities {
		if err := importGraphCommunityOnTx(ctx, tx, record); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE graph_revisions SET state = ?, updated_at = ?
		WHERE tenant_id = ? AND workspace = ? AND configuration_id = ? AND id = ? AND state = ?`,
		core.GraphRevisionReady, formatGraphTime(batchReadyTime(batch)), batch.Scope.TenantID,
		batch.Scope.WorkspaceID, batch.ConfigurationID, batch.RevisionID, core.GraphRevisionImporting)
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(result); err != nil {
		return err
	}
	return tx.Commit()
}

func rejectTombstonedGraphEvidence(ctx context.Context, tx *sql.Tx, evidence []core.GraphEvidence) error {
	for _, item := range evidence {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM graph_deletion_tombstones
			WHERE tenant_id = ? AND workspace = ? AND canonical_kind = ? AND canonical_id = ?)`,
			item.Scope.TenantID, item.Scope.WorkspaceID, item.CanonicalKind, item.CanonicalID).Scan(&exists); err != nil {
			return err
		}
		if exists == 1 {
			return fmt.Errorf("graph evidence is deletion-tombstoned")
		}
	}
	return nil
}

func validateGraphRevisionBatch(batch contracts.GraphRevisionImportBatch) error {
	if err := batch.Scope.Validate(); err != nil {
		return err
	}
	if batch.ConfigurationID == "" || batch.RevisionID == "" || batch.ExpectedEntities != len(batch.Entities) ||
		batch.ExpectedEdges != len(batch.Edges) || batch.ExpectedCommunities != len(batch.Communities) {
		return fmt.Errorf("invalid graph revision import batch")
	}
	for _, record := range batch.Entities {
		if record.Entity.Scope != batch.Scope || record.Entity.LastRevisionID != batch.RevisionID || record.Version.RevisionID != batch.RevisionID || len(record.Evidence) == 0 {
			return fmt.Errorf("invalid graph entity batch record")
		}
	}
	for _, record := range batch.Edges {
		if record.Edge.Scope != batch.Scope || record.Edge.LastRevisionID != batch.RevisionID || record.Version.RevisionID != batch.RevisionID || len(record.Evidence) == 0 {
			return fmt.Errorf("invalid graph edge batch record")
		}
	}
	for _, record := range batch.Communities {
		if record.Community.Scope != batch.Scope || record.Community.RevisionID != batch.RevisionID || record.Report.Scope != batch.Scope || record.Report.RevisionID != batch.RevisionID {
			return fmt.Errorf("invalid graph community batch record")
		}
	}
	return nil
}

func batchReadyTime(batch contracts.GraphRevisionImportBatch) time.Time {
	var latest time.Time
	for _, record := range batch.Entities {
		if record.Entity.UpdatedAt.After(latest) {
			latest = record.Entity.UpdatedAt
		}
	}
	for _, record := range batch.Edges {
		if record.Edge.UpdatedAt.After(latest) {
			latest = record.Edge.UpdatedAt
		}
	}
	if latest.IsZero() {
		latest = time.Now().UTC()
	}
	return latest.UTC()
}

func importGraphEntityOnTx(ctx context.Context, tx *sql.Tx, record contracts.GraphEntityImportRecord) error {
	if err := validateGraphEntityImport(record.Entity, record.Version, record.Evidence); err != nil {
		return err
	}
	aliases, err := json.Marshal(record.Version.Aliases)
	if err != nil {
		return err
	}
	entity, version := record.Entity, record.Version
	result, err := tx.ExecContext(ctx, `INSERT INTO graph_entities
		(id,tenant_id,workspace,trust,first_revision_id,last_revision_id,superseded_by,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET last_revision_id=excluded.last_revision_id,updated_at=excluded.updated_at
		WHERE graph_entities.tenant_id=excluded.tenant_id AND graph_entities.workspace=excluded.workspace`,
		entity.ID, entity.Scope.TenantID, entity.Scope.WorkspaceID, entity.Trust, entity.FirstRevisionID,
		entity.LastRevisionID, entity.SupersededBy, formatGraphTime(entity.CreatedAt), formatGraphTime(entity.UpdatedAt))
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_entity_versions
		(entity_id,revision_id,external_id,name,entity_type,description,aliases_json,occurrence_count,degree)
		VALUES(?,?,?,?,?,?,?,?,?)`, version.EntityID, version.RevisionID, version.ExternalID, version.Name,
		version.EntityType, version.Description, string(aliases), version.OccurrenceCount, version.Degree); err != nil {
		return err
	}
	for _, evidence := range record.Evidence {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_entity_evidence
			(entity_id,revision_id,evidence_id,tenant_id,workspace,canonical_kind,canonical_id,canonical_fingerprint,locator,occurrence_count)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, entity.ID, version.RevisionID, evidence.ID, evidence.Scope.TenantID,
			evidence.Scope.WorkspaceID, evidence.CanonicalKind, evidence.CanonicalID, evidence.CanonicalFingerprint,
			evidence.Locator, evidence.OccurrenceCount); err != nil {
			return err
		}
	}
	return nil
}

func importGraphEdgeOnTx(ctx context.Context, tx *sql.Tx, record contracts.GraphEdgeImportRecord) error {
	if err := validateGraphEdgeImport(record.Edge, record.Version, record.Evidence); err != nil {
		return err
	}
	edge, version := record.Edge, record.Version
	result, err := tx.ExecContext(ctx, `INSERT INTO graph_edges
		(id,tenant_id,workspace,source_entity_id,target_entity_id,normalized_kind,external_kind,trust,first_revision_id,last_revision_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET last_revision_id=excluded.last_revision_id,updated_at=excluded.updated_at
		WHERE graph_edges.tenant_id=excluded.tenant_id AND graph_edges.workspace=excluded.workspace`, edge.ID,
		edge.Scope.TenantID, edge.Scope.WorkspaceID, edge.SourceEntityID, edge.TargetEntityID, edge.NormalizedKind,
		edge.ExternalKind, edge.Trust, edge.FirstRevisionID, edge.LastRevisionID, formatGraphTime(edge.CreatedAt), formatGraphTime(edge.UpdatedAt))
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_edge_versions
		(edge_id,revision_id,external_id,description,weight,origin,provenance_approved) VALUES(?,?,?,?,?,?,?)`, edge.ID, version.RevisionID,
		version.ExternalID, version.Description, version.Weight, version.Origin, version.ProvenanceApproved); err != nil {
		return err
	}
	for _, evidence := range record.Evidence {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_edge_evidence
			(edge_id,revision_id,evidence_id,tenant_id,workspace,canonical_kind,canonical_id,canonical_fingerprint,locator,occurrence_count)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, edge.ID, version.RevisionID, evidence.ID, evidence.Scope.TenantID,
			evidence.Scope.WorkspaceID, evidence.CanonicalKind, evidence.CanonicalID, evidence.CanonicalFingerprint,
			evidence.Locator, evidence.OccurrenceCount); err != nil {
			return err
		}
	}
	return nil
}

func importGraphCommunityOnTx(ctx context.Context, tx *sql.Tx, record contracts.GraphCommunityImportRecord) error {
	if record.Report.ReviewVersion < 1 {
		record.Report.ReviewVersion = 1
	}
	if err := validateGraphCommunityImport(record.Community, record.Members, record.Report); err != nil {
		return err
	}
	findings, err := json.Marshal(record.Report.Findings)
	if err != nil {
		return err
	}
	community, report := record.Community, record.Report
	result, err := tx.ExecContext(ctx, `INSERT INTO graph_communities
		(id,tenant_id,workspace,configuration_id,revision_id,external_id,parent_id,level,entity_count,edge_count,source_count,unresolved_count,membership_fingerprint,evidence_fingerprint)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision_id=excluded.revision_id,external_id=excluded.external_id,
		parent_id=excluded.parent_id,level=excluded.level,entity_count=excluded.entity_count,edge_count=excluded.edge_count,
		source_count=excluded.source_count,unresolved_count=excluded.unresolved_count,configuration_id=excluded.configuration_id,
		membership_fingerprint=excluded.membership_fingerprint,evidence_fingerprint=excluded.evidence_fingerprint
		WHERE graph_communities.tenant_id=excluded.tenant_id AND graph_communities.workspace=excluded.workspace`,
		community.ID, community.Scope.TenantID, community.Scope.WorkspaceID, community.ConfigurationID, community.RevisionID, community.ExternalID,
		community.ParentID, community.Level, community.EntityCount, community.EdgeCount, community.SourceCount, community.UnresolvedCount,
		community.MembershipFingerprint, community.EvidenceFingerprint)
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(result); err != nil {
		return err
	}
	for _, member := range record.Members {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_community_members
			(community_id,revision_id,kind,target_id) VALUES(?,?,?,?)`, community.ID, community.RevisionID, member.Kind, member.TargetID); err != nil {
			return err
		}
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO graph_reports
		(id,tenant_id,workspace,community_id,revision_id,title,summary,findings_json,rank,trust,admission_state,stale,evidence_count,unresolved_count,
		model_route,model_fingerprint,prompt_fingerprint,membership_fingerprint,evidence_fingerprint,review_version)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision_id=excluded.revision_id,title=excluded.title,
		summary=excluded.summary,findings_json=excluded.findings_json,rank=excluded.rank,stale=excluded.stale,
		evidence_count=excluded.evidence_count,unresolved_count=excluded.unresolved_count,admission_state=excluded.admission_state,
		model_route=excluded.model_route,model_fingerprint=excluded.model_fingerprint,prompt_fingerprint=excluded.prompt_fingerprint,
		membership_fingerprint=excluded.membership_fingerprint,evidence_fingerprint=excluded.evidence_fingerprint,review_version=excluded.review_version
		WHERE graph_reports.tenant_id=excluded.tenant_id AND graph_reports.workspace=excluded.workspace`,
		report.ID, report.Scope.TenantID, report.Scope.WorkspaceID, report.CommunityID, report.RevisionID,
		report.Title, report.Summary, string(findings), report.Rank, report.Trust, report.AdmissionState, report.Stale, report.EvidenceCount, report.UnresolvedCount,
		report.ModelRoute, report.ModelFingerprint, report.PromptFingerprint, report.MembershipFingerprint, report.EvidenceFingerprint, report.ReviewVersion)
	if err != nil {
		return err
	}
	return requireScopedGraphUpsert(result)
}
