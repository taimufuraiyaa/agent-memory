// Package operator owns least-privilege support and break-glass access.
package operator

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

var ErrAccessDenied = errors.New("operator access denied")

type SafeJob struct {
	ID, Type, State, SafeErrorCode string
	Attempts                       int
	CreatedAt                      time.Time
}
type SafeDeletion struct {
	ID, TargetType, State, PolicyVersion string
	RequestedAt                          time.Time
}
type SafeFinding struct {
	ID, RuleID, Severity, State, SummaryCode string
	LastObservedAt                           time.Time
}
type SafeLegalCase struct {
	ID, SourceID, CaseType, Jurisdiction, State, Priority string
	ValidationDueAt                                       time.Time
}
type CaseSnapshot struct {
	Jobs       []SafeJob
	Audit      []audit.Event
	Deletions  []SafeDeletion
	Findings   []SafeFinding
	LegalCases []SafeLegalCase
}
type Repository struct {
	pool  *pgxpool.Pool
	audit *audit.PostgresRepository
	now   func() time.Time
}

func NewRepository(pool *pgxpool.Pool, now func() time.Time) *Repository {
	if now == nil {
		now = time.Now
	}
	return &Repository{pool: pool, audit: audit.NewPostgresRepository(pool), now: now}
}

func (r *Repository) GrantRole(ctx context.Context, tenantID, operatorID, role, grantedBy string) error {
	if role != "support" && role != "trust" && role != "security_admin" {
		return ErrAccessDenied
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	at := r.now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO saas_operator_assignments(tenant_id,operator_id,role,state,granted_by,granted_at) VALUES($1,$2,$3,'active',$4,$5) ON CONFLICT(tenant_id,operator_id) DO UPDATE SET role=EXCLUDED.role,state='active',granted_by=EXCLUDED.granted_by,granted_at=EXCLUDED.granted_at,revoked_at=NULL`, tenantID, operatorID, role, grantedBy, at)
	if err == nil {
		err = audit.Append(ctx, tx, event(tenantID, grantedBy, "operator.role.grant", "operator", operatorID, "role_grant", map[string]any{"role": role}, at))
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) Inspect(ctx context.Context, tenantID, operatorID string) (CaseSnapshot, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return CaseSnapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = authorize(ctx, tx, tenantID, operatorID, "support", "trust", "security_admin"); err != nil {
		return CaseSnapshot{}, err
	}
	result := CaseSnapshot{Jobs: []SafeJob{}, Audit: []audit.Event{}, Deletions: []SafeDeletion{}, Findings: []SafeFinding{}, LegalCases: []SafeLegalCase{}}
	rows, err := tx.Query(ctx, `SELECT id::text,job_type,state,safe_error_code,attempts,created_at FROM saas_jobs WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v SafeJob
		if err := rows.Scan(&v.ID, &v.Type, &v.State, &v.SafeErrorCode, &v.Attempts, &v.CreatedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.Jobs = append(result.Jobs, v)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id::text,target_type,state,policy_version,requested_at FROM saas_deletion_operations WHERE tenant_id=$1 ORDER BY requested_at DESC LIMIT 100`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v SafeDeletion
		if err := rows.Scan(&v.ID, &v.TargetType, &v.State, &v.PolicyVersion, &v.RequestedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.Deletions = append(result.Deletions, v)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id::text,rule_id,severity,state,summary_code,last_observed_at FROM saas_security_findings WHERE tenant_id=$1 ORDER BY last_observed_at DESC LIMIT 100`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v SafeFinding
		if err := rows.Scan(&v.ID, &v.RuleID, &v.Severity, &v.State, &v.SummaryCode, &v.LastObservedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.Findings = append(result.Findings, v)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id::text,source_id::text,case_type,jurisdiction,state,priority,validation_due_at FROM saas_legal_cases WHERE tenant_id=$1 ORDER BY received_at DESC LIMIT 100`, tenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v SafeLegalCase
		if err := rows.Scan(&v.ID, &v.SourceID, &v.CaseType, &v.Jurisdiction, &v.State, &v.Priority, &v.ValidationDueAt); err != nil {
			rows.Close()
			return result, err
		}
		result.LegalCases = append(result.LegalCases, v)
	}
	rows.Close()
	if err = audit.Append(ctx, tx, event(tenantID, operatorID, "operator.case.inspect", "tenant", tenantID, "assigned_role", map[string]any{"job_count": len(result.Jobs), "deletion_count": len(result.Deletions), "finding_count": len(result.Findings), "legal_case_count": len(result.LegalCases)}, r.now().UTC())); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Audit, err = r.audit.Search(ctx, tenantID, audit.Filter{Limit: 100})
	return result, err
}

func (r *Repository) RequestElevation(ctx context.Context, tenantID, operatorID, sourceID, ticketRef, reason string, duration time.Duration) (string, error) {
	if strings.TrimSpace(ticketRef) == "" || strings.TrimSpace(reason) == "" || duration <= 0 || duration > time.Hour {
		return "", ErrAccessDenied
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = authorize(ctx, tx, tenantID, operatorID, "support", "trust", "security_admin"); err != nil {
		return "", err
	}
	at := r.now().UTC()
	id := uuid.NewString()
	tag, err := tx.Exec(ctx, `INSERT INTO saas_operator_elevations(tenant_id,id,operator_id,source_id,ticket_ref,reason_code,state,requested_at,expires_at) SELECT $1,$2,$3,$4,$5,$6,'requested',$7,$8 WHERE EXISTS(SELECT 1 FROM saas_sources WHERE tenant_id=$1 AND id=$4 AND state NOT IN ('deleting','deleted'))`, tenantID, id, operatorID, sourceID, ticketRef, reason, at, at.Add(duration))
	if err == nil && tag.RowsAffected() != 1 {
		return "", ErrAccessDenied
	}
	if err == nil {
		err = audit.Append(ctx, tx, event(tenantID, operatorID, "operator.elevation.request", "source", sourceID, reason, map[string]any{"ticket_ref": ticketRef, "duration_seconds": int(duration.Seconds())}, at))
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (r *Repository) ApproveElevation(ctx context.Context, tenantID, elevationID, approver string, approve bool) error {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = authorize(ctx, tx, tenantID, approver, "security_admin"); err != nil {
		return err
	}
	at := r.now().UTC()
	state := "denied"
	if approve {
		state = "approved"
	}
	tag, err := tx.Exec(ctx, `UPDATE saas_operator_elevations SET state=$3,approved_by=$4,approved_at=$5 WHERE tenant_id=$1 AND id=$2 AND state='requested' AND operator_id<>$4 AND expires_at>$5`, tenantID, elevationID, state, approver, at)
	if err != nil || tag.RowsAffected() != 1 {
		return ErrAccessDenied
	}
	err = audit.Append(ctx, tx, event(tenantID, approver, "operator.elevation."+state, "operator_elevation", elevationID, "independent_review", map[string]any{"approved": approve}, at))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) AuthorizeSourceAccess(ctx context.Context, tenantID, operatorID, sourceID string) error {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var elevationID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM saas_operator_elevations WHERE tenant_id=$1 AND operator_id=$2 AND source_id=$3 AND state='approved' AND expires_at>$4 AND revoked_at IS NULL ORDER BY expires_at DESC LIMIT 1`, tenantID, operatorID, sourceID, r.now().UTC()).Scan(&elevationID)
	if err != nil {
		return ErrAccessDenied
	}
	err = audit.Append(ctx, tx, event(tenantID, operatorID, "operator.source.read", "source", sourceID, "approved_elevation", map[string]any{"elevation_id": elevationID}, r.now().UTC()))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func authorize(ctx context.Context, tx pgx.Tx, tenantID, operatorID string, roles ...string) (string, error) {
	var role, state string
	if err := tx.QueryRow(ctx, `SELECT role,state FROM saas_operator_assignments WHERE tenant_id=$1 AND operator_id=$2`, tenantID, operatorID).Scan(&role, &state); err != nil || state != "active" {
		return "", ErrAccessDenied
	}
	for _, allowed := range roles {
		if role == allowed {
			return role, nil
		}
	}
	return "", ErrAccessDenied
}
func event(tenant, actor, operation, targetType, targetID, reason string, metadata map[string]any, at time.Time) audit.Event {
	request, trace := audit.NewRequestIDs()
	return audit.Event{TenantID: tenant, OccurredAt: at, ActorType: "operator", ActorID: actor, Service: "operator", Operation: operation, Outcome: "success", RequestID: request, TraceID: trace, TargetType: targetType, TargetID: targetID, PolicyVersion: "operator-access-v1", ReasonCode: reason, SafeMetadata: metadata}
}
func (r *Repository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if r == nil || r.pool == nil || tenantID == "" {
		return nil, ErrAccessDenied
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
