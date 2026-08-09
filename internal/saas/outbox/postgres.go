package outbox

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

type DeliveryStats struct {
	Pending       int        `json:"pending"`
	Published     int        `json:"published"`
	DeadLetters   int        `json:"dead_letters"`
	Checkpoints   int        `json:"checkpoints"`
	OldestPending *time.Time `json:"oldest_pending,omitempty"`
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}
func (r *PostgresRepository) ActiveTenantIDs(ctx context.Context) ([]string, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("outbox PostgreSQL repository is not configured")
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text FROM saas_tenants WHERE state='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *PostgresRepository) Claim(ctx context.Context, tenantID string, limit int, lease time.Duration, now time.Time) ([]Event, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claim := uuid.NewString()
	rows, err := tx.Query(ctx, `WITH candidates AS (SELECT id FROM saas_outbox WHERE tenant_id=$1 AND published_at IS NULL AND dead_lettered_at IS NULL AND next_attempt_at<=$2 AND (claimed_until IS NULL OR claimed_until<=$2) ORDER BY occurred_at,id FOR UPDATE SKIP LOCKED LIMIT $3) UPDATE saas_outbox o SET claim_token=$4,claimed_until=$5 FROM candidates c WHERE o.tenant_id=$1 AND o.id=c.id RETURNING o.tenant_id::text,o.id::text,o.event_type,o.spec_version,o.aggregate_type,o.aggregate_id::text,o.occurred_at,o.claim_token::text,o.attempts`, tenantID, now, limit, claim, now.Add(lease))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.TenantID, &event.ID, &event.Type, &event.SpecVersion, &event.AggregateType, &event.AggregateID, &event.OccurredAt, &event.ClaimToken, &event.Attempts); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return events, nil
}
func (r *PostgresRepository) MarkPublished(ctx context.Context, event Event, at time.Time) error {
	tx, err := r.begin(ctx, event.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_outbox SET published_at=$4,claim_token=NULL,claimed_until=NULL,last_error_code='' WHERE tenant_id=$1 AND id=$2 AND claim_token=$3 AND published_at IS NULL`, event.TenantID, event.ID, event.ClaimToken, at)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("outbox publication claim was lost")
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) MarkFailed(ctx context.Context, event Event, code string, at time.Time) error {
	tx, err := r.begin(ctx, event.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	attempts := event.Attempts + 1
	next := at.Add(retryDelay(attempts))
	var deadLettered any
	if attempts >= MaxAttempts {
		deadLettered = at
	}
	tag, err := tx.Exec(ctx, `UPDATE saas_outbox SET attempts=$4,next_attempt_at=$5,last_error_code=$6,dead_lettered_at=$7,claim_token=NULL,claimed_until=NULL WHERE tenant_id=$1 AND id=$2 AND claim_token=$3 AND published_at IS NULL`, event.TenantID, event.ID, event.ClaimToken, attempts, next, code, deadLettered)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("outbox failure claim was lost")
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) RecordCheckpoint(ctx context.Context, tenantID, consumer, eventID string, at time.Time) (bool, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO saas_consumer_checkpoints(tenant_id,consumer_name,event_id,processed_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenantID, consumer, eventID, at)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return tag.RowsAffected() == 0, nil
}

func (r *PostgresRepository) Stats(ctx context.Context, tenantID string) (DeliveryStats, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return DeliveryStats{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result DeliveryStats
	err = tx.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE published_at IS NULL AND dead_lettered_at IS NULL),
		count(*) FILTER (WHERE published_at IS NOT NULL),
		count(*) FILTER (WHERE dead_lettered_at IS NOT NULL),
		min(occurred_at) FILTER (WHERE published_at IS NULL AND dead_lettered_at IS NULL)
		FROM saas_outbox WHERE tenant_id=$1`, tenantID).Scan(&result.Pending, &result.Published, &result.DeadLetters, &result.OldestPending)
	if err != nil {
		return DeliveryStats{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM saas_consumer_checkpoints WHERE tenant_id=$1`, tenantID).Scan(&result.Checkpoints); err != nil {
		return DeliveryStats{}, err
	}
	return result, nil
}
func (r *PostgresRepository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
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
func retryDelay(attempt int) time.Duration {
	delay := time.Second * time.Duration(1<<min(attempt-1, 8))
	return delay
}
