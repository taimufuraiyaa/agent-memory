package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

var _ contracts.GraphOperationStore = (*GraphIndexRepository)(nil)

func (r *GraphIndexRepository) GraphIndexReadiness(ctx context.Context, scope core.GraphScope, configurationID string) (contracts.GraphIndexReadiness, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return contracts.GraphIndexReadiness{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	var id, name, version, schema string
	err = tx.QueryRow(ctx, `SELECT id::text,enabled,adapter_name,adapter_version,artifact_schema_version FROM saas_graph_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND ($3='' OR id=NULLIF($3,'')::uuid) ORDER BY version DESC,id LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID).Scan(&id, &enabled, &name, &version, &schema)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.GraphIndexReadiness{State: "unconfigured", Reason: "graph configuration not found"}, contracts.ErrGraphOperationNotFound
	}
	if err != nil {
		return contracts.GraphIndexReadiness{}, err
	}
	compatible := contracts.GraphAdapterCompatible(name, version, schema)
	ready := enabled && compatible
	state, reason := "ready", ""
	reasonCode := ""
	if !enabled {
		state, reasonCode, reason = "disabled", "configuration_disabled", "graph indexing is disabled"
	} else if !ready {
		state, reasonCode, reason = "incompatible", "unsupported_adapter_contract", "configured adapter contract is not supported"
	}
	return contracts.GraphIndexReadiness{ConfigurationID: id, Ready: ready, Enabled: enabled, Compatible: compatible, State: state, AdapterName: name, AdapterVersion: version, ArtifactSchemaVersion: schema, ReasonCode: reasonCode, Reason: reason}, nil
}

func (r *GraphIndexRepository) GraphIndexStatus(ctx context.Context, scope core.GraphScope, configurationID string) (contracts.GraphIndexStatus, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return contracts.GraphIndexStatus{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status contracts.GraphIndexStatus
	err = tx.QueryRow(ctx, `SELECT id::text,version,enabled,adapter_name,adapter_version,index_method,artifact_schema_version,COALESCE(active_revision_id::text,''),COALESCE(previous_revision_id::text,'') FROM saas_graph_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND ($3='' OR id=NULLIF($3,'')::uuid) ORDER BY version DESC,id LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID).Scan(&status.ConfigurationID, &status.ConfigurationVersion, &status.Enabled, &status.AdapterName, &status.AdapterVersion, &status.IndexMethod, &status.ArtifactSchemaVersion, &status.ActiveRevisionID, &status.PreviousRevisionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, contracts.ErrGraphOperationNotFound
	}
	if err != nil {
		return status, err
	}
	configurationID = status.ConfigurationID
	status.Compatible = contracts.GraphAdapterCompatible(status.AdapterName, status.AdapterVersion, status.ArtifactSchemaVersion)
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_graph_change_journal WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND processed_revision_id IS NULL`, scope.TenantID, scope.WorkspaceID).Scan(&status.PendingChanges); err != nil {
		return status, err
	}
	status.PendingRecords = status.PendingChanges
	row := tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,workspace_id::text,configuration_id::text,revision_id::text,idempotency_key,state,attempt,lease_owner,lease_expires_at,created_at,updated_at FROM saas_graph_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND state IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID)
	if job, scanErr := scanHostedGraphJob(row); scanErr == nil {
		status.CurrentJob = &job
		status.QueueAgeSeconds = int64(time.Since(job.CreatedAt).Seconds())
		if status.QueueAgeSeconds < 0 {
			status.QueueAgeSeconds = 0
		}
	} else if !errors.Is(scanErr, pgx.ErrNoRows) {
		return status, scanErr
	}
	if status.ActiveRevisionID != "" {
		if err := tx.QueryRow(ctx, `SELECT cutoff_sequence,COALESCE(cutoff_event_time,'epoch'::timestamptz),cutoff_digest FROM saas_graph_revisions WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND id=$4::uuid`, scope.TenantID, scope.WorkspaceID, configurationID, status.ActiveRevisionID).Scan(&status.IndexedWatermark.Sequence, &status.IndexedWatermark.EventTime, &status.IndexedWatermark.Digest); err != nil {
			return status, err
		}
	}
	_ = tx.QueryRow(ctx, `SELECT id::text,state FROM saas_graph_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid ORDER BY updated_at DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID).Scan(&status.LastJobID, &status.LastJobState)
	var completed time.Time
	if err := tx.QueryRow(ctx, `SELECT updated_at FROM saas_graph_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND state='completed' ORDER BY updated_at DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID).Scan(&completed); err == nil {
		status.LastSuccessfulAt = &completed
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return status, err
	}
	status.Fresh = status.Enabled && status.Compatible && status.ActiveRevisionID != "" && status.PendingChanges == 0 && status.CurrentJob == nil
	switch {
	case !status.Enabled:
		status.State = "disabled"
	case !status.Compatible:
		status.State = "incompatible"
	case status.CurrentJob != nil:
		status.State = string(status.CurrentJob.State)
	case status.ActiveRevisionID == "":
		status.State = "not_indexed"
	case status.PendingChanges > 0:
		status.State = "stale"
	default:
		status.State = "ready"
	}
	if status.CurrentJob == nil && (status.LastJobState == core.GraphJobFailed || status.LastJobState == core.GraphJobDeadLetter || status.LastJobState == core.GraphJobCancelled) && status.PendingChanges > 0 {
		status.State = string(status.LastJobState)
	}
	contracts.PopulateGraphStatusPolicy(&status)
	return status, nil
}

func (r *GraphIndexRepository) ApplyGraphOperation(ctx context.Context, request contracts.GraphOperationRequest) (contracts.GraphOperationResult, error) {
	now := time.Now().UTC()
	result := contracts.GraphOperationResult{Action: request.Action}
	tx, err := r.begin(ctx, request.Scope)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	var active, previous string
	err = tx.QueryRow(ctx, `SELECT enabled,COALESCE(active_revision_id::text,''),COALESCE(previous_revision_id::text,'') FROM saas_graph_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid FOR UPDATE`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID).Scan(&enabled, &active, &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, contracts.ErrGraphOperationNotFound
	}
	if err != nil {
		return result, err
	}
	switch request.Action {
	case contracts.GraphOperationUpdate, contracts.GraphOperationRebuild:
		if !enabled {
			return result, contracts.ErrGraphOperationDisabled
		}
		if request.ExpectedRevision != active {
			return result, contracts.ErrGraphOperationConflict
		}
		row := tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,workspace_id::text,configuration_id::text,revision_id::text,idempotency_key,state,attempt,lease_owner,lease_expires_at,created_at,updated_at FROM saas_graph_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND idempotency_key=$4`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, request.IdempotencyKey)
		if existing, scanErr := scanHostedGraphJob(row); scanErr == nil {
			result.Accepted = true
			result.Coalesced = true
			result.Job = &existing
			result.RevisionID = existing.RevisionID
			if err := tx.Commit(ctx); err != nil {
				return result, err
			}
			result.Status, _ = r.GraphIndexStatus(ctx, request.Scope, request.ConfigurationID)
			return result, nil
		} else if !errors.Is(scanErr, pgx.ErrNoRows) {
			return result, scanErr
		}
		var busy int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_graph_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND state IN ('queued','running')`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID).Scan(&busy); err != nil {
			return result, err
		}
		if busy > 0 {
			return result, contracts.ErrGraphOperationConflict
		}
		seed := request.Scope.TenantID + "\x00" + request.Scope.WorkspaceID + "\x00" + request.ConfigurationID + "\x00" + string(request.Action) + "\x00" + request.IdempotencyKey
		revisionID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("revision\x00"+seed)).String()
		jobID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("job\x00"+seed)).String()
		base := active
		if request.Action == contracts.GraphOperationRebuild {
			base = ""
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_revisions(tenant_id,workspace_id,id,configuration_id,base_revision_id,state,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,NULLIF($5,'')::uuid,'queued',$6,$6)`, request.Scope.TenantID, request.Scope.WorkspaceID, revisionID, request.ConfigurationID, base, now); err != nil {
			return result, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_jobs(tenant_id,workspace_id,id,configuration_id,revision_id,idempotency_key,state,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'queued',$7,$7)`, request.Scope.TenantID, request.Scope.WorkspaceID, jobID, request.ConfigurationID, revisionID, request.IdempotencyKey, now); err != nil {
			return result, err
		}
		job := core.GraphJob{ID: jobID, Scope: request.Scope, ConfigurationID: request.ConfigurationID, RevisionID: revisionID, IdempotencyKey: request.IdempotencyKey, State: core.GraphJobQueued, CreatedAt: now, UpdatedAt: now}
		result.Accepted = true
		result.Job = &job
		result.RevisionID = revisionID
	case contracts.GraphOperationCancel:
		tag, err := tx.Exec(ctx, `UPDATE saas_graph_jobs SET state='cancelled',lease_owner='',lease_expires_at=NULL,updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND id=$4::uuid AND state IN ('queued','running')`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, request.JobID, now)
		if err != nil {
			return result, err
		}
		if tag.RowsAffected() != 1 {
			return result, contracts.ErrGraphOperationConflict
		}
		result.Accepted = true
	case contracts.GraphOperationRetry:
		var revisionID, key, state string
		var attempt int
		if err := tx.QueryRow(ctx, `SELECT revision_id::text,idempotency_key,state,attempt FROM saas_graph_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND id=$4::uuid`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, request.JobID).Scan(&revisionID, &key, &state, &attempt); errors.Is(err, pgx.ErrNoRows) {
			return result, contracts.ErrGraphOperationNotFound
		} else if err != nil {
			return result, err
		}
		if state != "failed" && state != "cancelled" && state != "dead_letter" {
			return result, contracts.ErrGraphOperationConflict
		}
		if request.IdempotencyKey != "" {
			key = request.IdempotencyKey
		} else {
			key = fmt.Sprintf("%s-retry-%d", key, attempt+1)
		}
		jobID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(request.JobID+"\x00"+key)).String()
		_, err := tx.Exec(ctx, `INSERT INTO saas_graph_jobs(tenant_id,workspace_id,id,configuration_id,revision_id,idempotency_key,state,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'queued',$7,$7) ON CONFLICT(tenant_id,workspace_id,configuration_id,idempotency_key) DO NOTHING`, request.Scope.TenantID, request.Scope.WorkspaceID, jobID, request.ConfigurationID, revisionID, key, now)
		if err != nil {
			return result, err
		}
		job := core.GraphJob{ID: jobID, Scope: request.Scope, ConfigurationID: request.ConfigurationID, RevisionID: revisionID, IdempotencyKey: key, State: core.GraphJobQueued, CreatedAt: now, UpdatedAt: now}
		result.Accepted = true
		result.Job = &job
		result.RevisionID = revisionID
	case contracts.GraphOperationDisable:
		if request.ExpectedRevision != active {
			return result, contracts.ErrGraphOperationConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_configurations SET enabled=false,version=version+1,updated_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, now); err != nil {
			return result, err
		}
		_, _ = tx.Exec(ctx, `UPDATE saas_graph_jobs SET state='cancelled',lease_owner='',lease_expires_at=NULL,updated_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND state IN ('queued','running')`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, now)
		result.Accepted = true
	case contracts.GraphOperationRollback:
		if request.ExpectedRevision != active || previous == "" {
			return result, contracts.ErrGraphOperationConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='previous',updated_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, request.Scope.TenantID, request.Scope.WorkspaceID, active, now); err != nil {
			return result, err
		}
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='active',previous_revision_id=$4::uuid,updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, request.Scope.TenantID, request.Scope.WorkspaceID, previous, active, now); err != nil {
			return result, err
		}
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_configurations SET active_revision_id=$4::uuid,previous_revision_id=$5::uuid,updated_at=$6 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, previous, active, now); err != nil {
			return result, err
		}
		result.Accepted = true
		result.RevisionID = previous
	default:
		return result, contracts.ErrGraphOperationInvalid
	}
	safe := map[string]any{"configuration_id": request.ConfigurationID, "revision_id": result.RevisionID, "job_id": request.JobID}
	if err := audit.Append(ctx, tx, audit.Event{TenantID: request.Scope.TenantID, ID: uuid.NewString(), OccurredAt: now, ActorType: "account", ActorID: request.Actor, Service: "graph-index", Operation: "graph_index." + string(request.Action), Outcome: "success", RequestID: request.IdempotencyKey, TraceID: request.IdempotencyKey, TargetType: "workspace", TargetID: request.Scope.WorkspaceID, PolicyVersion: "graph-operator-v1", ReasonCode: "authorized", SafeMetadata: safe}); err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Status, _ = r.GraphIndexStatus(ctx, request.Scope, request.ConfigurationID)
	return result, nil
}
