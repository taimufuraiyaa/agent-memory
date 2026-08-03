package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func (s *Store) PutReadingProgress(ctx context.Context, v library.ReadingProgress) error {
	if err := v.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO reading_progress(principal_id,edition_id,progress_json,updated_at) VALUES(?,?,?,?) ON CONFLICT(principal_id,edition_id) DO UPDATE SET progress_json=excluded.progress_json,updated_at=excluded.updated_at`, v.PrincipalID, v.EditionID, string(b), v.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	return err
}
func (s *Store) GetReadingProgress(ctx context.Context, principalID, editionID string) (library.ReadingProgress, error) {
	var b string
	err := s.db.QueryRowContext(ctx, `SELECT progress_json FROM reading_progress WHERE principal_id=? AND edition_id=?`, principalID, editionID).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return library.ReadingProgress{}, errors.New("reading progress not found")
	}
	if err != nil {
		return library.ReadingProgress{}, err
	}
	var v library.ReadingProgress
	err = json.Unmarshal([]byte(b), &v)
	return v, err
}
