package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func (s *Store) GetSourceAsset(ctx context.Context, id string) (library.SourceAsset, error) {
	var asset library.SourceAsset
	var policyJSON, importedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT id, edition_id, format, byte_fingerprint, normalized_fingerprint, parser_version, policy_json, imported_at
FROM source_assets WHERE id = ?
`, id).Scan(&asset.ID, &asset.EditionID, &asset.Format, &asset.ByteFingerprint, &asset.NormalizedFingerprint,
		&asset.ParserVersion, &policyJSON, &importedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return library.SourceAsset{}, fmt.Errorf("source asset not found: %s", id)
	}
	if err != nil {
		return library.SourceAsset{}, fmt.Errorf("get source asset: %w", err)
	}
	if err := json.Unmarshal([]byte(policyJSON), &asset.Policy); err != nil {
		return library.SourceAsset{}, fmt.Errorf("decode source policy: %w", err)
	}
	asset.ImportedAt, err = time.Parse(time.RFC3339Nano, importedAt)
	if err != nil {
		return library.SourceAsset{}, fmt.Errorf("parse source import time: %w", err)
	}
	return asset, nil
}

func (s *Store) PutPassages(ctx context.Context, passages []library.Passage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin passage write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, passage := range passages {
		if err := passage.Validate(); err != nil {
			return err
		}
		locator, err := json.Marshal(passage.Locator)
		if err != nil {
			return fmt.Errorf("encode passage locator: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO source_passages (id, edition_id, source_asset_id, structural_node_id, text, locator_json, fingerprint)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET text = excluded.text, locator_json = excluded.locator_json, fingerprint = excluded.fingerprint
`, passage.ID, passage.EditionID, passage.SourceAssetID, passage.StructuralNodeID, passage.Text, string(locator), passage.Fingerprint); err != nil {
			return fmt.Errorf("put passage: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) GetPassage(ctx context.Context, id string) (library.Passage, error) {
	var passage library.Passage
	var locatorJSON string
	err := s.db.QueryRowContext(ctx, `
SELECT id, edition_id, source_asset_id, structural_node_id, text, locator_json, fingerprint
FROM source_passages WHERE id = ?
`, id).Scan(&passage.ID, &passage.EditionID, &passage.SourceAssetID, &passage.StructuralNodeID,
		&passage.Text, &locatorJSON, &passage.Fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return library.Passage{}, fmt.Errorf("passage not found: %s", id)
	}
	if err != nil {
		return library.Passage{}, fmt.Errorf("get passage: %w", err)
	}
	if err := json.Unmarshal([]byte(locatorJSON), &passage.Locator); err != nil {
		return library.Passage{}, fmt.Errorf("decode passage locator: %w", err)
	}
	return passage, nil
}

func (s *Store) ListPassages(ctx context.Context, editionID string) ([]library.Passage, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id FROM source_passages
WHERE edition_id = ? AND source_asset_id NOT IN (SELECT source_asset_id FROM source_deletions)
ORDER BY id
`, editionID)
	if err != nil {
		return nil, fmt.Errorf("list passages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	passages := []library.Passage{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		passage, err := s.GetPassage(ctx, id)
		if err != nil {
			return nil, err
		}
		passages = append(passages, passage)
	}
	return passages, rows.Err()
}

func (s *Store) ListPassagesForEditions(ctx context.Context, editionIDs []string) ([]library.Passage, error) {
	if len(editionIDs) == 0 {
		return []library.Passage{}, nil
	}
	placeholders := make([]string, len(editionIDs))
	args := make([]any, len(editionIDs))
	for index, editionID := range editionIDs {
		placeholders[index] = "?"
		args[index] = editionID
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM source_passages WHERE edition_id IN (`+strings.Join(placeholders, ",")+`) AND source_asset_id NOT IN (SELECT source_asset_id FROM source_deletions) ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list authorized passages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	passages := []library.Passage{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		passage, err := s.GetPassage(ctx, id)
		if err != nil {
			return nil, err
		}
		passages = append(passages, passage)
	}
	return passages, rows.Err()
}

func (s *Store) PutCitation(ctx context.Context, citation core.Citation) error {
	if err := citation.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(citation)
	if err != nil {
		return fmt.Errorf("encode citation: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO source_citations (id, passage_id, citation_json, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET citation_json = excluded.citation_json
`, citation.ID, citation.PassageID, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("put citation: %w", err)
	}
	return nil
}

func (s *Store) GetCitation(ctx context.Context, id string) (core.Citation, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT citation_json FROM source_citations WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Citation{}, fmt.Errorf("citation not found: %s", id)
	}
	if err != nil {
		return core.Citation{}, fmt.Errorf("get citation: %w", err)
	}
	var citation core.Citation
	if err := json.Unmarshal([]byte(payload), &citation); err != nil {
		return core.Citation{}, fmt.Errorf("decode citation: %w", err)
	}
	return citation, nil
}
