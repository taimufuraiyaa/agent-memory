package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

var _ contracts.GraphLifecycleRepository = (*GraphIndexRepository)(nil)
var _ contracts.GraphLifecycleAtomicRepository = (*GraphIndexRepository)(nil)

// DeleteGraphEvidenceAtomic makes the canonical deletion tombstone authoritative
// in the same transaction that revokes derived evidence and queues repair.
func (r *GraphIndexRepository) DeleteGraphEvidenceAtomic(ctx context.Context, request contracts.GraphDeletionRequest) (contracts.GraphDeletionImpact, error) {
	tx, err := r.begin(ctx, request.Scope)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	impact, err := revokeHostedGraphEvidence(ctx, tx, request)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if err := recordHostedGraphDeletion(ctx, tx, request, impact); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if err := appendHostedGraphDeletionAudit(ctx, tx, request, impact); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	return impact, nil
}

// The split methods satisfy the provider-neutral compatibility contract. New
// application code detects and uses DeleteGraphEvidenceAtomic instead.
func (r *GraphIndexRepository) RevokeGraphEvidence(ctx context.Context, request contracts.GraphDeletionRequest) (contracts.GraphDeletionImpact, error) {
	tx, err := r.begin(ctx, request.Scope)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	impact, err := revokeHostedGraphEvidence(ctx, tx, request)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	return impact, tx.Commit(ctx)
}

func (r *GraphIndexRepository) RecordGraphDeletionAndScheduleRepair(ctx context.Context, request contracts.GraphDeletionRequest, impact contracts.GraphDeletionImpact) error {
	tx, err := r.begin(ctx, request.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := recordHostedGraphDeletion(ctx, tx, request, impact); err != nil {
		return err
	}
	if err := appendHostedGraphDeletionAudit(ctx, tx, request, impact); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func revokeHostedGraphEvidence(ctx context.Context, tx pgx.Tx, request contracts.GraphDeletionRequest) (contracts.GraphDeletionImpact, error) {
	args := []any{request.Scope.TenantID, request.Scope.WorkspaceID, request.CanonicalKind, request.CanonicalID}
	if _, err := tx.Exec(ctx, `DELETE FROM saas_graph_edge_evidence WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND canonical_kind=$3 AND canonical_id=$4`, args...); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM saas_graph_entity_evidence WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND canonical_kind=$3 AND canonical_id=$4`, args...); err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	edges, err := tx.Exec(ctx, `UPDATE saas_graph_edges edge SET trust='deleted',record_version=record_version+1,updated_at=$3
		WHERE edge.tenant_id=$1::uuid AND edge.workspace_id=$2::uuid AND edge.trust<>'deleted'
		AND NOT EXISTS (SELECT 1 FROM saas_graph_edge_evidence evidence WHERE evidence.tenant_id=edge.tenant_id AND evidence.workspace_id=edge.workspace_id AND evidence.edge_id=edge.id)`,
		request.Scope.TenantID, request.Scope.WorkspaceID, request.DeletedAt.UTC())
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	entities, err := tx.Exec(ctx, `UPDATE saas_graph_entities entity SET trust='deleted',record_version=record_version+1,updated_at=$3
		WHERE entity.tenant_id=$1::uuid AND entity.workspace_id=$2::uuid AND entity.trust<>'deleted'
		AND NOT EXISTS (SELECT 1 FROM saas_graph_entity_evidence evidence WHERE evidence.tenant_id=entity.tenant_id AND evidence.workspace_id=entity.workspace_id AND evidence.entity_id=entity.id)`,
		request.Scope.TenantID, request.Scope.WorkspaceID, request.DeletedAt.UTC())
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	reports, err := tx.Exec(ctx, `UPDATE saas_graph_reports report SET stale=true
		WHERE report.tenant_id=$1::uuid AND report.workspace_id=$2::uuid AND report.stale=false
		AND EXISTS (SELECT 1 FROM saas_graph_community_members member
			JOIN saas_graph_entities entity ON entity.tenant_id=member.tenant_id AND entity.workspace_id=member.workspace_id AND member.kind='entity' AND member.target_id=entity.id::text
			WHERE member.tenant_id=report.tenant_id AND member.workspace_id=report.workspace_id AND member.community_id=report.community_id AND entity.trust='deleted')`,
		request.Scope.TenantID, request.Scope.WorkspaceID)
	if err != nil {
		return contracts.GraphDeletionImpact{}, err
	}
	return contracts.GraphDeletionImpact{AffectedEntities: int(entities.RowsAffected()), AffectedEdges: int(edges.RowsAffected()), AffectedReports: int(reports.RowsAffected())}, nil
}

func recordHostedGraphDeletion(ctx context.Context, tx pgx.Tx, request contracts.GraphDeletionRequest, impact contracts.GraphDeletionImpact) error {
	if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_deletion_tombstones
		(tenant_id,workspace_id,canonical_kind,canonical_id,deleted_at) VALUES($1::uuid,$2::uuid,$3,$4,$5)
		ON CONFLICT(tenant_id,workspace_id,canonical_kind,canonical_id) DO UPDATE SET deleted_at=GREATEST(saas_graph_deletion_tombstones.deleted_at,excluded.deleted_at)`,
		request.Scope.TenantID, request.Scope.WorkspaceID, request.CanonicalKind, request.CanonicalID, request.DeletedAt.UTC()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO saas_graph_repair_queue
		(tenant_id,workspace_id,canonical_kind,canonical_id,affected_entities,affected_edges,affected_reports,deadline_at,state)
		VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,'queued')
		ON CONFLICT(tenant_id,workspace_id,canonical_kind,canonical_id) DO UPDATE SET
		affected_entities=excluded.affected_entities,affected_edges=excluded.affected_edges,affected_reports=excluded.affected_reports,
		deadline_at=excluded.deadline_at,state='queued'`, request.Scope.TenantID, request.Scope.WorkspaceID,
		request.CanonicalKind, request.CanonicalID, impact.AffectedEntities, impact.AffectedEdges, impact.AffectedReports, request.RepairDeadline.UTC())
	return err
}

func appendHostedGraphDeletionAudit(ctx context.Context, tx pgx.Tx, request contracts.GraphDeletionRequest, impact contracts.GraphDeletionImpact) error {
	return audit.Append(ctx, tx, audit.Event{
		TenantID: request.Scope.TenantID, ID: uuid.NewString(), OccurredAt: request.DeletedAt.UTC(), ActorType: "system", ActorID: "graph-lifecycle",
		Service: "graph-index", Operation: "graph_index.delete_evidence", Outcome: "success", RequestID: uuid.NewString(), TraceID: uuid.NewString(),
		TargetType: "workspace", TargetID: request.Scope.WorkspaceID, PolicyVersion: "graph-lifecycle-v1", ReasonCode: "canonical_deleted",
		SafeMetadata: map[string]any{"canonical_kind": request.CanonicalKind, "affected_entities": impact.AffectedEntities, "affected_edges": impact.AffectedEdges, "affected_reports": impact.AffectedReports},
	})
}
