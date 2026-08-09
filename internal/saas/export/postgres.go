package export

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"time"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}
func (r *PostgresRepository) Request(ctx context.Context, request auth.RequestContext, workspaceID string, at time.Time) (Operation, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workspace any
	if workspaceID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_workspaces WHERE tenant_id=$1 AND id=$2 AND state='active')`, request.TenantID, workspaceID).Scan(&exists); err != nil || !exists {
			return Operation{}, auth.ErrTenantUnavailable
		}
		workspace = workspaceID
	}
	operation := Operation{ID: uuid.NewString(), TenantID: request.TenantID, WorkspaceID: workspaceID, State: "queued", RequestedAt: at}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_exports(tenant_id,id,account_id,workspace_id,state,requested_at) VALUES($1,$2,$3,$4,'queued',$5)`, request.TenantID, operation.ID, request.AccountID, workspace, at); err != nil {
		return Operation{}, err
	}
	payload, _ := json.Marshal(map[string]string{"export_id": operation.ID})
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,'export.requested','1.0','export',$3,$4,$5,$5)`, request.TenantID, uuid.NewString(), operation.ID, payload, at); err != nil {
		return Operation{}, err
	}
	if err := exportAudit(ctx, tx, request.TenantID, request.AccountID, request.RequestID, request.TraceID, "export.request", operation.ID, at); err != nil {
		return Operation{}, err
	}
	return operation, tx.Commit(ctx)
}
func (r *PostgresRepository) Status(ctx context.Context, request auth.RequestContext, id string) (Operation, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var operation Operation
	err = tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,COALESCE(workspace_id::text,''),state,checksum_sha256,requested_at,completed_at,expires_at
		FROM saas_exports WHERE tenant_id=$1 AND id=$2 AND account_id=$3`, request.TenantID, id, request.AccountID).Scan(
		&operation.ID, &operation.TenantID, &operation.WorkspaceID, &operation.State, &operation.ChecksumSHA256, &operation.RequestedAt, &operation.CompletedAt, &operation.ExpiresAt)
	if err != nil {
		return Operation{}, auth.ErrTenantUnavailable
	}
	return operation, nil
}
func (r *PostgresRepository) Get(ctx context.Context, request auth.RequestContext, id string, now time.Time) (Operation, string, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return Operation{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var op Operation
	var key string
	err = tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,COALESCE(workspace_id::text,''),state,checksum_sha256,requested_at,completed_at,expires_at,object_key FROM saas_exports WHERE tenant_id=$1 AND id=$2 AND account_id=$3`, request.TenantID, id, request.AccountID).Scan(&op.ID, &op.TenantID, &op.WorkspaceID, &op.State, &op.ChecksumSHA256, &op.RequestedAt, &op.CompletedAt, &op.ExpiresAt, &key)
	if err != nil || op.State != "ready" || op.ExpiresAt == nil || !now.Before(*op.ExpiresAt) {
		return Operation{}, "", auth.ErrTenantUnavailable
	}
	if err := exportAudit(ctx, tx, request.TenantID, request.AccountID, request.RequestID, request.TraceID, "export.download", id, now); err != nil {
		return Operation{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, "", err
	}
	return op, key, nil
}
func (r *PostgresRepository) ActiveTenantIDs(ctx context.Context) ([]string, error) {
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
func (r *PostgresRepository) Claim(ctx context.Context, tenantID string, at time.Time) (*Claimed, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var claim Claimed
	err = tx.QueryRow(ctx, `WITH candidate AS(SELECT id FROM saas_exports WHERE tenant_id=$1 AND state='queued' ORDER BY requested_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE saas_exports e SET state='running' FROM candidate c WHERE e.tenant_id=$1 AND e.id=c.id RETURNING e.id::text,e.tenant_id::text,e.account_id::text,COALESCE(e.workspace_id::text,''),e.state,e.requested_at`, tenantID).Scan(&claim.ID, &claim.TenantID, &claim.AccountID, &claim.WorkspaceID, &claim.State, &claim.RequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &claim, nil
}
func (r *PostgresRepository) LoadBundle(ctx context.Context, claim Claimed, at time.Time) (Bundle, error) {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return Bundle{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	bundle := Bundle{Format: "agent-memory-portable", Version: "2.0", MinReaderVersion: "2.0", ExportedAt: at, TenantID: claim.TenantID, WorkspaceID: claim.WorkspaceID, Memories: []map[string]any{}, Notes: []map[string]any{}, Sources: []map[string]any{}, SourceVersions: []map[string]any{}, Lineage: []map[string]any{}, Attestations: []map[string]any{}, Policies: []map[string]any{}, SourceBytesIncluded: false}
	workspaceFilter := claim.WorkspaceID
	memoryRows, err := tx.Query(ctx, `SELECT id::text,workspace_id::text,memory_type,content,source,entities,tags,keywords,confidence,storage_tier,created_at,updated_at FROM saas_memories WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2='' OR workspace_id::text=$2) ORDER BY created_at,id`, claim.TenantID, workspaceFilter)
	if err != nil {
		return Bundle{}, err
	}
	for memoryRows.Next() {
		var id, workspaceID, memoryType, content, storage string
		var source, keywords []byte
		var entities, tags []string
		var confidence float64
		var created, updated time.Time
		if err := memoryRows.Scan(&id, &workspaceID, &memoryType, &content, &source, &entities, &tags, &keywords, &confidence, &storage, &created, &updated); err != nil {
			return Bundle{}, err
		}
		bundle.Memories = append(bundle.Memories, map[string]any{"id": id, "workspace_id": workspaceID, "type": memoryType, "content": content, "source": json.RawMessage(source), "entities": entities, "tags": tags, "keywords": json.RawMessage(keywords), "confidence": confidence, "storage_tier": storage, "created_at": created, "updated_at": updated})
	}
	memoryRows.Close()
	noteRows, err := tx.Query(ctx, `SELECT id::text,workspace_id::text,path,title,body,properties,version,content_hash,created_at,updated_at FROM saas_notes WHERE tenant_id=$1 AND deleted_at IS NULL AND ($2='' OR workspace_id::text=$2) ORDER BY created_at,id`, claim.TenantID, workspaceFilter)
	if err != nil {
		return Bundle{}, err
	}
	for noteRows.Next() {
		var id, workspaceID, path, title, body, hash string
		var properties []byte
		var version int
		var created, updated time.Time
		if err := noteRows.Scan(&id, &workspaceID, &path, &title, &body, &properties, &version, &hash, &created, &updated); err != nil {
			return Bundle{}, err
		}
		bundle.Notes = append(bundle.Notes, map[string]any{"id": id, "workspace_id": workspaceID, "path": path, "title": title, "body": body, "properties": json.RawMessage(properties), "version": version, "content_hash": hash, "created_at": created, "updated_at": updated})
	}
	noteRows.Close()
	sourceRows, err := tx.Query(ctx, `SELECT id::text,workspace_id::text,state,rights_basis,attestation_receipt_id::text,active_version,created_at,updated_at FROM saas_sources WHERE tenant_id=$1 AND state<>'deleted' AND ($2='' OR workspace_id::text=$2) ORDER BY created_at,id`, claim.TenantID, workspaceFilter)
	if err != nil {
		return Bundle{}, err
	}
	for sourceRows.Next() {
		var id, workspaceID, state, rights, receipt string
		var version int
		var created, updated time.Time
		if err := sourceRows.Scan(&id, &workspaceID, &state, &rights, &receipt, &version, &created, &updated); err != nil {
			return Bundle{}, err
		}
		bundle.Sources = append(bundle.Sources, map[string]any{"id": id, "workspace_id": workspaceID, "state": state, "rights_basis": rights, "attestation_receipt_id": receipt, "active_version": version, "created_at": created, "updated_at": updated})
	}
	sourceRows.Close()
	versionRows, err := tx.Query(ctx, `SELECT v.source_id::text,v.version,v.content_sha256,v.media_type,v.parser_version,v.normalization_version,v.published_at,v.created_at FROM saas_source_versions v JOIN saas_sources s ON s.tenant_id=v.tenant_id AND s.id=v.source_id WHERE v.tenant_id=$1 AND ($2='' OR s.workspace_id::text=$2) ORDER BY v.source_id,v.version`, claim.TenantID, workspaceFilter)
	if err != nil {
		return Bundle{}, err
	}
	for versionRows.Next() {
		var sourceID, checksum, mediaType, parser, normalization string
		var version int
		var published *time.Time
		var created time.Time
		if err := versionRows.Scan(&sourceID, &version, &checksum, &mediaType, &parser, &normalization, &published, &created); err != nil {
			return Bundle{}, err
		}
		bundle.SourceVersions = append(bundle.SourceVersions, map[string]any{"source_id": sourceID, "version": version, "content_sha256": checksum, "media_type": mediaType, "parser_version": parser, "normalization_version": normalization, "published_at": published, "created_at": created})
	}
	versionRows.Close()
	lineageRows, err := tx.Query(ctx, `SELECT id::text,from_type,from_id::text,to_type,to_id::text,transformation,transformation_version,created_at FROM saas_lineage_edges l WHERE tenant_id=$1 AND ($2='' OR EXISTS(SELECT 1 FROM saas_memories m WHERE m.tenant_id=l.tenant_id AND m.workspace_id::text=$2 AND (m.id=l.from_id OR m.id=l.to_id)) OR EXISTS(SELECT 1 FROM saas_sources s WHERE s.tenant_id=l.tenant_id AND s.workspace_id::text=$2 AND (s.id=l.from_id OR s.id=l.to_id))) ORDER BY created_at,id`, claim.TenantID, workspaceFilter)
	if err != nil {
		return Bundle{}, err
	}
	for lineageRows.Next() {
		var id, fromType, fromID, toType, toID, transformation, version string
		var created time.Time
		if err := lineageRows.Scan(&id, &fromType, &fromID, &toType, &toID, &transformation, &version, &created); err != nil {
			return Bundle{}, err
		}
		bundle.Lineage = append(bundle.Lineage, map[string]any{"id": id, "from_type": fromType, "from_id": fromID, "to_type": toType, "to_id": toID, "transformation": transformation, "transformation_version": version, "created_at": created})
	}
	lineageRows.Close()
	receiptRows, err := tx.Query(ctx, `SELECT id::text,policy_version,statement_digest,accepted_statement_ids,accepted_at,expires_at FROM saas_attestation_receipts WHERE tenant_id=$1 ORDER BY accepted_at,id`, claim.TenantID)
	if err != nil {
		return Bundle{}, err
	}
	for receiptRows.Next() {
		var id, version, digest string
		var accepted []byte
		var acceptedAt, expires time.Time
		if err := receiptRows.Scan(&id, &version, &digest, &accepted, &acceptedAt, &expires); err != nil {
			return Bundle{}, err
		}
		bundle.Attestations = append(bundle.Attestations, map[string]any{"id": id, "policy_version": version, "statement_digest": digest, "accepted_statement_ids": json.RawMessage(accepted), "accepted_at": acceptedAt, "expires_at": expires})
	}
	receiptRows.Close()
	policyRows, err := tx.Query(ctx, `SELECT data_class,version,owner,retention_trigger,duration_seconds,deletion_method,hold_behavior FROM saas_retention_policies WHERE retired_at IS NULL ORDER BY data_class`)
	if err != nil {
		return Bundle{}, err
	}
	for policyRows.Next() {
		var class, version, owner, trigger, deletion, hold string
		var duration int64
		if err := policyRows.Scan(&class, &version, &owner, &trigger, &duration, &deletion, &hold); err != nil {
			return Bundle{}, err
		}
		bundle.Policies = append(bundle.Policies, map[string]any{"data_class": class, "version": version, "owner": owner, "trigger": trigger, "duration_seconds": duration, "deletion_method": deletion, "hold_behavior": hold})
	}
	policyRows.Close()
	return bundle, nil
}
func (r *PostgresRepository) Complete(ctx context.Context, claim Claimed, key, checksum string, completed, expires time.Time) error {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_exports SET state='ready',object_key=$3,checksum_sha256=$4,completed_at=$5,expires_at=$6 WHERE tenant_id=$1 AND id=$2 AND state='running'`, claim.TenantID, claim.ID, key, checksum, completed, expires)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.New("export claim lost")
	}
	payload, _ := json.Marshal(map[string]string{"export_id": claim.ID})
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,'export.ready','1.0','export',$3,$4,$5,$5)`, claim.TenantID, uuid.NewString(), claim.ID, payload, completed); err != nil {
		return err
	}
	if err := exportAudit(ctx, tx, claim.TenantID, "export-worker", claim.ID, claim.ID, "export.complete", claim.ID, completed); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) Fail(ctx context.Context, claim Claimed, code string, at time.Time) error {
	tx, err := r.begin(ctx, claim.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `UPDATE saas_exports SET state='failed',safe_error_code=$3,completed_at=$4 WHERE tenant_id=$1 AND id=$2 AND state='running'`, claim.TenantID, claim.ID, code, at)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *PostgresRepository) begin(ctx context.Context, tenant string) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("export repository is not configured")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenant); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func exportAudit(ctx context.Context, tx pgx.Tx, tenant, actor, requestID, correlation, operation, target string, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO saas_audit_events(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,occurred_at) VALUES($1,$2,'member',$3,$4,'success',$5,$6,'export',$7,$8)`, tenant, uuid.NewString(), actor, operation, requestID, correlation, target, at)
	return err
}
