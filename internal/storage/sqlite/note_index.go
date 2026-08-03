package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) SetNoteIndexState(
	ctx context.Context,
	workspace, noteID string,
	revision int,
	state core.NoteIndexState,
	indexedRevision int,
	indexError string,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE notes
		SET index_state = ?, indexed_revision = ?, index_error = ?
		WHERE workspace = ? AND id = ? AND revision = ? AND deleted_at IS NULL`,
		string(state), indexedRevision, strings.TrimSpace(indexError),
		strings.TrimSpace(workspace), strings.TrimSpace(noteID), revision)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrNoteRevisionConflict
	}
	return nil
}

func (s *Store) ActivateNoteChunks(
	ctx context.Context,
	workspace, noteID string,
	revision int,
	chunks []core.NoteMemoryChunk,
) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentRevision int
	var deletedAt sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT revision, deleted_at FROM notes WHERE workspace = ? AND id = ?`,
		workspace, noteID).Scan(&currentRevision, &deletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoteNotFound
		}
		return nil, err
	}
	if deletedAt.Valid || currentRevision != revision {
		return nil, ErrNoteRevisionConflict
	}

	oldIDs, err := activeNoteMemoryIDsTx(ctx, tx, workspace, noteID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE note_memory_chunks SET active = 0 WHERE workspace = ? AND note_id = ?`, workspace, noteID); err != nil {
		return nil, err
	}
	for _, chunk := range chunks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO note_memory_chunks
				(workspace, note_id, revision, ordinal, heading, start_line, end_line, content_hash, memory_id, active)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
			ON CONFLICT(note_id, revision, ordinal) DO UPDATE SET
				heading = excluded.heading,
				start_line = excluded.start_line,
				end_line = excluded.end_line,
				content_hash = excluded.content_hash,
				memory_id = excluded.memory_id,
				active = 1`,
			workspace, noteID, revision, chunk.Ordinal, chunk.Heading, chunk.StartLine, chunk.EndLine,
			chunk.ContentHash, chunk.MemoryID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notes SET index_state = ?, indexed_revision = ?, index_error = ''
		WHERE workspace = ? AND id = ? AND revision = ? AND deleted_at IS NULL`,
		string(core.NoteIndexReady), revision, workspace, noteID, revision); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	activeIDs := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		activeIDs[chunk.MemoryID] = struct{}{}
	}
	retired := make([]string, 0, len(oldIDs))
	for _, id := range oldIDs {
		if _, stillActive := activeIDs[id]; !stillActive {
			retired = append(retired, id)
		}
	}
	return retired, nil
}

func (s *Store) ListActiveNoteChunks(ctx context.Context, workspace, noteID string) ([]core.NoteMemoryChunk, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT workspace, note_id, revision, ordinal, heading, start_line, end_line, content_hash, memory_id, active
		FROM note_memory_chunks
		WHERE workspace = ? AND note_id = ? AND active = 1
		ORDER BY ordinal`, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]core.NoteMemoryChunk, 0)
	for rows.Next() {
		var item core.NoteMemoryChunk
		var active int
		if err := rows.Scan(&item.Workspace, &item.NoteID, &item.Revision, &item.Ordinal, &item.Heading, &item.StartLine, &item.EndLine, &item.ContentHash, &item.MemoryID, &active); err != nil {
			return nil, err
		}
		item.Active = active != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) RetireNoteChunks(ctx context.Context, workspace, noteID string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	ids, err := activeNoteMemoryIDsTx(ctx, tx, workspace, noteID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE note_memory_chunks SET active = 0 WHERE workspace = ? AND note_id = ?`, workspace, noteID); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}

func activeNoteMemoryIDsTx(ctx context.Context, tx *sql.Tx, workspace, noteID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT memory_id FROM note_memory_chunks
		WHERE workspace = ? AND note_id = ? AND active = 1`, workspace, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
