package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/outbox"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) WriteMemory(ctx context.Context, write Write) (core.MemoryEntry, bool, error) {
	if r == nil || r.pool == nil || write.TenantID == "" {
		return core.MemoryEntry{}, false, errors.New("PostgreSQL memory repository and tenant are required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.MemoryEntry{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", write.TenantID); err != nil {
		return core.MemoryEntry{}, false, err
	}
	var workspaceExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM saas_workspaces WHERE tenant_id=$1 AND id=$2 AND state='active')`, write.TenantID, write.Memory.Workspace).Scan(&workspaceExists); err != nil || !workspaceExists {
		return core.MemoryEntry{}, false, auth.ErrTenantUnavailable
	}

	existing, requestHash, found, err := findByIdempotency(ctx, tx, write.TenantID, write.Memory.Workspace, write.IdempotencyKey)
	if err != nil {
		return core.MemoryEntry{}, false, err
	}
	if found {
		if requestHash != write.RequestHash {
			return core.MemoryEntry{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}

	sourceJSON, err := json.Marshal(write.Memory.Source)
	if err != nil {
		return core.MemoryEntry{}, false, err
	}
	keywordsJSON, err := json.Marshal(write.Memory.Keywords)
	if err != nil {
		return core.MemoryEntry{}, false, err
	}
	var outcomeJSON any
	if write.Memory.Outcome != nil {
		encoded, err := json.Marshal(write.Memory.Outcome)
		if err != nil {
			return core.MemoryEntry{}, false, err
		}
		outcomeJSON = encoded
	}
	var sessionID any
	if parsed, err := uuid.Parse(write.Memory.Source.SessionID); err == nil {
		sessionID = parsed
	}
	var insertedID string
	err = tx.QueryRow(ctx, `INSERT INTO saas_memories
		(tenant_id,id,workspace_id,memory_type,content,content_hash,source_kind,source,entities,tags,keywords,outcome,
		 confidence,storage_tier,session_id,idempotency_key,request_hash,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$18)
		ON CONFLICT DO NOTHING RETURNING id::text`, write.TenantID, write.Memory.ID, write.Memory.Workspace,
		write.Memory.Type, write.Memory.Content, write.ContentHash, write.Memory.Source.Type, sourceJSON,
		write.Memory.Entities, write.Memory.Tags, keywordsJSON, outcomeJSON, write.Memory.Confidence,
		write.Memory.StorageTier, sessionID, write.IdempotencyKey, write.RequestHash, write.Memory.CreatedAt).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, found, err := findByContentHash(ctx, tx, write.TenantID, write.Memory.Workspace, write.ContentHash)
		if err != nil {
			return core.MemoryEntry{}, false, err
		}
		if !found {
			return core.MemoryEntry{}, false, errors.New("memory conflict could not be reconciled")
		}
		return existing, true, nil
	}
	if err != nil {
		return core.MemoryEntry{}, false, fmt.Errorf("insert hosted memory: %w", err)
	}
	payload, _ := json.Marshal(map[string]string{"memory_id": insertedID, "workspace_id": write.Memory.Workspace})
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox
		(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at)
		VALUES ($1,$2,'memory.created','1.0','memory',$3,$4,$5,$5)`, write.TenantID, uuid.NewString(), insertedID, payload, write.Memory.CreatedAt); err != nil {
		return core.MemoryEntry{}, false, fmt.Errorf("append memory outbox event: %w", err)
	}
	if err := outbox.AppendGraphChangeEventsTx(ctx, tx, outbox.GraphChangeInput{
		TenantID: write.TenantID, WorkspaceID: write.Memory.Workspace, SubjectKind: "memory",
		SubjectID: insertedID, SubjectFingerprint: write.ContentHash, ChangeKind: "create",
		OccurredAt: write.Memory.CreatedAt,
	}); err != nil {
		return core.MemoryEntry{}, false, fmt.Errorf("append graph change event: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_audit_events
		(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,occurred_at)
		VALUES ($1,$2,'member',$3,'memory.write','success',$4,$4,'memory',$5,$6)`, write.TenantID, uuid.NewString(), write.ActorID, write.RequestID, insertedID, write.Memory.CreatedAt); err != nil {
		return core.MemoryEntry{}, false, fmt.Errorf("append memory audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return core.MemoryEntry{}, false, err
	}
	return write.Memory, false, nil
}

func findByIdempotency(ctx context.Context, tx pgx.Tx, tenantID, workspaceID, key string) (core.MemoryEntry, string, bool, error) {
	row := tx.QueryRow(ctx, memorySelect+` WHERE tenant_id=$1 AND workspace_id=$2 AND idempotency_key=$3`, tenantID, workspaceID, key)
	memory, requestHash, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.MemoryEntry{}, "", false, nil
	}
	return memory, requestHash, err == nil, err
}

func findByContentHash(ctx context.Context, tx pgx.Tx, tenantID, workspaceID, contentHash string) (core.MemoryEntry, bool, error) {
	row := tx.QueryRow(ctx, memorySelect+` WHERE tenant_id=$1 AND workspace_id=$2 AND content_hash=$3`, tenantID, workspaceID, contentHash)
	memory, _, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.MemoryEntry{}, false, nil
	}
	return memory, err == nil, err
}

const memorySelect = `SELECT id::text, memory_type, content, workspace_id::text, source, entities, tags, keywords,
	outcome, confidence, storage_tier, created_at, updated_at, request_hash FROM saas_memories`

type rowScanner interface {
	Scan(...any) error
}

func scanMemory(row rowScanner) (core.MemoryEntry, string, error) {
	var memory core.MemoryEntry
	var sourceJSON, keywordsJSON []byte
	var outcomeJSON []byte
	var requestHash string
	err := row.Scan(&memory.ID, &memory.Type, &memory.Content, &memory.Workspace, &sourceJSON, &memory.Entities, &memory.Tags,
		&keywordsJSON, &outcomeJSON, &memory.Confidence, &memory.StorageTier, &memory.CreatedAt, &memory.UpdatedAt, &requestHash)
	if err != nil {
		return core.MemoryEntry{}, "", err
	}
	if err := json.Unmarshal(sourceJSON, &memory.Source); err != nil {
		return core.MemoryEntry{}, "", err
	}
	if err := json.Unmarshal(keywordsJSON, &memory.Keywords); err != nil {
		return core.MemoryEntry{}, "", err
	}
	if len(outcomeJSON) > 0 {
		memory.Outcome = &core.Outcome{}
		if err := json.Unmarshal(outcomeJSON, memory.Outcome); err != nil {
			return core.MemoryEntry{}, "", err
		}
	}
	return memory, requestHash, nil
}
