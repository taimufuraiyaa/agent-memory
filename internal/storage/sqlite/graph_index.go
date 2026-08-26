package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var ErrGraphEvidenceRequired = errors.New("graph evidence is required")

var _ contracts.GraphRepository = (*Store)(nil)

type GraphCommunityMember = contracts.GraphCommunityMember

func (s *Store) ImportGraphEntity(ctx context.Context, entity core.GraphEntity, version core.GraphEntityVersion, evidence []core.GraphEvidence) error {
	if len(evidence) == 0 {
		return ErrGraphEvidenceRequired
	}
	if err := validateGraphEntityImport(entity, version, evidence); err != nil {
		return err
	}
	aliases, err := json.Marshal(version.Aliases)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stableResult, err := tx.ExecContext(ctx, `INSERT INTO graph_entities (
		id, tenant_id, workspace, trust, first_revision_id, last_revision_id, superseded_by, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET last_revision_id = excluded.last_revision_id, updated_at = excluded.updated_at
	WHERE graph_entities.tenant_id = excluded.tenant_id AND graph_entities.workspace = excluded.workspace`,
		entity.ID, entity.Scope.TenantID, entity.Scope.WorkspaceID, entity.Trust, entity.FirstRevisionID,
		entity.LastRevisionID, entity.SupersededBy, formatGraphTime(entity.CreatedAt), formatGraphTime(entity.UpdatedAt))
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(stableResult); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_entity_versions (
		entity_id, revision_id, external_id, name, entity_type, description, aliases_json, occurrence_count, degree
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, version.EntityID, version.RevisionID, version.ExternalID,
		version.Name, version.EntityType, version.Description, string(aliases), version.OccurrenceCount, version.Degree); err != nil {
		return err
	}
	for _, item := range evidence {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_entity_evidence (
			entity_id, revision_id, evidence_id, tenant_id, workspace, canonical_kind, canonical_id,
			canonical_fingerprint, locator, occurrence_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, entity.ID, version.RevisionID, item.ID,
			item.Scope.TenantID, item.Scope.WorkspaceID, item.CanonicalKind, item.CanonicalID,
			item.CanonicalFingerprint, item.Locator, item.OccurrenceCount); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ImportGraphEdge(ctx context.Context, edge core.GraphEdge, version core.GraphEdgeVersion, evidence []core.GraphEvidence) error {
	if len(evidence) == 0 {
		return ErrGraphEvidenceRequired
	}
	if err := validateGraphEdgeImport(edge, version, evidence); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stableResult, err := tx.ExecContext(ctx, `INSERT INTO graph_edges (
		id, tenant_id, workspace, source_entity_id, target_entity_id, normalized_kind, external_kind,
		trust, first_revision_id, last_revision_id, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET last_revision_id = excluded.last_revision_id, updated_at = excluded.updated_at
	WHERE graph_edges.tenant_id = excluded.tenant_id AND graph_edges.workspace = excluded.workspace`,
		edge.ID, edge.Scope.TenantID, edge.Scope.WorkspaceID, edge.SourceEntityID, edge.TargetEntityID,
		edge.NormalizedKind, edge.ExternalKind, edge.Trust, edge.FirstRevisionID, edge.LastRevisionID,
		formatGraphTime(edge.CreatedAt), formatGraphTime(edge.UpdatedAt))
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(stableResult); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_edge_versions (
		edge_id, revision_id, external_id, description, weight, origin, provenance_approved
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, version.EdgeID, version.RevisionID, version.ExternalID, version.Description, version.Weight, version.Origin, version.ProvenanceApproved); err != nil {
		return err
	}
	for _, item := range evidence {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_edge_evidence (
			edge_id, revision_id, evidence_id, tenant_id, workspace, canonical_kind, canonical_id,
			canonical_fingerprint, locator, occurrence_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, edge.ID, version.RevisionID, item.ID,
			item.Scope.TenantID, item.Scope.WorkspaceID, item.CanonicalKind, item.CanonicalID,
			item.CanonicalFingerprint, item.Locator, item.OccurrenceCount); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ImportGraphCommunity(ctx context.Context, community core.GraphCommunity, members []GraphCommunityMember, report core.GraphReport) error {
	if report.ReviewVersion < 1 {
		report.ReviewVersion = 1
	}
	if err := validateGraphCommunityImport(community, members, report); err != nil {
		return err
	}
	findings, err := json.Marshal(report.Findings)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	communityResult, err := tx.ExecContext(ctx, `INSERT INTO graph_communities (
		id, tenant_id, workspace, configuration_id, revision_id, external_id, parent_id, level, entity_count, edge_count, source_count, unresolved_count, membership_fingerprint, evidence_fingerprint
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET revision_id = excluded.revision_id, external_id = excluded.external_id,
		parent_id = excluded.parent_id, level = excluded.level, entity_count = excluded.entity_count,
		edge_count = excluded.edge_count, source_count = excluded.source_count, unresolved_count = excluded.unresolved_count,
		configuration_id = excluded.configuration_id, membership_fingerprint = excluded.membership_fingerprint, evidence_fingerprint = excluded.evidence_fingerprint
	WHERE graph_communities.tenant_id = excluded.tenant_id AND graph_communities.workspace = excluded.workspace`,
		community.ID, community.Scope.TenantID, community.Scope.WorkspaceID, community.ConfigurationID, community.RevisionID,
		community.ExternalID, community.ParentID, community.Level, community.EntityCount, community.EdgeCount,
		community.SourceCount, community.UnresolvedCount, community.MembershipFingerprint, community.EvidenceFingerprint)
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(communityResult); err != nil {
		return err
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO graph_community_members
			(community_id, revision_id, kind, target_id) VALUES (?, ?, ?, ?)`,
			community.ID, community.RevisionID, member.Kind, member.TargetID); err != nil {
			return err
		}
	}
	reportResult, err := tx.ExecContext(ctx, `INSERT INTO graph_reports (
		id, tenant_id, workspace, community_id, revision_id, title, summary, findings_json,
		rank, trust, admission_state, stale, evidence_count, unresolved_count, model_route, model_fingerprint,
		prompt_fingerprint, membership_fingerprint, evidence_fingerprint, review_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET revision_id = excluded.revision_id, title = excluded.title,
		summary = excluded.summary, findings_json = excluded.findings_json, rank = excluded.rank,
		admission_state = excluded.admission_state, stale = excluded.stale, evidence_count = excluded.evidence_count, unresolved_count = excluded.unresolved_count,
		model_route = excluded.model_route, model_fingerprint = excluded.model_fingerprint, prompt_fingerprint = excluded.prompt_fingerprint,
		membership_fingerprint = excluded.membership_fingerprint, evidence_fingerprint = excluded.evidence_fingerprint, review_version = excluded.review_version
	WHERE graph_reports.tenant_id = excluded.tenant_id AND graph_reports.workspace = excluded.workspace`,
		report.ID, report.Scope.TenantID, report.Scope.WorkspaceID, report.CommunityID, report.RevisionID,
		report.Title, report.Summary, string(findings), report.Rank, report.Trust, report.AdmissionState, report.Stale,
		report.EvidenceCount, report.UnresolvedCount, report.ModelRoute, report.ModelFingerprint, report.PromptFingerprint,
		report.MembershipFingerprint, report.EvidenceFingerprint, report.ReviewVersion)
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(reportResult); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkGraphReportStale(ctx context.Context, scope core.GraphScope, reportID string) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE graph_reports SET stale = 1
		WHERE tenant_id = ? AND workspace = ? AND id = ?`, scope.TenantID, scope.WorkspaceID, reportID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("graph report not found")
	}
	return nil
}

func (s *Store) GraphReport(ctx context.Context, scope core.GraphScope, reportID string) (core.GraphReport, error) {
	if err := scope.Validate(); err != nil {
		return core.GraphReport{}, err
	}
	var report core.GraphReport
	var findings string
	err := s.db.QueryRowContext(ctx, `SELECT id, tenant_id, workspace, community_id, revision_id,
		title, summary, findings_json, rank, trust, admission_state, stale, evidence_count, unresolved_count,
		model_route, model_fingerprint, prompt_fingerprint, membership_fingerprint, evidence_fingerprint, review_version
		FROM graph_reports WHERE tenant_id = ? AND workspace = ? AND id = ?`,
		scope.TenantID, scope.WorkspaceID, reportID).Scan(&report.ID, &report.Scope.TenantID,
		&report.Scope.WorkspaceID, &report.CommunityID, &report.RevisionID, &report.Title, &report.Summary,
		&findings, &report.Rank, &report.Trust, &report.AdmissionState, &report.Stale, &report.EvidenceCount, &report.UnresolvedCount,
		&report.ModelRoute, &report.ModelFingerprint, &report.PromptFingerprint, &report.MembershipFingerprint, &report.EvidenceFingerprint, &report.ReviewVersion)
	if err != nil {
		return core.GraphReport{}, err
	}
	if err := json.Unmarshal([]byte(findings), &report.Findings); err != nil {
		return core.GraphReport{}, err
	}
	return report, nil
}

func validateGraphEntityImport(entity core.GraphEntity, version core.GraphEntityVersion, evidence []core.GraphEvidence) error {
	if err := entity.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(entity.ID) == "" || version.EntityID != entity.ID ||
		strings.TrimSpace(version.RevisionID) == "" || version.RevisionID != entity.LastRevisionID ||
		strings.TrimSpace(version.Name) == "" || strings.TrimSpace(version.EntityType) == "" {
		return fmt.Errorf("invalid graph entity import")
	}
	return validateGraphEvidenceScope(entity.Scope, evidence)
}

func validateGraphEdgeImport(edge core.GraphEdge, version core.GraphEdgeVersion, evidence []core.GraphEvidence) error {
	if err := edge.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(edge.ID) == "" || version.EdgeID != edge.ID || version.RevisionID != edge.LastRevisionID ||
		strings.TrimSpace(edge.SourceEntityID) == "" || strings.TrimSpace(edge.TargetEntityID) == "" ||
		strings.TrimSpace(edge.NormalizedKind) == "" || version.Weight < 0 || version.Weight > 1 {
		return fmt.Errorf("invalid graph edge import")
	}
	return validateGraphEvidenceScope(edge.Scope, evidence)
}

func validateGraphEvidenceScope(scope core.GraphScope, evidence []core.GraphEvidence) error {
	for _, item := range evidence {
		if item.Scope != scope || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.CanonicalKind) == "" ||
			strings.TrimSpace(item.CanonicalID) == "" || strings.TrimSpace(item.CanonicalFingerprint) == "" || item.OccurrenceCount < 0 {
			return fmt.Errorf("invalid or cross-scope graph evidence")
		}
	}
	return nil
}

func validateGraphCommunityImport(community core.GraphCommunity, members []GraphCommunityMember, report core.GraphReport) error {
	if err := community.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(community.ID) == "" || strings.TrimSpace(community.RevisionID) == "" || len(members) == 0 ||
		report.Scope != community.Scope || report.CommunityID != community.ID || report.RevisionID != community.RevisionID ||
		strings.TrimSpace(report.ID) == "" || strings.TrimSpace(report.Title) == "" || strings.TrimSpace(report.Summary) == "" {
		return fmt.Errorf("invalid graph community import")
	}
	for _, member := range members {
		if (member.Kind != "entity" && member.Kind != "edge" && member.Kind != "text_unit") || strings.TrimSpace(member.TargetID) == "" {
			return fmt.Errorf("invalid graph community member")
		}
	}
	return nil
}

func requireScopedGraphUpsert(result interface{ RowsAffected() (int64, error) }) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrGraphScopeConflict
	}
	return nil
}
