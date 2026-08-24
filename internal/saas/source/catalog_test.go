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
	tasks   []ProcessingTaskRecord
	retried string
}

func (r *catalogRepositoryFixture) ListSources(context.Context, string, string) ([]SourceRecord, error) {
	return append([]SourceRecord(nil), r.records...), nil
}
func (r *catalogRepositoryFixture) ListSourceStatuses(context.Context, string, string) ([]SourceStatusRecord, error) {
	result := make([]SourceStatusRecord, 0, len(r.records))
	for _, record := range r.records {
		result = append(result, SourceStatusRecord{ID: record.ID, State: record.State, SafeErrorCode: record.SafeErrorCode, HasRetainedOriginal: record.HasRetainedOriginal, UpdatedAt: record.UpdatedAt})
	}
	return result, nil
}
func (r *catalogRepositoryFixture) ListProcessingTasks(context.Context, string, string) ([]ProcessingTaskRecord, error) {
	return append([]ProcessingTaskRecord(nil), r.tasks...), nil
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

func TestCatalogExcludesCompletedDeletionTombstones(t *testing.T) {
	repository := &catalogRepositoryFixture{records: []SourceRecord{
		{ID: "active", State: "ready"},
		{ID: "completed-deletion", State: "deleted"},
	}}
	service := NewCatalogService(repository, nil)
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: "tenant", Capabilities: map[string]struct{}{"source:read": {}}})

	views, err := service.List(ctx, "workspace")
	if err != nil || len(views) != 1 || views[0].ID != "active" {
		t.Fatalf("catalog views=%+v err=%v", views, err)
	}
}

func TestCatalogStatusListReturnsOnlyLifecycleInformation(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	repository := &catalogRepositoryFixture{records: []SourceRecord{{
		ID: "source-processing", WorkspaceID: "workspace", Filename: "private.pdf", MediaType: "application/pdf",
		State: "processing", RightsBasis: "licensed", AttestationReceiptID: "receipt-secret", ContentSHA256: strings.Repeat("a", 64),
		SafeErrorCode: "", HasRetainedOriginal: true, RetentionState: "retained_private_vault", UpdatedAt: now,
	}}}
	service := NewCatalogService(repository, nil)
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: "tenant", Capabilities: map[string]struct{}{"source:read": {}}})

	statuses, err := service.ListStatuses(ctx, "workspace")
	if err != nil || len(statuses) != 1 {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	encoded, err := json.Marshal(statuses)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"filename", "media_type", "rights_basis", "attestation", "provenance", "retention_state", "workspace_id", "created_at", "private.pdf", "receipt-secret"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("status response contains catalog-only value %q: %s", forbidden, encoded)
		}
	}
	for _, required := range []string{"source-processing", "state", "progress", "failure", "updated_at"} {
		if !bytes.Contains(encoded, []byte(required)) {
			t.Fatalf("status response missing %q: %s", required, encoded)
		}
	}
}

func TestProcessingTasksNormalizeLifecycleAndSerializeOnlySafeTrackingFields(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	repository := &catalogRepositoryFixture{tasks: []ProcessingTaskRecord{
		{ID: "source:one", Kind: "source_ingestion", SubjectID: "one", Title: "handbook.pdf", State: "processing", SafeErrorCode: "", HasRetainedOriginal: true, CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
		{ID: "delete:two", Kind: "source_deletion", SubjectID: "two", Title: "", State: "completed", SafeErrorCode: "raw database detail", CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
	}}
	service := NewCatalogService(repository, nil)
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: "tenant", Capabilities: map[string]struct{}{"source:read": {}}})

	tasks, err := service.ListProcessingTasks(ctx, "workspace")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	if tasks[0].State != "running" || tasks[0].Progress.Percent != 60 || tasks[0].Title != "handbook.pdf" {
		t.Fatalf("running task=%+v", tasks[0])
	}
	if tasks[1].State != "completed" || tasks[1].Progress.Percent != 100 || tasks[1].Title != "Private source" {
		t.Fatalf("deletion task=%+v", tasks[1])
	}
	encoded, err := json.Marshal(tasks)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"raw database detail", "rights_basis", "attestation", "receipt_sha256", "vault_object_key", "content_sha256"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("processing response contains %q: %s", forbidden, encoded)
		}
	}
}

func TestProcessingTasksRequireSourceReadCapability(t *testing.T) {
	service := NewCatalogService(&catalogRepositoryFixture{}, nil)
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: "tenant", Capabilities: map[string]struct{}{}})
	if _, err := service.ListProcessingTasks(ctx, "workspace"); !errors.Is(err, auth.ErrTenantUnavailable) {
		t.Fatalf("error=%v", err)
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
