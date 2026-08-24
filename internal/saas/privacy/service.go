// Package privacy assembles customer-visible retention and lifecycle controls.
package privacy

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type RetainedClass struct {
	DataClass       string `json:"data_class"`
	Purpose         string `json:"purpose"`
	PolicyVersion   string `json:"policy_version"`
	Owner           string `json:"owner"`
	Trigger         string `json:"trigger"`
	DeletionMethod  string `json:"deletion_method"`
	HoldBehavior    string `json:"hold_behavior"`
	CustomerImpact  string `json:"customer_impact"`
	DurationSeconds int64  `json:"duration_seconds"`
}
type AccessEvent struct {
	Operation  string    `json:"operation"`
	Outcome    string    `json:"outcome"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	ReasonCode string    `json:"reason_code"`
	OccurredAt time.Time `json:"occurred_at"`
}
type Export struct {
	ID            string     `json:"id"`
	State         string     `json:"state"`
	SafeErrorCode string     `json:"safe_error_code"`
	RequestedAt   time.Time  `json:"requested_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}
type Deletion struct {
	ID                string     `json:"id"`
	TargetType        string     `json:"target_type"`
	TargetID          string     `json:"target_id"`
	State             string     `json:"state"`
	PolicyVersion     string     `json:"policy_version"`
	SafeErrorCode     string     `json:"safe_error_code"`
	ReceiptSHA256     string     `json:"receipt_sha256"`
	RequestedAt       time.Time  `json:"requested_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	PendingSubsystems int        `json:"pending_subsystems"`
}
type Overview struct {
	RetainedClasses          []RetainedClass `json:"retained_classes"`
	SourceAccess             []AccessEvent   `json:"source_access"`
	Exports                  []Export        `json:"exports"`
	Deletions                []Deletion      `json:"deletions"`
	ConsentExpiresAt         *time.Time      `json:"consent_expires_at,omitempty"`
	RevocationExplanation    string          `json:"revocation_explanation"`
	PhysicalPurgeExplanation string          `json:"physical_purge_explanation"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("account:manage") || s == nil || s.pool == nil {
		return Overview{}, errors.New("privacy overview is unavailable")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Overview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", request.TenantID); err != nil {
		return Overview{}, err
	}
	result := Overview{RetainedClasses: []RetainedClass{}, SourceAccess: []AccessEvent{}, Exports: []Export{}, Deletions: []Deletion{}, RevocationExplanation: "Access is revoked immediately when deletion starts.", PhysicalPurgeExplanation: "Encrypted objects, derived text, indexes, queues, and caches purge asynchronously; completion appears only after every subsystem confirms deletion."}
	rows, err := tx.Query(ctx, `SELECT data_class,purpose,version,owner,retention_trigger,duration_seconds,deletion_method,hold_behavior,customer_impact FROM saas_retention_policies WHERE retired_at IS NULL ORDER BY data_class`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v RetainedClass
		if err := rows.Scan(&v.DataClass, &v.Purpose, &v.PolicyVersion, &v.Owner, &v.Trigger, &v.DurationSeconds, &v.DeletionMethod, &v.HoldBehavior, &v.CustomerImpact); err != nil {
			rows.Close()
			return result, err
		}
		result.RetainedClasses = append(result.RetainedClasses, v)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT operation,outcome,target_type,target_id,reason_code,occurred_at FROM saas_audit_events WHERE tenant_id=$1 AND operation IN ('retrieval.query','operator.source.read','source.upload_complete','source.retry_extraction') ORDER BY occurred_at DESC LIMIT 100`, request.TenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v AccessEvent
		if err := rows.Scan(&v.Operation, &v.Outcome, &v.TargetType, &v.TargetID, &v.ReasonCode, &v.OccurredAt); err != nil {
			rows.Close()
			return result, err
		}
		result.SourceAccess = append(result.SourceAccess, v)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id::text,state,safe_error_code,requested_at,expires_at FROM saas_exports WHERE tenant_id=$1 AND account_id=$2 ORDER BY requested_at DESC LIMIT 50`, request.TenantID, request.AccountID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v Export
		if err := rows.Scan(&v.ID, &v.State, &v.SafeErrorCode, &v.RequestedAt, &v.ExpiresAt); err != nil {
			rows.Close()
			return result, err
		}
		result.Exports = append(result.Exports, v)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT d.id::text,d.target_type,d.target_id::text,d.state,d.policy_version,d.safe_error_code,d.receipt_sha256,d.requested_at,d.completed_at,count(*) FILTER(WHERE c.state<>'confirmed') FROM saas_deletion_operations d LEFT JOIN saas_deletion_confirmations c ON c.tenant_id=d.tenant_id AND c.operation_id=d.id WHERE d.tenant_id=$1 GROUP BY d.tenant_id,d.id ORDER BY d.requested_at DESC LIMIT 100`, request.TenantID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v Deletion
		if err := rows.Scan(&v.ID, &v.TargetType, &v.TargetID, &v.State, &v.PolicyVersion, &v.SafeErrorCode, &v.ReceiptSHA256, &v.RequestedAt, &v.CompletedAt, &v.PendingSubsystems); err != nil {
			rows.Close()
			return result, err
		}
		result.Deletions = append(result.Deletions, v)
	}
	rows.Close()
	var expiry time.Time
	if err = tx.QueryRow(ctx, `SELECT expires_at FROM saas_attestation_receipts WHERE tenant_id=$1 AND subject_id=$2 ORDER BY accepted_at DESC LIMIT 1`, request.TenantID, request.AccountID).Scan(&expiry); err == nil {
		result.ConsentExpiresAt = &expiry
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	return result, nil
}
