package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type catalogRepositoryFixture struct {
	records []SourceRecord
	retried string
}

func (r *catalogRepositoryFixture) ListSources(context.Context, string, string) ([]SourceRecord, error) {
	return append([]SourceRecord(nil), r.records...), nil
}
func (r *catalogRepositoryFixture) GetSource(_ context.Context, _, sourceID string) (SourceRecord, error) {
	for _, record := range r.records {
		if record.ID == sourceID {
			return record, nil
		}
	}
	return SourceRecord{}, auth.ErrTenantUnavailable
}
func (r *catalogRepositoryFixture) RetrySource(_ context.Context, _, sourceID string, _ time.Time) error {
	for _, record := range r.records {
		if record.ID == sourceID && record.State == "failed" && record.HasRetainedOriginal && record.SafeErrorCode == "extraction_failed" {
			r.retried = sourceID
			return nil
		}
	}
	return auth.ErrTenantUnavailable
}

func TestCatalogReturnsEveryProgressStateWithSafeActionableDetails(t *testing.T) {
	now := time.Date(2026, 8, 5, 21, 0, 0, 0, time.UTC)
	repository := &catalogRepositoryFixture{records: []SourceRecord{
		{ID: "source-upload", WorkspaceID: "workspace", Filename: "upload.pdf", State: "uploading", RightsBasis: "lawfully_acquired_private_use", AttestationPolicyVersion: "rights-v1", RetentionState: "pending", CreatedAt: now, UpdatedAt: now},
		{ID: "source-failed", WorkspaceID: "workspace", Filename: "failed.epub", State: "failed", RightsBasis: "author_owned", AttestationPolicyVersion: "rights-v1", SafeErrorCode: "extraction_failed", HasRetainedOriginal: true, ActiveVersion: 1, ParserVersion: "epub-v1", NormalizationVersion: "unicode-text-v1", RetentionState: "retained_private_vault", CreatedAt: now, UpdatedAt: now},
		{ID: "source-unsafe", WorkspaceID: "workspace", Filename: "unsafe.txt", State: "failed", RightsBasis: "licensed", AttestationPolicyVersion: "rights-v1", SafeErrorCode: "postgres_password=secret source text leaked", CreatedAt: now, UpdatedAt: now},
	}}
	service := NewCatalogService(repository, func() time.Time { return now })
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: "tenant", Capabilities: map[string]struct{}{"source:read": {}, "source:write": {}}})

	views, err := service.List(ctx, "workspace")
	if err != nil || len(views) != 3 {
		t.Fatalf("views=%+v err=%v", views, err)
	}
	if views[0].Progress.Label != "Uploading" || views[0].Progress.Percent != 15 {
		t.Fatalf("upload progress=%+v", views[0].Progress)
	}
	failed := views[1]
	if !failed.Failure.RetryAllowed || failed.Failure.Code != "extraction_failed" || failed.Failure.Action == "" {
		t.Fatalf("failed source guidance=%+v", failed.Failure)
	}
	unsafe := views[2]
	if unsafe.Failure.Code != "processing_failed" || unsafe.Failure.Message == "postgres_password=secret source text leaked" || unsafe.Failure.RetryAllowed {
		t.Fatalf("unsafe failure escaped=%+v", unsafe.Failure)
	}
	encoded, err := json.Marshal(views)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"vault_object_key", "postgres_password", "source text leaked"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("catalog serialized forbidden value %q: %s", forbidden, encoded)
		}
	}
}

func TestCatalogRetryRequiresWriteCapabilityAndRetainedRetryableOriginal(t *testing.T) {
	repository := &catalogRepositoryFixture{records: []SourceRecord{{ID: "retryable", State: "failed", SafeErrorCode: "extraction_failed", HasRetainedOriginal: true}}}
	service := NewCatalogService(repository, nil)
	readOnly := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: "tenant", Capabilities: map[string]struct{}{"source:read": {}}})
	if err := service.Retry(readOnly, "retryable"); !errors.Is(err, auth.ErrTenantUnavailable) {
		t.Fatalf("read-only retry error=%v", err)
	}
	writer := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: "tenant", Capabilities: map[string]struct{}{"source:read": {}, "source:write": {}}})
	if err := service.Retry(writer, "retryable"); err != nil || repository.retried != "retryable" {
		t.Fatalf("retry=%q err=%v", repository.retried, err)
	}
	if _, err := service.Get(writer, "other-tenant-id"); !errors.Is(err, auth.ErrTenantUnavailable) {
		t.Fatalf("missing source error=%v", err)
	}
}

func TestSafeFailureExplainsUnreadablePDFTextAndRetainedRetry(t *testing.T) {
	failure := safeFailure("pdf_text_unreadable", true)
	if !failure.RetryAllowed || failure.Code != "pdf_text_unreadable" || !strings.Contains(strings.ToLower(failure.Message+" "+failure.Action), "text") {
		t.Fatalf("failure=%+v", failure)
	}
}
