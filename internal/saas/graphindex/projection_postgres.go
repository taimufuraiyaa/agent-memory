package graphindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type PostgresProjectionRepository struct{ pool *pgxpool.Pool }

const graphAdapterStateRebuildAge = 21 * 24 * time.Hour

func NewPostgresProjectionRepository(pool *pgxpool.Pool) *PostgresProjectionRepository {
	return &PostgresProjectionRepository{pool: pool}
}

func (r *PostgresProjectionRepository) ScheduleDueGraphWork(ctx context.Context, now time.Time) (int, error) {
	if r == nil || r.pool == nil || now.IsZero() {
		return 0, fmt.Errorf("graph projection repository is unavailable")
	}
	tenants, err := r.activeTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	scheduled := 0
	var failures []error
	for _, tenantID := range tenants {
		created, scheduleErr := r.scheduleTenant(ctx, tenantID, now.UTC())
		if scheduleErr != nil {
			failures = append(failures, scheduleErr)
		} else if created {
			scheduled++
		}
	}
	return scheduled, errors.Join(failures...)
}

func (r *PostgresProjectionRepository) scheduleTenant(ctx context.Context, tenantID string, now time.Time) (bool, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workspaceID, configurationID, projectionVersion, activeRevision string
	var deletionRequiresFull bool
	var activeRevisionCreatedAt time.Time
	var configurationVersion int64
	err = tx.QueryRow(ctx, `SELECT journal.workspace_id::text,journal.configuration_id::text,configuration.version,configuration.projection_version,COALESCE(configuration.active_revision_id::text,''),bool_or(journal.change_kind IN ('delete','purge')),COALESCE(active_revision.created_at,$2)
		FROM saas_graph_change_journal journal
		JOIN saas_graph_configurations configuration ON configuration.tenant_id=journal.tenant_id AND configuration.workspace_id=journal.workspace_id AND configuration.id=journal.configuration_id
		LEFT JOIN saas_graph_revisions active_revision ON active_revision.tenant_id=configuration.tenant_id AND active_revision.workspace_id=configuration.workspace_id AND active_revision.id=configuration.active_revision_id
		WHERE journal.tenant_id=$1::uuid AND journal.processed_revision_id IS NULL AND configuration.enabled=true
		AND NOT EXISTS (SELECT 1 FROM saas_graph_jobs job WHERE job.tenant_id=journal.tenant_id AND job.workspace_id=journal.workspace_id AND job.configuration_id=journal.configuration_id AND job.state IN ('queued','running'))
		GROUP BY journal.workspace_id,journal.configuration_id,configuration.version,configuration.projection_version,configuration.active_revision_id,active_revision.created_at
		HAVING count(*)>=50 OR min(journal.occurred_at)<=$2
		ORDER BY min(journal.occurred_at),journal.workspace_id,journal.configuration_id LIMIT 1`, tenantID, now.Add(-15*time.Minute)).Scan(&workspaceID, &configurationID, &configurationVersion, &projectionVersion, &activeRevision, &deletionRequiresFull, &activeRevisionCreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if requiresFullGraphProjection(deletionRequiresFull, activeRevision, activeRevisionCreatedAt, now) {
		activeRevision = ""
	}
	rows, err := tx.Query(ctx, `SELECT id::text,subject_fingerprint,occurred_at FROM saas_graph_change_journal WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND processed_revision_id IS NULL ORDER BY occurred_at,id LIMIT 10000 FOR UPDATE`, tenantID, workspaceID, configurationID)
	if err != nil {
		return false, err
	}
	var ids, fingerprints []string
	var eventTime time.Time
	for rows.Next() {
		var id, fingerprint string
		var occurred time.Time
		if err := rows.Scan(&id, &fingerprint, &occurred); err != nil {
			rows.Close()
			return false, err
		}
		ids, fingerprints = append(ids, id), append(fingerprints, fingerprint)
		if occurred.After(eventTime) {
			eventTime = occurred
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return false, err
	}
	sort.Strings(fingerprints)
	digest := sha256.Sum256([]byte(strings.Join(fingerprints, "\x00")))
	cutoffDigest := "sha256:" + hex.EncodeToString(digest[:])
	identity := tenantID + "\x00" + workspaceID + "\x00" + configurationID + "\x00" + fmt.Sprint(configurationVersion) + "\x00" + projectionVersion + "\x00" + cutoffDigest
	revisionID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("revision\x00"+identity)).String()
	jobID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("job\x00"+identity)).String()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_revisions(tenant_id,workspace_id,id,configuration_id,base_revision_id,state,cutoff_sequence,cutoff_event_time,cutoff_digest,created_at,updated_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,NULLIF($5,'')::uuid,'queued',$6,$7,$8,$9,$9) ON CONFLICT(tenant_id,id) DO NOTHING`, tenantID, workspaceID, revisionID, configurationID, activeRevision, len(ids), eventTime, cutoffDigest, now); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_jobs(tenant_id,workspace_id,id,configuration_id,revision_id,idempotency_key,state,created_at,updated_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'queued',$7,$7) ON CONFLICT(tenant_id,workspace_id,configuration_id,idempotency_key) DO NOTHING`, tenantID, workspaceID, jobID, configurationID, revisionID, cutoffDigest, now); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_graph_change_journal SET processed_revision_id=$4::uuid WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=ANY($3::uuid[]) AND processed_revision_id IS NULL`, tenantID, workspaceID, ids, revisionID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func requiresFullGraphProjection(deletionRequiresFull bool, activeRevision string, activeRevisionCreatedAt, now time.Time) bool {
	return deletionRequiresFull || (strings.TrimSpace(activeRevision) != "" && !activeRevisionCreatedAt.After(now.Add(-graphAdapterStateRebuildAge)))
}

func (r *PostgresProjectionRepository) ClaimGraphProjection(ctx context.Context, owner string, lease time.Duration, now time.Time) (ProjectionWork, bool, error) {
	if r == nil || r.pool == nil || strings.TrimSpace(owner) == "" || lease <= 0 {
		return ProjectionWork{}, false, fmt.Errorf("graph projection claim is invalid")
	}
	tenants, err := r.activeTenantIDs(ctx)
	if err != nil {
		return ProjectionWork{}, false, err
	}
	for _, tenantID := range tenants {
		work, claimed, claimErr := r.claimTenantProjection(ctx, tenantID, owner, lease, now.UTC())
		if claimErr != nil || claimed {
			return work, claimed, claimErr
		}
	}
	return ProjectionWork{}, false, nil
}

func (r *PostgresProjectionRepository) claimTenantProjection(ctx context.Context, tenantID, owner string, lease time.Duration, now time.Time) (ProjectionWork, bool, error) {
	tx, err := r.begin(ctx, tenantID)
	if err != nil {
		return ProjectionWork{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var work ProjectionWork
	var jobState core.GraphJobState
	var leaseExpiry *time.Time
	err = tx.QueryRow(ctx, `SELECT job.id::text,job.workspace_id::text,job.configuration_id::text,job.revision_id::text,job.idempotency_key,job.state,job.attempt,job.created_at,job.updated_at,
		COALESCE(configuration.active_revision_id::text,''),COALESCE(revision.base_revision_id::text,''),configuration.index_method,configuration.projection_version,configuration.prompt_fingerprint,configuration.model_route,
		revision.cutoff_sequence,COALESCE(revision.cutoff_event_time,job.created_at),revision.cutoff_digest
		FROM saas_graph_jobs job JOIN saas_graph_configurations configuration ON configuration.tenant_id=job.tenant_id AND configuration.workspace_id=job.workspace_id AND configuration.id=job.configuration_id
		JOIN saas_graph_revisions revision ON revision.tenant_id=job.tenant_id AND revision.workspace_id=job.workspace_id AND revision.id=job.revision_id
		WHERE job.tenant_id=$1::uuid AND configuration.enabled=true AND (job.state='queued' OR (job.state='running' AND job.lease_expires_at<=$2))
		ORDER BY job.created_at,job.id FOR UPDATE OF job SKIP LOCKED LIMIT 1`, tenantID, now).Scan(
		&work.Job.ID, &work.Job.Scope.WorkspaceID, &work.Job.ConfigurationID, &work.Job.RevisionID, &work.Job.IdempotencyKey, &jobState, &work.Job.Attempt, &work.Job.CreatedAt, &work.Job.UpdatedAt,
		&work.ExpectedRevision, &work.BaseRevisionID, &work.IndexMethod, &work.ProjectionVersion, &work.PromptFingerprint, &work.ModelRoute,
		&work.Cutoff.Sequence, &work.Cutoff.EventTime, &work.Cutoff.Digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectionWork{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return ProjectionWork{}, false, err
	}
	work.Job.Scope.TenantID = tenantID
	work.Job.State, work.Job.Attempt, work.Job.LeaseOwner = core.GraphJobRunning, work.Job.Attempt+1, owner
	expires := now.Add(lease)
	leaseExpiry = &expires
	work.Job.LeaseExpiresAt, work.Job.UpdatedAt = leaseExpiry, now
	if _, err := tx.Exec(ctx, `UPDATE saas_graph_jobs SET state='running',attempt=attempt+1,lease_owner=$4,lease_expires_at=$5,updated_at=$6 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, tenantID, work.Job.Scope.WorkspaceID, work.Job.ID, owner, expires, now); err != nil {
		return ProjectionWork{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='projecting',updated_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='queued'`, tenantID, work.Job.Scope.WorkspaceID, work.Job.RevisionID, now); err != nil {
		return ProjectionWork{}, false, err
	}
	loadRecords := func(query string, arguments ...any) error {
		rows, queryErr := tx.Query(ctx, query, arguments...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var record application.GraphProjectionRecord
			var memoryType string
			if scanErr := rows.Scan(&record.ID, &memoryType, &record.Content, &record.Fingerprint, &record.EventTime); scanErr != nil {
				return scanErr
			}
			record.Kind, record.Authorized, record.Exportable = application.GraphProjectionMemory, true, true
			work.Records = append(work.Records, record)
		}
		return rows.Err()
	}
	if work.BaseRevisionID != "" {
		err = loadRecords(`SELECT DISTINCT memory.id::text,memory.memory_type,memory.content,memory.content_hash,memory.updated_at
			FROM saas_graph_change_journal journal JOIN saas_memories memory
			ON memory.tenant_id=journal.tenant_id AND memory.workspace_id=journal.workspace_id AND memory.id::text=journal.subject_id
			WHERE journal.tenant_id=$1::uuid AND journal.workspace_id=$2::uuid AND journal.processed_revision_id=$3::uuid
			AND journal.subject_kind='memory' AND memory.deleted_at IS NULL AND memory.storage_tier<>'cold' ORDER BY memory.id`, tenantID, work.Job.Scope.WorkspaceID, work.Job.RevisionID)
	} else {
		err = loadRecords(`SELECT id::text,memory_type,content,content_hash,updated_at FROM saas_memories WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND deleted_at IS NULL AND storage_tier<>'cold' ORDER BY id`, tenantID, work.Job.Scope.WorkspaceID)
	}
	if err != nil {
		return ProjectionWork{}, false, err
	}
	// An operator-triggered update can exist without a journal-bound delta. In
	// that case, fail over to an explicit full rebuild instead of publishing an
	// empty or semantically false incremental job.
	if work.BaseRevisionID != "" && len(work.Records) == 0 {
		work.BaseRevisionID = ""
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET base_revision_id=NULL WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, tenantID, work.Job.Scope.WorkspaceID, work.Job.RevisionID); err != nil {
			return ProjectionWork{}, false, err
		}
		if err := loadRecords(`SELECT id::text,memory_type,content,content_hash,updated_at FROM saas_memories WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND deleted_at IS NULL AND storage_tier<>'cold' ORDER BY id`, tenantID, work.Job.Scope.WorkspaceID); err != nil {
			return ProjectionWork{}, false, err
		}
	}
	if work.Cutoff.Digest == "" {
		fingerprints := make([]string, 0, len(work.Records))
		for _, record := range work.Records {
			fingerprints = append(fingerprints, record.Fingerprint)
		}
		sort.Strings(fingerprints)
		digest := sha256.Sum256([]byte(strings.Join(fingerprints, "\x00")))
		work.Cutoff = core.GraphWatermark{Sequence: int64(len(work.Records)), EventTime: now, Digest: "sha256:" + hex.EncodeToString(digest[:])}
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET cutoff_sequence=$4,cutoff_event_time=$5,cutoff_digest=$6 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, tenantID, work.Job.Scope.WorkspaceID, work.Job.RevisionID, work.Cutoff.Sequence, work.Cutoff.EventTime, work.Cutoff.Digest); err != nil {
			return ProjectionWork{}, false, err
		}
	}
	return work, true, tx.Commit(ctx)
}

func (r *PostgresProjectionRepository) FinishGraphProjection(ctx context.Context, work ProjectionWork, projectionHash string, now time.Time) error {
	return r.finishProjection(ctx, work, projectionHash, "", now)
}

func (r *PostgresProjectionRepository) FailGraphProjection(ctx context.Context, work ProjectionWork, code string, now time.Time) error {
	return r.finishProjection(ctx, work, "", code, now)
}

func (r *PostgresProjectionRepository) finishProjection(ctx context.Context, work ProjectionWork, projectionHash, failureCode string, now time.Time) error {
	tx, err := r.begin(ctx, work.Job.Scope.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if failureCode == "" {
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='indexing',projection_hash=$4,updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='projecting'`, work.Job.Scope.TenantID, work.Job.Scope.WorkspaceID, work.Job.RevisionID, projectionHash, now); err != nil {
			return err
		}
	} else {
		state := "queued"
		if work.Job.Attempt >= 5 {
			state = "dead_letter"
			if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='failed',updated_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, work.Job.Scope.TenantID, work.Job.Scope.WorkspaceID, work.Job.RevisionID, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_jobs SET state=$4,failure_code=$5,lease_owner='',lease_expires_at=NULL,updated_at=$6 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, work.Job.Scope.TenantID, work.Job.Scope.WorkspaceID, work.Job.ID, state, failureCode, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *PostgresProjectionRepository) activeTenantIDs(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text FROM saas_tenants WHERE state='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *PostgresProjectionRepository) begin(ctx context.Context, tenantID string) (pgx.Tx, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
