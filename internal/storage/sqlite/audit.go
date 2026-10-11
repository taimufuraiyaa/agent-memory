package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const maxAuditTargetIDs = 100

type AuditEventInput struct {
	Workspace, Operation, Outcome, Actor, Source, RequestID, SessionID string
	TargetType                                                         string
	TargetIDs                                                          []string
	TargetCount                                                        int
	Reason                                                             string
	Metadata                                                           map[string]any
	OccurredAt                                                         time.Time
}

type AuditEvent struct {
	ID            string         `json:"id"`
	SchemaVersion string         `json:"schema_version"`
	Workspace     string         `json:"workspace"`
	Operation     string         `json:"operation"`
	Outcome       string         `json:"outcome"`
	Actor         string         `json:"actor,omitempty"`
	Source        string         `json:"source,omitempty"`
	RequestID     string         `json:"request_id,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	TargetType    string         `json:"target_type,omitempty"`
	TargetIDs     []string       `json:"target_ids,omitempty"`
	TargetCount   int            `json:"target_count"`
	Reason        string         `json:"reason,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	OccurredAt    time.Time      `json:"occurred_at"`
}

type AuditFilter struct {
	Workspace, Operation, Actor, RequestID string
	From, To                               *time.Time
	Limit                                  int
}

func (s *Store) AppendAuditEvent(ctx context.Context, input AuditEventInput) (AuditEvent, error) {
	event, idsJSON, metadataJSON, err := prepareAuditEvent(input)
	if err != nil {
		return AuditEvent{}, err
	}
	_, err = s.db.ExecContext(ctx, auditInsertSQL, event.ID, event.SchemaVersion, event.Workspace, event.Operation, event.Outcome, event.Actor, event.Source, event.RequestID, event.SessionID, event.TargetType, idsJSON, event.TargetCount, event.Reason, metadataJSON, event.OccurredAt.Format(time.RFC3339Nano))
	return event, err
}

const auditInsertSQL = `INSERT INTO audit_events (id, schema_version, workspace, operation, outcome, actor, source, request_id, session_id, target_type, target_ids_json, target_count, reason, metadata_json, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func prepareAuditEvent(input AuditEventInput) (AuditEvent, string, string, error) {
	if strings.TrimSpace(input.Workspace) == "" || strings.TrimSpace(input.Operation) == "" || strings.TrimSpace(input.Outcome) == "" {
		return AuditEvent{}, "", "", errors.New("workspace, operation, and outcome are required")
	}
	originalCount := len(input.TargetIDs)
	if input.TargetCount < originalCount {
		input.TargetCount = originalCount
	}
	ids := append([]string(nil), input.TargetIDs...)
	if len(ids) > maxAuditTargetIDs {
		ids = ids[:maxAuditTargetIDs]
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return AuditEvent{}, "", "", err
	}
	metadataJSON, err := json.Marshal(input.Metadata)
	if err != nil {
		return AuditEvent{}, "", "", err
	}
	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	}
	event := AuditEvent{ID: uuid.NewString(), SchemaVersion: "v1", Workspace: strings.TrimSpace(input.Workspace), Operation: strings.TrimSpace(input.Operation), Outcome: strings.TrimSpace(input.Outcome), Actor: strings.TrimSpace(input.Actor), Source: strings.TrimSpace(input.Source), RequestID: strings.TrimSpace(input.RequestID), SessionID: strings.TrimSpace(input.SessionID), TargetType: strings.TrimSpace(input.TargetType), TargetIDs: ids, TargetCount: input.TargetCount, Reason: strings.TrimSpace(input.Reason), Metadata: input.Metadata, OccurredAt: input.OccurredAt.UTC()}
	return event, string(idsJSON), string(metadataJSON), nil
}

func (s *Store) DeleteByIDsAudited(ctx context.Context, ids []string, input AuditEventInput) error {
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return errors.New("memory ids are required")
	}
	input.TargetIDs = clean
	input.TargetType = "memory"
	event, idsJSON, metadataJSON, err := prepareAuditEvent(input)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	placeholders := strings.TrimRight(strings.Repeat("?,", len(clean)), ",")
	args := make([]any, len(clean))
	memories := make([]*core.MemoryEntry, 0, len(clean))
	for index, id := range clean {
		args[index] = id
		memory, err := memoryForGraphChangeTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if memory != nil {
			memories = append(memories, memory)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, auditInsertSQL, event.ID, event.SchemaVersion, event.Workspace, event.Operation, event.Outcome, event.Actor, event.Source, event.RequestID, event.SessionID, event.TargetType, idsJSON, event.TargetCount, event.Reason, metadataJSON, event.OccurredAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	for _, memory := range memories {
		if err := appendGraphChangesForMemoryTx(ctx, tx, memory, "delete", event.OccurredAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.useTurbovec && s.turbovecIndex != nil {
		for _, id := range clean {
			s.turbovecIndex.Delete(id)
		}
	}
	return nil
}

func (s *Store) ListAuditEvents(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	where := []string{"workspace = ?"}
	args := []any{strings.TrimSpace(filter.Workspace)}
	if filter.Operation != "" {
		where = append(where, "operation = ?")
		args = append(args, filter.Operation)
	}
	if filter.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, filter.Actor)
	}
	if filter.RequestID != "" {
		where = append(where, "request_id = ?")
		args = append(args, filter.RequestID)
	}
	if filter.From != nil {
		where = append(where, "occurred_at >= ?")
		args = append(args, filter.From.UTC().Format(time.RFC3339Nano))
	}
	if filter.To != nil {
		where = append(where, "occurred_at <= ?")
		args = append(args, filter.To.UTC().Format(time.RFC3339Nano))
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 1000 {
		filter.Limit = 1000
	}
	args = append(args, filter.Limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id, schema_version, workspace, operation, outcome, actor, source, request_id, session_id, target_type, target_ids_json, target_count, reason, metadata_json, occurred_at FROM audit_events WHERE `+strings.Join(where, " AND ")+` ORDER BY occurred_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var idsJSON, metadataJSON, occurred string
		if err := rows.Scan(&event.ID, &event.SchemaVersion, &event.Workspace, &event.Operation, &event.Outcome, &event.Actor, &event.Source, &event.RequestID, &event.SessionID, &event.TargetType, &idsJSON, &event.TargetCount, &event.Reason, &metadataJSON, &occurred); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(idsJSON), &event.TargetIDs)
		_ = json.Unmarshal([]byte(metadataJSON), &event.Metadata)
		event.OccurredAt, _ = time.Parse(time.RFC3339Nano, occurred)
		events = append(events, event)
	}
	return events, rows.Err()
}
