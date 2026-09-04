package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) GraphIndexReadiness(ctx context.Context, scope core.GraphScope, configurationID string) (contracts.GraphIndexReadiness, error) {
	var enabled bool
	var id, name, version, schema string
	err := s.db.QueryRowContext(ctx, `SELECT id,enabled,adapter_name,adapter_version,artifact_schema_version FROM graph_configurations WHERE tenant_id=? AND workspace=? AND (?='' OR id=?) ORDER BY version DESC,id LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID, configurationID).Scan(&id, &enabled, &name, &version, &schema)
	if errors.Is(err, sql.ErrNoRows) {
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

func (s *Store) GraphIndexStatus(ctx context.Context, scope core.GraphScope, configurationID string) (contracts.GraphIndexStatus, error) {
	var status contracts.GraphIndexStatus
	err := s.db.QueryRowContext(ctx, `SELECT id,version,enabled,adapter_name,adapter_version,index_method,artifact_schema_version,active_revision_id,previous_revision_id FROM graph_configurations WHERE tenant_id=? AND workspace=? AND (?='' OR id=?) ORDER BY version DESC,id LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID, configurationID).Scan(&status.ConfigurationID, &status.ConfigurationVersion, &status.Enabled, &status.AdapterName, &status.AdapterVersion, &status.IndexMethod, &status.ArtifactSchemaVersion, &status.ActiveRevisionID, &status.PreviousRevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return status, contracts.ErrGraphOperationNotFound
	}
	if err != nil {
		return status, err
	}
	configurationID = status.ConfigurationID
	status.Compatible = contracts.GraphAdapterCompatible(status.AdapterName, status.AdapterVersion, status.ArtifactSchemaVersion)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_change_journal WHERE workspace=? AND processed_revision_id=''`, scope.WorkspaceID).Scan(&status.PendingChanges); err != nil {
		return status, err
	}
	status.PendingRecords = status.PendingChanges
	row := s.db.QueryRowContext(ctx, `SELECT id, tenant_id, workspace, configuration_id, revision_id, idempotency_key, state, attempt, lease_owner, lease_expires_at, created_at, updated_at FROM graph_jobs WHERE tenant_id=? AND workspace=? AND configuration_id=? AND state IN (?,?) ORDER BY created_at DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID, core.GraphJobQueued, core.GraphJobRunning)
	if job, scanErr := scanGraphJob(row); scanErr == nil {
		status.CurrentJob = &job
		status.QueueAgeSeconds = int64(time.Since(job.CreatedAt).Seconds())
		if status.QueueAgeSeconds < 0 {
			status.QueueAgeSeconds = 0
		}
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		return status, scanErr
	}
	if status.ActiveRevisionID != "" {
		var eventTime string
		if err := s.db.QueryRowContext(ctx, `SELECT cutoff_sequence,cutoff_event_time,cutoff_digest FROM graph_revisions WHERE tenant_id=? AND workspace=? AND configuration_id=? AND id=?`, scope.TenantID, scope.WorkspaceID, configurationID, status.ActiveRevisionID).Scan(&status.IndexedWatermark.Sequence, &eventTime, &status.IndexedWatermark.Digest); err != nil {
			return status, err
		}
		if eventTime != "" {
			status.IndexedWatermark.EventTime, err = parseGraphTime(eventTime)
			if err != nil {
				return status, err
			}
		}
	}
	_ = s.db.QueryRowContext(ctx, `SELECT id,state FROM graph_jobs WHERE tenant_id=? AND workspace=? AND configuration_id=? ORDER BY updated_at DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID).Scan(&status.LastJobID, &status.LastJobState)
	var completed string
	if err := s.db.QueryRowContext(ctx, `SELECT updated_at FROM graph_jobs WHERE tenant_id=? AND workspace=? AND configuration_id=? AND state=? ORDER BY updated_at DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, configurationID, core.GraphJobCompleted).Scan(&completed); err == nil {
		parsed, parseErr := parseGraphTime(completed)
		if parseErr != nil {
			return status, parseErr
		}
		status.LastSuccessfulAt = &parsed
	} else if !errors.Is(err, sql.ErrNoRows) {
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

func (s *Store) ApplyGraphOperation(ctx context.Context, request contracts.GraphOperationRequest) (contracts.GraphOperationResult, error) {
	now := time.Now().UTC()
	var result contracts.GraphOperationResult
	result.Action = request.Action
	switch request.Action {
	case contracts.GraphOperationUpdate, contracts.GraphOperationRebuild:
		var enabled bool
		var active string
		if err := s.db.QueryRowContext(ctx, `SELECT enabled, active_revision_id FROM graph_configurations WHERE tenant_id=? AND workspace=? AND id=?`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID).Scan(&enabled, &active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return result, contracts.ErrGraphOperationNotFound
			}
			return result, err
		}
		if !enabled {
			return result, contracts.ErrGraphOperationDisabled
		}
		if request.ExpectedRevision != active {
			return result, contracts.ErrGraphOperationConflict
		}
		if existing, err := s.graphJobByIdempotency(ctx, request.Scope, request.ConfigurationID, request.IdempotencyKey); err == nil {
			result.Accepted, result.Coalesced, result.Job, result.RevisionID = true, true, &existing, existing.RevisionID
			result.Status, _ = s.GraphIndexStatus(ctx, request.Scope, request.ConfigurationID)
			return result, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		var busy int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_jobs WHERE tenant_id=? AND workspace=? AND configuration_id=? AND state IN (?,?)`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, core.GraphJobQueued, core.GraphJobRunning).Scan(&busy); err != nil {
			return result, err
		}
		if busy > 0 {
			return result, contracts.ErrGraphOperationConflict
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(request.Scope.TenantID+"\x00"+request.Scope.WorkspaceID+"\x00"+request.ConfigurationID+"\x00"+string(request.Action)+"\x00"+request.IdempotencyKey)))
		revisionID, jobID := "gr-"+digest[:24], "gj-"+digest[24:48]
		base := active
		if request.Action == contracts.GraphOperationRebuild {
			base = ""
		}
		revision := core.GraphRevision{ID: revisionID, Scope: request.Scope, ConfigurationID: request.ConfigurationID, BaseRevisionID: base, State: core.GraphRevisionQueued, Cutoff: core.GraphWatermark{EventTime: now}, CreatedAt: now, UpdatedAt: now}
		if err := s.CreateGraphRevision(ctx, revision); err != nil {
			return result, err
		}
		job := core.GraphJob{ID: jobID, Scope: request.Scope, ConfigurationID: request.ConfigurationID, RevisionID: revisionID, IdempotencyKey: request.IdempotencyKey, State: core.GraphJobQueued, CreatedAt: now, UpdatedAt: now}
		stored, created, err := s.EnqueueGraphJob(ctx, job)
		if err != nil {
			return result, err
		}
		result.Accepted, result.Coalesced, result.Job, result.RevisionID = true, !created, &stored, revisionID
	case contracts.GraphOperationCancel:
		if err := s.CancelGraphJob(ctx, request.Scope, request.JobID, now); err != nil {
			return result, contracts.ErrGraphOperationConflict
		}
		result.Accepted = true
	case contracts.GraphOperationRetry:
		var prior core.GraphJob
		row := s.db.QueryRowContext(ctx, `SELECT id, tenant_id, workspace, configuration_id, revision_id, idempotency_key, state, attempt, lease_owner, lease_expires_at, created_at, updated_at FROM graph_jobs WHERE tenant_id=? AND workspace=? AND configuration_id=? AND id=?`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, request.JobID)
		var err error
		prior, err = scanGraphJob(row)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return result, contracts.ErrGraphOperationNotFound
			}
			return result, err
		}
		if prior.State != core.GraphJobFailed && prior.State != core.GraphJobCancelled && prior.State != core.GraphJobDeadLetter {
			return result, contracts.ErrGraphOperationConflict
		}
		key := request.IdempotencyKey
		if key == "" {
			key = prior.IdempotencyKey + fmt.Sprintf("-retry-%d", prior.Attempt+1)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(prior.ID+"\x00"+key)))
		job := core.GraphJob{ID: "gj-" + digest[:24], Scope: request.Scope, ConfigurationID: request.ConfigurationID, RevisionID: prior.RevisionID, IdempotencyKey: key, State: core.GraphJobQueued, CreatedAt: now, UpdatedAt: now}
		stored, created, err := s.EnqueueGraphJob(ctx, job)
		if err != nil {
			return result, err
		}
		result.Accepted, result.Coalesced, result.Job, result.RevisionID = true, !created, &stored, prior.RevisionID
	case contracts.GraphOperationDisable:
		res, err := s.db.ExecContext(ctx, `UPDATE graph_configurations SET enabled=0, version=version+1, updated_at=? WHERE tenant_id=? AND workspace=? AND id=? AND active_revision_id=?`, formatGraphTime(now), request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, request.ExpectedRevision)
		if err != nil {
			return result, err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return result, contracts.ErrGraphOperationConflict
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE graph_jobs SET state=?, lease_owner='', lease_expires_at='', updated_at=? WHERE tenant_id=? AND workspace=? AND configuration_id=? AND state IN (?,?)`, core.GraphJobCancelled, formatGraphTime(now), request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, core.GraphJobQueued, core.GraphJobRunning)
		result.Accepted = true
	case contracts.GraphOperationRollback:
		active, previous, err := s.ActiveGraphRevisions(ctx, request.Scope, request.ConfigurationID)
		if err != nil {
			return result, err
		}
		if active != request.ExpectedRevision || previous == "" {
			return result, contracts.ErrGraphOperationConflict
		}
		if err := s.ActivateGraphRevision(ctx, core.GraphActivation{Scope: request.Scope, ConfigurationID: request.ConfigurationID, ExpectedRevision: active, CandidateRevision: previous}); err != nil {
			return result, contracts.ErrGraphOperationConflict
		}
		result.Accepted, result.RevisionID = true, previous
	}
	if _, err := s.AppendAuditEvent(ctx, AuditEventInput{Workspace: request.Scope.WorkspaceID, Operation: "graph_index." + string(request.Action), Outcome: "success", Actor: request.Actor, Source: "graph_operations", RequestID: request.IdempotencyKey, TargetType: "graph_revision", TargetIDs: []string{result.RevisionID}, Metadata: map[string]any{"configuration_id": request.ConfigurationID, "job_id": request.JobID}}); err != nil {
		return contracts.GraphOperationResult{}, err
	}
	status, err := s.GraphIndexStatus(ctx, request.Scope, request.ConfigurationID)
	if err != nil {
		return result, err
	}
	result.Status = status
	return result, nil
}
