package memory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestPostgresNoteFeedbackAndSessionEndAreTransactionalAndIdempotent(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := saaspostgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := saaspostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE saas_accounts CASCADE"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 14, 0, 0, 0, time.UTC)
	account, err := control.NewPostgresStore(pool).ProvisionPersonalAccount(ctx, control.ProvisionCommand{AccountID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ExternalSubject: "provider|workflow", VerifiedEmail: "workflow@example.test", RequestID: uuid.NewString(), OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	request := auth.RequestContext{AccountID: account.AccountID, TenantID: account.TenantID, Capabilities: map[string]struct{}{"memory:write": {}}, RequestID: uuid.NewString(), TraceID: uuid.NewString()}
	authenticated := auth.WithRequestContext(ctx, request)
	repository := NewPostgresRepository(pool)
	workflow := NewWorkflowService(repository, func() time.Time { return now })
	created, duplicate, err := workflow.CreateNote(authenticated, NoteCreate{Input: core.CreateNoteInput{Workspace: account.WorkspaceID, Path: "design.md", Body: "# Design\nBody"}, IdempotencyKey: "note-create-key-0001"})
	if err != nil || duplicate || created.Revision != 1 {
		t.Fatalf("create=%+v duplicate=%v err=%v", created, duplicate, err)
	}
	retried, duplicate, err := workflow.CreateNote(authenticated, NoteCreate{Input: core.CreateNoteInput{Workspace: account.WorkspaceID, Path: "design.md", Body: "# Design\nBody"}, IdempotencyKey: "note-create-key-0001"})
	if err != nil || !duplicate || retried.ID != created.ID {
		t.Fatalf("retry=%+v duplicate=%v err=%v", retried, duplicate, err)
	}
	updated, duplicate, err := workflow.UpdateNote(authenticated, NoteUpdate{Input: core.UpdateNoteInput{Workspace: account.WorkspaceID, NoteID: created.ID, ExpectedRevision: 1, Path: "design.md", Title: "Design", Body: "# Design\nUpdated"}})
	if err != nil || duplicate || updated.Revision != 2 {
		t.Fatalf("update=%+v duplicate=%v err=%v", updated, duplicate, err)
	}
	_, duplicate, err = workflow.UpdateNote(authenticated, NoteUpdate{Input: core.UpdateNoteInput{Workspace: account.WorkspaceID, NoteID: created.ID, ExpectedRevision: 1, Path: "design.md", Title: "Design", Body: "# Design\nUpdated"}})
	if err != nil || !duplicate {
		t.Fatalf("update retry duplicate=%v err=%v", duplicate, err)
	}
	memoryService := NewService(repository, func() time.Time { return now })
	entry, _, err := memoryService.Write(authenticated, Command{WorkspaceID: account.WorkspaceID, Type: core.SemanticMemory, Content: "feedback target", Source: core.MemorySource{Type: core.SourceUserInput}, IdempotencyKey: "memory-feedback-0001"})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err = workflow.Feedback(authenticated, FeedbackCommand{MemoryID: entry.ID, RequestID: "retrieval-request-1", Outcome: core.FeedbackHelpful})
	if err != nil || duplicate {
		t.Fatalf("feedback duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = workflow.Feedback(authenticated, FeedbackCommand{MemoryID: entry.ID, RequestID: "retrieval-request-1", Outcome: core.FeedbackHelpful})
	if err != nil || !duplicate {
		t.Fatalf("feedback retry duplicate=%v err=%v", duplicate, err)
	}
	session := SessionEndCommand{SessionID: uuid.NewString(), WorkspaceID: account.WorkspaceID, Transcript: "User and agent completed the design.", IdempotencyKey: "session-end-key-0001"}
	duplicate, err = workflow.EndSession(authenticated, session)
	if err != nil || duplicate {
		t.Fatalf("session duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = workflow.EndSession(authenticated, session)
	if err != nil || !duplicate {
		t.Fatalf("session retry duplicate=%v err=%v", duplicate, err)
	}
	for table, want := range map[string]int{"saas_notes": 1, "saas_note_revisions": 2, "saas_feedback": 1, "saas_sessions_memory": 1} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s count=%d want=%d", table, count, want)
		}
	}
}
