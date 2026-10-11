package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// NativeGraphWork binds one consistent canonical projection to the precise
// journal records it may acknowledge. Later writes are outside this cutoff.
type NativeGraphWork struct {
	Configuration    core.GraphConfiguration
	ActiveRevisionID string
	JournalIDs       []string
	ChangedIDs       map[string]bool
	Rebuild          bool
	OldestChange     time.Time
	Watermark        core.GraphWatermark
	Memories         []core.MemoryEntry
}

// AlignNativeGraphPrompt upgrades the previously shipped reviewed prompt;
// arbitrary configured prompt overrides remain unsupported by the local
// adapter. Versioning the change forces rebuilding rather than stale reuse.
func (s *Store) AlignNativeGraphPrompt(ctx context.Context, scope core.GraphScope, id, current string, now time.Time) error {
	tx, err := s.beginGraphWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prompt string
	if err = tx.QueryRowContext(ctx, `SELECT prompt_fingerprint FROM graph_configurations WHERE tenant_id=? AND workspace=? AND id=? AND enabled=1`, scope.TenantID, scope.WorkspaceID, id).Scan(&prompt); err != nil {
		return err
	}
	if prompt == current {
		return tx.Commit()
	}
	if prompt != "sha256:35635c8c6d1aad0005484e2b0a9f3c8424d173bdf682e49e43ccfeefc89e3f99" {
		return fmt.Errorf("configured prompt is not a supported native reviewed prompt")
	}
	_, err = tx.ExecContext(ctx, `UPDATE graph_configurations SET prompt_fingerprint=?,version=version+1,updated_at=? WHERE tenant_id=? AND workspace=? AND id=? AND prompt_fingerprint=? AND enabled=1`, current, formatGraphTime(now), scope.TenantID, scope.WorkspaceID, id, prompt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) retryNativeGraphOperation(ctx context.Context, request contracts.GraphOperationRequest, prior core.GraphJob, key string, now time.Time) (core.GraphJob, bool, error) {
	if existing, err := s.graphJobByIdempotency(ctx, request.Scope, request.ConfigurationID, key); err == nil {
		return existing, false, nil
	} else if err != sql.ErrNoRows {
		return core.GraphJob{}, false, err
	}
	tx, err := s.beginGraphWriteTx(ctx)
	if err != nil {
		return core.GraphJob{}, false, err
	}
	defer tx.Rollback()
	var active string
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT active_revision_id,version FROM graph_configurations WHERE tenant_id=? AND workspace=? AND id=? AND enabled=1`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID).Scan(&active, &version); err != nil {
		return core.GraphJob{}, false, contracts.ErrGraphOperationDisabled
	}
	var busy int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_jobs WHERE tenant_id=? AND workspace=? AND configuration_id=? AND state IN (?,?)`, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, core.GraphJobQueued, core.GraphJobRunning).Scan(&busy); err != nil {
		return core.GraphJob{}, false, err
	}
	if busy > 0 {
		return core.GraphJob{}, false, contracts.ErrGraphOperationConflict
	}
	digest := uuid.NewSHA1(uuid.NameSpaceOID, []byte(prior.ID+"\x00"+key)).String()
	job := core.GraphJob{ID: "gj-" + digest, Scope: request.Scope, ConfigurationID: request.ConfigurationID, RevisionID: "gr-" + digest, IdempotencyKey: key, State: core.GraphJobQueued, CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx, `INSERT INTO graph_revisions(id,tenant_id,workspace,configuration_id,base_revision_id,state,cutoff_sequence,cutoff_event_time,cutoff_digest,projection_hash,artifact_hash,previous_revision_id,created_at,updated_at,configuration_version) VALUES(?,?,?,?,?,?,0,?,'','','',?,?,?,?)`, job.RevisionID, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, active, core.GraphRevisionQueued, formatGraphTime(now), active, formatGraphTime(now), formatGraphTime(now), version)
	if err != nil {
		return core.GraphJob{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO graph_jobs(id,tenant_id,workspace,configuration_id,revision_id,idempotency_key,state,attempt,lease_owner,lease_expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,0,'','',?,?)`, job.ID, request.Scope.TenantID, request.Scope.WorkspaceID, request.ConfigurationID, job.RevisionID, key, core.GraphJobQueued, formatGraphTime(now), formatGraphTime(now))
	if err != nil {
		return core.GraphJob{}, false, err
	}
	return job, true, tx.Commit()
}

func (s *Store) NativeGraphProtectedRevisions(ctx context.Context, scope core.GraphScope) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT active_revision_id,previous_revision_id FROM graph_configurations WHERE tenant_id=? AND workspace=?`, scope.TenantID, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	protected := map[string]bool{}
	for rows.Next() {
		var active, previous string
		if err = rows.Scan(&active, &previous); err != nil {
			return nil, err
		}
		if active != "" {
			protected[active] = true
		}
		if previous != "" {
			protected[previous] = true
		}
	}
	return protected, rows.Err()
}

func (s *Store) EnabledNativeGraphConfigurations(ctx context.Context, scope core.GraphScope) ([]string, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM graph_configurations WHERE tenant_id=? AND workspace=? AND enabled=1 ORDER BY id`, scope.TenantID, scope.WorkspaceID)
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

func (s *Store) LoadNativeGraphWork(ctx context.Context, scope core.GraphScope, id string, maxRecords int) (NativeGraphWork, error) {
	var work NativeGraphWork
	if err := scope.Validate(); err != nil {
		return work, err
	}
	if maxRecords < 1 || maxRecords > 100000 {
		return work, fmt.Errorf("invalid graph projection limit")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return work, err
	}
	defer tx.Rollback()
	c := &work.Configuration
	c.ID = id
	c.Scope = scope
	var created, updated string
	err = tx.QueryRowContext(ctx, `SELECT version,enabled,adapter_name,adapter_version,index_method,projection_version,artifact_schema_version,prompt_fingerprint,model_route,active_revision_id,created_at,updated_at FROM graph_configurations WHERE tenant_id=? AND workspace=? AND id=? AND enabled=1`, scope.TenantID, scope.WorkspaceID, id).Scan(&c.Version, &c.Enabled, &c.AdapterName, &c.AdapterVersion, &c.IndexMethod, &c.ProjectionVersion, &c.ArtifactSchemaVersion, &c.PromptFingerprint, &c.ModelRoute, &work.ActiveRevisionID, &created, &updated)
	if err != nil {
		return work, err
	}
	c.CreatedAt, _ = parseGraphTime(created)
	c.UpdatedAt, _ = parseGraphTime(updated)
	work.Rebuild = work.ActiveRevisionID == ""
	if !work.Rebuild {
		var indexedVersion int64
		if err = tx.QueryRowContext(ctx, `SELECT configuration_version FROM graph_revisions WHERE id=? AND tenant_id=? AND workspace=? AND configuration_id=?`, work.ActiveRevisionID, scope.TenantID, scope.WorkspaceID, id).Scan(&indexedVersion); err != nil {
			return work, err
		}
		work.Rebuild = indexedVersion != c.Version
	}
	work.ChangedIDs = map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT rowid,id,subject_id,subject_fingerprint,change_kind,occurred_at FROM graph_change_journal WHERE workspace=? AND configuration_version=? AND processed_revision_id='' ORDER BY rowid LIMIT ?`, scope.WorkspaceID, fmt.Sprintf("%s@%d", id, c.Version), maxRecords+1)
	if err != nil {
		return work, err
	}
	hash := sha256.New()
	for rows.Next() {
		var sequence int64
		var journal, subject, fingerprint, kind, at string
		if err := rows.Scan(&sequence, &journal, &subject, &fingerprint, &kind, &at); err != nil {
			rows.Close()
			return work, err
		}
		work.JournalIDs = append(work.JournalIDs, journal)
		work.ChangedIDs[subject] = true
		work.Rebuild = work.Rebuild || kind != "create"
		t, err := parseGraphTime(at)
		if err != nil {
			rows.Close()
			return work, err
		}
		if work.OldestChange.IsZero() || t.Before(work.OldestChange) {
			work.OldestChange = t
		}
		if t.After(work.Watermark.EventTime) {
			work.Watermark.EventTime = t
		}
		work.Watermark.Sequence = sequence
		fmt.Fprintf(hash, "%s\x00%s\n", journal, fingerprint)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return work, err
	}
	rows.Close()
	if len(work.JournalIDs) > maxRecords {
		return work, fmt.Errorf("graph change limit exceeded")
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,content,type,confidence,storage_tier,created_at,updated_at FROM memories WHERE workspace=? AND superseded_by IS NULL AND storage_tier<>? AND suppression_score<1 ORDER BY id LIMIT ?`, scope.WorkspaceID, core.TierCold, maxRecords+1)
	if err != nil {
		return work, err
	}
	var bytes int64
	for rows.Next() {
		m := core.MemoryEntry{Workspace: scope.WorkspaceID}
		var created, updated string
		if err := rows.Scan(&m.ID, &m.Content, &m.Type, &m.Confidence, &m.StorageTier, &created, &updated); err != nil {
			rows.Close()
			return work, err
		}
		m.CreatedAt, _ = parseGraphTime(created)
		m.UpdatedAt, _ = parseGraphTime(updated)
		bytes += int64(len(m.Content))
		if bytes > 512<<20 {
			rows.Close()
			return work, fmt.Errorf("graph projection byte limit exceeded")
		}
		work.Memories = append(work.Memories, m)
		fmt.Fprintf(hash, "%s\x00%s\n", m.ID, core.FingerprintText(m.Content))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return work, err
	}
	rows.Close()
	if len(work.Memories) > maxRecords {
		return work, fmt.Errorf("graph memory limit exceeded")
	}
	work.Watermark.Digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if work.Watermark.EventTime.IsZero() {
		work.Watermark.EventTime = time.Now().UTC()
	}
	return work, tx.Commit()
}

// ScheduleNativeGraphWork atomically coalesces one workspace/configuration.
// Failed work gets finite retries with durable backoff; changing the captured
// input gives it a fresh retry budget instead of mutating failed revisions.
func (s *Store) ScheduleNativeGraphWork(ctx context.Context, work NativeGraphWork, batch int, maxWait time.Duration, maxAttempts int, now time.Time) (bool, error) {
	c := work.Configuration
	if len(work.Memories) == 0 || (!work.Rebuild && len(work.JournalIDs) == 0) {
		return false, nil
	}
	if !work.Rebuild && len(work.JournalIDs) < batch && now.Sub(work.OldestChange) < maxWait {
		return false, nil
	}
	tx, err := s.beginGraphWriteTx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var active string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT active_revision_id,version FROM graph_configurations WHERE tenant_id=? AND workspace=? AND id=? AND enabled=1`, c.Scope.TenantID, c.Scope.WorkspaceID, c.ID).Scan(&active, &version); err != nil {
		return false, err
	}
	if version != c.Version || active != work.ActiveRevisionID {
		return false, ErrGraphRevisionConflict
	}
	var busy int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_jobs WHERE tenant_id=? AND workspace=? AND configuration_id=? AND state IN (?,?)`, c.Scope.TenantID, c.Scope.WorkspaceID, c.ID, core.GraphJobQueued, core.GraphJobRunning).Scan(&busy); err != nil {
		return false, err
	}
	if busy > 0 {
		return false, nil
	}
	key := fmt.Sprintf("native:%s:%d:%s", c.ID, c.Version, work.Watermark.Digest)
	attempt := 0
	var previousKey, state, updated string
	err = tx.QueryRowContext(ctx, `SELECT idempotency_key,state,attempt,updated_at FROM graph_jobs WHERE tenant_id=? AND workspace=? AND configuration_id=? ORDER BY updated_at DESC,id DESC LIMIT 1`, c.Scope.TenantID, c.Scope.WorkspaceID, c.ID).Scan(&previousKey, &state, &attempt, &updated)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if (state == string(core.GraphJobCancelled) || state == string(core.GraphJobDeadLetter)) && (strings.HasPrefix(previousKey, key) || !strings.HasPrefix(previousKey, "native:")) {
		return false, nil
	}
	if state == string(core.GraphJobFailed) && (strings.HasPrefix(previousKey, key) || !strings.HasPrefix(previousKey, "native:")) {
		if attempt >= maxAttempts {
			_, err = tx.ExecContext(ctx, `UPDATE graph_jobs SET state=? WHERE tenant_id=? AND workspace=? AND configuration_id=? AND idempotency_key=? AND state=?`, core.GraphJobDeadLetter, c.Scope.TenantID, c.Scope.WorkspaceID, c.ID, previousKey, core.GraphJobFailed)
			if err != nil {
				return false, err
			}
			return false, tx.Commit()
		}
		at, _ := parseGraphTime(updated)
		backoff := time.Duration(1<<min(attempt, 8)) * 10 * time.Second
		if now.Sub(at) < backoff {
			return false, nil
		}
	} else {
		attempt = 0
	}
	key = fmt.Sprintf("%s:attempt:%d", key, attempt+1)
	digest := uuid.NewSHA1(uuid.NameSpaceOID, []byte(c.Scope.WorkspaceID+"\x00"+key)).String()
	revisionID, jobID := "gr-"+digest, "gj-"+digest
	base := active
	if work.Rebuild {
		base = ""
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO graph_revisions(id,tenant_id,workspace,configuration_id,base_revision_id,state,cutoff_sequence,cutoff_event_time,cutoff_digest,projection_hash,artifact_hash,previous_revision_id,created_at,updated_at,configuration_version) VALUES(?,?,?,?,?,?,0,?,'','','',?,?,?,?)`, revisionID, c.Scope.TenantID, c.Scope.WorkspaceID, c.ID, base, core.GraphRevisionQueued, formatGraphTime(now), active, formatGraphTime(now), formatGraphTime(now), c.Version)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO graph_jobs(id,tenant_id,workspace,configuration_id,revision_id,idempotency_key,state,attempt,lease_owner,lease_expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?, '', '',?,?)`, jobID, c.Scope.TenantID, c.Scope.WorkspaceID, c.ID, revisionID, key, core.GraphJobQueued, attempt, formatGraphTime(now), formatGraphTime(now))
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// FailNativeGraphJob cannot mutate work after another worker owns its lease.
// Cancellation stays cancellation; failures exhaust a durable retry budget.
func (s *Store) FailNativeGraphJob(ctx context.Context, job core.GraphJob, maxAttempts int, now time.Time) error {
	tx, err := s.beginGraphWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := core.GraphJobFailed
	if job.Attempt >= maxAttempts {
		state = core.GraphJobDeadLetter
	}
	res, err := tx.ExecContext(ctx, `UPDATE graph_jobs SET state=?,lease_owner='',lease_expires_at='',updated_at=? WHERE tenant_id=? AND workspace=? AND id=? AND state=? AND lease_owner=? AND lease_expires_at>?`, state, formatGraphTime(now), job.Scope.TenantID, job.Scope.WorkspaceID, job.ID, core.GraphJobRunning, job.LeaseOwner, formatGraphTime(now))
	if err != nil {
		return err
	}
	if err = requireScopedGraphUpsert(res); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE graph_revisions SET state=?,updated_at=? WHERE tenant_id=? AND workspace=? AND configuration_id=? AND id=? AND state NOT IN (?,?,?)`, core.GraphRevisionFailed, formatGraphTime(now), job.Scope.TenantID, job.Scope.WorkspaceID, job.ConfigurationID, job.RevisionID, core.GraphRevisionActive, core.GraphRevisionPrevious, core.GraphRevisionCancelled)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RenewNativeGraphLease(ctx context.Context, job core.GraphJob, configurationVersion int64, lease time.Duration, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE graph_jobs SET lease_expires_at=?,updated_at=? WHERE tenant_id=? AND workspace=? AND id=? AND state=? AND lease_owner=? AND lease_expires_at>? AND EXISTS(SELECT 1 FROM graph_configurations c WHERE c.id=graph_jobs.configuration_id AND c.workspace=graph_jobs.workspace AND c.tenant_id=graph_jobs.tenant_id AND c.version=? AND c.enabled=1)`, formatGraphTime(now.Add(lease)), formatGraphTime(now), job.Scope.TenantID, job.Scope.WorkspaceID, job.ID, core.GraphJobRunning, job.LeaseOwner, formatGraphTime(now), configurationVersion)
	if err != nil {
		return err
	}
	return requireScopedGraphUpsert(res)
}

// PublishNativeGraphJob fences activation, job completion and cutoff
// acknowledgement in one transaction, so a crash cannot split these facts.
func (s *Store) PublishNativeGraphJob(ctx context.Context, job core.GraphJob, configurationVersion int64, expected string, journalIDs []string, now time.Time) error {
	tx, err := s.beginGraphWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int64
	var active string
	err = tx.QueryRowContext(ctx, `SELECT version,active_revision_id FROM graph_configurations WHERE tenant_id=? AND workspace=? AND id=? AND enabled=1`, job.Scope.TenantID, job.Scope.WorkspaceID, job.ConfigurationID).Scan(&version, &active)
	if err != nil {
		return err
	}
	if version != configurationVersion || active != expected {
		return ErrGraphRevisionConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE graph_jobs SET state=?,lease_owner='',lease_expires_at='',updated_at=? WHERE tenant_id=? AND workspace=? AND id=? AND revision_id=? AND state=? AND lease_owner=? AND lease_expires_at>?`, core.GraphJobCompleted, formatGraphTime(now), job.Scope.TenantID, job.Scope.WorkspaceID, job.ID, job.RevisionID, core.GraphJobRunning, job.LeaseOwner, formatGraphTime(now))
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(res); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `UPDATE graph_revisions SET state=?,previous_revision_id=?,updated_at=?,configuration_version=? WHERE tenant_id=? AND workspace=? AND configuration_id=? AND id=? AND state=?`, core.GraphRevisionActive, active, formatGraphTime(now), configurationVersion, job.Scope.TenantID, job.Scope.WorkspaceID, job.ConfigurationID, job.RevisionID, core.GraphRevisionReady)
	if err != nil {
		return err
	}
	if err := requireScopedGraphUpsert(res); err != nil {
		return err
	}
	if active != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE graph_revisions SET state=?,updated_at=? WHERE tenant_id=? AND workspace=? AND id=? AND state=?`, core.GraphRevisionPrevious, formatGraphTime(now), job.Scope.TenantID, job.Scope.WorkspaceID, active, core.GraphRevisionActive); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE graph_configurations SET previous_revision_id=active_revision_id,active_revision_id=?,updated_at=? WHERE tenant_id=? AND workspace=? AND id=?`, job.RevisionID, formatGraphTime(now), job.Scope.TenantID, job.Scope.WorkspaceID, job.ConfigurationID); err != nil {
		return err
	}
	for _, id := range journalIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE graph_change_journal SET processed_revision_id=? WHERE id=? AND workspace=? AND configuration_version=? AND processed_revision_id=''`, job.RevisionID, id, job.Scope.WorkspaceID, fmt.Sprintf("%s@%d", job.ConfigurationID, configurationVersion)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
