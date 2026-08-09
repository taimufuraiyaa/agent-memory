package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type recordingRepository struct {
	write Write
	err   error
}

func (r *recordingRepository) WriteMemory(_ context.Context, write Write) (core.MemoryEntry, bool, error) {
	r.write = write
	return write.Memory, false, r.err
}

func TestServiceWritesUsingAuthenticatedTenant(t *testing.T) {
	repository := &recordingRepository{}
	now := time.Date(2026, 8, 5, 13, 0, 0, 0, time.UTC)
	service := NewService(repository, func() time.Time { return now })
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{
		AccountID: "account-1", TenantID: "tenant-1", RequestID: "request-1",
		Capabilities: map[string]struct{}{"memory:write": {}},
	})

	memory, duplicate, err := service.Write(ctx, Command{
		WorkspaceID: "workspace-1", Type: core.SemanticMemory, Content: "  PostgreSQL is authoritative.  ",
		Source: core.MemorySource{Type: core.SourceUserInput}, IdempotencyKey: "idempotency-key-1",
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if duplicate || memory.ID == "" || memory.Content != "PostgreSQL is authoritative." {
		t.Fatalf("unexpected memory result: %+v duplicate=%v", memory, duplicate)
	}
	if repository.write.TenantID != "tenant-1" || repository.write.ActorID != "account-1" || repository.write.RequestHash == "" || repository.write.ContentHash == "" {
		t.Fatalf("incomplete repository write: %+v", repository.write)
	}
}

func TestServiceRejectsMissingCapability(t *testing.T) {
	service := NewService(&recordingRepository{}, time.Now)
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{AccountID: "account", TenantID: "tenant", Capabilities: map[string]struct{}{}})
	_, _, err := service.Write(ctx, Command{WorkspaceID: "workspace", Type: core.SemanticMemory, Content: "content", IdempotencyKey: "key"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Write() error = %v, want ErrForbidden", err)
	}
}
