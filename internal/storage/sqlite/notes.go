package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var (
	ErrNoteNotFound         = errors.New("note not found")
	ErrNotePathConflict     = errors.New("note path already exists")
	ErrNoteRevisionConflict = errors.New("note revision conflict")
)

var noteLinkPattern = regexp.MustCompile(`\[\[([^\]\n|#]+)(?:#[^\]\n|]+)?(?:\|[^\]\n]+)?\]\]`)

func normalizeNotePath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return "", errors.New("note path is required")
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", errors.New("note path escapes workspace")
	}
	if !strings.HasSuffix(strings.ToLower(clean), ".md") {
		clean += ".md"
	}
	if len(clean) > 512 {
		return "", errors.New("note path is too long")
	}
	return clean, nil
}

func noteContentHash(body string, properties map[string]any) (string, error) {
	if properties == nil {
		properties = map[string]any{}
	}
	raw, err := json.Marshal(properties)
	if err != nil {
		return "", fmt.Errorf("marshal note properties: %w", err)
	}
	sum := sha256.Sum256(append(append([]byte(body), '\n'), raw...))
	return hex.EncodeToString(sum[:]), nil
}

func notePropertiesJSON(properties map[string]any) (string, error) {
	if properties == nil {
		properties = map[string]any{}
	}
	raw, err := json.Marshal(properties)
	if err != nil {
		return "", fmt.Errorf("marshal note properties: %w", err)
	}
	return string(raw), nil
}

func noteAuthorKind(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "human"
	}
	return value
}

func (s *Store) CreateNote(ctx context.Context, input core.CreateNoteInput) (*core.Note, error) {
	workspace := strings.TrimSpace(input.Workspace)
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}
	notePath, err := normalizeNotePath(input.Path)
	if err != nil {
		return nil, err
	}
	title := core.NormalizeNoteTitle(input.Title, notePath)
	propertiesJSON, err := notePropertiesJSON(input.Properties)
	if err != nil {
		return nil, err
	}
	hash, err := noteContentHash(input.Body, input.Properties)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	note := &core.Note{
		ID:          uuid.NewString(),
		Workspace:   workspace,
		Path:        notePath,
		Title:       title,
		Body:        input.Body,
		Properties:  nonNilProperties(input.Properties),
		Revision:    1,
		ContentHash: hash,
		IndexState:  core.NoteIndexPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO notes
			(id, workspace, path, title, body, properties_json, revision, content_hash, index_state, indexed_revision, index_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, 0, '', ?, ?)`,
		note.ID, workspace, notePath, title, input.Body, propertiesJSON, hash, string(core.NoteIndexPending), noteFormatTime(now), noteFormatTime(now))
	if err != nil {
		if isUniqueConstraint(err) {
			return nil, ErrNotePathConflict
		}
		return nil, err
	}
	if err := insertNoteRevisionTx(ctx, tx, note, noteAuthorKind(input.AuthorKind), propertiesJSON); err != nil {
		return nil, err
	}
	if err := replaceNoteLinksTx(ctx, tx, note, input.Body); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *Store) UpdateNote(ctx context.Context, input core.UpdateNoteInput) (*core.Note, error) {
	notePath, err := normalizeNotePath(input.Path)
	if err != nil {
		return nil, err
	}
	if input.ExpectedRevision < 1 {
		return nil, errors.New("expected_revision must be positive")
	}
	propertiesJSON, err := notePropertiesJSON(input.Properties)
	if err != nil {
		return nil, err
	}
	hash, err := noteContentHash(input.Body, input.Properties)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := getNoteTx(ctx, tx, strings.TrimSpace(input.Workspace), strings.TrimSpace(input.NoteID))
	if err != nil {
		return nil, err
	}
	if current.DeletedAt != nil {
		return nil, ErrNoteNotFound
	}
	if current.Revision != input.ExpectedRevision {
		return nil, ErrNoteRevisionConflict
	}
	title := core.NormalizeNoteTitle(input.Title, notePath)
	if current.Path == notePath && current.Title == title && current.ContentHash == hash {
		return current, nil
	}
	now := time.Now().UTC()
	nextRevision := current.Revision + 1
	result, err := tx.ExecContext(ctx, `
		UPDATE notes
		SET path = ?, title = ?, body = ?, properties_json = ?, revision = ?, content_hash = ?,
			index_state = ?, index_error = '', updated_at = ?
		WHERE id = ? AND workspace = ? AND revision = ? AND deleted_at IS NULL`,
		notePath, title, input.Body, propertiesJSON, nextRevision, hash,
		string(core.NoteIndexPending), noteFormatTime(now), current.ID, current.Workspace, current.Revision)
	if err != nil {
		if isUniqueConstraint(err) {
			return nil, ErrNotePathConflict
		}
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, ErrNoteRevisionConflict
	}
	current.Path = notePath
	current.Title = title
	current.Body = input.Body
	current.Properties = nonNilProperties(input.Properties)
	current.Revision = nextRevision
	current.ContentHash = hash
	current.IndexState = core.NoteIndexPending
	current.IndexError = ""
	current.UpdatedAt = now
	if err := insertNoteRevisionTx(ctx, tx, current, noteAuthorKind(input.AuthorKind), propertiesJSON); err != nil {
		return nil, err
	}
	if err := replaceNoteLinksTx(ctx, tx, current, input.Body); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Store) GetNote(ctx context.Context, workspace, noteID string) (*core.Note, error) {
	return getNoteQuery(ctx, s.db, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
}

func (s *Store) ListNotes(ctx context.Context, workspace string, includeDeleted bool) ([]core.Note, error) {
	query := `
		SELECT id, workspace, path, title, body, properties_json, revision, content_hash,
			index_state, indexed_revision, index_error, created_at, updated_at, deleted_at
		FROM notes WHERE workspace = ?`
	if !includeDeleted {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY path COLLATE NOCASE ASC`
	rows, err := s.db.QueryContext(ctx, query, strings.TrimSpace(workspace))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]core.Note, 0)
	for rows.Next() {
		note, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, *note)
	}
	return notes, rows.Err()
}

func (s *Store) ListNoteRevisions(ctx context.Context, workspace, noteID string) ([]core.NoteRevision, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT note_id, workspace, revision, path, title, body, properties_json, content_hash, author_kind, created_at
		FROM note_revisions
		WHERE workspace = ? AND note_id = ?
		ORDER BY revision DESC`, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]core.NoteRevision, 0)
	for rows.Next() {
		var item core.NoteRevision
		var propertiesJSON, createdAt string
		if err := rows.Scan(&item.NoteID, &item.Workspace, &item.Revision, &item.Path, &item.Title, &item.Body, &propertiesJSON, &item.ContentHash, &item.AuthorKind, &createdAt); err != nil {
			return nil, err
		}
		item.Properties = map[string]any{}
		if err := json.Unmarshal([]byte(propertiesJSON), &item.Properties); err != nil {
			return nil, err
		}
		item.CreatedAt = noteParseTime(createdAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetNoteRevision(ctx context.Context, workspace, noteID string, revision int) (*core.NoteRevision, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT note_id, workspace, revision, path, title, body, properties_json, content_hash, author_kind, created_at
		FROM note_revisions
		WHERE workspace = ? AND note_id = ? AND revision = ?`,
		strings.TrimSpace(workspace), strings.TrimSpace(noteID), revision)
	var item core.NoteRevision
	var propertiesJSON, createdAt string
	if err := row.Scan(&item.NoteID, &item.Workspace, &item.Revision, &item.Path, &item.Title, &item.Body, &propertiesJSON, &item.ContentHash, &item.AuthorKind, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoteNotFound
		}
		return nil, err
	}
	item.Properties = map[string]any{}
	if err := json.Unmarshal([]byte(propertiesJSON), &item.Properties); err != nil {
		return nil, err
	}
	item.CreatedAt = noteParseTime(createdAt)
	return &item, nil
}

func (s *Store) TrashNote(ctx context.Context, workspace, noteID string) (*core.Note, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE notes SET deleted_at = ?, index_state = ?, updated_at = ?
		WHERE workspace = ? AND id = ? AND deleted_at IS NULL`,
		noteFormatTime(now), string(core.NoteIndexRetired), noteFormatTime(now), strings.TrimSpace(workspace), strings.TrimSpace(noteID))
	if err != nil {
		return nil, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return nil, ErrNoteNotFound
	}
	return s.GetNote(ctx, workspace, noteID)
}

func (s *Store) RestoreNote(ctx context.Context, workspace, noteID string) (*core.Note, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE notes SET deleted_at = NULL, index_state = ?, index_error = '', updated_at = ?
		WHERE workspace = ? AND id = ? AND deleted_at IS NOT NULL`,
		string(core.NoteIndexPending), noteFormatTime(now), strings.TrimSpace(workspace), strings.TrimSpace(noteID))
	if err != nil {
		if isUniqueConstraint(err) {
			return nil, ErrNotePathConflict
		}
		return nil, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return nil, ErrNoteNotFound
	}
	return s.GetNote(ctx, workspace, noteID)
}

func (s *Store) DeleteNotePermanently(ctx context.Context, workspace, noteID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE workspace = ? AND id = ? AND deleted_at IS NOT NULL`,
		strings.TrimSpace(workspace), strings.TrimSpace(noteID))
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return ErrNoteNotFound
	}
	return nil
}

func (s *Store) ListNoteBacklinks(ctx context.Context, workspace, noteID string) ([]core.NoteLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT source_note_id, COALESCE(target_note_id, ''), raw_target, line, snippet
		FROM note_links
		WHERE workspace = ? AND target_note_id = ?
		ORDER BY source_note_id, line`, strings.TrimSpace(workspace), strings.TrimSpace(noteID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]core.NoteLink, 0)
	for rows.Next() {
		var item core.NoteLink
		if err := rows.Scan(&item.SourceNoteID, &item.TargetNoteID, &item.RawTarget, &item.Line, &item.Snippet); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func insertNoteRevisionTx(ctx context.Context, tx *sql.Tx, note *core.Note, authorKind, propertiesJSON string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO note_revisions
			(note_id, workspace, revision, path, title, body, properties_json, content_hash, author_kind, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		note.ID, note.Workspace, note.Revision, note.Path, note.Title, note.Body, propertiesJSON, note.ContentHash, authorKind, noteFormatTime(note.UpdatedAt))
	return err
}

func replaceNoteLinksTx(ctx context.Context, tx *sql.Tx, note *core.Note, body string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_links WHERE source_note_id = ?`, note.ID); err != nil {
		return err
	}
	lines := strings.Split(body, "\n")
	for index, line := range lines {
		matches := noteLinkPattern.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			rawTarget := strings.TrimSpace(match[1])
			var targetID sql.NullString
			err := tx.QueryRowContext(ctx, `
				SELECT id FROM notes
				WHERE workspace = ? AND deleted_at IS NULL
					AND (title = ? COLLATE NOCASE OR path = ? COLLATE NOCASE OR path = ? COLLATE NOCASE)
				ORDER BY updated_at DESC LIMIT 1`,
				note.Workspace, rawTarget, rawTarget, rawTarget+".md").Scan(&targetID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO note_links(source_note_id, workspace, revision, target_note_id, raw_target, line, snippet)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				note.ID, note.Workspace, note.Revision, targetID, rawTarget, index+1, strings.TrimSpace(line)); err != nil {
				return err
			}
		}
	}
	return nil
}

func getNoteTx(ctx context.Context, tx *sql.Tx, workspace, noteID string) (*core.Note, error) {
	return getNoteQuery(ctx, tx, workspace, noteID)
}

type noteQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getNoteQuery(ctx context.Context, queryer noteQueryer, workspace, noteID string) (*core.Note, error) {
	row := queryer.QueryRowContext(ctx, `
		SELECT id, workspace, path, title, body, properties_json, revision, content_hash,
			index_state, indexed_revision, index_error, created_at, updated_at, deleted_at
		FROM notes WHERE workspace = ? AND id = ?`, workspace, noteID)
	note, err := scanNote(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoteNotFound
	}
	return note, err
}

type noteScanner interface {
	Scan(...any) error
}

func scanNote(scanner noteScanner) (*core.Note, error) {
	var note core.Note
	var propertiesJSON, indexState, createdAt, updatedAt string
	var deletedAt sql.NullString
	if err := scanner.Scan(
		&note.ID, &note.Workspace, &note.Path, &note.Title, &note.Body, &propertiesJSON,
		&note.Revision, &note.ContentHash, &indexState, &note.IndexedRevision, &note.IndexError,
		&createdAt, &updatedAt, &deletedAt,
	); err != nil {
		return nil, err
	}
	note.Properties = map[string]any{}
	if err := json.Unmarshal([]byte(propertiesJSON), &note.Properties); err != nil {
		return nil, err
	}
	note.IndexState = core.NoteIndexState(indexState)
	note.CreatedAt = noteParseTime(createdAt)
	note.UpdatedAt = noteParseTime(updatedAt)
	if deletedAt.Valid && deletedAt.String != "" {
		value := noteParseTime(deletedAt.String)
		note.DeletedAt = &value
	}
	return &note, nil
}

func nonNilProperties(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func noteFormatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func noteParseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func SortNotesByModified(notes []core.Note) {
	sort.SliceStable(notes, func(i, j int) bool {
		return notes[i].UpdatedAt.After(notes[j].UpdatedAt)
	})
}
