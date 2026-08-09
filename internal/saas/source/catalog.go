package source

import (
	"context"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type SourceRecord struct {
	ID                       string
	WorkspaceID              string
	Filename                 string
	MediaType                string
	State                    string
	RightsBasis              string
	AttestationReceiptID     string
	AttestationPolicyVersion string
	AttestationAcceptedAt    time.Time
	AttestationExpiresAt     time.Time
	ActiveVersion            int64
	ContentSHA256            string
	ParserVersion            string
	NormalizationVersion     string
	VaultEncryptionVersion   string
	PublishedAt              *time.Time
	SafeErrorCode            string
	HasRetainedOriginal      bool
	RetentionState           string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type SourceProgress struct {
	State   string `json:"state"`
	Label   string `json:"label"`
	Percent int    `json:"percent"`
}

type SourceFailure struct {
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
	Action       string `json:"action,omitempty"`
	RetryAllowed bool   `json:"retry_allowed"`
}

type SourceAttestation struct {
	ReceiptID     string    `json:"receipt_id"`
	PolicyVersion string    `json:"policy_version"`
	AcceptedAt    time.Time `json:"accepted_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type SourceProvenance struct {
	SourceVersion          int64      `json:"source_version"`
	ContentSHA256          string     `json:"content_sha256,omitempty"`
	ParserVersion          string     `json:"parser_version,omitempty"`
	NormalizationVersion   string     `json:"normalization_version,omitempty"`
	VaultEncryptionVersion string     `json:"vault_encryption_version,omitempty"`
	PublishedAt            *time.Time `json:"published_at,omitempty"`
}

type SourceView struct {
	ID             string            `json:"id"`
	WorkspaceID    string            `json:"workspace_id"`
	Filename       string            `json:"filename"`
	MediaType      string            `json:"media_type"`
	State          string            `json:"state"`
	Progress       SourceProgress    `json:"progress"`
	Failure        SourceFailure     `json:"failure"`
	RightsBasis    string            `json:"rights_basis"`
	Attestation    SourceAttestation `json:"attestation"`
	Provenance     SourceProvenance  `json:"provenance"`
	RetentionState string            `json:"retention_state"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type CatalogRepository interface {
	ListSources(context.Context, string, string) ([]SourceRecord, error)
	GetSource(context.Context, string, string) (SourceRecord, error)
	RetrySource(context.Context, string, string, time.Time) error
}

type CatalogService struct {
	repository CatalogRepository
	now        func() time.Time
}

func NewCatalogService(repository CatalogRepository, now func() time.Time) *CatalogService {
	if now == nil {
		now = time.Now
	}
	return &CatalogService{repository: repository, now: now}
}

func (s *CatalogService) List(ctx context.Context, workspaceID string) ([]SourceView, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("source:read") || s == nil || s.repository == nil {
		return nil, auth.ErrTenantUnavailable
	}
	records, err := s.repository.ListSources(ctx, request.TenantID, strings.TrimSpace(workspaceID))
	if err != nil {
		return nil, err
	}
	result := make([]SourceView, 0, len(records))
	for _, record := range records {
		result = append(result, sourceView(record))
	}
	return result, nil
}

func (s *CatalogService) Get(ctx context.Context, sourceID string) (SourceView, error) {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("source:read") || s == nil || s.repository == nil || strings.TrimSpace(sourceID) == "" {
		return SourceView{}, auth.ErrTenantUnavailable
	}
	record, err := s.repository.GetSource(ctx, request.TenantID, sourceID)
	if err != nil {
		return SourceView{}, err
	}
	return sourceView(record), nil
}

func (s *CatalogService) Retry(ctx context.Context, sourceID string) error {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("source:write") || s == nil || s.repository == nil || strings.TrimSpace(sourceID) == "" {
		return auth.ErrTenantUnavailable
	}
	return s.repository.RetrySource(ctx, request.TenantID, sourceID, s.now().UTC())
}

func sourceView(record SourceRecord) SourceView {
	return SourceView{
		ID: record.ID, WorkspaceID: record.WorkspaceID, Filename: record.Filename, MediaType: record.MediaType,
		State: record.State, Progress: progressForState(record.State), Failure: safeFailure(record.SafeErrorCode, record.HasRetainedOriginal),
		RightsBasis:    record.RightsBasis,
		Attestation:    SourceAttestation{ReceiptID: record.AttestationReceiptID, PolicyVersion: record.AttestationPolicyVersion, AcceptedAt: record.AttestationAcceptedAt, ExpiresAt: record.AttestationExpiresAt},
		Provenance:     SourceProvenance{SourceVersion: record.ActiveVersion, ContentSHA256: record.ContentSHA256, ParserVersion: pendingAsEmpty(record.ParserVersion), NormalizationVersion: pendingAsEmpty(record.NormalizationVersion), VaultEncryptionVersion: record.VaultEncryptionVersion, PublishedAt: record.PublishedAt},
		RetentionState: record.RetentionState, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func progressForState(state string) SourceProgress {
	progress := map[string]SourceProgress{
		"pending":    {State: "uploading", Label: "Waiting for upload", Percent: 5},
		"uploading":  {State: "uploading", Label: "Uploading", Percent: 15},
		"validating": {State: "validating", Label: "Validating", Percent: 35},
		"processing": {State: "processing", Label: "Processing", Percent: 60},
		"indexing":   {State: "indexing", Label: "Indexing", Percent: 85},
		"ready":      {State: "ready", Label: "Ready", Percent: 100},
		"failed":     {State: "failed", Label: "Needs attention", Percent: 0},
		"disabled":   {State: "disabled", Label: "Disabled", Percent: 0},
		"deleting":   {State: "deleting", Label: "Deleting", Percent: 0},
		"deleted":    {State: "deleting", Label: "Deleted", Percent: 0},
	}
	if value, ok := progress[state]; ok {
		return value
	}
	return SourceProgress{State: "failed", Label: "Status unavailable", Percent: 0}
}

func safeFailure(code string, retained bool) SourceFailure {
	code = strings.TrimSpace(code)
	switch code {
	case "":
		return SourceFailure{}
	case "extraction_failed":
		return SourceFailure{Code: code, Message: "We could not process this source.", Action: "Retry processing or upload a different copy.", RetryAllowed: retained}
	case "source_unavailable":
		return SourceFailure{Code: code, Message: "The retained source could not be read.", Action: "Retry processing. If it fails again, upload a new copy.", RetryAllowed: retained}
	case "format_unsupported", "signature_mismatch", "text_invalid":
		return SourceFailure{Code: code, Message: "This file is not a supported or valid source.", Action: "Upload a valid PDF, EPUB, Markdown, or plain-text file."}
	case "size_mismatch", "checksum_mismatch":
		return SourceFailure{Code: code, Message: "The uploaded file did not match the upload request.", Action: "Upload the file again."}
	case "malware_detected":
		return SourceFailure{Code: code, Message: "The upload was rejected by the security scan.", Action: "Check the file locally before uploading another copy."}
	case "grant_expired":
		return SourceFailure{Code: code, Message: "The upload window expired.", Action: "Start a new upload."}
	default:
		return SourceFailure{Code: "processing_failed", Message: "This source could not be processed safely.", Action: "Upload a new copy or contact support with the request ID."}
	}
}

func pendingAsEmpty(value string) string {
	if value == "pending" {
		return ""
	}
	return value
}
