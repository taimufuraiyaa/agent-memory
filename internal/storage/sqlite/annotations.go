package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func (s *Store) PutAnnotation(ctx context.Context, annotation library.Annotation) error {
	if err := annotation.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO book_annotations (
  id, edition_id, citation_id, content, owner_id, owner_kind, visibility, organization_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, annotation.ID, annotation.EditionID, annotation.CitationID, annotation.Content, annotation.Owner.ID,
		annotation.Owner.Kind, annotation.Visibility, annotation.OrganizationID, annotation.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("put annotation: %w", err)
	}
	return nil
}

func (s *Store) GetAnnotation(ctx context.Context, id string) (library.Annotation, error) {
	var annotation library.Annotation
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
SELECT id, edition_id, citation_id, content, owner_id, owner_kind, visibility, organization_id, created_at
FROM book_annotations WHERE id = ?
`, id).Scan(&annotation.ID, &annotation.EditionID, &annotation.CitationID, &annotation.Content,
		&annotation.Owner.ID, &annotation.Owner.Kind, &annotation.Visibility, &annotation.OrganizationID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return library.Annotation{}, library.ErrLibraryResourceNotFound
	}
	if err != nil {
		return library.Annotation{}, fmt.Errorf("get annotation: %w", err)
	}
	annotation.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return library.Annotation{}, err
	}
	return annotation, nil
}

func (s *Store) ListAnnotations(ctx context.Context, editionID string) ([]library.Annotation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM book_annotations WHERE edition_id = ? ORDER BY created_at, id`, editionID)
	if err != nil {
		return nil, fmt.Errorf("list annotations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	annotations := []library.Annotation{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		annotation, err := s.GetAnnotation(ctx, id)
		if err != nil {
			return nil, err
		}
		annotations = append(annotations, annotation)
	}
	return annotations, rows.Err()
}

func (s *Store) UpdateAnnotationVisibility(ctx context.Context, id string, visibility core.Visibility, organizationID string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE book_annotations SET visibility = ?, organization_id = ? WHERE id = ?
`, visibility, organizationID, id)
	if err != nil {
		return fmt.Errorf("update annotation visibility: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return library.ErrLibraryResourceNotFound
	}
	return nil
}
