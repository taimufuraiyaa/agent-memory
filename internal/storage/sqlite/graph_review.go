package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) ReviewGraphRecord(ctx context.Context, review core.GraphReview) error {
	if err := review.Scope.Validate(); err != nil {
		return err
	}
	if review.TargetKind != "entity" && review.TargetKind != "edge" && review.TargetKind != "report" {
		return fmt.Errorf("unsupported graph review target %q", review.TargetKind)
	}
	if strings.TrimSpace(review.ID) == "" || strings.TrimSpace(review.TargetID) == "" ||
		strings.TrimSpace(review.ReviewerID) == "" || review.ExpectedVersion < 1 {
		return fmt.Errorf("invalid graph review")
	}
	if err := core.ValidateGraphReviewAction(review); err != nil {
		return err
	}
	table := "graph_entities"
	if review.TargetKind == "edge" {
		table = "graph_edges"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current core.GraphTrustState
	var version int64
	versionColumn := "record_version"
	if review.TargetKind == "report" {
		table, versionColumn = "graph_reports", "review_version"
	}
	query := `SELECT trust, ` + versionColumn + ` FROM ` + table + ` WHERE tenant_id = ? AND workspace = ? AND id = ?`
	if err := tx.QueryRowContext(ctx, query, review.Scope.TenantID, review.Scope.WorkspaceID, review.TargetID).Scan(&current, &version); err != nil {
		return err
	}
	if current != review.From || version != review.ExpectedVersion {
		return fmt.Errorf("graph review version conflict")
	}
	update := `UPDATE ` + table + ` SET trust = ?, ` + versionColumn + ` = ` + versionColumn + ` + 1`
	args := []any{review.To, review.Scope.TenantID, review.Scope.WorkspaceID, review.TargetID, review.From, review.ExpectedVersion}
	if review.TargetKind == "report" {
		update += `, stale = 1 WHERE tenant_id = ? AND workspace = ? AND id = ? AND trust = ? AND ` + versionColumn + ` = ?`
	} else {
		update += `, updated_at = ? WHERE tenant_id = ? AND workspace = ? AND id = ? AND trust = ? AND ` + versionColumn + ` = ?`
		args = append([]any{review.To, formatGraphTime(time.Now().UTC())}, args[1:]...)
	}
	result, err := tx.ExecContext(ctx, update, args...)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return fmt.Errorf("graph review version conflict")
	}
	createdAt := review.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO graph_reviews (
		id, tenant_id, workspace, action, target_kind, target_id, from_state, to_state,
		expected_version, reason, reviewer_id, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, review.ID, review.Scope.TenantID,
		review.Scope.WorkspaceID, review.Action, review.TargetKind, review.TargetID, review.From, review.To,
		review.ExpectedVersion, review.Reason, review.ReviewerID, formatGraphTime(createdAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListQueryableGraphEdges(ctx context.Context, scope core.GraphScope) ([]core.GraphEdge, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, tenant_id, workspace, source_entity_id, target_entity_id,
		normalized_kind, external_kind, trust, first_revision_id, last_revision_id, created_at, updated_at
		FROM graph_edges e WHERE tenant_id = ? AND workspace = ? AND trust IN (?, ?, ?)
		AND EXISTS (
			SELECT 1 FROM graph_edge_versions ev
			JOIN graph_revisions r ON r.id = ev.revision_id
			JOIN graph_configurations c ON c.id = r.configuration_id AND c.tenant_id = r.tenant_id AND c.workspace = r.workspace
			WHERE ev.edge_id = e.id AND ev.revision_id = c.active_revision_id
		)
		ORDER BY id`, scope.TenantID, scope.WorkspaceID, core.GraphTrustProposed, core.GraphTrustReviewed, core.GraphTrustApproved)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []core.GraphEdge
	for rows.Next() {
		var edge core.GraphEdge
		var createdAt, updatedAt string
		if err := rows.Scan(&edge.ID, &edge.Scope.TenantID, &edge.Scope.WorkspaceID, &edge.SourceEntityID,
			&edge.TargetEntityID, &edge.NormalizedKind, &edge.ExternalKind, &edge.Trust,
			&edge.FirstRevisionID, &edge.LastRevisionID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		var parseErr error
		edge.CreatedAt, parseErr = parseGraphTime(createdAt)
		if parseErr != nil {
			return nil, parseErr
		}
		edge.UpdatedAt, parseErr = parseGraphTime(updatedAt)
		if parseErr != nil {
			return nil, parseErr
		}
		result = append(result, edge)
	}
	return result, rows.Err()
}

func (s *Store) RecordGraphFeedback(ctx context.Context, feedback core.GraphFeedback) error {
	if err := feedback.Validate(); err != nil {
		return err
	}
	createdAt := feedback.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO graph_feedback (
		id, tenant_id, workspace, request_id, target_kind, target_id, outcome, reason, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, feedback.ID, feedback.Scope.TenantID,
		feedback.Scope.WorkspaceID, feedback.RequestID, feedback.TargetKind, feedback.TargetID,
		feedback.Outcome, feedback.Reason, formatGraphTime(createdAt))
	return err
}
