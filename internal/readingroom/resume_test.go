package readingroom_test

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionResumeSeparatesConversationKnowledgeAndProgress(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := core.Principal{ID: "reader", Kind: core.PrincipalUser}
	scope := core.AuthorizationScope{Principal: owner, Capabilities: []core.Capability{core.CapabilityDiscuss}, PolicyVersion: "v1"}
	policy := core.AccessPolicy{Version: "v1", Ownership: core.ResourceOwnership{Owner: owner, Visibility: core.VisibilityPrivate}}
	if err := store.PutBookWork(ctx, library.BookWork{ID: "work", Title: "Book", NormalizedTitle: "book"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBookEdition(ctx, library.BookEdition{ID: "edition", WorkID: "work", Label: "v1", Language: "en", ContentFingerprint: "fp"}); err != nil {
		t.Fatal(err)
	}
	locator := core.SourceLocator{Kind: core.LocatorMarkdown, Display: "Chapter", ParserVersion: "v1", NormalizationVersion: "v1", Text: &core.TextLocator{SourceStart: 0, SourceEnd: 5, NormalizedStart: 0, NormalizedEnd: 5}}
	if err := store.PutReadingProgress(ctx, library.ReadingProgress{PrincipalID: owner.ID, EditionID: "edition", State: library.ReadingStudied, Locator: locator, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	session := readingroom.StudySession{ID: "session", Workspace: "books", Owner: owner, Scope: readingroom.StudyScope{LibraryID: "library", EditionIDs: []string{"edition"}}, Policy: policy, Retention: readingroom.SessionRetentionRaw, CreatedAt: time.Now().UTC()}
	service := readingroom.NewStudySessionService(store)
	if err := service.Start(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := service.AddTurn(ctx, scope, readingroom.StudyTurn{ID: "turn", SessionID: "session", Principal: owner, Content: "Is the claim universally true?", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	proposal := core.BookMemoryProposal{ID: "proposal", Workspace: "books", RequestedBy: owner, MemoryType: core.SemanticMemory, Content: "Accepted interpretation", Provenance: core.KnowledgeProvenance{Attribution: core.Attribution{Kind: core.AttributionReader, SubjectID: owner.ID}, Form: core.KnowledgeInsight, Derivation: core.DerivationDiscussed}, Confidence: .8, Status: core.ProposalSuggested, CreatedAt: time.Now().UTC()}
	if err := store.PutBookMemoryProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	proposal.Status = core.ProposalAccepted
	proposal.MemoryID = "memory"
	proposal.ReviewedBy = &owner
	proposal.ReviewedAt = &now
	if err := store.UpdateBookMemoryProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	resume, err := readingroom.NewSessionResumeAssembler(store).Build(ctx, scope, "session", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(resume.Progress) != 1 || len(resume.Items) != 2 {
		t.Fatalf("unexpected resume context: %+v", resume)
	}
	if resume.Items[0].Kind != readingroom.ResumeAcceptedKnowledge || resume.Items[1].Kind != readingroom.ResumeOpenQuestion {
		t.Fatalf("labels collapsed: %+v", resume.Items)
	}
	peer := scope
	peer.Principal.ID = "peer"
	if _, err := readingroom.NewSessionResumeAssembler(store).Build(ctx, peer, "session", 100); err == nil {
		t.Fatal("private resume context leaked")
	}
}
