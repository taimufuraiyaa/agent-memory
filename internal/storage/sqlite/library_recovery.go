package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) PutLibraryImportJob(ctx context.Context, id, workspace, state, payload string, createdAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO library_import_jobs(id,workspace,state,job_json,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,job_json=excluded.job_json,updated_at=excluded.updated_at`, id, workspace, state, payload, createdAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) GetLibraryImportJob(ctx context.Context, id string) (string, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT job_json FROM library_import_jobs WHERE id=?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("library import job not found")
	}
	return payload, err
}
func (s *Store) RecoverLibraryImportJobs(ctx context.Context) (int, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE library_import_jobs SET state='queued',job_json=json_set(job_json,'$.state','queued'),updated_at=? WHERE state='running'`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}
func (s *Store) RebuildLibraryIndexes(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `REINDEX idx_source_passages_edition`)
	return err
}
func (s *Store) ApplySourceRetention(ctx context.Context, assetID string, mode core.RetentionMode, at time.Time) error {
	if at.IsZero() {
		return errors.New("retention action time is required")
	}
	switch mode {
	case core.RetentionRetained:
		return nil
	case core.RetentionOnDemand, core.RetentionSessionOnly, core.RetentionDeleted:
	default:
		return errors.New("invalid retention action")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO source_deletions(source_asset_id,mode,deleted_at) VALUES(?,?,?) ON CONFLICT(source_asset_id) DO UPDATE SET mode=excluded.mode,deleted_at=excluded.deleted_at`, assetID, mode, at.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if mode == core.RetentionDeleted {
		asset, err := s.GetSourceAsset(ctx, assetID)
		if err != nil {
			return err
		}
		asset.Policy = core.SourcePolicy{Retention: core.RetentionDeleted}
		return s.PutSourceAsset(ctx, asset)
	}
	return nil
}
func (s *Store) IsSourceAvailable(ctx context.Context, assetID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM source_deletions WHERE source_asset_id=?`, assetID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}
