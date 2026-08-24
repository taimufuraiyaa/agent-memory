package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func (s *Store) PutBookWork(ctx context.Context, work library.BookWork) error {
	if err := work.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO book_works (id, title, normalized_title, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET title = excluded.title, normalized_title = excluded.normalized_title
`, work.ID, work.Title, work.NormalizedTitle, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("put book work: %w", err)
	}
	return nil
}

func (s *Store) PutBookEdition(ctx context.Context, edition library.BookEdition) error {
	if err := edition.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO book_editions (id, work_id, label, language, content_fingerprint, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET label = excluded.label, language = excluded.language
`, edition.ID, edition.WorkID, edition.Label, edition.Language, edition.ContentFingerprint, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("put book edition: %w", err)
	}
	return nil
}

func (s *Store) GetBookEdition(ctx context.Context, id string) (library.BookEdition, error) {
	var edition library.BookEdition
	err := s.db.QueryRowContext(ctx, `
SELECT id, work_id, label, language, content_fingerprint FROM book_editions WHERE id = ?
`, id).Scan(&edition.ID, &edition.WorkID, &edition.Label, &edition.Language, &edition.ContentFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return library.BookEdition{}, fmt.Errorf("book edition not found: %s", id)
	}
	if err != nil {
		return library.BookEdition{}, fmt.Errorf("get book edition: %w", err)
	}
	return edition, nil
}

func (s *Store) ListBookEditions(ctx context.Context) ([]library.BookEdition, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, work_id, label, language, content_fingerprint FROM book_editions ORDER BY id
`)
	if err != nil {
		return nil, fmt.Errorf("list book editions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	editions := []library.BookEdition{}
	for rows.Next() {
		var edition library.BookEdition
		if err := rows.Scan(&edition.ID, &edition.WorkID, &edition.Label, &edition.Language, &edition.ContentFingerprint); err != nil {
			return nil, fmt.Errorf("scan book edition: %w", err)
		}
		editions = append(editions, edition)
	}
	return editions, rows.Err()
}

func (s *Store) PutSourceAsset(ctx context.Context, asset library.SourceAsset) error {
	if err := asset.Validate(); err != nil {
		return err
	}
	policy, err := json.Marshal(asset.Policy)
	if err != nil {
		return fmt.Errorf("marshal source policy: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO source_assets (
  id, edition_id, format, byte_fingerprint, normalized_fingerprint, parser_version, policy_json, imported_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET policy_json = excluded.policy_json
`, asset.ID, asset.EditionID, asset.Format, asset.ByteFingerprint, asset.NormalizedFingerprint,
		asset.ParserVersion, string(policy), asset.ImportedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("put source asset: %w", err)
	}
	return nil
}

func (s *Store) FindSourceAssetByByteFingerprint(ctx context.Context, fingerprint string) (library.SourceAsset, bool, error) {
	var asset library.SourceAsset
	var policyJSON, importedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT id, edition_id, format, byte_fingerprint, normalized_fingerprint, parser_version, policy_json, imported_at
FROM source_assets WHERE byte_fingerprint = ?
`, fingerprint).Scan(&asset.ID, &asset.EditionID, &asset.Format, &asset.ByteFingerprint, &asset.NormalizedFingerprint,
		&asset.ParserVersion, &policyJSON, &importedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return library.SourceAsset{}, false, nil
	}
	if err != nil {
		return library.SourceAsset{}, false, fmt.Errorf("find source asset: %w", err)
	}
	if err := json.Unmarshal([]byte(policyJSON), &asset.Policy); err != nil {
		return library.SourceAsset{}, false, fmt.Errorf("decode source policy: %w", err)
	}
	asset.ImportedAt, err = time.Parse(time.RFC3339Nano, importedAt)
	if err != nil {
		return library.SourceAsset{}, false, fmt.Errorf("parse source import time: %w", err)
	}
	return asset, true, nil
}

func (s *Store) FindBookEditionByFingerprint(ctx context.Context, workID, fingerprint string) (library.BookEdition, bool, error) {
	var edition library.BookEdition
	err := s.db.QueryRowContext(ctx, `
SELECT id, work_id, label, language, content_fingerprint
FROM book_editions WHERE work_id = ? AND content_fingerprint = ?
`, workID, fingerprint).Scan(&edition.ID, &edition.WorkID, &edition.Label, &edition.Language, &edition.ContentFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return library.BookEdition{}, false, nil
	}
	if err != nil {
		return library.BookEdition{}, false, fmt.Errorf("find book edition: %w", err)
	}
	return edition, true, nil
}

func (s *Store) ReplaceStructuralNodes(ctx context.Context, editionID string, nodes []library.StructuralNode) error {
	if err := library.ValidateStructure(nodes); err != nil {
		return err
	}
	for _, node := range nodes {
		if node.EditionID != editionID {
			return errors.New("structural node edition mismatch")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin structural node replacement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM structural_nodes WHERE edition_id = ?`, editionID); err != nil {
		return fmt.Errorf("clear structural nodes: %w", err)
	}
	for _, node := range nodes {
		var parent any
		if node.ParentID != nil {
			parent = *node.ParentID
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO structural_nodes (id, edition_id, parent_id, kind, ordinal, title, start_offset, end_offset, explicit)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, node.ID, node.EditionID, parent, node.Kind, node.Ordinal, node.Title, node.StartOffset, node.EndOffset, node.Explicit); err != nil {
			return fmt.Errorf("insert structural node: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit structural nodes: %w", err)
	}
	return nil
}

func (s *Store) ListStructuralNodes(ctx context.Context, editionID string) ([]library.StructuralNode, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, edition_id, parent_id, kind, ordinal, title, start_offset, end_offset, explicit
FROM structural_nodes WHERE edition_id = ? ORDER BY ordinal, id
`, editionID)
	if err != nil {
		return nil, fmt.Errorf("list structural nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	nodes := []library.StructuralNode{}
	for rows.Next() {
		var node library.StructuralNode
		var parent sql.NullString
		if err := rows.Scan(&node.ID, &node.EditionID, &parent, &node.Kind, &node.Ordinal, &node.Title, &node.StartOffset, &node.EndOffset, &node.Explicit); err != nil {
			return nil, fmt.Errorf("scan structural node: %w", err)
		}
		if parent.Valid {
			node.ParentID = &parent.String
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}
