package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

var (
	ErrPolicyDenied       = errors.New("security policy denied containment")
	ErrApprovalRequired   = errors.New("independent containment approval is required")
	ErrFindingUnavailable = errors.New("security finding is unavailable")
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) PutPolicy(ctx context.Context, tenantID, actor string, policy Policy, at time.Time) error {
	if !validAction(policy.Action) || !severityAtLeast(Critical, policy.MinimumSeverity) || strings.TrimSpace(policy.Version) == "" {
		return errors.New("invalid security policy")
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO saas_security_policies(tenant_id,action,enabled,minimum_severity,approval_required,policy_version,updated_by,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(tenant_id,action) DO UPDATE SET enabled=EXCLUDED.enabled,
		minimum_severity=EXCLUDED.minimum_severity,approval_required=EXCLUDED.approval_required,
		policy_version=EXCLUDED.policy_version,updated_by=EXCLUDED.updated_by,updated_at=EXCLUDED.updated_at`, tenantID, policy.Action, policy.Enabled, policy.MinimumSeverity, policy.ApprovalRequired, policy.Version, actor, at)
	if err == nil {
		err = audit.Append(ctx, tx, audit.Event{TenantID: tenantID, OccurredAt: at, ActorType: "operator", ActorID: actor, Service: "operator", Operation: "operator.security_policy.update", Outcome: "success", RequestID: uuid.NewString(), TraceID: uuid.NewString(), TargetType: "security_policy", TargetID: string(policy.Action), PolicyVersion: policy.Version, ReasonCode: "policy_change", SafeMetadata: map[string]any{"enabled": policy.Enabled, "minimum_severity": string(policy.MinimumSeverity), "approval_required": policy.ApprovalRequired}})
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) StoreFindings(ctx context.Context, tenantID string, findings []Finding, at time.Time) (int, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	created := 0
	for _, finding := range findings {
		if finding.TenantID != tenantID {
			continue
		}
		evidence, err := json.Marshal(finding.Evidence)
		if err != nil {
			return created, err
		}
		id := uuid.NewString()
		tag, err := tx.Exec(ctx, `INSERT INTO saas_security_findings(tenant_id,id,rule_id,severity,summary_code,state,evidence_refs,first_observed_at,last_observed_at)
			VALUES($1,$2,$3,$4,$5,'open',$6,$7,$8) ON CONFLICT DO NOTHING`, tenantID, id, finding.RuleID, finding.Severity, finding.SummaryCode, evidence, finding.FirstObservedAt, finding.LastObservedAt)
		if err != nil {
			return created, err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		created++
		if finding.Severity == High || finding.Severity == Critical {
			_, err = tx.Exec(ctx, `INSERT INTO saas_security_incidents(tenant_id,id,finding_id,severity,state,page_required,created_at) VALUES($1,$2,$3,$4,'open',$5,$6)`, tenantID, uuid.NewString(), id, finding.Severity, finding.Severity == Critical, at)
			if err != nil {
				return created, err
			}
		}
		if err = audit.Append(ctx, tx, audit.Event{TenantID: tenantID, OccurredAt: at, ActorType: "system", ActorID: "security-rules", Service: "operator", Operation: "security.finding.create", Outcome: "success", RequestID: uuid.NewString(), TraceID: uuid.NewString(), TargetType: "security_finding", TargetID: id, PolicyVersion: "rules-v1", ReasonCode: finding.SummaryCode, RiskSignals: []string{finding.RuleID, string(finding.Severity)}, SafeMetadata: map[string]any{"evidence_count": len(finding.Evidence)}}); err != nil {
			return created, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return created, nil
}

func (r *PostgresRepository) Contain(ctx context.Context, request ContainmentRequest, at time.Time) (string, error) {
	if !validAction(request.Action) || request.TenantID == "" || request.FindingID == "" || request.RequestedBy == "" || request.ReasonCode == "" {
		return "", errors.New("invalid containment request")
	}
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var severity Severity
	var findingState string
	if err = tx.QueryRow(ctx, `SELECT severity,state FROM saas_security_findings WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, request.TenantID, request.FindingID).Scan(&severity, &findingState); err != nil {
		return "", ErrFindingUnavailable
	}
	if findingState != "open" && findingState != "overridden" {
		return "", ErrFindingUnavailable
	}
	var policy Policy
	policy.Action = request.Action
	if err = tx.QueryRow(ctx, `SELECT enabled,minimum_severity,approval_required,policy_version FROM saas_security_policies WHERE tenant_id=$1 AND action=$2`, request.TenantID, request.Action).Scan(&policy.Enabled, &policy.MinimumSeverity, &policy.ApprovalRequired, &policy.Version); err != nil || !policy.Enabled || !severityAtLeast(severity, policy.MinimumSeverity) {
		return "", ErrPolicyDenied
	}
	if policy.ApprovalRequired && (request.ApprovedBy == "" || request.ApprovedBy == request.RequestedBy) {
		return "", ErrApprovalRequired
	}
	if err = applyContainment(ctx, tx, request, at); err != nil {
		return "", err
	}
	actionID := uuid.NewString()
	expires := nullableExpiry(request, at)
	_, err = tx.Exec(ctx, `INSERT INTO saas_containment_actions(tenant_id,id,finding_id,action,target_type,target_id,state,policy_version,requested_by,approved_by,reason_code,expires_at,created_at,executed_at)
		VALUES($1,$2,$3,$4,$5,$6,'executed',$7,$8,$9,$10,$11,$12,$12)`, request.TenantID, actionID, request.FindingID, request.Action, request.TargetType, request.TargetID, policy.Version, request.RequestedBy, request.ApprovedBy, request.ReasonCode, expires, at)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE saas_security_findings SET state='contained',last_observed_at=GREATEST(last_observed_at,$3) WHERE tenant_id=$1 AND id=$2`, request.TenantID, request.FindingID, at)
	}
	if err == nil {
		err = audit.Append(ctx, tx, audit.Event{TenantID: request.TenantID, OccurredAt: at, ActorType: "operator", ActorID: request.RequestedBy, Service: "operator", Operation: "security.containment.execute", Outcome: "success", RequestID: uuid.NewString(), TraceID: uuid.NewString(), TargetType: request.TargetType, TargetID: request.TargetID, PolicyVersion: policy.Version, ReasonCode: request.ReasonCode, RiskSignals: []string{string(request.Action), string(severity)}, SafeMetadata: map[string]any{"finding_id": request.FindingID, "approval_present": request.ApprovedBy != ""}})
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return actionID, nil
}

func (r *PostgresRepository) ReviewFinding(ctx context.Context, tenantID, findingID, reviewer, state, reason string, at time.Time) error {
	if state != "false_positive" && state != "overridden" && state != "resolved" {
		return errors.New("invalid finding review state")
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("finding review reason is required")
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_security_findings SET state=$3,reviewed_by=$4,review_reason=$5,reviewed_at=$6 WHERE tenant_id=$1 AND id=$2 AND state IN ('open','contained','overridden')`, tenantID, findingID, state, reviewer, reason, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrFindingUnavailable
	}
	err = audit.Append(ctx, tx, audit.Event{TenantID: tenantID, OccurredAt: at, ActorType: "operator", ActorID: reviewer, Service: "operator", Operation: "security.finding.review", Outcome: "success", RequestID: uuid.NewString(), TraceID: uuid.NewString(), TargetType: "security_finding", TargetID: findingID, PolicyVersion: "review-v1", ReasonCode: state, SafeMetadata: map[string]any{"review_reason_code": reason}})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func applyContainment(ctx context.Context, tx pgx.Tx, request ContainmentRequest, at time.Time) error {
	switch request.Action {
	case RateLimit:
		if request.TargetType != "tenant" || request.TargetID != request.TenantID {
			return ErrPolicyDenied
		}
		until := at.Add(request.Duration)
		if request.Duration <= 0 {
			until = at.Add(15 * time.Minute)
		}
		_, err := tx.Exec(ctx, `INSERT INTO saas_tenant_security_controls(tenant_id,rate_limited_until,updated_at) VALUES($1,$2,$3) ON CONFLICT(tenant_id) DO UPDATE SET rate_limited_until=GREATEST(saas_tenant_security_controls.rate_limited_until,EXCLUDED.rate_limited_until),updated_at=EXCLUDED.updated_at`, request.TenantID, until, at)
		return err
	case UploadQuarantine:
		if request.TargetType != "tenant" || request.TargetID != request.TenantID {
			return ErrPolicyDenied
		}
		until := at.Add(request.Duration)
		if request.Duration <= 0 {
			until = at.Add(time.Hour)
		}
		_, err := tx.Exec(ctx, `INSERT INTO saas_tenant_security_controls(tenant_id,uploads_quarantined_until,updated_at) VALUES($1,$2,$3) ON CONFLICT(tenant_id) DO UPDATE SET uploads_quarantined_until=GREATEST(saas_tenant_security_controls.uploads_quarantined_until,EXCLUDED.uploads_quarantined_until),updated_at=EXCLUDED.updated_at`, request.TenantID, until, at)
		return err
	case CredentialRevoke:
		if request.TargetType != "credential" {
			return ErrPolicyDenied
		}
		tag, err := tx.Exec(ctx, `UPDATE saas_api_credentials SET revoked_at=COALESCE(revoked_at,$3) WHERE tenant_id=$1 AND id=$2`, request.TenantID, request.TargetID, at)
		return affected(tag, err)
	case SourceDisable:
		if request.TargetType != "source" {
			return ErrPolicyDenied
		}
		tag, err := tx.Exec(ctx, `UPDATE saas_sources SET state='disabled',updated_at=$3 WHERE tenant_id=$1 AND id=$2 AND state NOT IN ('deleting','deleted')`, request.TenantID, request.TargetID, at)
		return affected(tag, err)
	case TenantSuspend:
		if request.TargetType != "tenant" || request.TargetID != request.TenantID {
			return ErrPolicyDenied
		}
		tag, err := tx.Exec(ctx, `UPDATE saas_tenants SET state='suspended',updated_at=$2 WHERE id=$1 AND state='active'`, request.TenantID, at)
		return affected(tag, err)
	default:
		return ErrPolicyDenied
	}
}

func affected(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrPolicyDenied
	}
	return nil
}
func nullableExpiry(request ContainmentRequest, at time.Time) any {
	if request.Duration <= 0 {
		return nil
	}
	return at.Add(request.Duration)
}
func validAction(action Action) bool {
	switch action {
	case RateLimit, CredentialRevoke, UploadQuarantine, SourceDisable, TenantSuspend:
		return true
	}
	return false
}
func (r *PostgresRepository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if r == nil || r.pool == nil || tenantID == "" {
		return nil, errors.New("tenant-scoped security repository is required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("set security tenant: %w", err)
	}
	return tx, nil
}
