// Package application exposes transport-neutral agent-memory operations.
package application

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// MemoryService is the canonical write and single-workspace search surface
// shared by CLI, HTTP, and protocol adapters.
type MemoryService struct {
	store     *sqlite.Store
	writer    *engine.WritePipeline
	retrieval *engine.RetrievalEngine
}

type FeedbackInput struct {
	MemoryID              string
	Outcome               core.RetrievalFeedback
	OccurredAt            time.Time
	ReconsolidationAction core.ReconsolidationAction
	SuccessorMemoryID     string
}

func (s *MemoryService) Feedback(ctx context.Context, input FeedbackInput) (*core.MemoryEntry, error) {
	at := input.OccurredAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	updated, err := s.store.ApplyRetrievalFeedback(ctx, strings.TrimSpace(input.MemoryID), input.Outcome, at)
	if err != nil {
		return nil, err
	}
	if input.ReconsolidationAction != "" {
		updated, err = s.store.ApplyReconsolidation(ctx, strings.TrimSpace(input.MemoryID), input.ReconsolidationAction, strings.TrimSpace(input.SuccessorMemoryID), at)
		if err == nil {
			_, err = s.store.AppendAuditEvent(ctx, sqlite.AuditEventInput{Workspace: updated.Workspace, Operation: string(input.ReconsolidationAction), Outcome: "success", Actor: "application", TargetType: "memory", TargetIDs: []string{input.MemoryID, input.SuccessorMemoryID}, OccurredAt: at})
		}
		return updated, err
	}
	return updated, nil
}

func NewMemoryService(store *sqlite.Store, writer *engine.WritePipeline, retrieval *engine.RetrievalEngine) *MemoryService {
	return &MemoryService{store: store, writer: writer, retrieval: retrieval}
}

func (s *MemoryService) Write(ctx context.Context, input engine.WriteInput) (*engine.WriteResult, error) {
	return s.writer.Write(ctx, input)
}

func (s *MemoryService) Delete(ctx context.Context, workspace string, memoryIDs []string, actor, source, requestID, reason string) error {
	return s.store.DeleteByIDsAudited(ctx, memoryIDs, sqlite.AuditEventInput{Workspace: workspace, Operation: "delete", Outcome: "success", Actor: actor, Source: source, RequestID: requestID, Reason: reason, OccurredAt: time.Now().UTC()})
}

func (s *MemoryService) Search(ctx context.Context, options engine.RetrievalOptions) (*engine.RetrievalResult, error) {
	requestID := uuid.NewString()
	if s.store != nil {
		_ = s.store.LogRetrievalRequest(ctx, requestID, options.Workspace, "search", options.Query)
	}
	result, err := s.retrieval.Retrieve(ctx, options)
	if err != nil {
		return nil, err
	}
	result.RequestID = requestID
	if s.store != nil {
		tokens := hitTokens(result.Hits)
		_ = s.store.AddTokenMetricV2(ctx, options.Workspace, "search", tokens, tokens, engine.RunLabel(), engine.MemoryEnabled())
	}
	return result, nil
}

func hitTokens(hits []engine.RetrievalHit) int {
	total := 0
	for _, hit := range hits {
		total += len(strings.Fields(hit.Memory.Content))
	}
	return total
}
