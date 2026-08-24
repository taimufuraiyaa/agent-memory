package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

func TestSearchServiceAuthorizesNormalizesAndPaginates(t *testing.T) {
	workspaceID := uuid.NewString()
	now := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	repository := &searchRepositoryFixture{rows: []SearchRow{
		{Item: SearchItem{ID: uuid.NewString(), WorkspaceID: workspaceID, Type: core.SemanticMemory, Content: "first", SourceKind: core.SourceUserInput, StorageTier: core.TierVector, CreatedAt: now, UpdatedAt: now}, Score: 0.9},
		{Item: SearchItem{ID: uuid.NewString(), WorkspaceID: workspaceID, Type: core.SemanticMemory, Content: "second", SourceKind: core.SourceUserInput, StorageTier: core.TierVector, CreatedAt: now, UpdatedAt: now.Add(-time.Second)}, Score: 0.8},
		{Item: SearchItem{ID: uuid.NewString(), WorkspaceID: workspaceID, Type: core.SemanticMemory, Content: "third", SourceKind: core.SourceUserInput, StorageTier: core.TierVector, CreatedAt: now, UpdatedAt: now.Add(-2 * time.Second)}, Score: 0.7},
	}}
	service := NewSearchService(repository)
	human := searchContext("tenant-one", "account-one", "session-one", "")

	first, err := service.Search(human, SearchCommand{WorkspaceID: workspaceID, Query: "  Durable   Memory  ", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || repository.query.Text != "Durable Memory" || repository.query.Limit != 3 {
		t.Fatalf("unexpected first page=%+v query=%+v", first, repository.query)
	}

	repository.rows = repository.rows[2:]
	second, err := service.Search(human, SearchCommand{WorkspaceID: workspaceID, Query: "Durable Memory", Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" || repository.query.After == nil {
		t.Fatalf("unexpected second page=%+v query=%+v error=%v", second, repository.query, err)
	}
	if repository.query.After.ID != first.Items[1].ID || repository.query.After.Score != first.Items[1].Score {
		t.Fatalf("cursor position=%+v, want second item %+v", repository.query.After, first.Items[1])
	}
}

func TestSearchServiceUsesSameReadScopeForHumanAndAgent(t *testing.T) {
	workspaceID := uuid.NewString()
	now := time.Now().UTC()
	repository := &searchRepositoryFixture{rows: []SearchRow{{Item: SearchItem{ID: uuid.NewString(), WorkspaceID: workspaceID, Type: core.SemanticMemory, SourceKind: core.SourceUserInput, StorageTier: core.TierVector, CreatedAt: now, UpdatedAt: now}, Score: 1}}}
	service := NewSearchService(repository)

	human, err := service.Search(searchContext("tenant-one", "account-one", "session-one", ""), SearchCommand{WorkspaceID: workspaceID, Query: "fact"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := service.Search(searchContext("tenant-one", "account-one", "", "credential-one"), SearchCommand{WorkspaceID: workspaceID, Query: "fact"})
	if err != nil {
		t.Fatal(err)
	}
	if len(human.Items) != 1 || len(agent.Items) != 1 || human.Items[0].ID != agent.Items[0].ID {
		t.Fatalf("human=%+v agent=%+v", human, agent)
	}
}

func TestSearchServiceRejectsUnauthorizedInvalidAndReboundCursors(t *testing.T) {
	workspaceID := uuid.NewString()
	repository := &searchRepositoryFixture{rows: []SearchRow{
		{Item: SearchItem{ID: uuid.NewString(), WorkspaceID: workspaceID, Type: core.SemanticMemory, SourceKind: core.SourceUserInput, StorageTier: core.TierVector, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, Score: 0.5},
		{Item: SearchItem{ID: uuid.NewString(), WorkspaceID: workspaceID, Type: core.SemanticMemory, SourceKind: core.SourceUserInput, StorageTier: core.TierVector, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC().Add(-time.Second)}, Score: 0.4},
	}}
	service := NewSearchService(repository)
	if _, err := service.Search(context.Background(), SearchCommand{WorkspaceID: workspaceID, Query: "fact"}); !errors.Is(err, ErrSearchForbidden) {
		t.Fatalf("unauthorized error=%v", err)
	}
	ctx := searchContext("tenant-one", "account-one", "session-one", "")
	for name, command := range map[string]SearchCommand{
		"workspace":  {WorkspaceID: "not-a-uuid", Query: "fact"},
		"query":      {WorkspaceID: workspaceID, Query: "   "},
		"long query": {WorkspaceID: workspaceID, Query: string(make([]byte, 4001))},
		"limit":      {WorkspaceID: workspaceID, Query: "fact", Limit: 201},
		"cursor":     {WorkspaceID: workspaceID, Query: "fact", Cursor: "not-base64"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Search(ctx, command); !errors.Is(err, ErrInvalidSearch) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	first, err := service.Search(ctx, SearchCommand{WorkspaceID: workspaceID, Query: "fact", Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	for name, command := range map[string]SearchCommand{
		"query":     {WorkspaceID: workspaceID, Query: "other", Limit: 1, Cursor: first.NextCursor},
		"workspace": {WorkspaceID: uuid.NewString(), Query: "fact", Limit: 1, Cursor: first.NextCursor},
	} {
		t.Run("rebound "+name, func(t *testing.T) {
			if _, err := service.Search(ctx, command); !errors.Is(err, ErrInvalidSearch) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type searchRepositoryFixture struct {
	rows  []SearchRow
	query SearchQuery
	err   error
}

func (r *searchRepositoryFixture) SearchMemories(_ context.Context, _ string, query SearchQuery) ([]SearchRow, error) {
	r.query = query
	return append([]SearchRow(nil), r.rows...), r.err
}

func searchContext(tenantID, accountID, sessionID, credentialID string) context.Context {
	return auth.WithRequestContext(context.Background(), auth.RequestContext{
		TenantID: tenantID, AccountID: accountID, SessionID: sessionID, CredentialID: credentialID,
		Capabilities: map[string]struct{}{"memory:read": {}},
	})
}
