package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestBookMemoryProposalRequiresAcceptanceBeforeDurableWrite(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "book-memory.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()
	service := NewBookMemoryService(NewWritePipeline(store), store)
	statement := readingroom.AnswerStatement{
		ID: "statement-1", Text: "Different paths may lead to the same outcome.", EvidenceState: readingroom.EvidenceInterpretation,
		Provenance: core.KnowledgeProvenance{
			Attribution: core.Attribution{Kind: core.AttributionAgent, SubjectID: "agent-1"},
			Form:        core.KnowledgeInsight, Derivation: core.DerivationDiscussed,
		},
		Confidence: 0.8,
	}
	proposal, err := service.Propose(ctx, BookMemoryProposalInput{
		ID: "proposal-1", Workspace: "book-workspace", RequestedBy: core.Principal{ID: "user-1", Kind: core.PrincipalUser},
		MemoryType: core.SemanticMemory, Statement: statement, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if count, _ := store.CountMemories(ctx); count != 0 || proposal.Status != core.ProposalSuggested {
		t.Fatalf("proposal must not write durable memory: count=%d proposal=%+v", count, proposal)
	}

	accepted, err := service.Accept(ctx, proposal.ID, core.Principal{ID: "user-1", Kind: core.PrincipalUser})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if count, _ := store.CountMemories(ctx); count != 1 || accepted.MemoryID == "" || accepted.Status != core.ProposalAccepted {
		t.Fatalf("accepted proposal should create one memory: count=%d proposal=%+v", count, accepted)
	}
	lineage, err := store.GetBookMemoryLineage(ctx, accepted.MemoryID)
	if err != nil || lineage.Provenance.Form != core.KnowledgeInsight || lineage.ProposalID != proposal.ID {
		t.Fatalf("expected provenance lineage: %+v err=%v", lineage, err)
	}
}

func TestRejectedBookMemoryProposalDoesNotWriteMemory(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "book-memory-reject.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()
	service := NewBookMemoryService(NewWritePipeline(store), store)
	statement := readingroom.AnswerStatement{
		ID: "statement-1", Text: "Temporary thought", EvidenceState: readingroom.EvidenceInterpretation,
		Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionReader, SubjectID: "user-1"}, Form: core.KnowledgeNote, Derivation: core.DerivationDiscussed},
	}
	_, err = service.Propose(ctx, BookMemoryProposalInput{ID: "proposal-1", Workspace: "book-workspace", RequestedBy: core.Principal{ID: "user-1", Kind: core.PrincipalUser}, MemoryType: core.SemanticMemory, Statement: statement, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := service.Reject(ctx, "proposal-1", core.Principal{ID: "user-1", Kind: core.PrincipalUser}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if count, _ := store.CountMemories(ctx); count != 0 {
		t.Fatalf("rejection must not create memory: %d", count)
	}
}
