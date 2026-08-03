package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
)

func (s *Store) PutStudySession(ctx context.Context, v readingroom.StudySession) error {
	if err := v.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO study_sessions(id,workspace,session_json,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET session_json=excluded.session_json`, v.ID, v.Workspace, string(b), v.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	return err
}
func (s *Store) GetStudySession(ctx context.Context, id string) (readingroom.StudySession, error) {
	var b string
	err := s.db.QueryRowContext(ctx, `SELECT session_json FROM study_sessions WHERE id=?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return readingroom.StudySession{}, errors.New("study session not found")
	}
	if err != nil {
		return readingroom.StudySession{}, err
	}
	var v readingroom.StudySession
	err = json.Unmarshal([]byte(b), &v)
	return v, err
}
func (s *Store) PutStudyTurn(ctx context.Context, v readingroom.StudyTurn) error {
	if err := v.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO study_turns(id,session_id,turn_json,created_at) VALUES(?,?,?,?)`, v.ID, v.SessionID, string(b), v.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	return err
}
func (s *Store) ListStudyTurns(ctx context.Context, id string) ([]readingroom.StudyTurn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT turn_json FROM study_turns WHERE session_id=? ORDER BY created_at,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []readingroom.StudyTurn{}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var v readingroom.StudyTurn
		if err := json.Unmarshal([]byte(b), &v); err != nil {
			return nil, fmt.Errorf("decode study turn: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
