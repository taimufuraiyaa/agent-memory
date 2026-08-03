package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

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

	cutoff := occurredAt.Add(-1 * dedupWindow)
	var existingID string
	err := s.db.QueryRowContext(ctx, `
SELECT id
FROM observations
WHERE workspace = ? AND content_hash = ? AND occurred_at >= ?
ORDER BY occurred_at DESC
LIMIT 1
`, in.Workspace, hash, cutoff.Format(time.RFC3339Nano)).Scan(&existingID)
	if err == nil && existingID != "" {
		return core.Observation{ID: existingID}, true, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return core.Observation{}, false, err
	}

	id := uuid.NewString()
	toolName := strings.TrimSpace(in.ToolName)
	createdAt := now

	_, err = s.db.ExecContext(ctx, `
INSERT INTO observations (
  id, workspace, session_id, occurred_at, kind, tool_name, summary, content_hash,
  source_agent, source_adapter, hook_event, external_event_id, schema_version, capture_mode, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, id, in.Workspace, in.SessionID, occurredAt.Format(time.RFC3339Nano), in.Kind, toolName, in.Summary, hash,
		strings.TrimSpace(in.SourceAgent), strings.TrimSpace(in.SourceAdapter), strings.TrimSpace(in.HookEvent),
		strings.TrimSpace(in.ExternalEventID), strings.TrimSpace(in.SchemaVersion), strings.TrimSpace(in.CaptureMode), createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return core.Observation{}, false, err
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
