package engine_test

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestBookReconsolidationCreatesSuccessorAndPreservesProvenance(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "reconsolidate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer := engine.NewWritePipeline(store)
	first, err := writer.Write(ctx, engine.WriteInput{Workspace: "books", Type: core.SemanticMemory, Content: "Original interpretation", Source: core.MemorySource{Type: core.SourceReflection}, Mode: engine.ExtractFast})
	if err != nil || first.Rejected {
		t.Fatalf("write original: %+v %v", first, err)
	}
	provenance := core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionReader, SubjectID: "reader"}, Form: core.KnowledgeInsight, Derivation: core.DerivationDiscussed, CitationIDs: []string{"citation-1"}}
	proposal := core.BookMemoryProposal{ID: "proposal", Workspace: "books", RequestedBy: core.Principal{ID: "reader", Kind: core.PrincipalUser}, MemoryType: core.SemanticMemory, Content: "Original interpretation", Provenance: provenance, Confidence: .8, Status: core.ProposalSuggested, CreatedAt: time.Now().UTC()}
	if err := store.PutBookMemoryProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBookMemoryLineage(ctx, core.BookMemoryLineage{MemoryID: first.ID, ProposalID: "proposal", Provenance: provenance, CitationIDs: []string{"citation-1"}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	record, err := engine.NewBookReconsolidator(writer, store).Run(ctx, engine.BookReconsolidationInput{ID: "reconsolidation", Workspace: "books", PreviousMemoryID: first.ID, Content: "Clarified interpretation", MemoryType: core.SemanticMemory, Action: core.ReconsolidateClarified, AdditionalCitationIDs: []string{"citation-2"}, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if record.NewMemoryID == first.ID || len(record.CitationIDs) != 2 {
		t.Fatalf("reconsolidation overwrote or dropped citations: %+v", record)
	}
	oldLineage, err := store.GetBookMemoryLineage(ctx, first.ID)
	if err != nil || len(oldLineage.CitationIDs) != 1 {
		t.Fatal("historical lineage was mutated")
	}
	newLineage, err := store.GetBookMemoryLineage(ctx, record.NewMemoryID)
	if err != nil || len(newLineage.Provenance.DerivedFrom) == 0 {
		t.Fatalf("successor lineage missing: %+v err=%v", newLineage, err)
	}
}
