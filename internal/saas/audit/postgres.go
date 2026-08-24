package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) ActiveTenantIDs(ctx context.Context) ([]string, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("audit PostgreSQL repository is not configured")
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text FROM saas_tenants WHERE state IN ('active','suspended') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func Append(ctx context.Context, tx pgx.Tx, event Event) error {
	if tx == nil {
		return errors.New("audit transaction is required")
	}
	if err := ValidateMetadata(event.SafeMetadata); err != nil {
		return err
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.SchemaVersion == "" {
		event.SchemaVersion = SchemaVersion
	}
	metadata, err := json.Marshal(event.SafeMetadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	if event.RiskSignals == nil {
		event.RiskSignals = []string{}
	}
	risk, err := json.Marshal(event.RiskSignals)
	if err != nil {
		return fmt.Errorf("encode audit risk signals: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO saas_audit_events(
		tenant_id,id,schema_version,occurred_at,actor_type,actor_id,credential_ref,
		session_ref,service,operation,outcome,request_id,correlation_id,trace_id,
		target_type,target_id,policy_version,reason_code,risk_signals,safe_metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		event.TenantID, event.ID, event.SchemaVersion, event.OccurredAt, event.ActorType,
		event.ActorID, event.CredentialRef, event.SessionRef, event.Service, event.Operation,
		event.Outcome, event.RequestID, event.TraceID, event.TraceID, event.TargetType,
		event.TargetID, event.PolicyVersion, event.ReasonCode, risk, metadata)
	return err
}

func (r *PostgresRepository) Search(ctx context.Context, tenantID string, filter Filter) ([]Event, error) {
	if r == nil || r.pool == nil || tenantID == "" {
		return nil, errors.New("tenant-scoped audit repository is required")
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT e.tenant_id::text,e.id::text,e.schema_version,e.occurred_at,e.received_at,
		e.actor_type,COALESCE(p.pseudonym,e.actor_id),e.credential_ref,e.session_ref,e.service,e.operation,e.outcome,e.request_id,
		trace_id,target_type,target_id,policy_version,reason_code,risk_signals,safe_metadata,
		previous_hash,event_hash FROM saas_audit_events e LEFT JOIN saas_audit_pseudonymization p ON p.tenant_id=e.tenant_id AND p.actor_id=e.actor_id
		WHERE e.tenant_id=$1 AND ($2='' OR COALESCE(p.pseudonym,e.actor_id)=$2) AND ($3='' OR e.request_id=$3)
		AND ($4='' OR e.target_id=$4) AND ($5='' OR e.operation=$5) AND ($6='' OR e.outcome=$6)
		AND ($7::timestamptz IS NULL OR e.occurred_at >= $7) AND ($8::timestamptz IS NULL OR e.occurred_at <= $8)
		ORDER BY e.occurred_at DESC,e.id DESC LIMIT $9`, tenantID, filter.ActorID, filter.RequestID,
		filter.TargetID, filter.Operation, filter.Outcome, nullableTime(filter.From), nullableTime(filter.To), filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) ClaimArchive(ctx context.Context, tenantID string, limit int, now time.Time) ([]ArchiveRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claim := uuid.NewString()
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT event_id FROM saas_audit_archive_queue WHERE tenant_id=$1 AND archived_at IS NULL
		AND next_attempt_at <= $2 AND (claimed_until IS NULL OR claimed_until <= $2)
		ORDER BY event_id FOR UPDATE SKIP LOCKED LIMIT $3)
		UPDATE saas_audit_archive_queue q SET claim_token=$4,claimed_until=$5
		FROM candidates c WHERE q.tenant_id=$1 AND q.event_id=c.event_id
		RETURNING q.event_id::text,q.attempts`, tenantID, now, limit, claim, now.Add(time.Minute))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type claimed struct {
		id       string
		attempts int
	}
	claimedEvents := []claimed{}
	for rows.Next() {
		var eventID string
		var attempts int
		if err := rows.Scan(&eventID, &attempts); err != nil {
			return nil, err
		}
		claimedEvents = append(claimedEvents, claimed{id: eventID, attempts: attempts})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	records := make([]ArchiveRecord, 0, len(claimedEvents))
	for _, claimedEvent := range claimedEvents {
		eventRows, err := tx.Query(ctx, `SELECT tenant_id::text,id::text,schema_version,occurred_at,received_at,
			actor_type,actor_id,credential_ref,session_ref,service,operation,outcome,request_id,
			trace_id,target_type,target_id,policy_version,reason_code,risk_signals,safe_metadata,
			previous_hash,event_hash FROM saas_audit_events WHERE tenant_id=$1 AND id=$2`, tenantID, claimedEvent.id)
		if err != nil || !eventRows.Next() {
			if err == nil {
				err = errors.New("claimed audit event is missing")
			}
			if eventRows != nil {
				eventRows.Close()
			}
			return nil, err
		}
		event, err := scanEvent(eventRows)
		eventRows.Close()
		if err != nil {
			return nil, err
		}
		records = append(records, ArchiveRecord{Event: event, ClaimToken: claim, Attempts: claimedEvent.attempts})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return records, nil
}

func (r *PostgresRepository) MarkArchived(ctx context.Context, record ArchiveRecord, key, checksum string, at time.Time) error {
	tx, err := r.begin(ctx, record.Event.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_audit_archive_queue SET archived_at=$4,archive_key=$5,
		archive_sha256=$6,claim_token=NULL,claimed_until=NULL WHERE tenant_id=$1 AND event_id=$2
		AND claim_token=$3 AND archived_at IS NULL`, record.Event.TenantID, record.Event.ID,
		record.ClaimToken, at, key, checksum)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("audit archive claim was lost")
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) MarkArchiveFailed(ctx context.Context, record ArchiveRecord, code string, at time.Time) error {
	tx, err := r.begin(ctx, record.Event.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	attempts := record.Attempts + 1
	tag, err := tx.Exec(ctx, `UPDATE saas_audit_archive_queue SET attempts=$4,next_attempt_at=$5,
		last_error_code=$6,claim_token=NULL,claimed_until=NULL WHERE tenant_id=$1 AND event_id=$2
		AND claim_token=$3 AND archived_at IS NULL`, record.Event.TenantID, record.Event.ID,
		record.ClaimToken, attempts, at.Add(time.Duration(min(attempts, 10))*time.Minute), code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("audit archive claim was lost")
	}
	return tx.Commit(ctx)
}

type rowScanner interface{ Scan(...any) error }

func scanEvent(row rowScanner) (Event, error) {
	var event Event
	var riskJSON, metadataJSON []byte
	err := row.Scan(&event.TenantID, &event.ID, &event.SchemaVersion, &event.OccurredAt,
		&event.ReceivedAt, &event.ActorType, &event.ActorID, &event.CredentialRef,
		&event.SessionRef, &event.Service, &event.Operation, &event.Outcome, &event.RequestID,
		&event.TraceID, &event.TargetType, &event.TargetID, &event.PolicyVersion,
		&event.ReasonCode, &riskJSON, &metadataJSON, &event.PreviousHash, &event.EventHash)
	if err == nil {
		err = json.Unmarshal(riskJSON, &event.RiskSignals)
	}
	if err == nil {
		err = json.Unmarshal(metadataJSON, &event.SafeMetadata)
	}
	return event, err
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (r *PostgresRepository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if r == nil || r.pool == nil || tenantID == "" {
		return nil, errors.New("tenant-scoped audit repository is required")
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
