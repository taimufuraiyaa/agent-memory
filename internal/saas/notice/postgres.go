// Package notice owns explicit rights-notice and repeat-abuse workflows.
package notice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

var ErrInvalidTransition = errors.New("invalid notice transition")

type Case struct {
	TenantID, ID, SourceID, Jurisdiction, State, Priority string
	ReceivedAt, ValidationDueAt                           time.Time
	ResponseDueAt, ResolutionDueAt                        *time.Time
}
type Repository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRepository(pool *pgxpool.Pool, now func() time.Time) *Repository {
	if now == nil {
		now = time.Now
	}
	return &Repository{pool: pool, now: now}
}

func (r *Repository) Intake(ctx context.Context, tenantID, operatorID, sourceID, jurisdiction, claimant string, urgent bool) (Case, error) {
	if strings.TrimSpace(jurisdiction) == "" || strings.TrimSpace(claimant) == "" {
		return Case{}, ErrInvalidTransition
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = authorizeTrust(ctx, tx, tenantID, operatorID); err != nil {
		return Case{}, err
	}
	at := r.now().UTC()
	priority := "normal"
	due := at.Add(2 * 24 * time.Hour)
	if urgent {
		priority = "urgent"
		due = at.Add(4 * time.Hour)
	}
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(claimant))))
	value := Case{TenantID: tenantID, ID: uuid.NewString(), SourceID: sourceID, Jurisdiction: jurisdiction, State: "received", Priority: priority, ReceivedAt: at, ValidationDueAt: due}
	_, err = tx.Exec(ctx, `INSERT INTO saas_legal_cases(tenant_id,id,source_id,case_type,jurisdiction,claimant_ref,state,priority,received_at,validation_due_at) SELECT $1,$2,$3,'rights_notice',$4,$5,'received',$6,$7,$8 WHERE EXISTS(SELECT 1 FROM saas_sources WHERE tenant_id=$1 AND id=$3 AND state<>'deleted')`, tenantID, value.ID, sourceID, jurisdiction, hex.EncodeToString(hash[:16]), priority, at, due)
	if err == nil {
		err = transitionAudit(ctx, tx, value, "", "received", operatorID, "notice_intake", nil, at)
	}
	if err != nil {
		return Case{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return value, nil
}

func (r *Repository) Transition(ctx context.Context, tenantID, caseID, operatorID, toState, reason string, evidence []string) (Case, error) {
	if strings.TrimSpace(reason) == "" {
		return Case{}, ErrInvalidTransition
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return Case{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = authorizeTrust(ctx, tx, tenantID, operatorID); err != nil {
		return Case{}, err
	}
	var value Case
	value.TenantID = tenantID
	value.ID = caseID
	err = tx.QueryRow(ctx, `SELECT source_id::text,jurisdiction,state,priority,received_at,validation_due_at,response_due_at,resolution_due_at FROM saas_legal_cases WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, caseID).Scan(&value.SourceID, &value.Jurisdiction, &value.State, &value.Priority, &value.ReceivedAt, &value.ValidationDueAt, &value.ResponseDueAt, &value.ResolutionDueAt)
	if err != nil {
		return Case{}, ErrInvalidTransition
	}
	if !allowed(value.State, toState) {
		return Case{}, ErrInvalidTransition
	}
	at := r.now().UTC()
	from := value.State
	value.State = toState
	var responseDue, resolutionDue any
	if toState == "user_notified" {
		due := at.Add(14 * 24 * time.Hour)
		value.ResponseDueAt = &due
		responseDue = due
	}
	if toState == "response_received" || toState == "counter_notice_received" {
		due := at.Add(14 * 24 * time.Hour)
		value.ResolutionDueAt = &due
		resolutionDue = due
	}
	closed := any(nil)
	if toState == "invalid" || toState == "restored" || toState == "deletion_requested" {
		closed = at
	}
	_, err = tx.Exec(ctx, `UPDATE saas_legal_cases SET state=$3,response_due_at=COALESCE($4,response_due_at),resolution_due_at=COALESCE($5,resolution_due_at),closed_at=$6 WHERE tenant_id=$1 AND id=$2`, tenantID, caseID, toState, responseDue, resolutionDue, closed)
	if err == nil && (toState == "source_disabled") {
		_, err = tx.Exec(ctx, `UPDATE saas_sources SET state='disabled',updated_at=$3 WHERE tenant_id=$1 AND id=$2 AND state NOT IN ('deleting','deleted')`, tenantID, value.SourceID, at)
	}
	if err == nil && toState == "restored" {
		_, err = tx.Exec(ctx, `UPDATE saas_sources SET state='ready',updated_at=$3 WHERE tenant_id=$1 AND id=$2 AND state='disabled'`, tenantID, value.SourceID, at)
	}
	if err == nil {
		err = transitionAudit(ctx, tx, value, from, toState, operatorID, reason, evidence, at)
	}
	if err != nil {
		return Case{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Case{}, err
	}
	return value, nil
}

func (r *Repository) DecideRepeatAbuse(ctx context.Context, tenantID, operatorID, accountID, decision, reason string, caseIDs []string) (string, error) {
	allowedDecision := decision == "no_action" || decision == "warning" || decision == "upload_restriction" || decision == "suspension"
	if !allowedDecision || reason == "" || len(caseIDs) == 0 {
		return "", ErrInvalidTransition
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = authorizeTrust(ctx, tx, tenantID, operatorID); err != nil {
		return "", err
	}
	ids := make([]uuid.UUID, 0, len(caseIDs))
	for _, raw := range caseIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return "", ErrInvalidTransition
		}
		ids = append(ids, id)
	}
	var valid int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM saas_legal_cases WHERE tenant_id=$1 AND id=ANY($2)`, tenantID, ids).Scan(&valid); err != nil || valid != len(ids) {
		return "", ErrInvalidTransition
	}
	at := r.now().UTC()
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO saas_repeat_abuse_decisions(tenant_id,id,account_id,case_ids,decision,reason_code,reviewed_by,review_due_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, tenantID, id, accountID, ids, decision, reason, operatorID, at.Add(30*24*time.Hour), at)
	if err == nil && decision == "suspension" {
		_, err = tx.Exec(ctx, `UPDATE saas_tenants SET state='suspended',updated_at=$2 WHERE id=$1 AND state='active'`, tenantID, at)
	}
	if err == nil && decision == "upload_restriction" {
		_, err = tx.Exec(ctx, `INSERT INTO saas_tenant_security_controls(tenant_id,uploads_quarantined_until,updated_at) VALUES($1,$2,$3) ON CONFLICT(tenant_id) DO UPDATE SET uploads_quarantined_until=EXCLUDED.uploads_quarantined_until,updated_at=EXCLUDED.updated_at`, tenantID, at.Add(30*24*time.Hour), at)
	}
	if err == nil {
		request, trace := audit.NewRequestIDs()
		err = audit.Append(ctx, tx, audit.Event{TenantID: tenantID, OccurredAt: at, ActorType: "operator", ActorID: operatorID, Service: "operator", Operation: "notice.repeat_abuse_decision", Outcome: "success", RequestID: request, TraceID: trace, TargetType: "account", TargetID: accountID, PolicyVersion: "notice-workflow-v1", ReasonCode: reason, RiskSignals: []string{"repeat_notice"}, SafeMetadata: map[string]any{"decision": decision, "case_count": len(caseIDs)}})
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func allowed(from, to string) bool {
	next := map[string]map[string]bool{"received": {"invalid": true, "validated": true}, "validated": {"source_disabled": true}, "source_disabled": {"user_notified": true}, "user_notified": {"response_received": true, "counter_notice_received": true, "deletion_requested": true}, "response_received": {"restored": true, "deletion_requested": true}, "counter_notice_received": {"restored": true, "deletion_requested": true}}
	return next[from][to]
}
func authorizeTrust(ctx context.Context, tx pgx.Tx, tenantID, operatorID string) error {
	var role, state string
	if err := tx.QueryRow(ctx, `SELECT role,state FROM saas_operator_assignments WHERE tenant_id=$1 AND operator_id=$2`, tenantID, operatorID).Scan(&role, &state); err != nil || state != "active" || (role != "trust" && role != "security_admin") {
		return ErrInvalidTransition
	}
	return nil
}
func transitionAudit(ctx context.Context, tx pgx.Tx, value Case, from, to, actor, reason string, evidence []string, at time.Time) error {
	encoded, _ := json.Marshal(evidence)
	_, err := tx.Exec(ctx, `INSERT INTO saas_legal_case_transitions(tenant_id,case_id,id,from_state,to_state,reason_code,actor_id,evidence_refs,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, value.TenantID, value.ID, uuid.NewString(), from, to, reason, actor, encoded, at)
	if err != nil {
		return err
	}
	request, trace := audit.NewRequestIDs()
	return audit.Append(ctx, tx, audit.Event{TenantID: value.TenantID, OccurredAt: at, ActorType: "operator", ActorID: actor, Service: "operator", Operation: "notice." + to, Outcome: "success", RequestID: request, TraceID: trace, TargetType: "legal_case", TargetID: value.ID, PolicyVersion: "notice-workflow-v1", ReasonCode: reason, SafeMetadata: map[string]any{"from_state": from, "to_state": to, "evidence_count": len(evidence), "priority": value.Priority}})
}
func (r *Repository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, ErrInvalidTransition
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
