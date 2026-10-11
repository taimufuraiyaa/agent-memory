package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var _ contracts.GraphLifecycleRepository = (*Store)(nil)
var _ contracts.GraphLifecycleAtomicRepository = (*Store)(nil)

func (s *Store) DeleteGraphEvidenceAtomic(ctx context.Context, request contracts.GraphDeletionRequest) (contracts.GraphDeletionImpact, error) {
	if err := request.Scope.Validate(); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if request.RepairDeadline.IsZero() {
		return contracts.GraphDeletionImpact{}, fmt.Errorf("graph repair deadline is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	impact, err := revokeGraphEvidenceOnTx(ctx, tx, request)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if err := recordGraphDeletionOnTx(ctx, tx, request, impact); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	return impact, nil
}

func (s *Store) RevokeGraphEvidence(ctx context.Context, request contracts.GraphDeletionRequest) (contracts.GraphDeletionImpact, error) {
	if err := request.Scope.Validate(); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	impact, err := revokeGraphEvidenceOnTx(ctx, tx, request)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	return impact, nil
}

func revokeGraphEvidenceOnTx(ctx context.Context, tx *sql.Tx, request contracts.GraphDeletionRequest) (contracts.GraphDeletionImpact, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_edge_evidence WHERE tenant_id = ? AND workspace = ? AND canonical_kind = ? AND canonical_id = ?`,
		request.Scope.TenantID, request.Scope.WorkspaceID, request.CanonicalKind, request.CanonicalID); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_entity_evidence WHERE tenant_id = ? AND workspace = ? AND canonical_kind = ? AND canonical_id = ?`,
		request.Scope.TenantID, request.Scope.WorkspaceID, request.CanonicalKind, request.CanonicalID); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	edges, err := tx.ExecContext(ctx, `UPDATE graph_edges SET trust = ?, record_version = record_version + 1, updated_at = ?
		WHERE tenant_id = ? AND workspace = ? AND trust != ? AND NOT EXISTS (
			SELECT 1 FROM graph_edge_evidence evidence WHERE evidence.edge_id = graph_edges.id
		)`, core.GraphTrustDeleted, formatGraphTime(request.DeletedAt), request.Scope.TenantID, request.Scope.WorkspaceID, core.GraphTrustDeleted)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	entities, err := tx.ExecContext(ctx, `UPDATE graph_entities SET trust = ?, record_version = record_version + 1, updated_at = ?
		WHERE tenant_id = ? AND workspace = ? AND trust != ? AND NOT EXISTS (
			SELECT 1 FROM graph_entity_evidence evidence WHERE evidence.entity_id = graph_entities.id
		)`, core.GraphTrustDeleted, formatGraphTime(request.DeletedAt), request.Scope.TenantID, request.Scope.WorkspaceID, core.GraphTrustDeleted)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	reports, err := tx.ExecContext(ctx, `UPDATE graph_reports SET stale = 1 WHERE tenant_id = ? AND workspace = ? AND community_id IN (
		SELECT member.community_id FROM graph_community_members member
		JOIN graph_entities entity ON member.kind = 'entity' AND member.target_id = entity.id
		WHERE entity.tenant_id = ? AND entity.workspace = ? AND entity.trust = ?
	)`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.TenantID, request.Scope.WorkspaceID, core.GraphTrustDeleted)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	entityCount, _ := entities.RowsAffected()
	edgeCount, _ := edges.RowsAffected()
	reportCount, _ := reports.RowsAffected()
	return contracts.GraphDeletionImpact{AffectedEntities: int(entityCount), AffectedEdges: int(edgeCount), AffectedReports: int(reportCount)}, nil
}

func (s *Store) RecordGraphDeletionAndScheduleRepair(ctx context.Context, request contracts.GraphDeletionRequest, impact contracts.GraphDeletionImpact) error {
	if request.RepairDeadline.IsZero() {
		return fmt.Errorf("graph repair deadline is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := recordGraphDeletionOnTx(ctx, tx, request, impact); err != nil {
		return err
	}
	return tx.Commit()
}

func recordGraphDeletionOnTx(ctx context.Context, tx *sql.Tx, request contracts.GraphDeletionRequest, impact contracts.GraphDeletionImpact) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO graph_deletion_tombstones
		(tenant_id,workspace,canonical_kind,canonical_id,deleted_at) VALUES(?,?,?,?,?)
		ON CONFLICT(tenant_id,workspace,canonical_kind,canonical_id) DO UPDATE SET deleted_at = excluded.deleted_at`,
		request.Scope.TenantID, request.Scope.WorkspaceID, request.CanonicalKind, request.CanonicalID, formatGraphTime(request.DeletedAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO graph_repair_queue
		(tenant_id,workspace,canonical_kind,canonical_id,affected_entities,affected_edges,affected_reports,deadline_at,state)
		VALUES(?,?,?,?,?,?,?,?, 'queued') ON CONFLICT(tenant_id,workspace,canonical_kind,canonical_id) DO UPDATE SET
		affected_entities=excluded.affected_entities,affected_edges=excluded.affected_edges,
		affected_reports=excluded.affected_reports,deadline_at=excluded.deadline_at,state='queued'`,
		request.Scope.TenantID, request.Scope.WorkspaceID, request.CanonicalKind, request.CanonicalID,
		impact.AffectedEntities, impact.AffectedEdges, impact.AffectedReports, formatGraphTime(request.RepairDeadline)); err != nil {
		return err
	}
	return nil
}
