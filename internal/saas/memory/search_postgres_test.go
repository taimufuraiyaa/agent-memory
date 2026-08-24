package memory

import (
	"context"
	"errors"
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

func TestPostgresMemorySearchMatchesMetadataAndIsolatesTenantWorkspaceAndDeletion(t *testing.T) {
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := saaspostgres.Open(ctx, connectionURL)
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
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	store := control.NewPostgresStore(pool)
	one := provisionMemoryAccount(t, ctx, store, "provider|search-one", now)
	two := provisionMemoryAccount(t, ctx, store, "provider|search-two", now)
	workspaceOne := insertWorkspace(t, ctx, pool, one.TenantID, now)
	workspaceOther := insertWorkspace(t, ctx, pool, one.TenantID, now)
	workspaceTwo := insertWorkspace(t, ctx, pool, two.TenantID, now)
	writes := NewService(NewPostgresRepository(pool), func() time.Time { return now })
	write := func(account control.PersonalAccount, workspace, content, entity, tag, keyword, key string) core.MemoryEntry {
		t.Helper()
		entry, _, err := writes.Write(auth.WithRequestContext(ctx, auth.RequestContext{
			AccountID: account.AccountID, TenantID: account.TenantID, RequestID: uuid.NewString(),
			Capabilities: map[string]struct{}{"memory:write": {}},
		}), Command{
			WorkspaceID: workspace, Type: core.SemanticMemory, Content: content,
			Source: core.MemorySource{Type: core.SourceUserInput}, Entities: []string{entity}, Tags: []string{tag},
			Keywords: []core.MemoryTerm{{Term: keyword, Display: keyword, Source: core.TermSourceExplicit}}, IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	metadataMemory := write(one, workspaceOne, "neutral retained fact", "ProjectOrion", "blue-team", "nebula", "search-write-key-0001")
	deletedMemory := write(one, workspaceOne, "deleted-orion marker", "ProjectOrion", "blue-team", "nebula", "search-write-key-0002")
	write(one, workspaceOther, "workspace-orion marker", "ProjectOrion", "blue-team", "nebula", "search-write-key-0003")
	write(two, workspaceTwo, "tenant-orion marker", "ProjectOrion", "blue-team", "nebula", "search-write-key-0004")
	for index := 0; index < 3; index++ {
		write(one, workspaceOne, "shared pagination fact "+string(rune('a'+index)), "shared", "pagination", "shared", "search-page-key-000"+string(rune('5'+index)))
	}
	if _, err := pool.Exec(ctx, "UPDATE saas_memories SET deleted_at=$1 WHERE tenant_id=$2 AND id=$3", now, one.TenantID, deletedMemory.ID); err != nil {
		t.Fatal(err)
	}

	search := NewSearchService(NewPostgresSearchRepository(pool))
	searchCtx := auth.WithRequestContext(ctx, auth.RequestContext{
		AccountID: one.AccountID, TenantID: one.TenantID, RequestID: uuid.NewString(),
		Capabilities: map[string]struct{}{"memory:read": {}},
	})
	metadata, err := search.Search(searchCtx, SearchCommand{WorkspaceID: workspaceOne, Query: "ProjectOrion", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Items) != 1 || metadata.Items[0].ID != metadataMemory.ID || metadata.Items[0].SourceKind != core.SourceUserInput {
		t.Fatalf("metadata results=%+v", metadata)
	}

	first, err := search.Search(searchCtx, SearchCommand{WorkspaceID: workspaceOne, Query: "shared", Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page=%+v error=%v", first, err)
	}
	second, err := search.Search(searchCtx, SearchCommand{WorkspaceID: workspaceOne, Query: "shared", Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("second page=%+v error=%v", second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.ID] {
			t.Fatalf("pagination duplicated memory %s", item.ID)
		}
		seen[item.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("pagination returned %d unique memories", len(seen))
	}

	if _, err := search.Search(searchCtx, SearchCommand{WorkspaceID: workspaceTwo, Query: "tenant-orion"}); !errors.Is(err, auth.ErrTenantUnavailable) {
		t.Fatalf("cross-tenant workspace error=%v", err)
	}
}
