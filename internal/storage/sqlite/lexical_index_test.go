package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func openLexicalStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "lexical.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedLexicalMemory(t *testing.T, store *Store, id, workspace, content string, tags ...string) {
	t.Helper()
	if err := store.UpsertMemory(context.Background(), &core.MemoryEntry{
		ID: id, Type: core.SemanticMemory, Content: content, Workspace: workspace, Tags: tags,
		Source: core.MemorySource{Type: core.SourceCodeAnalysis}, StorageTier: core.TierVector, Confidence: 0.9,
	}); err != nil {
		t.Fatalf("seed memory %s: %v", id, err)
	}
}

func lexicalIDs(t *testing.T, store *Store, workspace, text string) []string {
	t.Helper()
	matches, err := store.SearchMemoryLexical(context.Background(), LexicalQuery{Workspace: workspace, Text: text, Limit: 20})
	if err != nil {
		t.Fatalf("lexical search %q: %v", text, err)
	}
	ids := make([]string, len(matches))
	for i, match := range matches {
		ids[i] = match.MemoryID
	}
	return ids
}

func TestLexicalTermsAreBoundedQuotedSafeTokens(t *testing.T) {
	got := LexicalTerms(`How does the "retry_backoff" NEAR(429 OR) -- ; DROP TABLE x* handle 429 429?`, 16)
	want := []string{"retry_backoff", "near", "429", "drop", "table", "handle"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("terms = %#v, want %#v", got, want)
	}
	if len(LexicalTerms(strings.Repeat("alpha beta gamma delta ", 3)+"x y z", 3)) != 3 {
		t.Fatal("term cap not applied")
	}
	if got := LexicalTerms("the a of -- ()", 8); len(got) != 0 {
		t.Fatalf("stopword-only query should have no terms: %#v", got)
	}
}

func TestMemoryLexicalIndexTracksInsertDeleteAndSupersession(t *testing.T) {
	ctx := context.Background()
	store := openLexicalStore(t)
	seedLexicalMemory(t, store, "m1", "ws", "Retry policy uses exponential backoff with jitter for ERR429 responses")
	seedLexicalMemory(t, store, "m2", "ws", "Database vacuum schedule runs nightly")
	seedLexicalMemory(t, store, "m3", "other", "Retry policy for another workspace with ERR429")

	if got := lexicalIDs(t, store, "ws", "ERR429 backoff"); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Fatalf("workspace-scoped lexical hit = %#v", got)
	}
	if got := lexicalIDs(t, store, "other", "ERR429"); !reflect.DeepEqual(got, []string{"m3"}) {
		t.Fatalf("workspace isolation violated: %#v", got)
	}
	if got := lexicalIDs(t, store, "ws", "retries"); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Fatalf("stemming should match retries to retry: %#v", got)
	}

	if err := store.MarkSuperseded(ctx, []string{"m1"}, "m2"); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if got := lexicalIDs(t, store, "ws", "ERR429 backoff"); len(got) != 0 {
		t.Fatalf("superseded memory must not be returned: %#v", got)
	}

	if err := store.DeleteByIDs(ctx, []string{"m2"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := lexicalIDs(t, store, "ws", "vacuum nightly"); len(got) != 0 {
		t.Fatalf("deleted memory still indexed: %#v", got)
	}
	status, err := store.LexicalIndexStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Drifted() || status.Memories != 2 || status.IndexedMemories != 2 {
		t.Fatalf("unexpected status after writes: %+v", status)
	}
}

func TestMemoryLexicalIndexIgnoresCounterOnlyUpdatesAndFollowsContentUpdates(t *testing.T) {
	ctx := context.Background()
	store := openLexicalStore(t)
	seedLexicalMemory(t, store, "m1", "ws", "Original kafka partition guidance")

	var docBefore int64
	if err := store.db.QueryRowContext(ctx, `SELECT doc_id FROM memory_lexical_docs WHERE memory_id = 'm1'`).Scan(&docBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE memories SET access_count = access_count + 1, updated_at = updated_at WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if got := lexicalIDs(t, store, "ws", "kafka"); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Fatalf("counter update broke index: %#v", got)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE memories SET content = 'Revised postgres replication guidance' WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if got := lexicalIDs(t, store, "ws", "kafka"); len(got) != 0 {
		t.Fatalf("stale content remained searchable: %#v", got)
	}
	if got := lexicalIDs(t, store, "ws", "postgres"); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Fatalf("updated content not searchable: %#v", got)
	}
	var docAfter int64
	if err := store.db.QueryRowContext(ctx, `SELECT doc_id FROM memory_lexical_docs WHERE memory_id = 'm1'`).Scan(&docAfter); err != nil {
		t.Fatal(err)
	}
	if docBefore != docAfter {
		t.Fatalf("doc id changed across update: %d -> %d", docBefore, docAfter)
	}
}

func TestMemoryLexicalRankingPrefersRareMatchingTerms(t *testing.T) {
	store := openLexicalStore(t)
	for i := 0; i < 8; i++ {
		seedLexicalMemory(t, store, "common"+string(rune('a'+i)), "ws", "deployment pipeline configuration notes for the service number "+string(rune('a'+i)))
	}
	seedLexicalMemory(t, store, "rare", "ws", "deployment pipeline incident linked to zxqv9 timeout")

	got := lexicalIDs(t, store, "ws", "deployment zxqv9")
	if len(got) == 0 || got[0] != "rare" {
		t.Fatalf("rare term should rank first: %#v", got)
	}
}

func TestMemoryLexicalSearchRejectsHostileInputWithoutError(t *testing.T) {
	store := openLexicalStore(t)
	seedLexicalMemory(t, store, "m1", "ws", "plain content about caching")
	for _, text := range []string{`"`, `")) OR 1=1 --`, `col:caching NEAR/2`, `*`, `caching AND`, "\x00caching", ``} {
		if _, err := store.SearchMemoryLexical(context.Background(), LexicalQuery{Workspace: "ws", Text: text}); err != nil {
			t.Fatalf("query %q must not error: %v", text, err)
		}
	}
	if _, err := store.SearchMemoryLexical(context.Background(), LexicalQuery{Text: "caching"}); err == nil {
		t.Fatal("workspace must be required")
	}
}

func TestLexicalMigrationBackfillsExistingRowsAndRebuildRepairsDrift(t *testing.T) {
	ctx := context.Background()
	store := openLexicalStore(t)
	seedLexicalMemory(t, store, "m1", "ws", "legacy memory about sharding strategy")
	seedLexicalMemory(t, store, "m2", "ws", "another memory about caching strategy")

	if _, err := store.db.ExecContext(ctx, `DELETE FROM memory_lexical_fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM memory_lexical_docs WHERE memory_id = 'm2'`); err != nil {
		t.Fatal(err)
	}
	status, err := store.LexicalIndexStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Drifted() {
		t.Fatalf("drift not detected: %+v", status)
	}
	if err := store.RebuildLexicalIndex(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := lexicalIDs(t, store, "ws", "strategy"); !reflect.DeepEqual(got, []string{"m1", "m2"}) && !reflect.DeepEqual(got, []string{"m2", "m1"}) {
		t.Fatalf("rebuild did not restore both rows: %#v", got)
	}
	if status, _ = store.LexicalIndexStatus(ctx); status.Drifted() {
		t.Fatalf("still drifted: %+v", status)
	}
	// Re-running the migration step is idempotent.
	if err := migrateLexicalIndexes(ctx, store); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	if status, _ = store.LexicalIndexStatus(ctx); status.Drifted() || status.IndexedMemories != 2 {
		t.Fatalf("migration re-run changed coverage: %+v", status)
	}
}

func TestPassageLexicalIndexFollowsPassageWritesAndRestrictsEditions(t *testing.T) {
	ctx := context.Background()
	store := openLexicalStore(t)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, edition, text string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, `INSERT INTO source_passages (id, edition_id, source_asset_id, structural_node_id, text, locator_json, fingerprint) VALUES (?, ?, 'asset', 'node', ?, '{}', ?)`, id, edition, text, id); err != nil {
			t.Fatalf("insert passage: %v", err)
		}
	}
	insert("p1", "ed-a", "The arbitration clause requires binding arbitration within thirty days")
	insert("p2", "ed-b", "Arbitration arbitration arbitration is discussed at length here")
	insert("p3", "ed-a", "Payment terms are net sixty")

	matches, err := store.SearchPassagesLexical(ctx, []string{"ed-a"}, "arbitration clause", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].PassageID != "p1" || matches[0].Rank != 1 {
		t.Fatalf("edition restriction failed: %+v", matches)
	}
	matches, err = store.SearchPassagesLexical(ctx, []string{"ed-a", "ed-b"}, "arbitration", 10)
	if err != nil || len(matches) != 2 {
		t.Fatalf("two editions should both match: %+v err=%v", matches, err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM source_passages WHERE id = 'p1'`); err != nil {
		t.Fatal(err)
	}
	matches, _ = store.SearchPassagesLexical(ctx, []string{"ed-a"}, "arbitration", 10)
	if len(matches) != 0 {
		t.Fatalf("deleted passage still indexed: %+v", matches)
	}
}

func TestMemoryLexicalSearchWidensFetchWhenCanonicalFilteringDropsTopCandidates(t *testing.T) {
	ctx := context.Background()
	store := openLexicalStore(t)
	seedLexicalMemory(t, store, "keep", "ws", "A single mention of crowdterm among other words")
	var superseded []string
	for i := 0; i < 80; i++ {
		id := "old" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		seedLexicalMemory(t, store, id, "ws", "crowdterm crowdterm crowdterm crowdterm crowdterm entry "+id)
		superseded = append(superseded, id)
	}
	if err := store.MarkSuperseded(ctx, superseded, "keep"); err != nil {
		t.Fatal(err)
	}
	matches, err := store.SearchMemoryLexical(ctx, LexicalQuery{Workspace: "ws", Text: "crowdterm", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].MemoryID != "keep" {
		t.Fatalf("widened fetch should surface the only live memory: %+v", matches)
	}
}
