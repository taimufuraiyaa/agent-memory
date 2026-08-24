// Package memory implements tenant-authorized hosted memory operations.
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

var (
	ErrForbidden           = errors.New("memory operation is forbidden")
	ErrIdempotencyConflict = errors.New("idempotency key was reused with different input")
)

type Command struct {
	WorkspaceID    string
	Type           core.MemoryType
	Content        string
	Source         core.MemorySource
	Entities       []string
	Tags           []string
	Keywords       []core.MemoryTerm
	Outcome        *core.Outcome
	IdempotencyKey string
}

type Write struct {
	TenantID       string
	ActorID        string
	RequestID      string
	IdempotencyKey string
	RequestHash    string
	ContentHash    string
	Memory         core.MemoryEntry
}

type Repository interface {
	WriteMemory(context.Context, Write) (core.MemoryEntry, bool, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, now: now}
}

func (s *Service) Write(ctx context.Context, command Command) (core.MemoryEntry, bool, error) {
	if s == nil || s.repository == nil {
		return core.MemoryEntry{}, false, errors.New("hosted memory repository is not configured")
	}
	request, ok := auth.FromContext(ctx)
	if !ok || request.AccountID == "" || request.TenantID == "" || !request.Can("memory:write") {
		return core.MemoryEntry{}, false, ErrForbidden
	}
	command.WorkspaceID = strings.TrimSpace(command.WorkspaceID)
	command.Content = strings.TrimSpace(command.Content)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 {
		return core.MemoryEntry{}, false, errors.New("idempotency key must contain 16 to 128 characters")
	}
	if len(command.Content) > 2000 || !validSource(command.Source.Type) {
		return core.MemoryEntry{}, false, errors.New("memory content or source is invalid")
	}
	now := s.now().UTC()
	entry := core.MemoryEntry{
		ID: uuid.NewString(), Type: command.Type, Content: command.Content, Workspace: command.WorkspaceID,
		Source: command.Source, Entities: append([]string{}, command.Entities...), Tags: append([]string{}, command.Tags...),
		Keywords: append([]core.MemoryTerm{}, command.Keywords...), Outcome: command.Outcome,
		Confidence: 0.8, StorageTier: core.TierVector, CreatedAt: now, UpdatedAt: now,
	}
	if err := entry.Validate(); err != nil {
		return core.MemoryEntry{}, false, err
	}
	canonical, err := json.Marshal(struct {
		Workspace string            `json:"workspace"`
		Type      core.MemoryType   `json:"type"`
		Content   string            `json:"content"`
		Source    core.MemorySource `json:"source"`
		Entities  []string          `json:"entities"`
		Tags      []string          `json:"tags"`
		Keywords  []core.MemoryTerm `json:"keywords"`
		Outcome   *core.Outcome     `json:"outcome"`
	}{entry.Workspace, entry.Type, entry.Content, entry.Source, entry.Entities, entry.Tags, entry.Keywords, entry.Outcome})
	if err != nil {
		return core.MemoryEntry{}, false, err
	}
	return s.repository.WriteMemory(ctx, Write{
		TenantID: request.TenantID, ActorID: request.AccountID, RequestID: request.RequestID,
		IdempotencyKey: command.IdempotencyKey, RequestHash: digest(canonical),
		ContentHash: digest([]byte(request.TenantID + "|" + entry.Workspace + "|" + string(entry.Type) + "|" + entry.Content)), Memory: entry,
	})
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func validSource(source core.SourceType) bool {
	switch source {
	case core.SourceAgentObservation, core.SourceUserInput, core.SourceCodeAnalysis, core.SourceConsolidation, core.SourceReflection, core.SourceReconstruction, core.SourceImport:
		return true
	default:
		return false
	}
}
