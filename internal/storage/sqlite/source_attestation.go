package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type SourceAttestation struct {
	SourceAssetID     string    `json:"source_asset_id"`
	SubjectID         string    `json:"subject_id"`
	ReceiptID         string    `json:"receipt_id"`
	PolicyVersion     string    `json:"policy_version"`
	RightsBasis       string    `json:"rights_basis"`
	SourceFingerprint string    `json:"source_fingerprint"`
	RecordedAt        time.Time `json:"recorded_at"`
}

func (s *Store) PutSourceAttestation(ctx context.Context, provenance SourceAttestation) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO source_attestations
		(source_asset_id, subject_id, receipt_id, policy_version, rights_basis, source_fingerprint, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, provenance.SourceAssetID, provenance.SubjectID, provenance.ReceiptID,
		provenance.PolicyVersion, provenance.RightsBasis, provenance.SourceFingerprint,
		provenance.RecordedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetSourceAttestation(ctx context.Context, sourceAssetID string) (SourceAttestation, error) {
	var provenance SourceAttestation
	var recordedAt string
	err := s.db.QueryRowContext(ctx, `SELECT source_asset_id, subject_id, receipt_id, policy_version, rights_basis, source_fingerprint, recorded_at
		FROM source_attestations WHERE source_asset_id = ?`, sourceAssetID).Scan(
		&provenance.SourceAssetID, &provenance.SubjectID, &provenance.ReceiptID, &provenance.PolicyVersion,
		&provenance.RightsBasis, &provenance.SourceFingerprint, &recordedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceAttestation{}, sql.ErrNoRows
	}
	if err != nil {
		return SourceAttestation{}, err
	}
	provenance.RecordedAt, err = time.Parse(time.RFC3339Nano, recordedAt)
	return provenance, err
}
