package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

func (r *PostgresRepository) workflowTx(ctx context.Context, tenantID string) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("workflow store is not configured")
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

func (r *PostgresRepository) CreateNote(ctx context.Context, request auth.RequestContext, command NoteCreate, at time.Time) (*core.Note, bool, error) {
	tx, err := r.workflowTx(ctx, request.TenantID)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existing core.Note
	err = tx.QueryRow(ctx, `SELECT id::text,workspace_id::text,path,title,body,properties,version,content_hash,index_state,indexed_version,index_error,created_at,updated_at,deleted_at FROM saas_notes WHERE tenant_id=$1 AND workspace_id=$2 AND idempotency_key=$3`, request.TenantID, command.Input.Workspace, command.IdempotencyKey).Scan(&existing.ID, &existing.Workspace, &existing.Path, &existing.Title, &existing.Body, &existing.Properties, &existing.Revision, &existing.ContentHash, &existing.IndexState, &existing.IndexedRevision, &existing.IndexError, &existing.CreatedAt, &existing.UpdatedAt, &existing.DeletedAt)
	if err == nil {
		return &existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var workspace bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_workspaces WHERE tenant_id=$1 AND id=$2 AND state='active')`, request.TenantID, command.Input.Workspace).Scan(&workspace); err != nil || !workspace {
		return nil, false, auth.ErrTenantUnavailable
	}
	note := &core.Note{ID: uuid.NewString(), Workspace: command.Input.Workspace, Path: command.Input.Path, Title: command.Input.Title, Body: command.Input.Body, Properties: command.Input.Properties, Revision: 1, ContentHash: textHash(command.Input.Body), IndexState: core.NoteIndexPending, CreatedAt: at, UpdatedAt: at}
	if note.Properties == nil {
		note.Properties = map[string]any{}
	}
	properties, _ := json.Marshal(note.Properties)
	if _, err := tx.Exec(ctx, `INSERT INTO saas_notes(tenant_id,id,workspace_id,path,title,body,properties,version,content_hash,index_state,idempotency_key,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,'pending',$9,$10,$10)`, request.TenantID, note.ID, note.Workspace, note.Path, note.Title, note.Body, properties, note.ContentHash, command.IdempotencyKey, at); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_note_revisions(tenant_id,note_id,version,path,title,body,properties,content_hash,author_kind,actor_id,request_id,created_at) VALUES($1,$2,1,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, request.TenantID, note.ID, note.Path, note.Title, note.Body, properties, note.ContentHash, author(command.Input.AuthorKind), request.AccountID, request.RequestID, at); err != nil {
		return nil, false, err
	}
	if err := workflowEvents(ctx, tx, request, "note.created", "note.create", "note", note.ID, note.Workspace, at); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return note, false, nil
}

func (r *PostgresRepository) UpdateNote(ctx context.Context, request auth.RequestContext, command NoteUpdate, at time.Time) (*core.Note, bool, error) {
	tx, err := r.workflowTx(ctx, request.TenantID)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current int
	var workspace string
	if err := tx.QueryRow(ctx, `SELECT version,workspace_id::text FROM saas_notes WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE`, request.TenantID, command.Input.NoteID).Scan(&current, &workspace); err != nil {
		return nil, false, auth.ErrTenantUnavailable
	}
	if current == command.Input.ExpectedRevision+1 {
		var duplicate bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_note_revisions WHERE tenant_id=$1 AND note_id=$2 AND version=$3 AND request_id=$4)`, request.TenantID, command.Input.NoteID, current, request.RequestID).Scan(&duplicate)
		if duplicate {
			note, err := loadNote(ctx, tx, request.TenantID, command.Input.NoteID)
			return note, true, err
		}
	}
	if current != command.Input.ExpectedRevision {
		return nil, false, errors.New("note revision conflict")
	}
	version := current + 1
	title := core.NormalizeNoteTitle(command.Input.Title, command.Input.Path)
	hash := textHash(command.Input.Body)
	properties := command.Input.Properties
	if properties == nil {
		properties = map[string]any{}
	}
	encoded, _ := json.Marshal(properties)
	if _, err := tx.Exec(ctx, `UPDATE saas_notes SET path=$3,title=$4,body=$5,properties=$6,version=$7,content_hash=$8,index_state='pending',index_error='',updated_at=$9 WHERE tenant_id=$1 AND id=$2`, request.TenantID, command.Input.NoteID, command.Input.Path, title, command.Input.Body, encoded, version, hash, at); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_note_revisions(tenant_id,note_id,version,path,title,body,properties,content_hash,author_kind,actor_id,request_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, request.TenantID, command.Input.NoteID, version, command.Input.Path, title, command.Input.Body, encoded, hash, author(command.Input.AuthorKind), request.AccountID, request.RequestID, at); err != nil {
		return nil, false, err
	}
	if err := workflowEvents(ctx, tx, request, "note.updated", "note.update", "note", command.Input.NoteID, workspace, at); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	note, err := loadNoteOutside(ctx, r, request.TenantID, command.Input.NoteID)
	return note, false, err
}

func (r *PostgresRepository) RecordFeedback(ctx context.Context, request auth.RequestContext, command FeedbackCommand, at time.Time) (bool, error) {
	tx, err := r.workflowTx(ctx, request.TenantID)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `INSERT INTO saas_feedback(tenant_id,id,memory_id,request_id,outcome,reason_category,occurred_at) SELECT $1,$2,id,$3,$4,$5,$6 FROM saas_memories WHERE tenant_id=$1 AND id=$7 ON CONFLICT(tenant_id,memory_id,request_id) DO NOTHING`, request.TenantID, uuid.NewString(), command.RequestID, command.Outcome, command.ReasonCategory, at, command.MemoryID)
	if err != nil || result.RowsAffected() == 0 {
		if err == nil {
			return true, nil
		}
		return false, err
	}
	if err := workflowEvents(ctx, tx, request, "memory.feedback", "memory.feedback", "memory", command.MemoryID, "", at); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (r *PostgresRepository) EndSession(ctx context.Context, request auth.RequestContext, command SessionEndCommand, transcriptHash string, at time.Time) (bool, error) {
	tx, err := r.workflowTx(ctx, request.TenantID)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existingHash string
	err = tx.QueryRow(ctx, `SELECT transcript_hash FROM saas_sessions_memory WHERE tenant_id=$1 AND workspace_id=$2 AND idempotency_key=$3`, request.TenantID, command.WorkspaceID, command.IdempotencyKey).Scan(&existingHash)
	if err == nil {
		if existingHash != transcriptHash {
			return false, ErrIdempotencyConflict
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_sessions_memory(tenant_id,id,workspace_id,state,started_at,completed_at,idempotency_key,transcript_hash) SELECT $1,$2,id,'completed',$3,$3,$4,$5 FROM saas_workspaces WHERE tenant_id=$1 AND id=$6 AND state='active'`, request.TenantID, command.SessionID, at, command.IdempotencyKey, transcriptHash, command.WorkspaceID); err != nil {
		return false, err
	}
	if err := workflowEvents(ctx, tx, request, "session.completed", "session.end", "session", command.SessionID, command.WorkspaceID, at); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func workflowEvents(ctx context.Context, tx pgx.Tx, request auth.RequestContext, eventType, operation, targetType, targetID, workspaceID string, at time.Time) error {
	payload, _ := json.Marshal(map[string]string{"id": targetID, "workspace_id": workspaceID})
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,'1.0',$4,$5,$6,$7,$7)`, request.TenantID, uuid.NewString(), eventType, targetType, targetID, payload, at); err != nil {
		return fmt.Errorf("append workflow outbox: %w", err)
	}
	_, err := tx.Exec(ctx, `INSERT INTO saas_audit_events(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,occurred_at) VALUES($1,$2,'member',$3,$4,'success',$5,$6,$7,$8,$9)`, request.TenantID, uuid.NewString(), request.AccountID, operation, request.RequestID, request.TraceID, targetType, targetID, at)
	return err
}
func textHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func author(value string) string {
	if value == "" {
		return "member"
	}
	return value
}
func loadNote(ctx context.Context, tx pgx.Tx, tenantID, noteID string) (*core.Note, error) {
	var n core.Note
	err := tx.QueryRow(ctx, `SELECT id::text,workspace_id::text,path,title,body,properties,version,content_hash,index_state,indexed_version,index_error,created_at,updated_at,deleted_at FROM saas_notes WHERE tenant_id=$1 AND id=$2`, tenantID, noteID).Scan(&n.ID, &n.Workspace, &n.Path, &n.Title, &n.Body, &n.Properties, &n.Revision, &n.ContentHash, &n.IndexState, &n.IndexedRevision, &n.IndexError, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt)
	return &n, err
}
func loadNoteOutside(ctx context.Context, r *PostgresRepository, tenantID, noteID string) (*core.Note, error) {
	tx, err := r.workflowTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return loadNote(ctx, tx, tenantID, noteID)
}
