package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var (
	ErrGraphRevisionConflict = errors.New("graph revision activation conflict")
	ErrGraphRevisionNotReady = errors.New("graph revision is not ready")
	ErrGraphScopeConflict    = errors.New("graph identity belongs to a different scope")
)

type GraphChangeRecord struct {
	ID                   string
	WorkspaceID          string
	SubjectKind          string
	SubjectID            string
	SubjectFingerprint   string
	ProjectionVersion    string
	ConfigurationVersion string
	ChangeKind           string
	OccurredAt           time.Time
	ProcessedRevisionID  string
}

func (s *Store) UpsertGraphConfiguration(ctx context.Context, configuration core.GraphConfiguration) error {
	if err := configuration.Validate(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO graph_configurations (
		id, tenant_id, workspace, version, enabled, adapter_name, adapter_version, index_method,
		projection_version, artifact_schema_version, prompt_fingerprint, model_route, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		tenant_id = excluded.tenant_id, workspace = excluded.workspace, version = excluded.version,
		enabled = excluded.enabled, adapter_name = excluded.adapter_name, adapter_version = excluded.adapter_version,
		index_method = excluded.index_method, projection_version = excluded.projection_version,
		artifact_schema_version = excluded.artifact_schema_version, prompt_fingerprint = excluded.prompt_fingerprint,
		model_route = excluded.model_route, updated_at = excluded.updated_at
	WHERE graph_configurations.tenant_id = excluded.tenant_id AND graph_configurations.workspace = excluded.workspace`,
		configuration.ID, configuration.Scope.TenantID, configuration.Scope.WorkspaceID, configuration.Version,
		configuration.Enabled, configuration.AdapterName, configuration.AdapterVersion, configuration.IndexMethod,
		configuration.ProjectionVersion, configuration.ArtifactSchemaVersion, configuration.PromptFingerprint,
		configuration.ModelRoute, formatGraphTime(configuration.CreatedAt), formatGraphTime(configuration.UpdatedAt))
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrGraphScopeConflict
	}
	return nil
}

func (s *Store) CreateGraphRevision(ctx context.Context, revision core.GraphRevision) error {
	if err := revision.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO graph_revisions (
		id, tenant_id, workspace, configuration_id, base_revision_id, state,
		cutoff_sequence, cutoff_event_time, cutoff_digest, projection_hash, artifact_hash,
		previous_revision_id, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.ID, revision.Scope.TenantID, revision.Scope.WorkspaceID, revision.ConfigurationID,
		revision.BaseRevisionID, revision.State, revision.Cutoff.Sequence, formatGraphTime(revision.Cutoff.EventTime),
		revision.Cutoff.Digest, revision.ProjectionHash, revision.ArtifactHash, revision.PreviousRevisionID,
		formatGraphTime(revision.CreatedAt), formatGraphTime(revision.UpdatedAt))
	return err
}

func (s *Store) EnqueueGraphJob(ctx context.Context, job core.GraphJob) (core.GraphJob, bool, error) {
	if err := validateGraphJob(job); err != nil {
		return core.GraphJob{}, false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO graph_jobs (
		id, tenant_id, workspace, configuration_id, revision_id, idempotency_key, state,
		attempt, lease_owner, lease_expires_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(workspace, configuration_id, idempotency_key) DO NOTHING`,
		job.ID, job.Scope.TenantID, job.Scope.WorkspaceID, job.ConfigurationID, job.RevisionID,
		job.IdempotencyKey, job.State, job.Attempt, job.LeaseOwner, formatOptionalGraphTime(job.LeaseExpiresAt),
		formatGraphTime(job.CreatedAt), formatGraphTime(job.UpdatedAt))
	if err != nil {
		return core.GraphJob{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return core.GraphJob{}, false, err
	}
	stored, err := s.graphJobByIdempotency(ctx, job.Scope, job.ConfigurationID, job.IdempotencyKey)
	return stored, rows == 1, err
}

func (s *Store) ClaimGraphJobs(ctx context.Context, scope core.GraphScope, owner string, limit int, lease time.Duration, now time.Time) ([]core.GraphJob, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(owner) == "" || limit < 1 || limit > 100 || lease <= 0 {
		return nil, fmt.Errorf("invalid graph job claim")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM graph_jobs
		WHERE tenant_id = ? AND workspace = ? AND
		(state = ? OR (state = ? AND lease_expires_at != '' AND lease_expires_at <= ?))
		ORDER BY created_at, id LIMIT ?`, scope.TenantID, scope.WorkspaceID, core.GraphJobQueued,
		core.GraphJobRunning, formatGraphTime(now), limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	claimed := make([]core.GraphJob, 0, len(ids))
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE graph_jobs SET state = ?, attempt = attempt + 1,
			lease_owner = ?, lease_expires_at = ?, updated_at = ? WHERE tenant_id = ? AND workspace = ? AND id = ? AND
			(state = ? OR (state = ? AND lease_expires_at != '' AND lease_expires_at <= ?))`, core.GraphJobRunning,
			owner, formatGraphTime(now.Add(lease)), formatGraphTime(now), scope.TenantID, scope.WorkspaceID, id,
			core.GraphJobQueued, core.GraphJobRunning, formatGraphTime(now))
		if err != nil {
			return nil, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if updated != 1 {
			continue
		}
		row := tx.QueryRowContext(ctx, `SELECT id, tenant_id, workspace, configuration_id, revision_id,
			idempotency_key, state, attempt, lease_owner, lease_expires_at, created_at, updated_at
			FROM graph_jobs WHERE tenant_id = ? AND workspace = ? AND id = ?`, scope.TenantID, scope.WorkspaceID, id)
		job, err := scanGraphJob(row)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, job)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) CancelGraphJob(ctx context.Context, scope core.GraphScope, jobID string, now time.Time) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE graph_jobs SET state = ?, lease_owner = '', lease_expires_at = '', updated_at = ?
		WHERE tenant_id = ? AND workspace = ? AND id = ? AND state IN (?, ?)`, core.GraphJobCancelled,
		formatGraphTime(now), scope.TenantID, scope.WorkspaceID, jobID, core.GraphJobQueued, core.GraphJobRunning)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("graph job cannot be cancelled")
	}
	return nil
}

func (s *Store) DeleteGraphWorkspace(ctx context.Context, scope core.GraphScope) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`DELETE FROM graph_feedback WHERE tenant_id = ? AND workspace = ?`,
		`DELETE FROM graph_reviews WHERE tenant_id = ? AND workspace = ?`,
		`DELETE FROM graph_reports WHERE tenant_id = ? AND workspace = ?`,
		`DELETE FROM graph_communities WHERE tenant_id = ? AND workspace = ?`,
		`DELETE FROM graph_edges WHERE tenant_id = ? AND workspace = ?`,
		`DELETE FROM graph_entities WHERE tenant_id = ? AND workspace = ?`,
		`DELETE FROM graph_configurations WHERE tenant_id = ? AND workspace = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, scope.TenantID, scope.WorkspaceID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) graphJobByIdempotency(ctx context.Context, scope core.GraphScope, configurationID, key string) (core.GraphJob, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, tenant_id, workspace, configuration_id, revision_id,
		idempotency_key, state, attempt, lease_owner, lease_expires_at, created_at, updated_at
		FROM graph_jobs WHERE tenant_id = ? AND workspace = ? AND configuration_id = ? AND idempotency_key = ?`,
		scope.TenantID, scope.WorkspaceID, configurationID, key)
	return scanGraphJob(row)
}

func (s *Store) ActivateGraphRevision(ctx context.Context, activation core.GraphActivation) error {
	if err := activation.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var active string
	if err := tx.QueryRowContext(ctx, `SELECT active_revision_id FROM graph_configurations
		WHERE tenant_id = ? AND workspace = ? AND id = ?`, activation.Scope.TenantID,
		activation.Scope.WorkspaceID, activation.ConfigurationID).Scan(&active); err != nil {
		return err
	}
	if active != activation.ExpectedRevision {
		return ErrGraphRevisionConflict
	}

	var candidateState core.GraphRevisionState
	if err := tx.QueryRowContext(ctx, `SELECT state FROM graph_revisions
		WHERE tenant_id = ? AND workspace = ? AND configuration_id = ? AND id = ?`, activation.Scope.TenantID,
		activation.Scope.WorkspaceID, activation.ConfigurationID, activation.CandidateRevision).Scan(&candidateState); err != nil {
		return err
	}
	if candidateState != core.GraphRevisionReady && candidateState != core.GraphRevisionPrevious {
		return ErrGraphRevisionNotReady
	}

	if active != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE graph_revisions SET state = ?, updated_at = ?
			WHERE tenant_id = ? AND workspace = ? AND configuration_id = ? AND id = ? AND state = ?`,
			core.GraphRevisionPrevious, formatGraphTime(time.Now().UTC()), activation.Scope.TenantID,
			activation.Scope.WorkspaceID, activation.ConfigurationID, active, core.GraphRevisionActive); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE graph_revisions SET state = ?, previous_revision_id = ?, updated_at = ?
		WHERE tenant_id = ? AND workspace = ? AND configuration_id = ? AND id = ?`, core.GraphRevisionActive,
		active, formatGraphTime(time.Now().UTC()), activation.Scope.TenantID, activation.Scope.WorkspaceID,
		activation.ConfigurationID, activation.CandidateRevision); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE graph_configurations
		SET previous_revision_id = active_revision_id, active_revision_id = ?, updated_at = ?
		WHERE tenant_id = ? AND workspace = ? AND id = ? AND active_revision_id = ?`,
		activation.CandidateRevision, formatGraphTime(time.Now().UTC()), activation.Scope.TenantID,
		activation.Scope.WorkspaceID, activation.ConfigurationID, activation.ExpectedRevision)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrGraphRevisionConflict
	}
	return tx.Commit()
}

func (s *Store) ActiveGraphRevisions(ctx context.Context, scope core.GraphScope, configurationID string) (string, string, error) {
	if err := scope.Validate(); err != nil {
		return "", "", err
	}
	var active, previous string
	err := s.db.QueryRowContext(ctx, `SELECT active_revision_id, previous_revision_id FROM graph_configurations
		WHERE tenant_id = ? AND workspace = ? AND id = ?`, scope.TenantID, scope.WorkspaceID, configurationID).Scan(&active, &previous)
	return active, previous, err
}

func (s *Store) AppendGraphChange(ctx context.Context, change GraphChangeRecord) (bool, error) {
	if err := validateGraphChange(change); err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO graph_change_journal (
		id, workspace, subject_kind, subject_id, subject_fingerprint, projection_version,
		configuration_version, change_kind, occurred_at, processed_revision_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(workspace, subject_kind, subject_id, subject_fingerprint, projection_version, configuration_version) DO NOTHING`,
		change.ID, change.WorkspaceID, change.SubjectKind, change.SubjectID, change.SubjectFingerprint,
		change.ProjectionVersion, change.ConfigurationVersion, change.ChangeKind,
		formatGraphTime(change.OccurredAt), change.ProcessedRevisionID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func validateGraphJob(job core.GraphJob) error {
	if err := job.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.ConfigurationID) == "" ||
		strings.TrimSpace(job.RevisionID) == "" || strings.TrimSpace(job.IdempotencyKey) == "" {
		return fmt.Errorf("invalid graph job identity")
	}
	if job.State == "" {
		return fmt.Errorf("invalid graph job state")
	}
	return nil
}

func validateGraphChange(change GraphChangeRecord) error {
	for _, value := range []string{change.ID, change.WorkspaceID, change.SubjectKind, change.SubjectID,
		change.SubjectFingerprint, change.ProjectionVersion, change.ConfigurationVersion, change.ChangeKind} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("invalid graph change: required identity is empty")
		}
	}
	if change.OccurredAt.IsZero() {
		return fmt.Errorf("invalid graph change: occurred_at is required")
	}
	return nil
}

type graphRowScanner interface {
	Scan(...any) error
}

func scanGraphJob(row graphRowScanner) (core.GraphJob, error) {
	var job core.GraphJob
	var state string
	var leaseExpires, createdAt, updatedAt string
	err := row.Scan(&job.ID, &job.Scope.TenantID, &job.Scope.WorkspaceID, &job.ConfigurationID,
		&job.RevisionID, &job.IdempotencyKey, &state, &job.Attempt, &job.LeaseOwner,
		&leaseExpires, &createdAt, &updatedAt)
	if err != nil {
		return core.GraphJob{}, err
	}
	job.State = core.GraphJobState(state)
	job.CreatedAt, err = parseGraphTime(createdAt)
	if err != nil {
		return core.GraphJob{}, err
	}
	job.UpdatedAt, err = parseGraphTime(updatedAt)
	if err != nil {
		return core.GraphJob{}, err
	}
	if leaseExpires != "" {
		parsed, parseErr := parseGraphTime(leaseExpires)
		if parseErr != nil {
			return core.GraphJob{}, parseErr
		}
		job.LeaseExpiresAt = &parsed
	}
	return job, nil
}

func formatGraphTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalGraphTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatGraphTime(*value)
}

func parseGraphTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
