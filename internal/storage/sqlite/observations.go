package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// observationDedupIndex is the unique index backing observation dedup. The schema
// migration in store.go creates this name as a non-unique index; observations.go
// upgrades it to UNIQUE lazily (see ensureObservationUniqueIndex) so that the
// dedup insert is safe under concurrency without a check-then-insert TOCTOU.
const observationDedupIndex = "idx_observations_dedup"

type ObservationInsert struct {
	Workspace       string
	SessionID       string
	OccurredAt      time.Time
	Kind            string
	ToolName        string
	Summary         string
	ContentHash     string
	SourceAgent     string
	SourceAdapter   string
	HookEvent       string
	ExternalEventID string
	SchemaVersion   string
	CaptureMode     string
}

type ObserveUpsertSessionInput struct {
	Workspace   string
	SessionID   string
	ProjectRoot string
	CWD         string
	OccurredAt  time.Time
	Kind        string
}

func (s *Store) GetObservation(ctx context.Context, id string) (core.Observation, error) {
	var observation core.Observation
	var occurredAt, createdAt string
	var tool sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, workspace, session_id, occurred_at, kind, tool_name, summary,
		source_agent, source_adapter, hook_event, external_event_id, schema_version, capture_mode, created_at
		FROM observations WHERE id = ?`, strings.TrimSpace(id)).Scan(&observation.ID, &observation.Workspace, &observation.SessionID,
		&occurredAt, &observation.Kind, &tool, &observation.Summary, &observation.SourceAgent, &observation.SourceAdapter,
		&observation.HookEvent, &observation.ExternalEventID, &observation.SchemaVersion, &observation.CaptureMode, &createdAt)
	if err != nil {
		return core.Observation{}, err
	}
	if tool.Valid {
		observation.ToolName = &tool.String
	}
	observation.OccurredAt, _ = time.Parse(time.RFC3339Nano, occurredAt)
	observation.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return observation, nil
}

func ComputeObservationHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

func (s *Store) InsertObservationDedupWindow(
	ctx context.Context,
	in ObservationInsert,
	dedupWindow time.Duration,
) (core.Observation, bool, error) {
	if strings.TrimSpace(in.Workspace) == "" {
		return core.Observation{}, false, errors.New("workspace is required")
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return core.Observation{}, false, errors.New("session_id is required")
	}
	if strings.TrimSpace(in.Kind) == "" {
		return core.Observation{}, false, errors.New("kind is required")
	}
	if strings.TrimSpace(in.Summary) == "" {
		return core.Observation{}, false, errors.New("summary is required")
	}
	if dedupWindow <= 0 {
		dedupWindow = 5 * time.Minute
	}

	now := time.Now().UTC()
	occurredAt := in.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = now
	}

	hash := strings.TrimSpace(in.ContentHash)
	if hash == "" {
		hash = ComputeObservationHash(in.Workspace, in.SessionID, in.Kind, in.ToolName, in.Summary)
	}

	// The unique index on (workspace, content_hash) makes dedup race-free: the
	// insert below is the only place a duplicate can be rejected, so no
	// SELECT-then-INSERT TOCTOU window exists. Because the index covers all
	// history, the dedup window is effectively infinite for exact-hash
	// duplicates; dedupWindow is retained for API compatibility and callers
	// that rely on the time-based semantics should pre-filter their inputs.
	if err := s.ensureObservationUniqueIndex(ctx); err != nil {
		return core.Observation{}, false, fmt.Errorf("ensure unique dedup index: %w", err)
	}

	id := uuid.NewString()
	toolName := strings.TrimSpace(in.ToolName)
	createdAt := now

	res, err := s.db.ExecContext(ctx, `
INSERT INTO observations (
  id, workspace, session_id, occurred_at, kind, tool_name, summary, content_hash,
  source_agent, source_adapter, hook_event, external_event_id, schema_version, capture_mode, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING
`, id, in.Workspace, in.SessionID, occurredAt.Format(time.RFC3339Nano), in.Kind, toolName, in.Summary, hash,
		strings.TrimSpace(in.SourceAgent), strings.TrimSpace(in.SourceAdapter), strings.TrimSpace(in.HookEvent),
		strings.TrimSpace(in.ExternalEventID), strings.TrimSpace(in.SchemaVersion), strings.TrimSpace(in.CaptureMode), createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return core.Observation{}, false, err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return core.Observation{}, false, err
	}
	if inserted == 0 {
		// Duplicate (workspace, content_hash) already exists: return the
		// earliest existing observation instead of inserting a second row.
		existing, err := s.earliestObservationByHash(ctx, in.Workspace, hash)
		if err != nil {
			return core.Observation{}, false, err
		}
		return existing, true, nil
	}

	var toolPtr *string
	if toolName != "" {
		toolPtr = &toolName
	}
	return core.Observation{
		ID:              id,
		Workspace:       in.Workspace,
		SessionID:       in.SessionID,
		OccurredAt:      occurredAt,
		Kind:            in.Kind,
		ToolName:        toolPtr,
		Summary:         in.Summary,
		SourceAgent:     strings.TrimSpace(in.SourceAgent),
		SourceAdapter:   strings.TrimSpace(in.SourceAdapter),
		HookEvent:       strings.TrimSpace(in.HookEvent),
		ExternalEventID: strings.TrimSpace(in.ExternalEventID),
		SchemaVersion:   strings.TrimSpace(in.SchemaVersion),
		CaptureMode:     strings.TrimSpace(in.CaptureMode),
		CreatedAt:       createdAt,
	}, false, nil
}

func (s *Store) UpsertSessionFromObservation(ctx context.Context, in ObserveUpsertSessionInput) error {
	if strings.TrimSpace(in.Workspace) == "" {
		return errors.New("workspace is required")
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return errors.New("session_id is required")
	}
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	lastSeen := in.OccurredAt.UTC()
	if lastSeen.After(now) {
		lastSeen = now
	}

	projectRoot := strings.TrimSpace(in.ProjectRoot)
	cwd := strings.TrimSpace(in.CWD)

	startedAt := ""
	endedAt := ""
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "session_start" {
		startedAt = in.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	if kind == "session_end" {
		endedAt = in.OccurredAt.UTC().Format(time.RFC3339Nano)
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (
  workspace, session_id, project_root, cwd, started_at, ended_at, observation_count, last_seen_at
) VALUES (?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(workspace, session_id) DO UPDATE SET
  project_root = CASE WHEN excluded.project_root != '' THEN excluded.project_root ELSE sessions.project_root END,
  cwd = CASE WHEN excluded.cwd != '' THEN excluded.cwd ELSE sessions.cwd END,
  started_at = CASE
    WHEN sessions.started_at = '' AND excluded.started_at != '' THEN excluded.started_at
    ELSE sessions.started_at
  END,
  ended_at = CASE
    WHEN excluded.ended_at != '' THEN excluded.ended_at
    ELSE sessions.ended_at
  END,
  observation_count = sessions.observation_count + 1,
  last_seen_at = CASE
    WHEN excluded.last_seen_at > sessions.last_seen_at THEN excluded.last_seen_at
    ELSE sessions.last_seen_at
  END
`, in.Workspace, in.SessionID, projectRoot, cwd, startedAt, endedAt, lastSeen.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListObservations(
	ctx context.Context,
	workspace string,
	sessionID string,
	from *time.Time,
	to *time.Time,
	limit int,
) ([]core.Observation, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	where := []string{"workspace = ?"}
	args := []any{workspace}

	if sessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, sessionID)
	}
	if from != nil && !from.IsZero() {
		where = append(where, "occurred_at >= ?")
		args = append(args, from.UTC().Format(time.RFC3339Nano))
	}
	if to != nil && !to.IsZero() {
		where = append(where, "occurred_at <= ?")
		args = append(args, to.UTC().Format(time.RFC3339Nano))
	}
	args = append(args, limit)

	q := `
SELECT id, workspace, session_id, occurred_at, kind, tool_name, summary,
       source_agent, source_adapter, hook_event, external_event_id, schema_version, capture_mode, created_at
FROM observations
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY occurred_at DESC
LIMIT ?
`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]core.Observation, 0, limit)
	for rows.Next() {
		var (
			id, ws, sid, occurredAtRaw, kind, toolNameRaw, summary, sourceAgent, sourceAdapter, hookEvent, externalEventID, schemaVersion, captureMode, createdAtRaw string
		)
		if err := rows.Scan(&id, &ws, &sid, &occurredAtRaw, &kind, &toolNameRaw, &summary, &sourceAgent, &sourceAdapter, &hookEvent, &externalEventID, &schemaVersion, &captureMode, &createdAtRaw); err != nil {
			return nil, err
		}
		occurredAt, _ := time.Parse(time.RFC3339Nano, occurredAtRaw)
		createdAt, _ := time.Parse(time.RFC3339Nano, createdAtRaw)
		var toolPtr *string
		if strings.TrimSpace(toolNameRaw) != "" {
			v := strings.TrimSpace(toolNameRaw)
			toolPtr = &v
		}
		out = append(out, core.Observation{
			ID:              id,
			Workspace:       ws,
			SessionID:       sid,
			OccurredAt:      occurredAt,
			Kind:            kind,
			ToolName:        toolPtr,
			Summary:         summary,
			SourceAgent:     sourceAgent,
			SourceAdapter:   sourceAdapter,
			HookEvent:       hookEvent,
			ExternalEventID: externalEventID,
			SchemaVersion:   schemaVersion,
			CaptureMode:     captureMode,
			CreatedAt:       createdAt,
		})
	}
	return out, rows.Err()
}

// observationDedupIndexIsUnique reports whether the dedup index already exists
// as a UNIQUE index. It guards the lazy migration in ensureObservationUniqueIndex
// so the collapse + DDL work runs only once per database.
func (s *Store) observationDedupIndexIsUnique(ctx context.Context) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA index_list('observations')`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			seq     int
			name    string
			unique  bool
			origin  string
			partial int
		)
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		if name == observationDedupIndex {
			return unique, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	// Index absent entirely (e.g. legacy database): treat as needing migration.
	return false, nil
}

// collapseDuplicateObservations removes later duplicates of (workspace,
// content_hash), keeping the earliest row (by occurred_at, then insertion
// order). It is idempotent and only touches rows with a non-empty hash, matching
// the partial unique index predicate.
func collapseDuplicateObservations(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM observations
WHERE rowid IN (
  SELECT rowid FROM (
    SELECT rowid,
           ROW_NUMBER() OVER (
             PARTITION BY workspace, content_hash
             ORDER BY occurred_at ASC, rowid ASC
           ) AS rn
    FROM observations
    WHERE content_hash != ''
  )
  WHERE rn > 1
)
`)
	return err
}

// ensureObservationUniqueIndex upgrades the dedup index to UNIQUE so concurrent
// identical inserts cannot both succeed. It runs the legacy-duplicate collapse
// and the DDL inside a single transaction (idempotent), so it is safe to call on
// every dedup insert: the PRAGMA guard short-circuits once the unique index
// exists.
func (s *Store) ensureObservationUniqueIndex(ctx context.Context) error {
	unique, err := s.observationDedupIndexIsUnique(ctx)
	if err != nil {
		return err
	}
	if unique {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := collapseDuplicateObservations(ctx, tx); err != nil {
		return fmt.Errorf("collapse duplicate observations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS `+observationDedupIndex); err != nil {
		return fmt.Errorf("drop non-unique dedup index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS `+observationDedupIndex+` ON observations(workspace, content_hash) WHERE content_hash != ''`); err != nil {
		return fmt.Errorf("create unique dedup index: %w", err)
	}
	return tx.Commit()
}

// earliestObservationByHash returns the earliest row for a (workspace,
// content_hash) dedup key. Used by the duplicate path so a repeated insert
// preserves the original observation (including its occurred_at) instead of
// creating a second row.
func (s *Store) earliestObservationByHash(ctx context.Context, workspace, hash string) (core.Observation, error) {
	var (
		id, sid, occurredAtRaw, kind, toolNameRaw, summary, sourceAgent, sourceAdapter, hookEvent, externalEventID, schemaVersion, captureMode, createdAtRaw string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, session_id, occurred_at, kind, tool_name, summary,
       source_agent, source_adapter, hook_event, external_event_id, schema_version, capture_mode, created_at
FROM observations
WHERE workspace = ? AND content_hash = ?
ORDER BY occurred_at ASC, rowid ASC
LIMIT 1
`, workspace, hash).Scan(&id, &sid, &occurredAtRaw, &kind, &toolNameRaw, &summary, &sourceAgent, &sourceAdapter, &hookEvent, &externalEventID, &schemaVersion, &captureMode, &createdAtRaw)
	if err != nil {
		return core.Observation{}, err
	}
	occurredAt, _ := time.Parse(time.RFC3339Nano, occurredAtRaw)
	createdAt, _ := time.Parse(time.RFC3339Nano, createdAtRaw)
	var toolPtr *string
	if strings.TrimSpace(toolNameRaw) != "" {
		v := strings.TrimSpace(toolNameRaw)
		toolPtr = &v
	}
	return core.Observation{
		ID:              id,
		Workspace:       workspace,
		SessionID:       sid,
		OccurredAt:      occurredAt,
		Kind:            kind,
		ToolName:        toolPtr,
		Summary:         summary,
		SourceAgent:     sourceAgent,
		SourceAdapter:   sourceAdapter,
		HookEvent:       hookEvent,
		ExternalEventID: externalEventID,
		SchemaVersion:   schemaVersion,
		CaptureMode:     captureMode,
		CreatedAt:       createdAt,
	}, nil
}

func (s *Store) ListSessions(ctx context.Context, workspace string, limit int) ([]core.Session, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT workspace, session_id, project_root, cwd, started_at, ended_at, observation_count, last_seen_at
FROM sessions
WHERE workspace = ?
ORDER BY last_seen_at DESC
LIMIT ?
`, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]core.Session, 0, limit)
	for rows.Next() {
		var (
			ws, sid, projectRoot, cwd, startedAtRaw, endedAtRaw, lastSeenRaw string
			count                                                            int
		)
		if err := rows.Scan(&ws, &sid, &projectRoot, &cwd, &startedAtRaw, &endedAtRaw, &count, &lastSeenRaw); err != nil {
			return nil, err
		}
		var startedAt *time.Time
		if strings.TrimSpace(startedAtRaw) != "" {
			if t, err := time.Parse(time.RFC3339Nano, startedAtRaw); err == nil {
				startedAt = &t
			}
		}
		var endedAt *time.Time
		if strings.TrimSpace(endedAtRaw) != "" {
			if t, err := time.Parse(time.RFC3339Nano, endedAtRaw); err == nil {
				endedAt = &t
			}
		}
		lastSeen, _ := time.Parse(time.RFC3339Nano, lastSeenRaw)
		out = append(out, core.Session{
			Workspace:        ws,
			SessionID:        sid,
			ProjectRoot:      projectRoot,
			CWD:              cwd,
			StartedAt:        startedAt,
			EndedAt:          endedAt,
			ObservationCount: count,
			LastSeenAt:       lastSeen,
		})
	}
	return out, rows.Err()
}
