// Package backup prevents restore from resurrecting later-deleted tenant data.
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

type Record struct{ TargetType, TargetID string }
type Guard struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewGuard(pool *pgxpool.Pool, now func() time.Time) *Guard {
	if now == nil {
		now = time.Now
	}
	return &Guard{pool: pool, now: now}
}
func (g *Guard) Filter(ctx context.Context, tenantID string, backupCreatedAt time.Time, records []Record) ([]Record, error) {
	if g == nil || g.pool == nil {
		return nil, errors.New("backup restore guard is not configured")
	}
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT target_type,target_id::text FROM saas_deletion_tombstones WHERE tenant_id=$1 AND deleted_at>$2`, tenantID, backupCreatedAt)
	if err != nil {
		return nil, err
	}
	deleted := map[string]bool{}
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			rows.Close()
			return nil, err
		}
		deleted[kind+":"+id] = true
	}
	rows.Close()
	safe := make([]Record, 0, len(records))
	for _, record := range records {
		if !deleted[record.TargetType+":"+record.TargetID] {
			safe = append(safe, record)
		}
	}
	return safe, nil
}
func (g *Guard) RecordDrill(ctx context.Context, tenantID string, backupCreatedAt time.Time, input, safe []Record) error {
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		return err
	}
	at := g.now().UTC()
	exposed := 0
	safeSet := map[string]bool{}
	for _, record := range safe {
		safeSet[record.TargetType+":"+record.TargetID] = true
	}
	var tombstones int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM saas_deletion_tombstones WHERE tenant_id=$1 AND deleted_at>$2`, tenantID, backupCreatedAt).Scan(&tombstones); err != nil {
		return err
	}
	for _, record := range input {
		var deleted bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_deletion_tombstones WHERE tenant_id=$1 AND target_type=$2 AND target_id=$3 AND deleted_at>$4)`, tenantID, record.TargetType, record.TargetID, backupCreatedAt).Scan(&deleted); err != nil {
			return err
		}
		if deleted && safeSet[record.TargetType+":"+record.TargetID] {
			exposed++
		}
	}
	outcome := "passed"
	if exposed > 0 {
		outcome = "failed"
	}
	material := fmt.Sprintf("%s|%s|%d|%d|%s", tenantID, backupCreatedAt.UTC(), tombstones, exposed, outcome)
	hash := sha256.Sum256([]byte(material))
	evidence := hex.EncodeToString(hash[:])
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO saas_backup_restore_drills(tenant_id,id,backup_created_at,restored_at,tombstones_replayed,exposed_deleted_count,outcome,evidence_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenantID, id, backupCreatedAt, at, tombstones, exposed, outcome, evidence)
	if err == nil {
		request, trace := audit.NewRequestIDs()
		err = audit.Append(ctx, tx, audit.Event{TenantID: tenantID, OccurredAt: at, ActorType: "system", ActorID: "backup-restore-guard", Service: "operator", Operation: "backup.restore_drill", Outcome: outcome, RequestID: request, TraceID: trace, TargetType: "backup_drill", TargetID: id, PolicyVersion: "retention-v1", ReasonCode: "tombstones_replayed", SafeMetadata: map[string]any{"tombstones_replayed": tombstones, "exposed_deleted_count": exposed, "evidence_sha256": evidence}})
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func beginTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
